package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCategoryVerificationIsIndependentAuditableAndRunScoped(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "category-verification.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{
		"title_de": "Verkehrskontrolle", "summary_de": "Die Polizei kontrollierte mehrere Fahrzeuge.", "category": "traffic",
	})
	plan := CategoryVerificationPlan{PromptVersion: "incident-category-verification-v1", Model: "verify:a"}
	if queued, err := database.QueueCategoryVerificationForRun(ctx, runID, plan, "scheduled", false, now); err != nil || queued != 1 {
		t.Fatalf("queue scheduled verification = %d/%v", queued, err)
	}
	if _, found, err := database.ClaimCategoryVerificationJob(ctx, false, nil, now); err != nil || found {
		t.Fatalf("scheduled verification escaped closed window: found=%t err=%v", found, err)
	}
	if _, found, err := database.ClaimCategoryVerificationJob(ctx, true, []string{"verify:a"}, now); err != nil || found {
		t.Fatalf("blocked verifier was claimed: found=%t err=%v", found, err)
	}
	job, found, err := database.ClaimCategoryVerificationJob(ctx, true, nil, now)
	if err != nil || !found || job.TitleDE != "Verkehrskontrolle" || job.SummaryDE == "" || job.InputCategory != "traffic" {
		t.Fatalf("claim verifier = %#v/%t/%v", job, found, err)
	}
	if _, found, err := database.ClaimCategoryVerificationJob(ctx, true, nil, now); err != nil || found {
		t.Fatalf("running verifier was claimed twice: found=%t err=%v", found, err)
	}
	wantHash := HashPipelineInput("title_de", job.TitleDE, "summary_de", job.SummaryDE, "category", "traffic", plan.PromptVersion, plan.Model)
	if job.InputHash != wantHash {
		t.Fatalf("category verifier input hash = %q, want %q", job.InputHash, wantHash)
	}
	if err := database.CompleteCategoryVerificationJob(ctx, job, false, "police_operation", job.ModelIdentity, job.InputHash, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	history, err := database.ListPipelineHistory(ctx, "fixture", 10, nil, nil)
	if err != nil || len(history.Entries) == 0 || history.Entries[0].Kind != pipelineHistoryKindCategoryVerification || history.Entries[0].StepKey != "category_verification" {
		t.Fatalf("category verification history = %#v/%v", history, err)
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", []string{"incident_metadata", "german_presentation"}, now.Add(time.Minute))
	if err != nil || snapshot.CategoryVerification.Succeeded != 1 || snapshot.CategoryVerification.Corrected != 1 {
		t.Fatalf("category verification stats = %#v/%v", snapshot.CategoryVerification, err)
	}
	record, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{PromptVersion: PipelineVersion, Language: "de", PublicOnly: true})
	if err != nil || record.AICategory != "police_operation" || record.AIOriginalCategory != "traffic" || record.AICategoryVerificationModel != "verify:a" {
		t.Fatalf("effective corrected category = %#v/%v", record, err)
	}

	if queued, err := database.QueueIncidentCategoryVerification(ctx, incidentID, CategoryVerificationPlan{PromptVersion: plan.PromptVersion, Model: "verify:b"}, now.Add(2*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue manual recheck = %d/%v", queued, err)
	}
	retry, found, err := database.ClaimCategoryVerificationJob(ctx, false, nil, now.Add(2*time.Minute))
	if err != nil || !found || retry.RequestKind != "manual" || retry.ModelIdentity != "verify:b" {
		t.Fatalf("manual verification did not bypass window = %#v/%t/%v", retry, found, err)
	}
	if err := database.FailCategoryVerificationJob(ctx, retry, "failed", "output", nil, now.Add(3*time.Minute), errors.New("synthetic invalid output")); err != nil {
		t.Fatal(err)
	}
	record, err = database.GetPresentationIncident(ctx, incidentID, PresentationScope{PromptVersion: PipelineVersion, Language: "de", PublicOnly: true})
	if err != nil || record.AICategory != "police_operation" || record.AICategoryVerificationModel != "verify:a" {
		t.Fatalf("failed retry displaced successful category = %#v/%v", record, err)
	}

	if queued, err := database.QueueIncidentCategoryVerification(ctx, incidentID, CategoryVerificationPlan{PromptVersion: plan.PromptVersion, Model: "verify:c"}, now.Add(4*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue second manual recheck = %d/%v", queued, err)
	}
	retry, found, err = database.ClaimCategoryVerificationJob(ctx, false, nil, now.Add(4*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim second recheck = %#v/%t/%v", retry, found, err)
	}
	if err := database.CompleteCategoryVerificationJob(ctx, retry, true, "traffic", retry.ModelIdentity, retry.InputHash, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	record, err = database.GetPresentationIncident(ctx, incidentID, PresentationScope{PromptVersion: PipelineVersion, Language: "de", PublicOnly: true})
	if err != nil || record.AICategory != "traffic" || record.AICategoryVerificationModel != "verify:c" {
		t.Fatalf("latest successful recheck = %#v/%v", record, err)
	}
	var successful int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM presentation_category_verifications WHERE presentation_run_id=? AND status='succeeded'`, runID).Scan(&successful); err != nil || successful != 2 {
		t.Fatalf("successful verification history = %d/%v", successful, err)
	}
	if queued, err := database.QueueAllCategoryVerifications(ctx, "fixture", CategoryVerificationPlan{PromptVersion: plan.PromptVersion, Model: "verify:all"}, true, true, now.Add(6*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("explicit all-category recheck = %d/%v", queued, err)
	}
	allJob, found, err := database.ClaimCategoryVerificationJob(ctx, false, nil, now.Add(6*time.Minute))
	if err != nil || !found || allJob.ModelIdentity != "verify:all" || allJob.RequestKind != "manual" {
		t.Fatalf("claim all-category recheck = %#v/%t/%v", allJob, found, err)
	}
	if err := database.FailCategoryVerificationJob(ctx, allJob, "failed", "output", nil, now.Add(6*time.Minute), errors.New("synthetic all-category failure")); err != nil {
		t.Fatal(err)
	}

	if queued, err := database.QueueIncidentCategoryVerification(ctx, incidentID, CategoryVerificationPlan{PromptVersion: plan.PromptVersion, Model: "verify:restart"}, now.Add(6*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue restart verification = %d/%v", queued, err)
	}
	interrupted, found, err := database.ClaimCategoryVerificationJob(ctx, false, nil, now.Add(6*time.Minute))
	if err != nil || !found || interrupted.AttemptCount != 1 {
		t.Fatalf("claim interrupted verification = %#v/%t/%v", interrupted, found, err)
	}
	if err := database.RecoverCategoryVerifications(ctx, now.Add(7*time.Minute)); err != nil {
		t.Fatal(err)
	}
	recovered, found, err := database.ClaimCategoryVerificationJob(ctx, false, nil, now.Add(7*time.Minute))
	if err != nil || !found || recovered.ID != interrupted.ID || recovered.AttemptCount != 2 {
		t.Fatalf("recover interrupted verification = %#v/%t/%v", recovered, found, err)
	}
	if err := database.FailCategoryVerificationJob(ctx, recovered, "failed", "transient", nil, now.Add(8*time.Minute), errors.New("synthetic shutdown retry")); err != nil {
		t.Fatal(err)
	}

	if queued, err := database.QueueIncidentCategoryVerification(ctx, incidentID, CategoryVerificationPlan{PromptVersion: plan.PromptVersion, Model: "verify:stale"}, now.Add(9*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue stale verification = %d/%v", queued, err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE incidents SET content_hash='replacement-source' WHERE id=?`, incidentID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.ClaimCategoryVerificationJob(ctx, false, nil, now.Add(9*time.Minute)); err != nil || found {
		t.Fatalf("obsolete source verification was claimable: found=%t err=%v", found, err)
	}
	var superseded int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM presentation_category_verifications WHERE presentation_run_id=? AND status='superseded'`, runID).Scan(&superseded); err != nil || superseded != 1 {
		t.Fatalf("stale verification superseded = %d/%v", superseded, err)
	}
}

func TestFailedCategoryVerificationFallsBackToOriginalCategory(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "failed-category-verification.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{
		"title_de": "Verkehrskontrolle", "summary_de": "Die Polizei kontrollierte mehrere Fahrzeuge.", "category": "traffic",
	})
	plan := CategoryVerificationPlan{PromptVersion: "incident-category-verification-v1", Model: "verify:failed"}
	if queued, err := database.QueueCategoryVerificationForRun(ctx, runID, plan, "scheduled", false, now); err != nil || queued != 1 {
		t.Fatalf("queue category verification = %d/%v", queued, err)
	}
	job, found, err := database.ClaimCategoryVerificationJob(ctx, true, nil, now)
	if err != nil || !found {
		t.Fatalf("claim category verification = %#v/%t/%v", job, found, err)
	}
	if err := database.FailCategoryVerificationJob(ctx, job, "needs_review", "output", nil, now.Add(time.Minute), errors.New("synthetic invalid output")); err != nil {
		t.Fatal(err)
	}

	record, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{PromptVersion: PipelineVersion, Language: "de", PublicOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if record.AICategory != "traffic" || record.AIOriginalCategory != "traffic" || record.AICategoryVerificationModel != "" || record.AICategoryVerificationGeneratedAt != nil {
		t.Fatalf("failed verification changed reader category or provenance: %#v", record)
	}
	admin, err := database.ListAdminCategoryVerifications(ctx, []int64{incidentID})
	if err != nil || len(admin) != 1 {
		t.Fatalf("admin category verification = %#v/%v", admin, err)
	}
	if admin[0].Status != "needs_review" || admin[0].EffectiveCategory != "traffic" || admin[0].IsCorrect != nil || admin[0].Model != "verify:failed" {
		t.Fatalf("failed verifier admin fallback = %#v", admin[0])
	}
}

func TestCategoryVerificationBackfillRespectsCutoverAndIncludesTranslatedFallback(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "category-backfill.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	older := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PreviousPipelineVersion, now, map[string]string{"title_de": "Alt", "summary_de": "Alt.", "category": "other"})
	newer := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now.Add(time.Minute), map[string]string{"title_de": "Neu", "summary_de": "Neu.", "category": "traffic"})
	categoryless := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PreviousPipelineVersion, now.Add(-time.Minute), map[string]string{
		"title_de": "Sehr alt", "summary_de": "Sehr alt.", "title_en": "Very old", "summary_en": "Very old.",
	})
	if _, err := database.db.ExecContext(ctx, `INSERT INTO presentation_translations(presentation_run_id,language_code,request_kind,status,title,summary,model_identity,prompt_version,input_hash,attempt_count,started_at,completed_at,created_at,updated_at) VALUES(?,'en','manual','succeeded','Old','Old.','translate','prompt','',1,?,?,?,?)`, older, formatTime(now), formatTime(now), formatTime(now), formatTime(now)); err != nil {
		t.Fatal(err)
	}
	plan := CategoryVerificationPlan{PromptVersion: "incident-category-verification-v1", Model: "verify"}
	if queued, err := database.QueueMissingCategoryVerifications(ctx, "fixture", plan, false, now); err != nil || queued != 0 {
		t.Fatalf("automatic pre-cutover queue = %d/%v", queued, err)
	}
	if queued, err := database.QueueMissingCategoryVerifications(ctx, "fixture", plan, true, now); err != nil || queued != 2 {
		t.Fatalf("backfill reader-selectable runs = %d/%v", queued, err)
	}
	var queuedRuns int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT presentation_run_id) FROM presentation_category_verifications WHERE presentation_run_id IN (?,?)`, older, newer).Scan(&queuedRuns); err != nil || queuedRuns != 2 {
		t.Fatalf("backfill run coverage = %d/%v", queuedRuns, err)
	}
	var unsupported int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM presentation_category_verifications WHERE presentation_run_id=?`, categoryless).Scan(&unsupported); err != nil || unsupported != 0 {
		t.Fatalf("category-less legacy run was queued = %d/%v", unsupported, err)
	}
}
