package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPresentationSelectionRequiresCompleteCurrentV2(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "presentation-order.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id, content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}

	v1 := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, "incident-pipeline-v1", now, map[string]string{
		"title_de": "V1 DE", "summary_de": "V1 summary.", "title_en": "V1 EN", "summary_en": "V1 English summary.",
		"category": "other", "privacy_status": "safe", "privacy_flags": "[]",
	})
	record, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{TranslationLanguage: "en"})
	if err != nil || record.HasAI {
		t.Fatalf("v1 audit run %d selected for readers: %#v, err=%v", v1, record, err)
	}

	for _, status := range []string{"processing", "failed"} {
		if _, err := database.db.ExecContext(ctx, `INSERT INTO presentation_runs(incident_id,source_hash,pipeline_version,status,legacy,created_at,completed_at) VALUES(?,?,?,?,0,?,?)`, incidentID, sourceHash, PipelineVersion, status, formatTime(now.Add(time.Minute)), formatTime(now.Add(2*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	record, err = database.GetPresentationIncident(ctx, incidentID, PresentationScope{TranslationLanguage: "en"})
	if err != nil || record.HasAI {
		t.Fatalf("incomplete v2 became selectable: %#v, err=%v", record, err)
	}

	insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now.Add(3*time.Minute), map[string]string{
		"title_de": "V2 DE", "summary_de": "V2 summary.", "title_en": "V2 EN", "summary_en": "V2 English summary.",
		"category": "traffic", "event_start_date": "2026-08-26",
		"report_kind": "incident", "public_assistance_status": "not_requested", "public_assistance_types": "[]", "privacy_status": "safe", "privacy_flags": "[]",
	})
	record, err = database.GetPresentationIncident(ctx, incidentID, PresentationScope{TranslationLanguage: "en"})
	if err != nil || record.AIPipelineVersion != PipelineVersion || record.AITranslatedTitle != "V2 EN" || record.AIEventStartDate != "2026-08-26" {
		t.Fatalf("completed v2 selection = %#v, err=%v", record, err)
	}

	insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now.Add(4*time.Minute), map[string]string{
		"title_de": "New V2 DE", "summary_de": "New V2 summary.", "category": "traffic",
		"report_kind": "incident", "public_assistance_status": "not_requested", "public_assistance_types": "[]", "privacy_status": "safe", "privacy_flags": "[]",
	})
	if _, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{Language: "en", TranslationLanguage: "en", PublicOnly: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("older v2 translation remained a cross-run fallback: %v", err)
	}
	translations, err := database.ListAdminTranslations(ctx, []int64{incidentID}, []string{"en"})
	if err != nil || len(translations) != 1 || translations[0].Title != "" {
		t.Fatalf("admin selected an older v2 translation: %#v/%v", translations, err)
	}
}

func insertCompletedPresentationRun(t *testing.T, ctx context.Context, database *Store, incidentID int64, sourceHash, pipelineVersion string, completed time.Time, values map[string]string) int64 {
	t.Helper()
	result, err := database.db.ExecContext(ctx, `INSERT INTO presentation_runs(incident_id,source_hash,pipeline_version,status,legacy,created_at,completed_at) VALUES(?,?,?,'complete',0,?,?)`, incidentID, sourceHash, pipelineVersion, formatTime(completed.Add(-time.Minute)), formatTime(completed))
	if err != nil {
		t.Fatal(err)
	}
	runID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for kind, value := range values {
		model, prompt := "qwen:4b", "incident-presentation-de-v2"
		if kind == "title_en" || kind == "summary_en" {
			model, prompt = "translate:4b", "incident-translation-en-v1"
		} else if pipelineVersion == PipelineVersion && kind != "title_de" && kind != "summary_de" && kind != "privacy_status" && kind != "privacy_flags" {
			prompt = "incident-metadata-v1"
		}
		if _, err := database.db.ExecContext(ctx, `INSERT INTO presentation_values(presentation_run_id,kind,value,model_identity,prompt_version,generated_at) VALUES(?,?,?,?,?,?)`, runID, kind, value, model, prompt, formatTime(completed)); err != nil {
			t.Fatal(err)
		}
	}
	if title, ok := values["title_en"]; ok {
		result, err := database.db.ExecContext(ctx, `INSERT INTO post_processing_jobs(
			presentation_run_id,processor_key,scope_key,request_kind,status,model_identity,prompt_version,input_hash,
			attempt_count,started_at,completed_at,created_at,updated_at
		) VALUES(?,'translation','en','imported','succeeded','translate:4b','incident-translation-en-v1','',0,?,?,?,?)`,
			runID, formatTime(completed), formatTime(completed), formatTime(completed), formatTime(completed))
		if err != nil {
			t.Fatal(err)
		}
		jobID, _ := result.LastInsertId()
		if _, err := database.db.ExecContext(ctx, `INSERT INTO post_processing_values(job_id,kind,value) VALUES(?,'title',?),(?,'summary',?)`, jobID, title, jobID, values["summary_en"]); err != nil {
			t.Fatal(err)
		}
	}
	return runID
}
