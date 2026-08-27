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
	stagedMigration, err := migrationFiles.ReadFile("migrations/006_staged_pipeline.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(string(stagedMigration)); err != nil {
		t.Fatalf("apply staged migration: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (6, '2026-08-25T10:30:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO presentation_runs(id,incident_id,source_hash,pipeline_version,status,legacy,created_at) VALUES(100,1,'content','incident-pipeline-v1','processing',0,'2026-08-25T10:31:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO processing_cycles(id,kind,source_mode,status,active_step,window_authorized,created_at,updated_at) VALUES(100,'scheduled','fixture','running',0,1,'2026-08-25T10:31:00Z','2026-08-25T10:31:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO processing_cycle_items(id,cycle_id,incident_id,source_hash,presentation_run_id,status,created_at,updated_at) VALUES(100,100,1,'content',100,'pending','2026-08-25T10:31:00Z','2026-08-25T10:31:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO processing_step_jobs(cycle_item_id,step_key,step_order,model_identity,prompt_version,status,created_at,updated_at) VALUES(100,'german_analysis',0,'qwen:4b','incident-analysis-de-v1','running','2026-08-25T10:31:00Z','2026-08-25T10:31:00Z'),(100,'english_translation',1,'translate:4b','incident-translation-en-v1','waiting','2026-08-25T10:31:00Z','2026-08-25T10:31:00Z')`); err != nil {
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
	var historyIndex int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='presentation_translations_history_idx'`).Scan(&historyIndex); err != nil || historyIndex != 1 {
		t.Fatalf("translation history index = %d/%v", historyIndex, err)
	}
	settings, err := database.PipelineStepSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byStep := map[string]string{}
	for _, setting := range settings {
		byStep[setting.StepKey] = setting.PreferredModel
	}
	if byStep["incident_metadata"] != "qwen:4b" || byStep["german_presentation"] != "qwen:4b" || byStep["translation"] != "" || byStep["english_translation"] != "" {
		t.Fatalf("migrated settings = %#v", byStep)
	}
	for table, query := range map[string]string{
		"cycle": `SELECT status FROM processing_cycles WHERE id=100`, "item": `SELECT status FROM processing_cycle_items WHERE id=100`, "run": `SELECT status FROM presentation_runs WHERE id=100`,
	} {
		var status string
		if err := database.db.QueryRowContext(ctx, query).Scan(&status); err != nil || status != "superseded" {
			t.Fatalf("unfinished v1 %s status = %q, err=%v", table, status, err)
		}
	}
	var supersededJobs int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_step_jobs WHERE cycle_item_id=100 AND status='superseded'`).Scan(&supersededJobs); err != nil || supersededJobs != 2 {
		t.Fatalf("superseded v1 jobs = %d, err=%v", supersededJobs, err)
	}
	var cutover string
	if err := database.db.QueryRowContext(ctx, `SELECT scheduled_after FROM pipeline_cutovers WHERE pipeline_version=?`, PipelineVersion).Scan(&cutover); err != nil {
		t.Fatalf("v2 cutover = %q, err=%v", cutover, err)
	}
	cutoverTime, err := time.Parse(time.RFC3339Nano, cutover)
	if err != nil || !cutoverTime.After(time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("v2 cutover did not record migration time: %q, err=%v", cutover, err)
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", []string{"incident_metadata", "german_presentation"}, cutoverTime)
	if err != nil || snapshot.ScheduledCandidates != 0 {
		t.Fatalf("historical incidents crossed v2 cutover = %#v, err=%v", snapshot, err)
	}
	newAt := formatTime(cutoverTime.Add(time.Second))
	if _, err := database.db.ExecContext(ctx, `INSERT INTO source_documents(id,external_id,source_url,title,published_at,discovered_at,last_seen_at,feed_fingerprint,fetch_status,source_hash) VALUES(2,'two','https://fixture.invalid/two','New release',?,?,?,?, 'fixture','source-two')`, newAt, newAt, newAt, "feed-two"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO incidents(id,source_document_id,incident_number,position,title_de,body_de,content_hash,created_at,updated_at) VALUES(2,2,'1',0,'New','New body','content-two',?,?)`, newAt, newAt); err != nil {
		t.Fatal(err)
	}
	snapshot, err = database.PipelineSnapshot(ctx, "fixture", []string{"incident_metadata", "german_presentation"}, cutoverTime.Add(time.Second))
	if err != nil || snapshot.ScheduledCandidates != 1 {
		t.Fatalf("new incident was not v2 eligible = %#v, err=%v", snapshot, err)
	}
	record, err := database.GetPresentationIncident(ctx, 1, PresentationScope{PromptVersion: PipelineVersion, TranslationLanguage: "en"})
	if err != nil || record.AITranslatedTitle != "Legacy EN" {
		t.Fatalf("legacy presentation = %#v, err=%v", record, err)
	}
	if !record.AILegacy || record.AIPipelineVersion != "legacy/incident-presentation-v2/qwen:4b" ||
		record.AIModel != "qwen:4b" || record.AITranslationModel != "qwen:4b" {
		t.Fatalf("legacy provenance = %#v", record)
	}
	var importedTranslations int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM presentation_translations WHERE presentation_run_id=(SELECT id FROM presentation_runs WHERE legacy=1 LIMIT 1) AND language_code='en' AND request_kind='imported' AND status='succeeded'`).Scan(&importedTranslations); err != nil || importedTranslations != 1 {
		t.Fatalf("imported legacy translation = %d/%v", importedTranslations, err)
	}
	history, err := database.ListPipelineHistory(ctx, "fixture", 100, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range history.Entries {
		if entry.Kind == pipelineHistoryKindTranslation && entry.RequestKind == "imported" {
			t.Fatalf("imported translation duplicated operational history: %#v", entry)
		}
	}
	incidentID := int64(1)
	request, err := database.CreateManualPipelineCycle(ctx, "fixture", testPipelinePlans(), "translate:4b", &incidentID, false, time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", nil, false, time.Date(2026, 8, 25, 11, 0, 1, 0, time.UTC))
	if err != nil || !found || cycle.ID != request.CycleID {
		t.Fatalf("activate replacement = %#v/%t/%v", cycle, found, err)
	}
	metadata, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, time.Date(2026, 8, 25, 11, 0, 2, 0, time.UTC))
	if err != nil || !found {
		t.Fatalf("claim metadata replacement = %#v/%t/%v", metadata, found, err)
	}
	metadataValues := []PipelineValue{{Kind: "category", Value: "other"}, {Kind: "report_kind", Value: "incident"}, {Kind: "public_assistance_status", Value: "not_requested"}, {Kind: "public_assistance_types", Value: "[]"}}
	if err := database.CompletePipelineJob(ctx, metadata, metadataValues, metadata.ModelIdentity, HashPipelineInput("metadata"), time.Date(2026, 8, 25, 11, 0, 3, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	record, err = database.GetPresentationIncident(ctx, 1, PresentationScope{PromptVersion: PipelineVersion, TranslationLanguage: "en"})
	if err != nil || record.AITranslatedTitle != "Legacy EN" {
		t.Fatalf("partial replacement displaced legacy = %#v, err=%v", record, err)
	}
	advance, err := database.AdvancePipelineCycle(ctx, cycle, len(testPipelinePlans()), time.Date(2026, 8, 25, 11, 0, 4, 0, time.UTC))
	if err != nil || !advance.Advanced {
		t.Fatalf("advance replacement = %#v/%v", advance, err)
	}
	cycle.ActiveStep = 1
	german, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 1, time.Date(2026, 8, 25, 11, 0, 5, 0, time.UTC))
	if err != nil || !found {
		t.Fatalf("claim German replacement = %#v/%t/%v", german, found, err)
	}
	if err := database.CompletePipelineJob(ctx, german, []PipelineValue{{Kind: "title_de", Value: "Staged DE"}, {Kind: "summary_de", Value: "Staged summary."}, {Kind: "privacy_status", Value: "safe"}, {Kind: "privacy_flags", Value: "[]"}}, german.ModelIdentity, HashPipelineInput("de"), time.Date(2026, 8, 25, 11, 0, 6, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	record, err = database.GetPresentationIncident(ctx, 1, PresentationScope{PromptVersion: PipelineVersion, Language: "de"})
	if err != nil || record.AITitleDE != "Staged DE" {
		t.Fatalf("German was not visible at canonical completion = %#v, err=%v", record, err)
	}
	englishFallback, err := database.GetPresentationIncident(ctx, 1, PresentationScope{PromptVersion: PipelineVersion, Language: "en"})
	if err != nil || englishFallback.AITranslatedTitle != "Legacy EN" {
		t.Fatalf("English fallback while v2 translation is absent = %#v, err=%v", englishFallback, err)
	}
	advance, err = database.AdvancePipelineCycle(ctx, cycle, len(testPipelinePlans()), time.Date(2026, 8, 25, 11, 0, 7, 0, time.UTC))
	if err != nil || !advance.Completed {
		t.Fatalf("complete canonical replacement = %#v/%v", advance, err)
	}
	if queued, err := database.QueueTranslationsForRun(ctx, german.PresentationRunID, []TranslationPlan{{Language: "en", PromptVersion: "incident-translation-en-v1", Model: "translate:4b"}}, "manual", time.Date(2026, 8, 25, 11, 0, 8, 0, time.UTC)); err != nil || queued != 1 {
		t.Fatalf("queue English replacement = %d/%v", queued, err)
	}
	translation, found, err := database.ClaimTranslationJob(ctx, false, nil, time.Date(2026, 8, 25, 11, 0, 8, 0, time.UTC))
	if err != nil || !found {
		t.Fatalf("claim translation replacement = %#v/%t/%v", translation, found, err)
	}
	if err := database.CompleteTranslationJob(ctx, translation, "Staged EN", "Staged English summary.", translation.ModelIdentity, translation.InputHash, time.Date(2026, 8, 25, 11, 0, 9, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	record, err = database.GetPresentationIncident(ctx, 1, PresentationScope{PromptVersion: PipelineVersion, Language: "en"})
	if err != nil || record.AITranslatedTitle != "Staged EN" {
		t.Fatalf("complete staged replacement = %#v, err=%v", record, err)
	}
	if record.AILegacy || record.AIPipelineVersion != PipelineVersion ||
		record.AIMetadataModel != "qwen:4b" || record.AIMetadataPromptVersion != "incident-metadata-v1" ||
		record.AIModel != "qwen:4b" || record.AIPromptVersion != "incident-presentation-de-v2" ||
		record.AITranslationModel != "translate:4b" || record.AITranslationPromptVersion != "incident-translation-en-v1" ||
		record.AIMetadataGeneratedAt == nil || !record.AIMetadataGeneratedAt.Equal(time.Date(2026, 8, 25, 11, 0, 3, 0, time.UTC)) ||
		record.AIGeneratedAt == nil || !record.AIGeneratedAt.Equal(time.Date(2026, 8, 25, 11, 0, 6, 0, time.UTC)) ||
		record.AITranslationGeneratedAt == nil || !record.AITranslationGeneratedAt.Equal(time.Date(2026, 8, 25, 11, 0, 9, 0, time.UTC)) {
		t.Fatalf("staged provenance = %#v", record)
	}
}
