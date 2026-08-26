package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
)

func testPipelinePlans() []PipelineStepPlan {
	return []PipelineStepPlan{
		{Key: "german_analysis", Order: 0, PromptVersion: "incident-analysis-de-v1", Model: "qwen:4b"},
		{Key: "english_translation", Order: 1, PromptVersion: "incident-translation-en-v1", Model: "translate:4b"},
	}
}

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

func TestPipelineFreezesTargetsGroupsStepsAndStartsNextCycleImmediately(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "pipeline.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one", "two")
	plans := testPipelinePlans()
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, true, now)
	if err != nil || !found || cycle.Kind != "scheduled" {
		t.Fatalf("activate scheduled cycle = %#v/%t/%v", cycle, found, err)
	}
	insertPipelineDocuments(t, ctx, database, now.Add(time.Minute), "three")

	var germanIDs []int64
	for {
		job, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
		germanIDs = append(germanIDs, job.IncidentID)
		values := []PipelineValue{{Kind: "title_de", Value: "Titel"}, {Kind: "summary_de", Value: "Zusammenfassung."}, {Kind: "category", Value: "other"}, {Kind: "privacy_status", Value: "safe"}, {Kind: "privacy_flags", Value: "[]"}}
		if err := database.CompletePipelineJob(ctx, job, values, job.ModelIdentity, HashPipelineInput(job.SourceHash), now); err != nil {
			t.Fatal(err)
		}
	}
	if len(germanIDs) != 2 {
		t.Fatalf("frozen German targets = %v, want two", germanIDs)
	}
	advance, err := database.AdvancePipelineCycle(ctx, cycle, 2, now)
	if err != nil || !advance.Advanced {
		t.Fatalf("advance to translation = %#v/%v", advance, err)
	}
	cycle.ActiveStep = 1
	translations := 0
	for {
		job, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 1, now)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
		translations++
		if job.TitleDE != "Titel" || job.SummaryDE != "Zusammenfassung." {
			t.Fatalf("translation input = %q/%q", job.TitleDE, job.SummaryDE)
		}
		if err := database.CompletePipelineJob(ctx, job, []PipelineValue{{Kind: "title_en", Value: "Title"}, {Kind: "summary_en", Value: "Summary."}}, job.ModelIdentity, HashPipelineInput(job.TitleDE, job.SummaryDE), now); err != nil {
			t.Fatal(err)
		}
	}
	if translations != 2 {
		t.Fatalf("translations = %d, want 2", translations)
	}
	advance, err = database.AdvancePipelineCycle(ctx, cycle, 2, now)
	if err != nil || !advance.Completed {
		t.Fatalf("complete cycle = %#v/%v", advance, err)
	}
	next, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, true, now)
	if err != nil || !found || next.ID == cycle.ID {
		t.Fatalf("immediate next cycle = %#v/%t/%v", next, found, err)
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", []string{"german_analysis", "english_translation"}, now)
	if err != nil || snapshot.CycleTotal != 2 {
		t.Fatalf("next frozen snapshot = %#v/%v", snapshot, err)
	}
}

func TestPipelineSnapshotSeparatesActiveWaitingWorkAndNewCandidates(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "pipeline-status.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 26, 1, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one", "two")
	plans := testPipelinePlans()
	request, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, nil, true, now)
	if err != nil || request.Requested != 2 {
		t.Fatalf("create manual cycle = %#v/%v", request, err)
	}
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", nil, false, now)
	if err != nil || !found || cycle.ID != request.CycleID {
		t.Fatalf("activate manual cycle = %#v/%t/%v", cycle, found, err)
	}
	job, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now)
	if err != nil || !found {
		t.Fatalf("claim German job = %#v/%t/%v", job, found, err)
	}
	values := []PipelineValue{
		{Kind: "title_de", Value: "Titel"}, {Kind: "summary_de", Value: "Zusammenfassung."},
		{Kind: "category", Value: "other"}, {Kind: "privacy_status", Value: "safe"}, {Kind: "privacy_flags", Value: "[]"},
	}
	if err := database.CompletePipelineJob(ctx, job, values, job.ModelIdentity, HashPipelineInput(job.SourceHash), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	snapshot, err := database.PipelineSnapshot(ctx, "fixture", []string{"german_analysis", "english_translation"}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveStepCompleted != 1 || snapshot.ActiveStepTotal != 2 || snapshot.CycleCompleted != 1 || snapshot.CycleTotal != 4 {
		t.Fatalf("active progress = step %d/%d pipeline %d/%d", snapshot.ActiveStepCompleted, snapshot.ActiveStepTotal, snapshot.CycleCompleted, snapshot.CycleTotal)
	}
	if snapshot.ScheduledCandidates != 0 {
		t.Fatalf("in-flight items reported as new candidates: %d", snapshot.ScheduledCandidates)
	}
	if len(snapshot.ActiveSteps) != 2 || len(snapshot.Steps) != 2 {
		t.Fatalf("step stats lengths = active:%d all:%d", len(snapshot.ActiveSteps), len(snapshot.Steps))
	}
	if german := snapshot.ActiveSteps[0]; german.Queued != 1 || german.Succeeded != 1 || german.Waiting != 0 {
		t.Fatalf("active German stats = %#v", german)
	}
	if translation := snapshot.ActiveSteps[1]; translation.Waiting != 2 || translation.ReadyAfterStage != 1 || translation.Queued != 0 {
		t.Fatalf("active translation stats = %#v", translation)
	}
	if snapshot.Steps[0].Succeeded != 1 || snapshot.Steps[1].Waiting != 2 {
		t.Fatalf("all-cycle stats = %#v", snapshot.Steps)
	}

	insertPipelineDocuments(t, ctx, database, now.Add(2*time.Minute), "three")
	snapshot, err = database.PipelineSnapshot(ctx, "fixture", []string{"german_analysis", "english_translation"}, now.Add(2*time.Minute))
	if err != nil || snapshot.ScheduledCandidates != 1 {
		t.Fatalf("new candidate outside frozen cycle = %#v/%v", snapshot, err)
	}
}

func TestManualCyclesCoalesceExactDuplicatesAndSupersedeQueuedAutomaticWork(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "manual-priority.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	plans := testPipelinePlans()

	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	condition, _ := sourceStatusCondition("fixture")
	automaticID, count, err := createPipelineCycleTx(ctx, tx, "scheduled", "fixture", true, "", condition+" AND COALESCE(i.body_de,'')<>''", nil, plans, now)
	if err != nil || count != 1 {
		t.Fatalf("create queued scheduled cycle = %d/%d/%v", automaticID, count, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	first, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, nil, true, now.Add(time.Second))
	if err != nil || first.Requested != 1 {
		t.Fatalf("first manual request = %#v/%v", first, err)
	}
	var automaticStatus string
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM processing_cycles WHERE id=?`, automaticID).Scan(&automaticStatus); err != nil {
		t.Fatal(err)
	}
	if automaticStatus != "superseded" {
		t.Fatalf("queued automatic status = %q", automaticStatus)
	}
	duplicate, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, nil, true, now.Add(2*time.Second))
	if err != nil || duplicate.CycleID != first.CycleID || duplicate.Requested != 0 || duplicate.Current != 1 {
		t.Fatalf("duplicate manual request = %#v/%v, first=%#v", duplicate, err, first)
	}
	different := append([]PipelineStepPlan(nil), plans...)
	different[1].Model = "other-translate:4b"
	separate, err := database.CreateManualPipelineCycle(ctx, "fixture", different, nil, true, now.Add(3*time.Second))
	if err != nil || separate.CycleID == first.CycleID {
		t.Fatalf("different-model manual request = %#v/%v", separate, err)
	}
}

func TestManualRequestDoesNotInterruptHealthyRunningScheduledCycle(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "healthy-priority.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	plans := testPipelinePlans()
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", plans, true, now)
	if err != nil || !found {
		t.Fatalf("activate scheduled cycle = %#v/%t/%v", cycle, found, err)
	}
	if _, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, nil, true, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM processing_cycles WHERE id=?`, cycle.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("healthy scheduled cycle was interrupted: %q", status)
	}
}

func TestPipelineRecoveryAndSourceRevisionSupersession(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "recover-supersede.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", testPipelinePlans(), true, now)
	if err != nil || !found {
		t.Fatalf("activate cycle = %#v/%t/%v", cycle, found, err)
	}
	job, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now)
	if err != nil || !found {
		t.Fatalf("claim before restart = %#v/%t/%v", job, found, err)
	}
	if err := database.RecoverPipeline(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	recovered, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now.Add(time.Minute))
	if err != nil || !found || recovered.ID != job.ID || recovered.AttemptCount != 2 {
		t.Fatalf("recovered job = %#v/%t/%v", recovered, found, err)
	}
	if err := database.CompletePipelineJob(ctx, recovered, []PipelineValue{{Kind: "title_de", Value: "Accepted DE"}, {Kind: "summary_de", Value: "Accepted summary."}, {Kind: "category", Value: "other"}, {Kind: "privacy_status", Value: "safe"}, {Kind: "privacy_flags", Value: "[]"}}, recovered.ModelIdentity, HashPipelineInput("de"), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	advance, err := database.AdvancePipelineCycle(ctx, cycle, 2, now.Add(3*time.Minute))
	if err != nil || !advance.Advanced {
		t.Fatalf("advance after recovery = %#v/%v", advance, err)
	}
	cycle.ActiveStep = 1
	revised := domain.SourceDocument{
		ExternalID: "one", SourceURL: "https://fixture.invalid/one", Title: "Release one", PublishedAt: now,
		FeedFingerprint: "feed-one-v2", SourceHash: "source-one-v2",
		Incidents: []domain.Incident{{Number: "1", Position: 0, TitleDE: "Titel one revised", BodyDE: "Revised body", ContentHash: "content-one-v2"}},
	}
	if err := database.UpsertDocuments(ctx, []domain.SourceDocument{revised}, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 1, now.Add(5*time.Minute)); err != nil || found {
		t.Fatalf("stale translation claim = found:%t err:%v", found, err)
	}
	var itemStatus, runStatus string
	if err := database.db.QueryRowContext(ctx, `SELECT ci.status,r.status FROM processing_cycle_items ci JOIN presentation_runs r ON r.id=ci.presentation_run_id WHERE ci.cycle_id=?`, cycle.ID).Scan(&itemStatus, &runStatus); err != nil {
		t.Fatal(err)
	}
	if itemStatus != "superseded" || runStatus != "superseded" {
		t.Fatalf("stale item/run = %q/%q", itemStatus, runStatus)
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", []string{"german_analysis", "english_translation"}, now.Add(5*time.Minute))
	if err != nil || snapshot.ScheduledCandidates != 1 {
		t.Fatalf("revised source eligibility = %#v/%v", snapshot, err)
	}
}

func insertPipelineDocuments(t *testing.T, ctx context.Context, database *Store, now time.Time, ids ...string) {
	t.Helper()
	documents := make([]domain.SourceDocument, 0, len(ids))
	for index, id := range ids {
		documents = append(documents, domain.SourceDocument{
			ExternalID: id, SourceURL: "https://fixture.invalid/" + id, Title: "Release " + id,
			PublishedAt: now.Add(time.Duration(index) * time.Second), FeedFingerprint: "feed-" + id, SourceHash: "source-" + id,
			Incidents: []domain.Incident{{Number: "1", Position: 0, TitleDE: "Titel " + id, BodyDE: "Text " + id, ContentHash: "content-" + id}},
		})
	}
	if err := database.UpsertDocuments(ctx, documents, now); err != nil {
		t.Fatal(err)
	}
}
