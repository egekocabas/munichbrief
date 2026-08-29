package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPostProcessingGenericLifecycleKeepsSuccessfulValueDuringForcedReplacement(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "post-processing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsurePostProcessingScopes(ctx, []PostProcessingScope{{ProcessorKey: "translation", ScopeKey: "en"}}, now); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now.Add(time.Minute), map[string]string{
		"title_de": "Titel", "summary_de": "Zusammenfassung.", "category": "other",
		"report_kind": "incident", "public_assistance_status": "not_requested", "public_assistance_types": "[]",
		"privacy_status": "safe", "privacy_flags": "[]",
	})
	plan := PostProcessingPlan{ProcessorKey: "translation", ScopeKey: "en", PromptVersion: "translation-v1", Model: "translate:4b", InputKinds: []string{"title_de", "summary_de"}}
	if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, false, now.Add(2*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("scheduled queue = %d/%v", queued, err)
	}
	if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, false, now.Add(3*time.Minute)); err != nil || queued != 0 {
		t.Fatalf("deduplicated scheduled queue = %d/%v", queued, err)
	}
	job, found, err := database.ClaimPostProcessingJob(ctx, "translation", true, nil, now.Add(4*time.Minute))
	if err != nil || !found || job.PresentationRunID != runID || job.RequestKind != "scheduled" || job.AttemptCount != 1 {
		t.Fatalf("scheduled claim = %#v/%t/%v", job, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, job, []PipelineValue{{Kind: "title", Value: "First title"}, {Kind: "summary", Value: "First summary."}}, "translate:4b", job.InputHash, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	if queued, err := database.QueueIncidentPostProcessing(ctx, incidentID, []PostProcessingPlan{plan}, now.Add(6*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("forced incident queue = %d/%v", queued, err)
	}
	visible, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{TranslationLanguage: "en"})
	if err != nil || visible.AITranslatedTitle != "First title" {
		t.Fatalf("successful fallback during replacement = %#v/%v", visible, err)
	}
	replacement, found, err := database.ClaimPostProcessingJob(ctx, "translation", false, nil, now.Add(7*time.Minute))
	if err != nil || !found || replacement.RequestKind != "manual" {
		t.Fatalf("manual replacement claim = %#v/%t/%v", replacement, found, err)
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", []string{"incident_metadata", "german_presentation"}, nil, now.Add(7*time.Minute+30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var translationStats *PostProcessingQueueStats
	for index := range snapshot.PostProcessing {
		if snapshot.PostProcessing[index].ProcessorKey == "translation" && snapshot.PostProcessing[index].ScopeKey == "en" {
			translationStats = &snapshot.PostProcessing[index]
			break
		}
	}
	if translationStats == nil || translationStats.Running != 1 || translationStats.RunningStartedAt == nil || !translationStats.RunningStartedAt.Equal(now.Add(7*time.Minute)) {
		t.Fatalf("running translation stats = %#v", translationStats)
	}
	if err := database.CompletePostProcessingJob(ctx, replacement, []PipelineValue{{Kind: "title", Value: "Second title"}, {Kind: "summary", Value: "Second summary."}}, "translate:4b", replacement.InputHash, now.Add(8*time.Minute)); err != nil {
		t.Fatal(err)
	}
	visible, err = database.GetPresentationIncident(ctx, incidentID, PresentationScope{TranslationLanguage: "en"})
	if err != nil || visible.AITranslatedTitle != "Second title" {
		t.Fatalf("replacement selection = %#v/%v", visible, err)
	}
}

func TestPostProcessingRetryRecoveryAndTransactionalCompletion(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "post-processing-recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{
		"title_de": "Titel", "summary_de": "Zusammenfassung.", "category": "other",
	})
	plan := PostProcessingPlan{ProcessorKey: "test_processor", ScopeKey: "default", PromptVersion: "test-v1", Model: "test:4b", InputKinds: []string{"title_de"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", false, now); err != nil || queued != 1 {
		t.Fatalf("queue retry job = %d/%v", queued, err)
	}
	job, found, err := database.ClaimPostProcessingJob(ctx, "test_processor", false, nil, now)
	if err != nil || !found {
		t.Fatalf("claim retry job = %#v/%t/%v", job, found, err)
	}
	retryAt := now.Add(time.Minute)
	if err := database.FailPostProcessingJob(ctx, job, "pending", "transient", &retryAt, now, errors.New("private provider failure")); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.ClaimPostProcessingJob(ctx, "test_processor", false, nil, now.Add(30*time.Second)); err != nil || found {
		t.Fatalf("early retry claim = %t/%v", found, err)
	}
	retried, found, err := database.ClaimPostProcessingJob(ctx, "test_processor", false, nil, retryAt)
	if err != nil || !found || retried.AttemptCount != 2 {
		t.Fatalf("due retry claim = %#v/%t/%v", retried, found, err)
	}
	if err := database.RecoverPostProcessing(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	recovered, found, err := database.ClaimPostProcessingJob(ctx, "test_processor", false, nil, now.Add(2*time.Minute))
	if err != nil || !found || recovered.ID != job.ID || recovered.AttemptCount != 3 {
		t.Fatalf("recovered claim = %#v/%t/%v", recovered, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, recovered, []PipelineValue{{Kind: "note", Value: "one"}, {Kind: "note", Value: "duplicate"}}, recovered.ModelIdentity, recovered.InputHash, now.Add(3*time.Minute)); err == nil {
		t.Fatal("duplicate output kinds unexpectedly committed")
	}
	var status string
	var valueCount int
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM post_processing_jobs WHERE id=?`, recovered.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_values WHERE job_id=?`, recovered.ID).Scan(&valueCount); err != nil {
		t.Fatal(err)
	}
	if status != "running" || valueCount != 0 {
		t.Fatalf("failed completion was not transactional: status=%s values=%d", status, valueCount)
	}
}

func TestPostProcessingLoadsImmutableRawGermanSourceIncludingEmptyBody(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "post-processing-source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 13, 30, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "source")
	var incidentID int64
	var sourceHash, originalTitle string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash,title_de FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash, &originalTitle); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE incidents SET body_de='' WHERE id=?`, incidentID); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{
		"title_de": "Generated title", "summary_de": "Generated summary.", "category": "other",
		"public_assistance_status": "not_requested", "public_assistance_types": "[]",
	})
	plan := PostProcessingPlan{ProcessorKey: "public_assistance_verification", ScopeKey: "default", PromptVersion: "assistance-v1", Model: "verify:4b", InputKinds: []string{"original_title", "incident_body", "public_assistance_status", "public_assistance_types"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", false, now); err != nil || queued != 1 {
		t.Fatalf("queue raw-source verifier = %d/%v", queued, err)
	}
	expectedHash := HashPipelineInput(
		"processor", plan.ProcessorKey, "scope", plan.ScopeKey,
		"original_title", originalTitle, "incident_body", "",
		"public_assistance_status", "not_requested", "public_assistance_types", "[]",
		plan.PromptVersion, plan.Model,
	)
	job, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, false, nil, now)
	if err != nil || !found {
		t.Fatalf("claim raw-source verifier = %#v/%t/%v", job, found, err)
	}
	if job.InputValues["original_title"] != originalTitle || job.InputValues["incident_body"] != "" || job.InputHash != expectedHash {
		t.Fatalf("raw source inputs/hash = %#v/%s, want title %q and hash %s", job.InputValues, job.InputHash, originalTitle, expectedHash)
	}
}

func TestPublicAssistanceVerificationKeepsLatestSuccessfulResult(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "public-assistance-selection.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "assistance-selection")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{
		"title_de": "Titel", "summary_de": "Zusammenfassung.", "category": "other",
		"public_assistance_status": "not_requested", "public_assistance_types": "[]",
	})
	plan := PostProcessingPlan{ProcessorKey: "public_assistance_verification", ScopeKey: "default", PromptVersion: "assistance-v1", Model: "verify:4b", InputKinds: []string{"original_title", "incident_body", "public_assistance_status", "public_assistance_types"}}

	assertEffective := func(status, types string) {
		t.Helper()
		record, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{Language: "de"})
		if err != nil {
			t.Fatal(err)
		}
		if record.AIPublicAssistanceStatus != status || record.AIPublicAssistanceTypes != types {
			t.Fatalf("effective assistance = %s/%s, want %s/%s", record.AIPublicAssistanceStatus, record.AIPublicAssistanceTypes, status, types)
		}
	}
	assertEffective("not_requested", "[]")

	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", false, now.Add(time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue first verification = %d/%v", queued, err)
	}
	first, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, false, nil, now.Add(2*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim first verification = %#v/%t/%v", first, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, first, []PipelineValue{
		{Kind: "is_correct", Value: "false"},
		{Kind: "corrected_public_assistance_status", Value: "requested"},
		{Kind: "corrected_public_assistance_types", Value: `["identify_person"]`},
	}, first.ModelIdentity, first.InputHash, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertEffective("requested", `["identify_person"]`)

	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", true, now.Add(4*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue failed replacement = %d/%v", queued, err)
	}
	failed, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, false, nil, now.Add(5*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim failed replacement = %#v/%t/%v", failed, found, err)
	}
	if err := database.FailPostProcessingJob(ctx, failed, "needs_review", "invalid_response", nil, now.Add(6*time.Minute), errors.New("private model output")); err != nil {
		t.Fatal(err)
	}
	assertEffective("requested", `["identify_person"]`)

	admin, err := database.ListAdminPublicAssistanceVerifications(ctx, []int64{incidentID})
	if err != nil || len(admin) != 1 || admin[0].Status != "needs_review" || admin[0].EffectiveStatus != "requested" || admin[0].IsCorrect == nil || *admin[0].IsCorrect {
		t.Fatalf("admin fallback = %#v/%v", admin, err)
	}

	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", true, now.Add(7*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue later replacement = %d/%v", queued, err)
	}
	later, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, false, nil, now.Add(8*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim later replacement = %#v/%t/%v", later, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, later, []PipelineValue{
		{Kind: "is_correct", Value: "true"},
		{Kind: "corrected_public_assistance_status", Value: "not_requested"},
		{Kind: "corrected_public_assistance_types", Value: "[]"},
	}, later.ModelIdentity, later.InputHash, now.Add(9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertEffective("not_requested", "[]")
}

func TestPublicAssistanceVerificationCutoverRequiresManualHistoricalBackfill(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "public-assistance-cutover.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "old")
	var oldIncidentID int64
	var oldHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents WHERE title_de='Titel old'`).Scan(&oldIncidentID, &oldHash); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"title_de": "Titel", "summary_de": "Zusammenfassung.", "category": "other",
		"public_assistance_status": "not_requested", "public_assistance_types": "[]",
	}
	insertCompletedPresentationRun(t, ctx, database, oldIncidentID, oldHash, PipelineVersion, now, values)
	cutover := now.Add(time.Minute)
	if err := database.EnsurePostProcessingScopes(ctx, []PostProcessingScope{{ProcessorKey: "public_assistance_verification", ScopeKey: "default"}}, cutover); err != nil {
		t.Fatal(err)
	}
	plan := PostProcessingPlan{ProcessorKey: "public_assistance_verification", ScopeKey: "default", PromptVersion: "assistance-v1", Model: "verify:4b", InputKinds: []string{"original_title", "incident_body", "public_assistance_status", "public_assistance_types"}}
	if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, false, now.Add(2*time.Minute)); err != nil || queued != 0 {
		t.Fatalf("automatic historical queue = %d/%v, want 0", queued, err)
	}

	insertPipelineDocuments(t, ctx, database, now.Add(3*time.Minute), "new")
	var newIncidentID int64
	var newHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents WHERE title_de='Titel new'`).Scan(&newIncidentID, &newHash); err != nil {
		t.Fatal(err)
	}
	insertCompletedPresentationRun(t, ctx, database, newIncidentID, newHash, PipelineVersion, now.Add(4*time.Minute), values)
	if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, false, now.Add(5*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("automatic post-cutover queue = %d/%v, want 1", queued, err)
	}
	if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, true, now.Add(6*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("manual historical backfill queue = %d/%v, want 1", queued, err)
	}
	manual, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, false, nil, now.Add(7*time.Minute))
	if err != nil || !found || manual.IncidentID != oldIncidentID || manual.RequestKind != "manual" {
		t.Fatalf("historical manual claim = %#v/%t/%v", manual, found, err)
	}
}

func TestEnsurePostProcessingScopesIsAtomic(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "post-processing-scopes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	processor := "atomic_test_processor"
	err = database.EnsurePostProcessingScopes(ctx, []PostProcessingScope{
		{ProcessorKey: processor, ScopeKey: "valid"},
		{ProcessorKey: processor, ScopeKey: ""},
	}, time.Now())
	if err == nil {
		t.Fatal("invalid scope initialization unexpectedly succeeded")
	}
	var count int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_scopes WHERE processor_key=?`, processor).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("partial scope initialization committed %d rows", count)
	}
}
