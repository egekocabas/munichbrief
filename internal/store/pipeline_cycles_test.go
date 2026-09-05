package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
)

func TestPipelineFreezesTargetsGroupsStepsAndStartsNextCycleImmediately(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "pipeline.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one", "two")
	plans := testPipelinePlans()
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, true, now)
	if err != nil || !found || cycle.Kind != "scheduled" {
		t.Fatalf("activate scheduled cycle = %#v/%t/%v", cycle, found, err)
	}
	insertPipelineDocuments(t, ctx, database, now.Add(time.Minute), "three")

	var metadataIDs []int64
	for {
		job, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
		metadataIDs = append(metadataIDs, job.IncidentID)
		values := []PipelineValue{{Kind: "category", Value: "other"}, {Kind: "report_kind", Value: "incident"}, {Kind: "public_assistance_status", Value: "not_requested"}, {Kind: "public_assistance_types", Value: "[]"}}
		if err := database.CompletePipelineJob(ctx, job, values, job.ModelIdentity, HashPipelineInput(job.SourceHash), now); err != nil {
			t.Fatal(err)
		}
	}
	if len(metadataIDs) != 2 {
		t.Fatalf("frozen metadata targets = %v, want two", metadataIDs)
	}
	advance, err := database.AdvancePipelineCycle(ctx, cycle, 3, now)
	if err != nil || !advance.Advanced {
		t.Fatalf("advance to German presentation = %#v/%v", advance, err)
	}
	cycle.ActiveStep = 1
	germanPresentations := 0
	var completedRuns []int64
	for {
		job, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 1, now)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
		germanPresentations++
		completedRuns = append(completedRuns, job.PresentationRunID)
		if job.InputValues["category"] != "other" {
			t.Fatalf("German metadata input = %#v", job.InputValues)
		}
		if err := database.CompletePipelineJob(ctx, job, []PipelineValue{{Kind: "title_de", Value: "Titel"}, {Kind: "summary_de", Value: "Zusammenfassung."}, {Kind: "privacy_status", Value: "safe"}, {Kind: "privacy_flags", Value: "[]"}}, job.ModelIdentity, HashPipelineInput(job.SourceHash), now); err != nil {
			t.Fatal(err)
		}
	}
	if germanPresentations != 2 {
		t.Fatalf("German presentations = %d, want 2", germanPresentations)
	}
	advance, err = database.AdvancePipelineCycle(ctx, cycle, len(plans), now)
	if err != nil || !advance.Completed {
		t.Fatalf("complete canonical cycle = %#v/%v", advance, err)
	}
	for _, runID := range completedRuns {
		if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{{ProcessorKey: "translation", ScopeKey: "en", PromptVersion: "incident-translation-en-v1", Model: "translate:4b", InputKinds: []string{"title_de", "summary_de"}}}, "scheduled", false, now); err != nil || queued != 1 {
			t.Fatalf("queue translation for run %d = %d/%v", runID, queued, err)
		}
	}
	translations := 0
	for {
		job, found, err := database.ClaimPostProcessingJob(ctx, "translation", testPostProcessingContract("en", "incident-translation-en-v1", []string{"title", "summary"}, "title_de", "summary_de"), true, nil, now)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
		translations++
		if job.InputValues["title_de"] != "Titel" || job.InputValues["summary_de"] != "Zusammenfassung." {
			t.Fatalf("translation input = %#v", job.InputValues)
		}
		if err := database.CompletePostProcessingJob(ctx, job, []PipelineValue{{Kind: "title", Value: "Title"}, {Kind: "summary", Value: "Summary."}}, job.ModelIdentity, job.InputHash, now); err != nil {
			t.Fatal(err)
		}
	}
	if translations != 2 {
		t.Fatalf("translations = %d, want 2", translations)
	}
	next, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, true, now)
	if err != nil || !found || next.ID == cycle.ID {
		t.Fatalf("immediate next cycle = %#v/%t/%v", next, found, err)
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", []string{"incident_metadata", "german_presentation"}, nil, now)
	if err != nil || snapshot.CycleTotal != 2 {
		t.Fatalf("next frozen snapshot = %#v/%v", snapshot, err)
	}
}

func TestManualCyclesCoalesceExactDuplicatesAndSupersedeQueuedAutomaticWork(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "manual-priority.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	plans := testPipelinePlans()

	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	condition, _ := sourceStatusCondition("fixture")
	automaticID, count, err := createPipelineCycleTx(ctx, tx, "scheduled", "fixture", true, "", condition+" AND COALESCE(i.body_de,'')<>''", nil, plans, nil, now)
	if err != nil || count != 1 {
		t.Fatalf("create queued scheduled cycle = %d/%d/%v", automaticID, count, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	postPlans := []PostProcessingPlan{{ProcessorKey: "translation", ScopeKey: "en", PromptVersion: "incident-translation-en-v1", Model: "translate:4b", InputKinds: []string{"title_de", "summary_de"}}}
	first, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, postPlans, nil, true, now.Add(time.Second))
	if err != nil || first.Requested != 1 {
		t.Fatalf("first manual request = %#v/%v", first, err)
	}
	var automaticStatus string
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM processing_cycles WHERE id=?`, automaticID).Scan(&automaticStatus); err != nil {
		t.Fatal(err)
	}
	if automaticStatus != "superseded" {
		t.Fatalf("queued automatic status = %q", automaticStatus)
	}
	duplicate, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, postPlans, nil, true, now.Add(2*time.Second))
	if err != nil || duplicate.CycleID != first.CycleID || duplicate.Requested != 0 || duplicate.Current != 1 {
		t.Fatalf("duplicate manual request = %#v/%v, first=%#v", duplicate, err, first)
	}
	differentAdapter := append([]PostProcessingPlan(nil), postPlans...)
	differentAdapter[0].AdapterKey = "hy-mt2"
	adapterRequest, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, differentAdapter, nil, true, now.Add(3*time.Second))
	if err != nil || adapterRequest.CycleID == first.CycleID {
		t.Fatalf("different-adapter manual request = %#v/%v", adapterRequest, err)
	}
	different := append([]PipelineStepPlan(nil), plans...)
	different[1].Model = "other-translate:4b"
	separate, err := database.CreateManualPipelineCycle(ctx, "fixture", different, postPlans, nil, true, now.Add(4*time.Second))
	if err != nil || separate.CycleID == first.CycleID {
		t.Fatalf("different-model manual request = %#v/%v", separate, err)
	}
}

func TestManualRequestDoesNotInterruptHealthyRunningScheduledCycle(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "healthy-priority.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	plans := testPipelinePlans()
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, true, now)
	if err != nil || !found {
		t.Fatalf("activate scheduled cycle = %#v/%t/%v", cycle, found, err)
	}
	if _, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, nil, nil, true, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM processing_cycles WHERE id=?`, cycle.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("healthy scheduled cycle was interrupted: %q", status)
	}
}

func TestBlockedContinuationReleasesLeaseToLaterManualCycle(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "continuation-manual-priority.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 10, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	plans := testPipelinePlans()
	scheduled, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, true, now)
	if err != nil || !found || scheduled.Kind != "scheduled" {
		t.Fatalf("activate scheduled cycle = %#v/%t/%v", scheduled, found, err)
	}
	firstManual, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, nil, nil, true, now.Add(time.Second))
	if err != nil || firstManual.Requested != 1 {
		t.Fatalf("first manual cycle = %#v/%v", firstManual, err)
	}
	continuationID, err := database.InterruptBlockedAutomaticCycle(ctx, scheduled.ID, now.Add(2*time.Second))
	if err != nil || continuationID == scheduled.ID {
		t.Fatalf("scheduled interruption = %d/%v", continuationID, err)
	}
	active, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, false, now.Add(3*time.Second))
	if err != nil || !found || active.ID != firstManual.CycleID {
		t.Fatalf("first manual activation = %#v/%t/%v", active, found, err)
	}
	formatted := formatTime(now.Add(4 * time.Second))
	if _, err := database.db.ExecContext(ctx, `UPDATE processing_step_jobs SET status='failed',completed_at=?,updated_at=? WHERE cycle_item_id IN (SELECT id FROM processing_cycle_items WHERE cycle_id=?)`, formatted, formatted, firstManual.CycleID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE processing_cycle_items SET status='failed',updated_at=? WHERE cycle_id=?`, formatted, firstManual.CycleID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE presentation_runs SET status='failed',completed_at=? WHERE id IN (SELECT presentation_run_id FROM processing_cycle_items WHERE cycle_id=?)`, formatted, firstManual.CycleID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE processing_cycles SET status='failed',completed_at=?,updated_at=? WHERE id=?`, formatted, formatted, firstManual.CycleID); err != nil {
		t.Fatal(err)
	}
	active, found, err = database.ActivateNextPipelineCycle(ctx, "fixture", plans, false, now.Add(5*time.Second))
	if err != nil || !found || active.ID != continuationID || active.Kind != "continuation" {
		t.Fatalf("continuation activation = %#v/%t/%v", active, found, err)
	}
	secondManual, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, nil, nil, true, now.Add(6*time.Second))
	if err != nil || secondManual.Requested != 1 {
		t.Fatalf("second manual cycle = %#v/%v", secondManual, err)
	}
	yieldedID, err := database.InterruptBlockedAutomaticCycle(ctx, continuationID, now.Add(7*time.Second))
	if err != nil || yieldedID != continuationID {
		t.Fatalf("continuation interruption = %d/%v", yieldedID, err)
	}
	active, found, err = database.ActivateNextPipelineCycle(ctx, "fixture", plans, false, now.Add(8*time.Second))
	if err != nil || !found || active.ID != secondManual.CycleID || active.Kind != "manual" {
		t.Fatalf("second manual activation = %#v/%t/%v", active, found, err)
	}
}

func TestPipelineRecoveryAndSourceRevisionSupersession(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "recover-supersede.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", testPipelinePlans(), true, now)
	if err != nil || !found {
		t.Fatalf("activate cycle = %#v/%t/%v", cycle, found, err)
	}
	job, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now)
	if err != nil || !found {
		t.Fatalf("claim before restart = %#v/%t/%v", job, found, err)
	}
	if err := database.RecoverPipeline(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	recovered, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now.Add(time.Minute))
	if err != nil || !found || recovered.ID != job.ID || recovered.AttemptCount != 2 {
		t.Fatalf("recovered job = %#v/%t/%v", recovered, found, err)
	}
	if err := database.CompletePipelineJob(ctx, recovered, []PipelineValue{{Kind: "title_de", Value: "Accepted DE"}, {Kind: "summary_de", Value: "Accepted summary."}, {Kind: "category", Value: "other"}, {Kind: "privacy_status", Value: "safe"}, {Kind: "privacy_flags", Value: "[]"}}, recovered.ModelIdentity, HashPipelineInput("de"), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	advance, err := database.AdvancePipelineCycle(ctx, cycle, 2, now.Add(3*time.Minute))
	if err != nil || !advance.Advanced {
		t.Fatalf("advance after recovery = %#v/%v", advance, err)
	}
	cycle.ActiveStep = 1
	revised := domain.SourceDocument{
		ExternalID: "one", SourceURL: "https://fixture.invalid/one", Title: "Release one", PublishedAt: now,
		FeedFingerprint: "feed-one-v2", SourceHash: "source-one-v2",
		Incidents: []domain.Incident{{Number: "1", Position: 0, TitleDE: "Titel one revised", BodyDE: "Revised body", ContentHash: "content-one-v2"}},
	}
	if err := database.UpsertDocuments(ctx, []domain.SourceDocument{revised}, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 1, now.Add(5*time.Minute)); err != nil || found {
		t.Fatalf("stale translation claim = found:%t err:%v", found, err)
	}
	var itemStatus, runStatus string
	if err := database.db.QueryRowContext(ctx, `SELECT ci.status,r.status FROM processing_cycle_items ci JOIN presentation_runs r ON r.id=ci.presentation_run_id WHERE ci.cycle_id=?`, cycle.ID).Scan(&itemStatus, &runStatus); err != nil {
		t.Fatal(err)
	}
	if itemStatus != "superseded" || runStatus != "superseded" {
		t.Fatalf("stale item/run = %q/%q", itemStatus, runStatus)
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", []string{"incident_metadata", "german_presentation"}, nil, now.Add(5*time.Minute))
	if err != nil || snapshot.ScheduledCandidates != 1 {
		t.Fatalf("revised source eligibility = %#v/%v", snapshot, err)
	}
}
