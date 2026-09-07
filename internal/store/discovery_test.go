package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Use synthetic, fully processed presentations so the benchmarks measure the
// overnight idle path, including repeated discovery of already satisfied work.
func discoveryFixture(t testing.TB, count int) (*Store, time.Time, PostProcessingPlan, []int64) {
	t.Helper()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "discovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Date(2026, 9, 7, 1, 0, 0, 0, time.UTC)
	if err := database.EnsurePostProcessingScopes(ctx, []PostProcessingScope{{ProcessorKey: "translation", ScopeKey: "en"}}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE post_processing_scopes SET automatic_after=?`, formatTime(now.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("synthetic-%d", i)
	}
	insertPipelineDocuments(t, ctx, database, now, ids...)
	plan := PostProcessingPlan{ProcessorKey: "translation", ScopeKey: "en", PromptVersion: "translation-v1", Model: "translate:4b", InputKinds: []string{"title_de", "summary_de"}}
	var runs []int64
	for _, id := range ids {
		var incidentID int64
		var sourceHash string
		if err := database.db.QueryRowContext(ctx, `SELECT i.id,i.content_hash FROM incidents i JOIN source_documents d ON d.id=i.source_document_id WHERE d.external_id=?`, id).Scan(&incidentID, &sourceHash); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{
			"title_de": "Synthetic title", "summary_de": strings.Repeat("Synthetic summary. ", 80),
			"title_en": "Synthetic translation", "summary_en": "Synthetic translated summary.",
		}))
	}
	return database, now, plan, runs
}

func TestIdleCanonicalDiscoveryDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	database, now, _, _ := discoveryFixture(t, 3)
	var before, after int
	if err := database.db.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", testPipelinePlans(), true, now.Add(time.Duration(i)*time.Second))
		if err != nil || found {
			t.Fatalf("idle discovery = %#v/%t/%v", cycle, found, err)
		}
	}
	if err := database.db.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("idle discovery wrote %d rows", after-before)
	}
	insertPipelineDocuments(t, ctx, database, now.Add(time.Minute), "new-arrival")
	if cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", testPipelinePlans(), true, now.Add(time.Minute)); err != nil || !found || cycle.Kind != "scheduled" {
		t.Fatalf("discovery after new arrival = %#v/%t/%v", cycle, found, err)
	}
}

func TestSatisfiedDiscoveryStillSupersedesOlderPendingJobs(t *testing.T) {
	for _, state := range []string{"pending", "running", "succeeded"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			database, now, plan, runs := discoveryFixture(t, 1)
			oldRun := runs[0]
			var incidentID int64
			var sourceHash string
			if err := database.db.QueryRowContext(ctx, `SELECT incident_id,source_hash FROM presentation_runs WHERE id=?`, oldRun).Scan(&incidentID, &sourceHash); err != nil {
				t.Fatal(err)
			}
			if _, err := database.db.ExecContext(ctx, `UPDATE post_processing_jobs SET status='pending' WHERE presentation_run_id=?`, oldRun); err != nil {
				t.Fatal(err)
			}
			newRun := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now.Add(time.Minute), map[string]string{
				"title_de": "New title", "summary_de": "New summary.", "title_en": "New translation", "summary_en": "Translated summary.",
			})
			if _, err := database.db.ExecContext(ctx, `UPDATE post_processing_jobs SET status=? WHERE presentation_run_id=?`, state, newRun); err != nil {
				t.Fatal(err)
			}
			if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, false, now.Add(2*time.Minute)); err != nil || queued != 0 {
				t.Fatalf("satisfied discovery = %d/%v", queued, err)
			}
			var oldState, newState string
			if err := database.db.QueryRowContext(ctx, `SELECT status FROM post_processing_jobs WHERE presentation_run_id=?`, oldRun).Scan(&oldState); err != nil {
				t.Fatal(err)
			}
			if err := database.db.QueryRowContext(ctx, `SELECT status FROM post_processing_jobs WHERE presentation_run_id=?`, newRun).Scan(&newState); err != nil {
				t.Fatal(err)
			}
			if oldState != "superseded" || newState != state {
				t.Fatalf("job states = %s/%s, want superseded/%s", oldState, newState, state)
			}
		})
	}
}

func BenchmarkIdleDiscovery(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("presentations=%d", count), func(b *testing.B) {
			database, now, plan, _ := discoveryFixture(b, count)
			ctx := context.Background()
			b.Run("canonical", func(b *testing.B) {
				plans := testPipelinePlans()
				b.ReportAllocs()
				for b.Loop() {
					if _, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, true, now); err != nil || found {
						b.Fatalf("idle canonical discovery = %t/%v", found, err)
					}
				}
			})
			b.Run("post_processing", func(b *testing.B) {
				plans := []PostProcessingPlan{plan}
				b.ReportAllocs()
				for b.Loop() {
					if count, err := database.QueuePostProcessingForAll(ctx, "fixture", plans, false, now); err != nil || count != 0 {
						b.Fatalf("idle post-processing discovery = %d/%v", count, err)
					}
				}
			})
		})
	}
}
