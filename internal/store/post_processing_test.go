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
	visible, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{PromptVersion: PipelineVersion, TranslationLanguage: "en"})
	if err != nil || visible.AITranslatedTitle != "First title" {
		t.Fatalf("successful fallback during replacement = %#v/%v", visible, err)
	}
	replacement, found, err := database.ClaimPostProcessingJob(ctx, "translation", false, nil, now.Add(7*time.Minute))
	if err != nil || !found || replacement.RequestKind != "manual" {
		t.Fatalf("manual replacement claim = %#v/%t/%v", replacement, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, replacement, []PipelineValue{{Kind: "title", Value: "Second title"}, {Kind: "summary", Value: "Second summary."}}, "translate:4b", replacement.InputHash, now.Add(8*time.Minute)); err != nil {
		t.Fatal(err)
	}
	visible, err = database.GetPresentationIncident(ctx, incidentID, PresentationScope{PromptVersion: PipelineVersion, TranslationLanguage: "en"})
	if err != nil || visible.AITranslatedTitle != "Second title" || visible.AITranslationFallback {
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
