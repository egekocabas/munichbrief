package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestUnifiedPostProcessingMigrationPreservesAuditAndCutsReadersToV2(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "unified-post-processing.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	names := []string{"001_initial.sql", "002_ai_titles.sql", "003_processing_privacy.sql", "004_manual_processing.sql", "005_runtime_models.sql", "006_staged_pipeline.sql", "007_metadata_pipeline.sql", "008_pipeline_history.sql", "009_translation_history.sql", "010_category_verification.sql"}
	for index, name := range names {
		contents, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(contents)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, index+1, "2026-08-29T08:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	for id := 1; id <= 2; id++ {
		if _, err := raw.Exec(`INSERT INTO source_documents(id,external_id,source_url,title,published_at,discovered_at,last_seen_at,feed_fingerprint,fetch_status,source_hash) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, fmt.Sprintf("doc-%d", id), fmt.Sprintf("https://fixture.invalid/%d", id), "Release", "2026-08-29T08:00:00Z", "2026-08-29T08:00:00Z", "2026-08-29T08:00:00Z", fmt.Sprintf("feed-%d", id), "fixture", fmt.Sprintf("source-%d", id)); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`INSERT INTO incidents(id,source_document_id,incident_number,position,title_de,body_de,content_hash,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, id, "1", 0, "Original", "Body", fmt.Sprintf("content-%d", id), "2026-08-29T08:00:00Z", "2026-08-29T08:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	for id := 1; id <= 7; id++ {
		legacy, pipeline, incident, hash := 0, PipelineVersion, 1, "content-1"
		if id == 7 {
			legacy, pipeline, incident, hash = 1, "legacy/import", 2, "content-2"
		}
		completedAt := fmt.Sprintf("2026-08-29T08:%02d:00Z", id)
		if id == 3 {
			completedAt = "2026-08-29T08:59:00Z"
		}
		if _, err := raw.Exec(`INSERT INTO presentation_runs(id,incident_id,source_hash,pipeline_version,status,legacy,created_at,completed_at) VALUES(?,?,?,?,"complete",?,?,?)`, id, incident, hash, pipeline, legacy, "2026-08-29T08:00:00Z", completedAt); err != nil {
			t.Fatal(err)
		}
		for kind, value := range map[string]string{"category": "traffic", "title_de": "Titel", "summary_de": "Zusammenfassung."} {
			if _, err := raw.Exec(`INSERT INTO presentation_values(presentation_run_id,kind,value,model_identity,prompt_version,generated_at) VALUES(?,?,?,?,?,?)`, id, kind, value, "canonical:4b", "canonical-v1", "2026-08-29T08:00:00Z"); err != nil {
				t.Fatal(err)
			}
		}
	}
	statuses := []string{"pending", "running", "succeeded", "needs_review", "failed", "superseded", "succeeded"}
	for index, status := range statuses {
		id := index + 1
		kind := "manual"
		if id == 7 {
			kind = "imported"
		}
		var title, summary any
		if status == "succeeded" {
			title, summary = fmt.Sprintf("English %d", id), "English summary."
		}
		if _, err := raw.Exec(`INSERT INTO presentation_translations(id,presentation_run_id,language_code,request_kind,status,title,summary,model_identity,prompt_version,input_hash,attempt_count,next_retry_at,failure_kind,error_message,started_at,completed_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, id, "en", kind, status, title, summary, "translate:4b", "translation-v1", fmt.Sprintf("hash-%d", id), id, nil, nil, nil, "2026-08-29T08:00:00Z", nullableCompleted(status), "2026-08-29T08:00:00Z", fmt.Sprintf("2026-08-29T08:%02d:00Z", id)); err != nil {
			t.Fatal(err)
		}
	}
	for index, status := range statuses[:6] {
		id := index + 1
		var correct, corrected any
		if status == "succeeded" {
			correct, corrected = 0, "other"
		}
		if _, err := raw.Exec(`INSERT INTO presentation_category_verifications(id,presentation_run_id,request_kind,status,input_category,is_correct,corrected_category,model_identity,prompt_version,input_hash,attempt_count,started_at,completed_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, id, "manual", status, "traffic", correct, corrected, "verify:4b", "verification-v1", fmt.Sprintf("verify-%d", id), id, "2026-08-29T08:00:00Z", nullableCompleted(status), "2026-08-29T08:00:00Z", fmt.Sprintf("2026-08-29T08:%02d:30Z", id)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`UPDATE translation_language_cutovers SET automatic_after='2026-08-29T07:00:00Z'; UPDATE category_verification_cutover SET automatic_after='2026-08-29T07:30:00Z'; INSERT INTO processing_cycles(id,kind,source_mode,status,active_step,window_authorized,translation_model_identity,category_verification_model_identity,created_at,updated_at) VALUES(50,'manual','fixture','succeeded',2,1,'translate:4b','verify:4b','2026-08-29T08:00:00Z','2026-08-29T08:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var jobs, values, plans int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_jobs`).Scan(&jobs); err != nil || jobs != 13 {
		t.Fatalf("migrated jobs = %d, err=%v", jobs, err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_values`).Scan(&values); err != nil || values != 6 {
		t.Fatalf("migrated values = %d, err=%v", values, err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cycle_post_processing_plans WHERE cycle_id=50`).Scan(&plans); err != nil || plans != 2 {
		t.Fatalf("migrated cycle plans = %d, err=%v", plans, err)
	}
	for processor, want := range map[string]string{"translation": "2026-08-29T07:00:00Z", "category_verification": "2026-08-29T07:30:00Z"} {
		var automaticAfter string
		if err := database.db.QueryRowContext(ctx, `SELECT automatic_after FROM post_processing_scopes WHERE processor_key=?`, processor).Scan(&automaticAfter); err != nil || automaticAfter != want {
			t.Fatalf("migrated %s cutover = %q/%v, want %q", processor, automaticAfter, err, want)
		}
	}
	for _, table := range []string{"presentation_translations", "presentation_category_verifications", "translation_language_cutovers", "category_verification_cutover"} {
		var count int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("old table %s remains: %d/%v", table, count, err)
		}
	}
	for _, index := range []string{"post_processing_jobs_one_active_idx", "post_processing_jobs_ready_idx", "post_processing_jobs_selection_idx", "post_processing_jobs_history_idx"} {
		var count int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&count); err != nil || count != 1 {
			t.Fatalf("index %s = %d/%v", index, count, err)
		}
	}
	for _, column := range []string{"translation_model_identity", "category_verification_model_identity"} {
		var count int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('processing_cycles') WHERE name=?`, column).Scan(&count); err != nil || count != 0 {
			t.Fatalf("old cycle column %s remains: %d/%v", column, count, err)
		}
	}
	if err := database.RecoverPostProcessing(ctx, time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var recovered string
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM post_processing_jobs WHERE id=2`).Scan(&recovered); err != nil || recovered != "pending" {
		t.Fatalf("running migration recovery = %q/%v", recovered, err)
	}
	record, err := database.GetPresentationIncident(ctx, 1, PresentationScope{PromptVersion: PipelineVersion, Language: "en", TranslationLanguage: "en", PublicOnly: true})
	if err != nil || record.AITranslatedTitle != "English 3" || record.AICategory != "other" {
		t.Fatalf("v2 result after migration = %#v/%v", record, err)
	}
	if _, err := database.GetPresentationIncident(ctx, 2, PresentationScope{PromptVersion: PipelineVersion, Language: "de", PublicOnly: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy-only incident remained public: %v", err)
	}
	_, unprocessed, err := database.ListAdminIncidents(ctx, 20, 0, "fixture", PresentationScope{PromptVersion: PipelineVersion, Language: "de"}, AdminIncidentsUnprocessed)
	if err != nil || unprocessed != 1 {
		t.Fatalf("legacy-only review state = %d/%v", unprocessed, err)
	}
}

func nullableCompleted(status string) any {
	if status == "pending" || status == "running" {
		return nil
	}
	return "2026-08-29T08:10:00Z"
}
