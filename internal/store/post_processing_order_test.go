package store

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestPostProcessingModelOrdering(t *testing.T) {
	type queuedJob struct {
		model, kind string
		age         int
		delayed     bool
	}
	tests := []struct {
		name    string
		jobs    []queuedJob
		options PostProcessingClaimOptions
		want    []int
	}{
		{"default FIFO", []queuedJob{{"A", "manual", 0, false}, {"B", "manual", 1, false}, {"A", "manual", 2, false}}, PostProcessingClaimOptions{}, []int{0, 1, 2}},
		{"model affinity across languages", []queuedJob{{"A", "manual", 0, false}, {"B", "manual", 1, false}, {"A", "manual", 2, false}}, PostProcessingClaimOptions{PreferredModel: "A"}, []int{0, 2, 1}},
		{"manual overrides affinity", []queuedJob{{"A", "scheduled", 0, false}, {"B", "manual", 1, false}, {"A", "manual", 2, false}}, PostProcessingClaimOptions{AllowScheduled: true, PreferredModel: "A"}, []int{2, 1, 0}},
		{"yield within priority group", []queuedJob{{"B", "scheduled", 0, false}, {"A", "manual", 1, false}, {"B", "manual", 2, false}}, PostProcessingClaimOptions{AllowScheduled: true, YieldModel: "A"}, []int{2, 1, 0}},
		{"oldest alternative then ID", []queuedJob{{"A", "manual", 0, false}, {"B", "manual", 3, false}, {"C", "manual", 1, false}, {"C", "manual", 1, false}}, PostProcessingClaimOptions{YieldModel: "A"}, []int{2, 3, 1, 0}},
		{"yield falls back to same model", []queuedJob{{"A", "manual", 0, false}, {"A", "manual", 1, false}}, PostProcessingClaimOptions{YieldModel: "A"}, []int{0, 1}},
		{"retry delayed preferred model", []queuedJob{{"A", "manual", 0, true}, {"B", "manual", 1, false}}, PostProcessingClaimOptions{PreferredModel: "A"}, []int{1}},
		{"blocked preferred model", []queuedJob{{"A", "manual", 0, false}, {"B", "manual", 1, false}}, PostProcessingClaimOptions{PreferredModel: "A", BlockedModels: []string{"A"}}, []int{1}},
		{"closed automatic window", []queuedJob{{"A", "scheduled", 0, false}, {"B", "manual", 1, false}}, PostProcessingClaimOptions{PreferredModel: "A"}, []int{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "order.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			now := time.Now().UTC().Add(time.Minute)
			insertPipelineDocuments(t, ctx, database, now, "ordering")
			var incidentID int64
			var sourceHash string
			if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
				t.Fatal(err)
			}
			runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{"title_de": "Titel"})
			contracts := PostProcessingContract{}
			ids := make(map[int64]int)
			for index, fixture := range tt.jobs {
				scope := fmt.Sprintf("language_%d", index)
				if err := database.EnsurePostProcessingScopes(ctx, []PostProcessingScope{{ProcessorKey: "translation", ScopeKey: scope}}, now); err != nil {
					t.Fatal(err)
				}
				if _, err := database.SetPostProcessingScopeEnabled(ctx, "translation", scope, true, now); err != nil {
					t.Fatal(err)
				}
				plan := PostProcessingPlan{ProcessorKey: "translation", ScopeKey: scope, Model: fixture.model, AdapterKey: fmt.Sprintf("adapter_%d", index), PromptVersion: "test-v1", InputKinds: []string{"title_de"}}
				if count, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, fixture.kind, false, now.Add(time.Duration(fixture.age)*time.Second)); err != nil || count != 1 {
					t.Fatalf("queue = %d/%v", count, err)
				}
				contracts[scope] = PostProcessingScopeContract{PromptVersion: plan.PromptVersion, InputKinds: plan.InputKinds, OutputKinds: []string{"note"}}
				var id int64
				if err := database.db.QueryRowContext(ctx, `SELECT id FROM post_processing_jobs WHERE scope_key=?`, scope).Scan(&id); err != nil {
					t.Fatal(err)
				}
				ids[id] = index
				if fixture.delayed {
					if _, err := database.db.ExecContext(ctx, `UPDATE post_processing_jobs SET next_retry_at=? WHERE id=?`, formatTime(now.Add(time.Hour)), id); err != nil {
						t.Fatal(err)
					}
				}
			}
			for range 2 {
				preview, found, err := database.PeekPostProcessingJob(ctx, "translation", contracts, tt.options, now.Add(time.Minute))
				if err != nil || !found || ids[preview.ID] != tt.want[0] || preview.AttemptCount != 0 {
					t.Fatalf("preview = %#v/%t/%v", preview, found, err)
				}
			}
			var attempts int
			if err := database.db.QueryRowContext(ctx, `SELECT SUM(attempt_count) FROM post_processing_jobs`).Scan(&attempts); err != nil || attempts != 0 {
				t.Fatalf("preview mutated attempts = %d/%v", attempts, err)
			}
			var got []int
			for range len(tt.jobs) + 1 {
				job, found, err := database.ClaimPostProcessingJob(ctx, "translation", contracts, tt.options, now.Add(time.Minute))
				if err != nil {
					t.Fatal(err)
				}
				if !found {
					break
				}
				index := ids[job.ID]
				got = append(got, index)
				if job.ModelIdentity != tt.jobs[index].model || job.AdapterKey != fmt.Sprintf("adapter_%d", index) || job.PromptVersion != "test-v1" {
					t.Fatalf("frozen provenance changed: %#v", job)
				}
				if err := database.CompletePostProcessingJob(ctx, job, []PipelineValue{{Kind: "note", Value: "Done"}}, job.ModelIdentity, job.InputHash, now.Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("claim order = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPostProcessingClaimContinuesPastMissingInput(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "missing-input-order.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	insertPipelineDocuments(t, ctx, database, now, "missing-input")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{"title_de": "Titel", "summary_de": "Summary"})
	plans := []PostProcessingPlan{
		{ProcessorKey: "translation", ScopeKey: "en", PromptVersion: "test-v1", Model: "A", InputKinds: []string{"summary_de"}},
		{ProcessorKey: "translation", ScopeKey: "tr", PromptVersion: "test-v1", Model: "B", InputKinds: []string{"title_de"}},
	}
	if count, err := database.QueuePostProcessingForRun(ctx, runID, plans, "manual", false, now); err != nil || count != 2 {
		t.Fatalf("queue=%d/%v", count, err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM presentation_values WHERE presentation_run_id=? AND kind='summary_de'`, runID); err != nil {
		t.Fatal(err)
	}
	contracts := PostProcessingContract{}
	for _, plan := range plans {
		contracts[plan.ScopeKey] = PostProcessingScopeContract{PromptVersion: plan.PromptVersion, InputKinds: plan.InputKinds, OutputKinds: []string{"note"}}
	}
	preview, previewFound, previewErr := database.PeekPostProcessingJob(ctx, "translation", contracts, PostProcessingClaimOptions{PreferredModel: "A"}, now)
	if previewErr != nil || !previewFound || preview.ModelIdentity != "B" {
		t.Fatalf("preview missing inputs = %#v/%t/%v", preview, previewFound, previewErr)
	}
	var pending int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_jobs WHERE status='pending'`).Scan(&pending); err != nil || pending != 2 {
		t.Fatalf("preview changed statuses = %d/%v", pending, err)
	}
	job, found, err := database.ClaimPostProcessingJob(ctx, "translation", contracts, PostProcessingClaimOptions{PreferredModel: "A"}, now)
	if err != nil || !found || job.ModelIdentity != "B" {
		t.Fatalf("claim after missing preferred input=%#v/%t/%v", job, found, err)
	}
	var status string
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM post_processing_jobs WHERE scope_key='en'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "skipped" {
		t.Fatalf("missing input job=%s", status)
	}
}
