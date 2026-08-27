package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPresentationSelectionPrefersCompleteV2AndRetainsV1Fallback(t *testing.T) {
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

	v1 := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PreviousPipelineVersion, now, map[string]string{
		"title_de": "V1 DE", "summary_de": "V1 summary.", "title_en": "V1 EN", "summary_en": "V1 English summary.",
		"category": "other", "privacy_status": "safe", "privacy_flags": "[]",
	})
	record, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{PromptVersion: PipelineVersion})
	if err != nil || record.AIPipelineVersion != PreviousPipelineVersion || record.AITitleEN != "V1 EN" {
		t.Fatalf("v1 fallback = %#v, err=%v, run=%d", record, err, v1)
	}

	for _, status := range []string{"processing", "failed"} {
		if _, err := database.db.ExecContext(ctx, `INSERT INTO presentation_runs(incident_id,source_hash,pipeline_version,status,legacy,created_at,completed_at) VALUES(?,?,?,?,0,?,?)`, incidentID, sourceHash, PipelineVersion, status, formatTime(now.Add(time.Minute)), formatTime(now.Add(2*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	record, err = database.GetPresentationIncident(ctx, incidentID, PresentationScope{PromptVersion: PipelineVersion})
	if err != nil || record.AIPipelineVersion != PreviousPipelineVersion || record.AITitleEN != "V1 EN" {
		t.Fatalf("incomplete v2 displaced v1 = %#v, err=%v", record, err)
	}

	insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now.Add(3*time.Minute), map[string]string{
		"title_de": "V2 DE", "summary_de": "V2 summary.", "title_en": "V2 EN", "summary_en": "V2 English summary.",
		"category": "traffic", "event_start_date": "2026-08-26",
		"report_kind": "incident", "public_assistance_status": "not_requested", "public_assistance_types": "[]", "privacy_status": "safe", "privacy_flags": "[]",
	})
	record, err = database.GetPresentationIncident(ctx, incidentID, PresentationScope{PromptVersion: PipelineVersion})
	if err != nil || record.AIPipelineVersion != PipelineVersion || record.AITitleEN != "V2 EN" || record.AIEventStartDate != "2026-08-26" {
		t.Fatalf("completed v2 selection = %#v, err=%v", record, err)
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
	return runID
}
