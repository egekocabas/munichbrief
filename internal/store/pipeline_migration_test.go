package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestStagedMigrationMovesPreferenceAndPreservesLegacyPresentation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "staged-upgrade.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 5; version++ {
		contents, err := migrationFiles.ReadFile("migrations/00" + string(rune('0'+version)) + "_" + map[int]string{1: "initial", 2: "ai_titles", 3: "processing_privacy", 4: "manual_processing", 5: "runtime_models"}[version] + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(contents)); err != nil {
			t.Fatalf("apply migration %d: %v", version, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, '2026-08-25T10:00:00Z')`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO source_documents(id,external_id,source_url,title,published_at,discovered_at,last_seen_at,feed_fingerprint,fetch_status,source_hash) VALUES(1,'one','https://fixture.invalid/one','Release','2026-08-25T10:00:00Z','2026-08-25T10:00:00Z','2026-08-25T10:00:00Z','feed','fixture','source')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO incidents(id,source_document_id,incident_number,position,title_de,body_de,content_hash,created_at,updated_at) VALUES(1,1,'1',0,'Original','Original body','content','2026-08-25T10:00:00Z','2026-08-25T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO ai_settings(id,preferred_model,updated_at) VALUES(1,'qwen:4b','2026-08-25T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for kind, value := range map[string]string{"title_de": "Legacy DE", "summary_de": "Legacy summary.", "title_en": "Legacy EN", "summary_en": "Legacy English summary."} {
		if _, err := raw.Exec(`INSERT INTO derivations(incident_id,kind,value,source_hash,model_identity,prompt_version,generated_at) VALUES(1,?,?, 'content','qwen:4b','incident-presentation-v2','2026-08-25T10:00:00Z')`, kind, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	settings, err := database.PipelineStepSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byStep := map[string]string{}
	for _, setting := range settings {
		byStep[setting.StepKey] = setting.PreferredModel
	}
	if byStep["german_analysis"] != "qwen:4b" || byStep["english_translation"] != "" {
		t.Fatalf("migrated settings = %#v", byStep)
	}
	record, err := database.GetPresentationIncident(ctx, 1, PresentationScope{PromptVersion: PipelineVersion})
	if err != nil || record.AITitleEN != "Legacy EN" {
		t.Fatalf("legacy presentation = %#v, err=%v", record, err)
	}
	if !record.AILegacy || record.AIPipelineVersion != "legacy/incident-presentation-v2/qwen:4b" ||
		record.AIModel != "qwen:4b" || record.AITranslationModel != "qwen:4b" {
		t.Fatalf("legacy provenance = %#v", record)
	}
	incidentID := int64(1)
	request, err := database.CreateManualPipelineCycle(ctx, "fixture", testPipelinePlans(), &incidentID, false, time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", nil, false, time.Date(2026, 8, 25, 11, 0, 1, 0, time.UTC))
	if err != nil || !found || cycle.ID != request.CycleID {
		t.Fatalf("activate replacement = %#v/%t/%v", cycle, found, err)
	}
	german, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, time.Date(2026, 8, 25, 11, 0, 2, 0, time.UTC))
	if err != nil || !found {
		t.Fatalf("claim German replacement = %#v/%t/%v", german, found, err)
	}
	if err := database.CompletePipelineJob(ctx, german, []PipelineValue{{Kind: "title_de", Value: "Staged DE"}, {Kind: "summary_de", Value: "Staged summary."}, {Kind: "category", Value: "other"}, {Kind: "privacy_status", Value: "safe"}, {Kind: "privacy_flags", Value: "[]"}}, german.ModelIdentity, HashPipelineInput("de"), time.Date(2026, 8, 25, 11, 0, 3, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	record, err = database.GetPresentationIncident(ctx, 1, PresentationScope{PromptVersion: PipelineVersion})
	if err != nil || record.AITitleEN != "Legacy EN" {
		t.Fatalf("partial replacement displaced legacy = %#v, err=%v", record, err)
	}
	advance, err := database.AdvancePipelineCycle(ctx, cycle, 2, time.Date(2026, 8, 25, 11, 0, 4, 0, time.UTC))
	if err != nil || !advance.Advanced {
		t.Fatalf("advance replacement = %#v/%v", advance, err)
	}
	cycle.ActiveStep = 1
	translation, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 1, time.Date(2026, 8, 25, 11, 0, 5, 0, time.UTC))
	if err != nil || !found {
		t.Fatalf("claim translation replacement = %#v/%t/%v", translation, found, err)
	}
	if err := database.CompletePipelineJob(ctx, translation, []PipelineValue{{Kind: "title_en", Value: "Staged EN"}, {Kind: "summary_en", Value: "Staged English summary."}}, translation.ModelIdentity, HashPipelineInput("en"), time.Date(2026, 8, 25, 11, 0, 6, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	advance, err = database.AdvancePipelineCycle(ctx, cycle, 2, time.Date(2026, 8, 25, 11, 0, 7, 0, time.UTC))
	if err != nil || !advance.Completed {
		t.Fatalf("complete replacement = %#v/%v", advance, err)
	}
	record, err = database.GetPresentationIncident(ctx, 1, PresentationScope{PromptVersion: PipelineVersion})
	if err != nil || record.AITitleEN != "Staged EN" {
		t.Fatalf("complete staged replacement = %#v, err=%v", record, err)
	}
	if record.AILegacy || record.AIPipelineVersion != PipelineVersion ||
		record.AIModel != "qwen:4b" || record.AIPromptVersion != "incident-analysis-de-v1" ||
		record.AITranslationModel != "translate:4b" || record.AITranslationPromptVersion != "incident-translation-en-v1" ||
		record.AIGeneratedAt == nil || !record.AIGeneratedAt.Equal(time.Date(2026, 8, 25, 11, 0, 3, 0, time.UTC)) ||
		record.AITranslationGeneratedAt == nil || !record.AITranslationGeneratedAt.Equal(time.Date(2026, 8, 25, 11, 0, 6, 0, time.UTC)) {
		t.Fatalf("staged provenance = %#v", record)
	}
}
