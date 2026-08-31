package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAdminTranslationOperationsSeparatePublicationFromLatestAttempt(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "translation-operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Date(2026, time.August, 31, 8, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "never", "active", "retained", "failed", "review", "skipped", "stale")

	type target struct {
		id    int64
		hash  string
		runID int64
	}
	rows, err := database.db.QueryContext(ctx, `SELECT i.id,i.content_hash,d.external_id FROM incidents i JOIN source_documents d ON d.id=i.source_document_id ORDER BY d.external_id`)
	if err != nil {
		t.Fatal(err)
	}
	targets := make(map[string]*target)
	for rows.Next() {
		var item target
		var externalID string
		if err := rows.Scan(&item.id, &item.hash, &externalID); err != nil {
			t.Fatal(err)
		}
		targets[externalID] = &item
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for name, item := range targets {
		item.runID = insertCompletedPresentationRun(t, ctx, database, item.id, item.hash, PipelineVersion, now.Add(time.Minute), map[string]string{
			"title_de": "Canonical " + name, "summary_de": "Canonical summary.", "privacy_status": "safe", "privacy_flags": "[]",
		})
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE incidents SET content_hash='new-stale-hash' WHERE id=?`, targets["stale"].id); err != nil {
		t.Fatal(err)
	}

	insertAttempt := func(name, status string, at time.Time, values bool) {
		t.Helper()
		completedAt := any(nil)
		if status == "succeeded" {
			completedAt = formatTime(at)
		}
		result, err := database.db.ExecContext(ctx, `INSERT INTO post_processing_jobs(
			presentation_run_id,processor_key,scope_key,request_kind,status,status_reason,status_detail,
			model_identity,prompt_version,input_hash,attempt_count,completed_at,created_at,updated_at
		) VALUES(?,'translation','en','manual',?,'test_reason','test_detail','translate:4b','incident-translation-en-v1','hash',1,?,?,?)`,
			targets[name].runID, status, completedAt, formatTime(at), formatTime(at))
		if err != nil {
			t.Fatal(err)
		}
		if !values {
			return
		}
		jobID, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(ctx, `INSERT INTO post_processing_values(job_id,kind,value) VALUES(?,'title',?),(?,'summary',?)`, jobID, "Published "+name, jobID, "Published summary."); err != nil {
			t.Fatal(err)
		}
	}
	insertAttempt("active", "pending", now.Add(2*time.Minute), false)
	insertAttempt("retained", "succeeded", now.Add(2*time.Minute), true)
	insertAttempt("retained", "failed", now.Add(3*time.Minute), false)
	insertAttempt("failed", "failed", now.Add(2*time.Minute), false)
	insertAttempt("review", "needs_review", now.Add(2*time.Minute), false)
	insertAttempt("skipped", "skipped", now.Add(2*time.Minute), false)

	canonical, coverage, err := database.AdminTranslationCoverage(ctx, "fixture", []string{"en", "fr"})
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Total != 7 || canonical.Published != 6 {
		t.Fatalf("canonical coverage = %#v, want 7 total and 6 published", canonical)
	}
	if len(coverage) != 2 || coverage[0] != (AdminLanguageCoverage{Language: "en", Eligible: 6, Published: 1, Unpublished: 5, NeverQueued: 1, Active: 1, Attention: 4, ReplacementAttention: 1}) || coverage[1] != (AdminLanguageCoverage{Language: "fr", Eligible: 6, Unpublished: 6, NeverQueued: 6}) {
		t.Fatalf("language coverage = %#v", coverage)
	}

	for _, test := range []struct {
		filter AdminTranslationFilter
		want   int
	}{
		{filter: AdminTranslationsAll, want: 6},
		{filter: AdminTranslationsPublished, want: 1},
		{filter: AdminTranslationsUnpublished, want: 5},
		{filter: AdminTranslationsNeverQueued, want: 1},
		{filter: AdminTranslationsActive, want: 1},
		{filter: AdminTranslationsAttention, want: 4},
	} {
		items, total, err := database.ListAdminTranslationIncidents(ctx, 2, 0, "fixture", "en", test.filter)
		if err != nil || total != test.want || len(items) != min(2, test.want) {
			t.Errorf("filter %s = %d/%d/%v, want total %d", test.filter, len(items), total, err, test.want)
		}
	}
	items, total, err := database.ListAdminTranslationIncidents(ctx, 1, 0, "fixture", "en", AdminTranslationsPublished)
	if err != nil || total != 1 || len(items) != 1 || items[0].Title != "Published retained" || items[0].Status != "failed" || items[0].AttemptModel != "translate:4b" {
		t.Fatalf("retained successful replacement state = %#v/%d/%v", items, total, err)
	}
	secondPage, total, err := database.ListAdminTranslationIncidents(ctx, 2, 2, "fixture", "en", AdminTranslationsAll)
	if err != nil || total != 6 || len(secondPage) != 2 {
		t.Fatalf("paginated translation incidents = %#v/%d/%v", secondPage, total, err)
	}
	if _, _, err := database.ListAdminTranslationIncidents(ctx, 1, 0, "fixture", "en", "invalid"); err == nil {
		t.Fatal("invalid translation filter accepted")
	}

	plan := PostProcessingPlan{ProcessorKey: "translation", ScopeKey: "en", PromptVersion: "incident-translation-en-v1", Model: "translate:4b", InputKinds: []string{"title_de", "summary_de"}}
	if err := database.SetAutomaticProcessing(ctx, false, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	queued, err := database.QueueUnpublishedPostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, now.Add(4*time.Minute))
	if err != nil || queued != 4 {
		t.Fatalf("queue unpublished = %d/%v, want four", queued, err)
	}
	_, after, err := database.AdminTranslationCoverage(ctx, "fixture", []string{"en"})
	if err != nil || len(after) != 1 || after[0].Published != 1 || after[0].Active != 5 || after[0].NeverQueued != 0 {
		t.Fatalf("coverage after unpublished queue = %#v/%v", after, err)
	}
}
