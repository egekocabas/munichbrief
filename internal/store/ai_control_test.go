package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAIControlDefaultsEnabledAndPersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ai-control.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	state, err := database.AIControl(ctx)
	if err != nil || !state.AutomaticProcessingEnabled {
		t.Fatalf("default AI control = %#v/%v", state, err)
	}
	if err := database.SetAutomaticProcessing(ctx, false, now); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	state, err = database.AIControl(ctx)
	if err != nil || state.AutomaticProcessingEnabled || !state.UpdatedAt.Equal(now) {
		t.Fatalf("persisted AI control = %#v/%v", state, err)
	}
}

func TestAutomaticControlGatesScheduledButNotManualCanonicalWork(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "ai-control-canonical.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 14, 15, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one", "two")
	plans := testPipelinePlans()
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, true, now)
	if err != nil || !found || cycle.Kind != "scheduled" {
		t.Fatalf("activate scheduled cycle = %#v/%t/%v", cycle, found, err)
	}
	job, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now)
	if err != nil || !found {
		t.Fatalf("claim first scheduled job = %#v/%t/%v", job, found, err)
	}
	if err := database.SetAutomaticProcessing(ctx, false, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := database.CompletePipelineJob(ctx, job, []PipelineValue{{Kind: "category", Value: "other"}}, job.ModelIdentity, HashPipelineInput("one"), now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now.Add(3*time.Second)); err != nil || found {
		t.Fatalf("disabled scheduled claim found=%t err=%v", found, err)
	}
	if suspended, err := database.SuspendAutomaticCycle(ctx, cycle.ID, now.Add(3*time.Second)); err != nil || !suspended {
		t.Fatalf("suspend scheduled cycle = %t/%v", suspended, err)
	}
	manual, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, nil, nil, true, now.Add(4*time.Second))
	if err != nil || manual.Requested != 2 {
		t.Fatalf("queue manual cycle = %#v/%v", manual, err)
	}
	active, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, true, now.Add(5*time.Second))
	if err != nil || !found || active.ID != manual.CycleID || active.Kind != "manual" {
		t.Fatalf("activate manual while disabled = %#v/%t/%v", active, found, err)
	}
}

func TestReenabledAutomaticControlResumesFrozenCycleOutsideWindow(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "ai-control-resume.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 14, 20, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	plans := testPipelinePlans()
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, true, now)
	if err != nil || !found {
		t.Fatalf("activate scheduled cycle = %#v/%t/%v", cycle, found, err)
	}
	if err := database.SetAutomaticProcessing(ctx, false, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, false, now.Add(2*time.Second)); err != nil || found {
		t.Fatalf("disabled activation outside window found=%t err=%v", found, err)
	}
	if err := database.SetAutomaticProcessing(ctx, true, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	resumed, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, false, now.Add(4*time.Second))
	if err != nil || !found || resumed.ID != cycle.ID || resumed.Kind != "scheduled" {
		t.Fatalf("resume frozen cycle outside window = %#v/%t/%v", resumed, found, err)
	}
}

func TestCanceledAutomaticWorkIsRediscoveredAfterReenable(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "ai-control-rediscovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 14, 25, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	plans := testPipelinePlans()
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, true, now)
	if err != nil || !found {
		t.Fatalf("activate scheduled cycle = %#v/%t/%v", cycle, found, err)
	}
	if _, err := database.CancelAllAIWork(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := database.SetAutomaticProcessing(ctx, true, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	rediscovered, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, true, now.Add(3*time.Second))
	if err != nil || !found || rediscovered.ID == cycle.ID || rediscovered.Kind != "scheduled" {
		t.Fatalf("rediscovered scheduled work = %#v/%t/%v", rediscovered, found, err)
	}
}

func TestAutomaticControlGatesScheduledPostProcessingClaimsAndDiscovery(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "ai-control-post.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 14, 30, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{"title_de": "Titel", "summary_de": "Zusammenfassung."})
	plan := PostProcessingPlan{ProcessorKey: "translation", ScopeKey: "en", PromptVersion: "translation-v1", Model: "translate:4b", InputKinds: []string{"title_de", "summary_de"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "scheduled", false, now); err != nil || queued != 1 {
		t.Fatalf("queue scheduled post-processing = %d/%v", queued, err)
	}
	if err := database.SetAutomaticProcessing(ctx, false, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	contract := testPostProcessingContract("en", plan.PromptVersion, []string{"title", "summary"}, plan.InputKinds...)
	if _, found, err := database.ClaimPostProcessingJob(ctx, "translation", contract, true, nil, now.Add(2*time.Second)); err != nil || found {
		t.Fatalf("disabled scheduled post-processing claim found=%t err=%v", found, err)
	}
	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{{ProcessorKey: "category_verification", ScopeKey: "default", PromptVersion: "category-v1", Model: "verify:4b", InputKinds: []string{"title_de", "summary_de"}}}, "scheduled", false, now.Add(2*time.Second)); err != nil || queued != 0 {
		t.Fatalf("disabled scheduled completion enqueue = %d/%v", queued, err)
	}
	if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{{ProcessorKey: "category_verification", ScopeKey: "default", PromptVersion: "category-v1", Model: "verify:4b", InputKinds: []string{"title_de", "summary_de"}}}, false, now.Add(2*time.Second)); err != nil || queued != 0 {
		t.Fatalf("disabled scheduled discovery = %d/%v", queued, err)
	}
	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{{ProcessorKey: "category_verification", ScopeKey: "default", PromptVersion: "category-v1", Model: "verify:4b", InputKinds: []string{"title_de", "summary_de"}}}, "manual", false, now.Add(3*time.Second)); err != nil || queued != 1 {
		t.Fatalf("disabled manual completion enqueue = %d/%v", queued, err)
	}
}

func TestStartupRecoveryWhileDisabledRequeuesWithoutRestartingAutomaticWork(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "ai-control-disabled-recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 14, 45, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", testPipelinePlans(), true, now)
	if err != nil || !found {
		t.Fatalf("activate scheduled cycle = %#v/%t/%v", cycle, found, err)
	}
	canonical, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now)
	if err != nil || !found {
		t.Fatalf("claim canonical job = %#v/%t/%v", canonical, found, err)
	}
	completedRun := insertCompletedPresentationRun(t, ctx, database, canonical.IncidentID, canonical.SourceHash, PipelineVersion, now, map[string]string{"title_de": "Titel", "summary_de": "Zusammenfassung."})
	postPlan := PostProcessingPlan{ProcessorKey: "translation", ScopeKey: "en", PromptVersion: "translation-v1", Model: "translate:4b", InputKinds: []string{"title_de", "summary_de"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, completedRun, []PostProcessingPlan{postPlan}, "scheduled", false, now); err != nil || queued != 1 {
		t.Fatalf("queue scheduled post-processing = %d/%v", queued, err)
	}
	contract := testPostProcessingContract("en", postPlan.PromptVersion, []string{"title", "summary"}, postPlan.InputKinds...)
	postJob, found, err := database.ClaimPostProcessingJob(ctx, "translation", contract, true, nil, now)
	if err != nil || !found {
		t.Fatalf("claim post-processing job = %#v/%t/%v", postJob, found, err)
	}
	if err := database.SetAutomaticProcessing(ctx, false, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := database.RecoverPipeline(ctx, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := database.RecoverPostProcessing(ctx, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", testPipelinePlans(), true, now.Add(3*time.Second)); err != nil || found {
		t.Fatalf("disabled recovered cycle activation found=%t err=%v", found, err)
	}
	if _, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now.Add(3*time.Second)); err != nil || found {
		t.Fatalf("disabled recovered canonical claim found=%t err=%v", found, err)
	}
	if _, found, err := database.ClaimPostProcessingJob(ctx, "translation", contract, true, nil, now.Add(3*time.Second)); err != nil || found {
		t.Fatalf("disabled recovered post-processing claim found=%t err=%v", found, err)
	}
	var cycleStatus, canonicalStatus, postStatus string
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM processing_cycles WHERE id=?`, cycle.ID).Scan(&cycleStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM processing_step_jobs WHERE id=?`, canonical.ID).Scan(&canonicalStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM post_processing_jobs WHERE id=?`, postJob.ID).Scan(&postStatus); err != nil {
		t.Fatal(err)
	}
	if cycleStatus != "queued" || canonicalStatus != "pending" || postStatus != "pending" {
		t.Fatalf("disabled recovery states = cycle:%s canonical:%s post:%s", cycleStatus, canonicalStatus, postStatus)
	}
}

func TestCancelAllAIWorkIsAtomicAuditableAndIdempotent(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "cancel-all.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	plans := testPipelinePlans()
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, true, now)
	if err != nil || !found {
		t.Fatalf("activate cycle = %#v/%t/%v", cycle, found, err)
	}
	job, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now)
	if err != nil || !found {
		t.Fatalf("claim canonical job = %#v/%t/%v", job, found, err)
	}
	completedRun := insertCompletedPresentationRun(t, ctx, database, job.IncidentID, job.SourceHash, PipelineVersion, now, map[string]string{"title_de": "Previous title", "summary_de": "Previous summary."})
	postPlan := PostProcessingPlan{ProcessorKey: "translation", ScopeKey: "en", PromptVersion: "translation-v1", Model: "translate:4b", InputKinds: []string{"title_de", "summary_de"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, completedRun, []PostProcessingPlan{postPlan}, "manual", false, now); err != nil || queued != 1 {
		t.Fatalf("queue post-processing = %d/%v", queued, err)
	}
	postJob, found, err := database.ClaimPostProcessingJob(ctx, "translation", testPostProcessingContract("en", postPlan.PromptVersion, []string{"title", "summary"}, postPlan.InputKinds...), false, nil, now)
	if err != nil || !found {
		t.Fatalf("claim post-processing = %#v/%t/%v", postJob, found, err)
	}

	canceled, err := database.CancelAllAIWork(ctx, now.Add(time.Second))
	if err != nil || canceled.Cycles != 1 || canceled.CanonicalJobs != 2 || canceled.PostProcessingJobs != 1 {
		t.Fatalf("cancel all = %#v/%v", canceled, err)
	}
	state, err := database.AIControl(ctx)
	if err != nil || state.AutomaticProcessingEnabled {
		t.Fatalf("control after cancel = %#v/%v", state, err)
	}
	if err := database.CompletePipelineJob(ctx, job, []PipelineValue{{Kind: "category", Value: "other"}}, job.ModelIdentity, HashPipelineInput("late"), now.Add(2*time.Second)); !errors.Is(err, ErrJobNotRunning) {
		t.Fatalf("late canonical completion error = %v", err)
	}
	if err := database.CompletePostProcessingJob(ctx, postJob, []PipelineValue{{Kind: "title", Value: "Late"}, {Kind: "summary", Value: "Late summary."}}, postJob.ModelIdentity, postJob.InputHash, now.Add(2*time.Second)); !errors.Is(err, ErrJobNotRunning) {
		t.Fatalf("late post-processing completion error = %v", err)
	}
	var canonicalValues, postValues int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM presentation_values WHERE presentation_run_id=?`, job.PresentationRunID).Scan(&canonicalValues); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_values WHERE job_id=?`, postJob.ID).Scan(&postValues); err != nil {
		t.Fatal(err)
	}
	if canonicalValues != 0 || postValues != 0 {
		t.Fatalf("late values committed: canonical=%d post=%d", canonicalValues, postValues)
	}
	var completedStatus, cycleStatus, cycleReason, jobStatus, jobReason, postStatus, postReason string
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM presentation_runs WHERE id=?`, completedRun).Scan(&completedStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT status,failure_kind FROM processing_cycles WHERE id=?`, cycle.ID).Scan(&cycleStatus, &cycleReason); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT status,failure_kind FROM processing_step_jobs WHERE id=?`, job.ID).Scan(&jobStatus, &jobReason); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT status,status_reason FROM post_processing_jobs WHERE id=?`, postJob.ID).Scan(&postStatus, &postReason); err != nil {
		t.Fatal(err)
	}
	if completedStatus != "complete" || cycleStatus != "superseded" || cycleReason != ProcessingStatusReasonOperatorCanceled || jobStatus != "superseded" || jobReason != ProcessingStatusReasonOperatorCanceled || postStatus != "superseded" || postReason != ProcessingStatusReasonOperatorCanceled {
		t.Fatalf("cancel audit states = completed:%s cycle:%s/%s job:%s/%s post:%s/%s", completedStatus, cycleStatus, cycleReason, jobStatus, jobReason, postStatus, postReason)
	}
	again, err := database.CancelAllAIWork(ctx, now.Add(3*time.Second))
	if err != nil || again != (PipelineCancellationResult{}) {
		t.Fatalf("idempotent cancel = %#v/%v", again, err)
	}
}

func TestCancelAllPreservesCanonicalAndPostProcessingCompletionsThatCommitFirst(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "cancel-all-completion-first.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 15, 15, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", testPipelinePlans(), true, now)
	if err != nil || !found {
		t.Fatalf("activate cycle = %#v/%t/%v", cycle, found, err)
	}
	canonical, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now)
	if err != nil || !found {
		t.Fatalf("claim canonical job = %#v/%t/%v", canonical, found, err)
	}
	if err := database.CompletePipelineJob(ctx, canonical, []PipelineValue{{Kind: "category", Value: "other"}}, canonical.ModelIdentity, HashPipelineInput("accepted"), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	completedRun := insertCompletedPresentationRun(t, ctx, database, canonical.IncidentID, canonical.SourceHash, PipelineVersion, now, map[string]string{"title_de": "Titel", "summary_de": "Zusammenfassung."})
	postPlan := PostProcessingPlan{ProcessorKey: "translation", ScopeKey: "en", PromptVersion: "translation-v1", Model: "translate:4b", InputKinds: []string{"title_de", "summary_de"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, completedRun, []PostProcessingPlan{postPlan}, "manual", false, now); err != nil || queued != 1 {
		t.Fatalf("queue post-processing = %d/%v", queued, err)
	}
	postJob, found, err := database.ClaimPostProcessingJob(ctx, "translation", testPostProcessingContract("en", postPlan.PromptVersion, []string{"title", "summary"}, postPlan.InputKinds...), false, nil, now)
	if err != nil || !found {
		t.Fatalf("claim post-processing = %#v/%t/%v", postJob, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, postJob, []PipelineValue{{Kind: "title", Value: "Accepted"}, {Kind: "summary", Value: "Accepted summary."}}, postJob.ModelIdentity, postJob.InputHash, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	canceled, err := database.CancelAllAIWork(ctx, now.Add(2*time.Second))
	if err != nil || canceled.Cycles != 1 || canceled.CanonicalJobs != 1 || canceled.PostProcessingJobs != 0 {
		t.Fatalf("completion-first cancel all = %#v/%v", canceled, err)
	}
	var canonicalStatus, postStatus, completedRunStatus string
	var canonicalValues, postValues int
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM processing_step_jobs WHERE id=?`, canonical.ID).Scan(&canonicalStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM presentation_values WHERE presentation_run_id=?`, canonical.PresentationRunID).Scan(&canonicalValues); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM post_processing_jobs WHERE id=?`, postJob.ID).Scan(&postStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_values WHERE job_id=?`, postJob.ID).Scan(&postValues); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM presentation_runs WHERE id=?`, completedRun).Scan(&completedRunStatus); err != nil {
		t.Fatal(err)
	}
	if canonicalStatus != "succeeded" || canonicalValues != 1 || postStatus != "succeeded" || postValues != 2 || completedRunStatus != "complete" {
		t.Fatalf("completion-first preservation = canonical:%s/%d post:%s/%d run:%s", canonicalStatus, canonicalValues, postStatus, postValues, completedRunStatus)
	}
}
