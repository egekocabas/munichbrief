package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func exhaustedPostProcessingFixture(t *testing.T, status, requestKind string) (*Store, time.Time, PostProcessingPlan, PostProcessingJob) {
	t.Helper()
	ctx := context.Background()
	database, now, plan, runs := discoveryFixture(t, 1)
	if _, err := database.db.ExecContext(ctx, `DELETE FROM post_processing_jobs WHERE presentation_run_id=?`, runs[0]); err != nil {
		t.Fatal(err)
	}
	if queued, err := database.QueuePostProcessingForRun(ctx, runs[0], []PostProcessingPlan{plan}, requestKind, false, now); err != nil || queued != 1 {
		t.Fatalf("initial queue = %d/%v", queued, err)
	}
	job, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract(plan.ScopeKey, plan.PromptVersion, []string{"title", "summary"}, plan.InputKinds...), PostProcessingClaimOptions{AllowScheduled: true}, now)
	if err != nil || !found {
		t.Fatalf("claim = %t/%v", found, err)
	}
	if err := database.FailPostProcessingJob(ctx, job, status, "output", nil, now, errors.New("invalid translation")); err != nil {
		t.Fatal(err)
	}
	return database, now, plan, job
}

func TestPostProcessingAutomaticQueuePreservesTerminalFailure(t *testing.T) {
	for _, status := range []string{"needs_review", "failed"} {
		for _, requestKind := range []string{"scheduled", "manual"} {
			t.Run(status+"/"+requestKind, func(t *testing.T) {
				ctx := context.Background()
				database, now, plan, job := exhaustedPostProcessingFixture(t, status, requestKind)
				for range 3 {
					now = now.Add(time.Hour)
					if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, false, now); err != nil || queued != 0 {
						t.Fatalf("discovery recreated terminal job: %d/%v", queued, err)
					}
					if queued, err := database.QueuePostProcessingForRun(ctx, job.PresentationRunID, []PostProcessingPlan{plan}, "scheduled", false, now); err != nil || queued != 0 {
						t.Fatalf("completion enqueue recreated terminal job: %d/%v", queued, err)
					}
				}
				var jobs int
				if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_jobs`).Scan(&jobs); err != nil || jobs != 1 {
					t.Fatalf("terminal history count = %d/%v", jobs, err)
				}
			})
		}
	}
}

func TestPostProcessingTerminalFailureAllowsChangedWorkAndManualRetry(t *testing.T) {
	for _, change := range []string{"model", "adapter", "prompt", "input", "manual", "unpublished"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			database, now, plan, original := exhaustedPostProcessingFixture(t, "needs_review", "scheduled")
			switch change {
			case "model":
				plan.Model = "replacement:4b"
			case "adapter":
				plan.AdapterKey = "translategemma"
			case "prompt":
				plan.PromptVersion = "translation-v2"
			case "input":
				if _, err := database.db.ExecContext(ctx, `UPDATE presentation_values SET value='Corrected German summary.' WHERE presentation_run_id=? AND kind='summary_de'`, original.PresentationRunID); err != nil {
					t.Fatal(err)
				}
			}
			var queued int
			var err error
			if change == "unpublished" {
				queued, err = database.QueueUnpublishedPostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, now)
			} else {
				queued, err = database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, change == "manual", now)
			}
			if err != nil || queued != 1 {
				t.Fatalf("queue after %s = %d/%v", change, queued, err)
			}
			job, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract(plan.ScopeKey, plan.PromptVersion, []string{"title", "summary"}, plan.InputKinds...), PostProcessingClaimOptions{AllowScheduled: true}, now)
			if err != nil || !found || job.ID == original.ID {
				t.Fatalf("replacement claim = %#v/%t/%v", job, found, err)
			}
			if change == "manual" || change == "unpublished" {
				if job.RequestKind != "manual" || job.InputHash != original.InputHash {
					t.Fatalf("manual retry = %#v", job)
				}
			} else if job.InputHash == original.InputHash {
				t.Fatal("changed work retained the exhausted input hash")
			}
		})
	}
}

func TestPostProcessingExistingScheduledRepeatCanFinish(t *testing.T) {
	ctx := context.Background()
	database, now, plan, original := exhaustedPostProcessingFixture(t, "needs_review", "scheduled")
	if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, true, now); err != nil || queued != 1 {
		t.Fatalf("prepare existing repeat = %d/%v", queued, err)
	}
	// Reproduce a pending scheduled replacement created before the fix.
	if _, err := database.db.ExecContext(ctx, `UPDATE post_processing_jobs SET request_kind='scheduled' WHERE status='pending'`); err != nil {
		t.Fatal(err)
	}
	if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, false, now); err != nil || queued != 0 {
		t.Fatalf("discovery with existing repeat = %d/%v", queued, err)
	}
	job, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract(plan.ScopeKey, plan.PromptVersion, []string{"title", "summary"}, plan.InputKinds...), PostProcessingClaimOptions{AllowScheduled: true}, now)
	if err != nil || !found || job.ID == original.ID || job.RequestKind != "scheduled" {
		t.Fatalf("existing repeat claim = %#v/%t/%v", job, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, job, []PipelineValue{{Kind: "title", Value: "Title"}, {Kind: "summary", Value: "Summary."}}, job.ModelIdentity, job.InputHash, now); err != nil {
		t.Fatal(err)
	}
	if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, false, now); err != nil || queued != 0 {
		t.Fatalf("discovery after existing repeat completes = %d/%v", queued, err)
	}
}
