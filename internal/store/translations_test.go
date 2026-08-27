package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestTranslationJobsAreIndependentAuditableAndManualRetriesBypassWindow(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "translations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{
		"title_de": "Sicherer Titel", "summary_de": "Sichere Zusammenfassung.",
		"category": "other", "report_kind": "incident", "public_assistance_status": "not_requested", "public_assistance_types": "[]",
	})
	plan := TranslationPlan{Language: "en", PromptVersion: "incident-translation-en-v1", Model: "translate:a"}
	queued, err := database.QueueTranslationsForRun(ctx, runID, []TranslationPlan{plan}, "scheduled", now)
	if err != nil || queued != 1 {
		t.Fatalf("queue scheduled translation = %d/%v", queued, err)
	}
	if _, found, err := database.ClaimTranslationJob(ctx, false, now); err != nil || found {
		t.Fatalf("scheduled translation escaped closed window: found=%t err=%v", found, err)
	}
	job, found, err := database.ClaimTranslationJob(ctx, true, now)
	if err != nil || !found || job.ModelIdentity != "translate:a" || job.TitleDE != "Sicherer Titel" {
		t.Fatalf("claim frozen translation = %#v/%t/%v", job, found, err)
	}
	if err := database.FailTranslationJob(ctx, job, "failed", "output", nil, now.Add(time.Minute), errors.New("synthetic invalid output")); err != nil {
		t.Fatal(err)
	}

	queued, err = database.QueueIncidentTranslation(ctx, incidentID, TranslationPlan{Language: "en", PromptVersion: plan.PromptVersion, Model: "translate:b"}, now.Add(2*time.Minute))
	if err != nil || queued != 1 {
		t.Fatalf("queue manual retry = %d/%v", queued, err)
	}
	retry, found, err := database.ClaimTranslationJob(ctx, false, now.Add(2*time.Minute))
	if err != nil || !found || retry.RequestKind != "manual" || retry.ModelIdentity != "translate:b" {
		t.Fatalf("manual retry did not bypass window = %#v/%t/%v", retry, found, err)
	}
	if err := database.CompleteTranslationJob(ctx, retry, "Safe title", "Safe summary.", retry.ModelIdentity, retry.InputHash, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM presentation_translations WHERE presentation_run_id=? AND language_code='en'`, runID).Scan(&attempts); err != nil || attempts != 2 {
		t.Fatalf("translation audit attempts = %d/%v", attempts, err)
	}
	record, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{PromptVersion: PipelineVersion, Language: "en", PublicOnly: true})
	if err != nil || record.AITitleEN != "Safe title" || record.AITranslationModel != "translate:b" {
		t.Fatalf("completed translation selection = %#v/%v", record, err)
	}
	if queued, err := database.QueueIncidentTranslation(ctx, incidentID, TranslationPlan{Language: "en", PromptVersion: plan.PromptVersion, Model: "translate:c"}, now.Add(4*time.Minute)); err != nil || queued != 0 {
		t.Fatalf("model change rewrote completed translation = %d/%v", queued, err)
	}
}

func TestEnglishSelectionUsesOlderTranslatedRunAndSameRunMetadata(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "translation-fallback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 27, 11, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PreviousPipelineVersion, now, map[string]string{
		"title_de": "Alt DE", "summary_de": "Alt.", "title_en": "Older English", "summary_en": "Older English summary.", "category": "traffic",
	})
	newRun := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now.Add(time.Minute), map[string]string{
		"title_de": "Neu DE", "summary_de": "Neu.", "category": "theft", "event_start_date": "2026-08-26",
	})
	if _, err := database.QueueTranslationsForRun(ctx, newRun, []TranslationPlan{{Language: "en", PromptVersion: "incident-translation-en-v1", Model: "translate:4b"}}, "scheduled", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	german, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{PromptVersion: PipelineVersion, Language: "de", PublicOnly: true})
	if err != nil || german.AITitleDE != "Neu DE" || german.AICategory != "theft" {
		t.Fatalf("newest German selection = %#v/%v", german, err)
	}
	english, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{PromptVersion: PipelineVersion, Language: "en", PublicOnly: true})
	if err != nil || english.AITitleEN != "Older English" || english.AICategory != "traffic" || !english.AITranslationFallback {
		t.Fatalf("older translated fallback and metadata = %#v/%v", english, err)
	}
}

func TestTranslationLanguageCutoverAndExplicitBackfill(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "translation-cutover.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cutover := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, cutover, "old", "new")
	rows, err := database.db.QueryContext(ctx, `SELECT id,content_hash FROM incidents ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	type incident struct {
		id   int64
		hash string
	}
	var incidents []incident
	for rows.Next() {
		var item incident
		if err := rows.Scan(&item.id, &item.hash); err != nil {
			t.Fatal(err)
		}
		incidents = append(incidents, item)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	insertCompletedPresentationRun(t, ctx, database, incidents[0].id, incidents[0].hash, PipelineVersion, cutover.Add(-time.Minute), map[string]string{"title_de": "Alt", "summary_de": "Alt."})
	insertCompletedPresentationRun(t, ctx, database, incidents[1].id, incidents[1].hash, PipelineVersion, cutover.Add(time.Minute), map[string]string{"title_de": "Neu", "summary_de": "Neu."})
	if _, err := database.db.ExecContext(ctx, `UPDATE translation_language_cutovers SET automatic_after=? WHERE language_code='en'`, formatTime(cutover)); err != nil {
		t.Fatal(err)
	}
	plan := TranslationPlan{Language: "en", PromptVersion: "incident-translation-en-v1", Model: "translate:4b"}
	if queued, err := database.QueueMissingTranslations(ctx, "fixture", []TranslationPlan{plan}, false, cutover.Add(2*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("automatic post-cutover translations = %d/%v", queued, err)
	}
	if queued, err := database.QueueMissingTranslations(ctx, "fixture", []TranslationPlan{plan}, true, cutover.Add(3*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("explicit historical backfill = %d/%v", queued, err)
	}
	var scheduled, backfill int
	if err := database.db.QueryRowContext(ctx, `SELECT
		SUM(CASE WHEN request_kind='scheduled' THEN 1 ELSE 0 END),
		SUM(CASE WHEN request_kind='backfill' THEN 1 ELSE 0 END)
		FROM presentation_translations WHERE language_code='en' AND status='pending'`).Scan(&scheduled, &backfill); err != nil || scheduled != 1 || backfill != 1 {
		t.Fatalf("translation request kinds = scheduled:%d backfill:%d err:%v", scheduled, backfill, err)
	}
}
