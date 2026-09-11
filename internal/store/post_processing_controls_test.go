package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPostProcessingScopeDefaultsAndOperatorChoice(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "scope-defaults.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 9, 11, 8, 0, 0, 0, time.UTC)
	scopes := []PostProcessingScope{
		{ProcessorKey: "translation", ScopeKey: "en"},
		{ProcessorKey: "translation", ScopeKey: "ru"},
		{ProcessorKey: "category_verification", ScopeKey: "default"},
		{ProcessorKey: "public_assistance_verification", ScopeKey: "default"},
	}
	if err := database.EnsurePostProcessingScopes(ctx, scopes, now); err != nil {
		t.Fatal(err)
	}
	settings, err := database.PostProcessingScopeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byScope := make(map[string]PostProcessingScopeSetting, len(settings))
	for _, setting := range settings {
		byScope[setting.ProcessorKey+"/"+setting.ScopeKey] = setting
	}
	for identity, want := range map[string]bool{
		"translation/en": true, "translation/ru": false,
		"category_verification/default": true, "public_assistance_verification/default": true,
	} {
		if got := byScope[identity]; got.Enabled != want || got.AutomaticAfter.IsZero() || got.EnabledUpdatedAt.IsZero() {
			t.Fatalf("scope %s = %#v, want enabled=%t with timestamps", identity, got, want)
		}
	}
	russianCutover := byScope["translation/ru"].AutomaticAfter
	updated := now.Add(time.Hour)
	if _, err := database.SetPostProcessingScopeEnabled(ctx, "translation", "ru", true, updated); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsurePostProcessingScopes(ctx, scopes, updated.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	settings, err = database.PostProcessingScopeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range settings {
		if setting.ProcessorKey == "translation" && setting.ScopeKey == "ru" {
			if !setting.Enabled || !setting.AutomaticAfter.Equal(russianCutover) || !setting.EnabledUpdatedAt.Equal(updated) {
				t.Fatalf("operator choice was overwritten: %#v", setting)
			}
			return
		}
	}
	t.Fatal("Russian scope setting missing")
}

func TestDisabledScopeBlocksAutomaticButAllowsManualWork(t *testing.T) {
	ctx := context.Background()
	database, now, plan, runs := discoveryFixture(t, 1)
	if _, err := database.db.ExecContext(ctx, `DELETE FROM post_processing_jobs WHERE presentation_run_id=?`, runs[0]); err != nil {
		t.Fatal(err)
	}
	if queued, err := database.QueuePostProcessingForRun(ctx, runs[0], []PostProcessingPlan{plan}, "manual", true, now); err != nil || queued != 1 {
		t.Fatalf("queue manual work = %d/%v", queued, err)
	}
	if skipped, err := database.SetPostProcessingScopeEnabled(ctx, plan.ProcessorKey, plan.ScopeKey, false, now); err != nil || skipped != 0 {
		t.Fatalf("disable with queued manual work = %d/%v", skipped, err)
	}
	if queued, err := database.QueuePostProcessingForRun(ctx, runs[0], []PostProcessingPlan{plan}, "scheduled", false, now); err != nil || queued != 0 {
		t.Fatalf("disabled scheduled queue = %d/%v", queued, err)
	}
	job, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract(plan.ScopeKey, plan.PromptVersion, []string{"title", "summary"}, plan.InputKinds...), true, nil, now)
	if err != nil || !found || job.RequestKind != "manual" {
		t.Fatalf("manual claim while disabled = %#v/%t/%v", job, found, err)
	}
}

func TestDisableSkipsAutomaticQueueAndReenableDiscoversBacklog(t *testing.T) {
	ctx := context.Background()
	database, now, plan, runs := discoveryFixture(t, 2)
	if _, err := database.db.ExecContext(ctx, `DELETE FROM post_processing_jobs WHERE presentation_run_id IN (?,?)`, runs[0], runs[1]); err != nil {
		t.Fatal(err)
	}
	if err := database.SetAutomaticProcessing(ctx, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetPostProcessingScopeEnabled(ctx, plan.ProcessorKey, plan.ScopeKey, true, now); err != nil {
		t.Fatal(err)
	}
	for _, runID := range runs {
		if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "scheduled", false, now); err != nil || queued != 1 {
			t.Fatalf("queue automatic work = %d/%v", queued, err)
		}
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE post_processing_jobs SET attempt_count=1,next_retry_at=? WHERE presentation_run_id=?`, formatTime(now.Add(time.Hour)), runs[1]); err != nil {
		t.Fatal(err)
	}
	skipped, err := database.SetPostProcessingScopeEnabled(ctx, plan.ProcessorKey, plan.ScopeKey, false, now.Add(time.Minute))
	if err != nil || skipped != 2 {
		t.Fatalf("disable result = %d/%v", skipped, err)
	}
	var disabled int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_jobs WHERE status='skipped' AND status_reason=? AND next_retry_at IS NULL AND started_at IS NULL`, PostProcessingStatusReasonScopeDisabled).Scan(&disabled); err != nil {
		t.Fatal(err)
	}
	if disabled != 2 {
		t.Fatalf("disabled jobs = %d, want 2", disabled)
	}
	if skipped, err := database.SetPostProcessingScopeEnabled(ctx, plan.ProcessorKey, plan.ScopeKey, true, now.Add(2*time.Minute)); err != nil || skipped != 0 {
		t.Fatalf("enable result = %d/%v", skipped, err)
	}
	var jobs int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_jobs WHERE presentation_run_id IN (?,?)`, runs[0], runs[1]).Scan(&jobs); err != nil || jobs != 2 {
		t.Fatalf("enable unexpectedly queued work = %d/%v", jobs, err)
	}
	if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, false, now.Add(3*time.Minute)); err != nil || queued != 2 {
		t.Fatalf("backlog discovery = %d/%v", queued, err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_jobs WHERE presentation_run_id IN (?,?)`, runs[0], runs[1]).Scan(&jobs); err != nil || jobs != 4 {
		t.Fatalf("jobs after re-enable = %d/%v", jobs, err)
	}
}

func TestDisableLetsRunningAutomaticJobFinish(t *testing.T) {
	ctx := context.Background()
	database, now, plan, runs := discoveryFixture(t, 1)
	if _, err := database.db.ExecContext(ctx, `DELETE FROM post_processing_jobs WHERE presentation_run_id=?`, runs[0]); err != nil {
		t.Fatal(err)
	}
	if err := database.SetAutomaticProcessing(ctx, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetPostProcessingScopeEnabled(ctx, plan.ProcessorKey, plan.ScopeKey, true, now); err != nil {
		t.Fatal(err)
	}
	if queued, err := database.QueuePostProcessingForRun(ctx, runs[0], []PostProcessingPlan{plan}, "scheduled", false, now); err != nil || queued != 1 {
		t.Fatalf("queue automatic work = %d/%v", queued, err)
	}
	contract := testPostProcessingContract(plan.ScopeKey, plan.PromptVersion, []string{"title", "summary"}, plan.InputKinds...)
	job, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, contract, true, nil, now.Add(time.Minute))
	if err != nil || !found {
		t.Fatalf("claim automatic work = %#v/%t/%v", job, found, err)
	}
	if skipped, err := database.SetPostProcessingScopeEnabled(ctx, plan.ProcessorKey, plan.ScopeKey, false, now.Add(2*time.Minute)); err != nil || skipped != 0 {
		t.Fatalf("disable running scope = %d/%v", skipped, err)
	}
	values := []PipelineValue{{Kind: "title", Value: "Title"}, {Kind: "summary", Value: "Summary."}}
	if err := database.CompletePostProcessingJob(ctx, job, values, job.ModelIdentity, job.InputHash, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("complete running job: %v", err)
	}
	var status string
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM post_processing_jobs WHERE id=?`, job.ID).Scan(&status); err != nil || status != "succeeded" {
		t.Fatalf("running job status = %q/%v", status, err)
	}
}
