package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func testPostProcessingContract(scope, promptVersion string, outputKinds []string, inputKinds ...string) PostProcessingContract {
	return PostProcessingContract{scope: {PromptVersion: promptVersion, InputKinds: inputKinds, OutputKinds: outputKinds}}
}

func TestPostProcessingGenericLifecycleKeepsSuccessfulValueDuringForcedReplacement(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "post-processing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var automaticAfter string
	if err := database.db.QueryRowContext(ctx, `SELECT automatic_after FROM post_processing_scopes WHERE processor_key='translation' AND scope_key='en'`).Scan(&automaticAfter); err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339Nano, automaticAfter)
	if err != nil {
		t.Fatal(err)
	}
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
	job, found, err := database.ClaimPostProcessingJob(ctx, "translation", testPostProcessingContract("en", plan.PromptVersion, []string{"title", "summary"}, "title_de", "summary_de"), PostProcessingClaimOptions{AllowScheduled: true}, now.Add(4*time.Minute))
	if err != nil || !found || job.PresentationRunID != runID || job.RequestKind != "scheduled" || job.AttemptCount != 1 {
		t.Fatalf("scheduled claim = %#v/%t/%v", job, found, err)
	}
	if len(job.InputValues) != 2 || job.InputValues["title_de"] != "Titel" || job.InputValues["summary_de"] != "Zusammenfassung." {
		t.Fatalf("translation claim received undeclared inputs: %#v", job.InputValues)
	}
	activeSnapshot, err := database.PipelineSnapshot(ctx, "fixture", []string{"incident_metadata", "german_presentation"}, nil, now.Add(4*time.Minute+30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var activeTranslationStats *PostProcessingQueueStats
	for index := range activeSnapshot.PostProcessing {
		if activeSnapshot.PostProcessing[index].ProcessorKey == "translation" && activeSnapshot.PostProcessing[index].ScopeKey == "en" {
			activeTranslationStats = &activeSnapshot.PostProcessing[index]
			break
		}
	}
	if activeTranslationStats == nil || activeTranslationStats.QueueStartedAt == nil || !activeTranslationStats.QueueStartedAt.Equal(now.Add(2*time.Minute)) || activeTranslationStats.RunningStartedAt == nil || !activeTranslationStats.RunningStartedAt.Equal(now.Add(4*time.Minute)) {
		t.Fatalf("active translation timing stats = %#v", activeTranslationStats)
	}
	if err := database.CompletePostProcessingJob(ctx, job, []PipelineValue{{Kind: "title", Value: "First title"}, {Kind: "summary", Value: "First summary."}}, "translate:4b", job.InputHash, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	if queued, err := database.QueueIncidentPostProcessing(ctx, incidentID, []PostProcessingPlan{plan}, now.Add(6*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("forced incident queue = %d/%v", queued, err)
	}
	visible, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{TranslationLanguage: "en"})
	if err != nil || visible.AITranslatedTitle != "First title" {
		t.Fatalf("successful fallback during replacement = %#v/%v", visible, err)
	}
	replacement, found, err := database.ClaimPostProcessingJob(ctx, "translation", testPostProcessingContract("en", plan.PromptVersion, []string{"title", "summary"}, "title_de", "summary_de"), PostProcessingClaimOptions{}, now.Add(7*time.Minute))
	if err != nil || !found || replacement.RequestKind != "manual" {
		t.Fatalf("manual replacement claim = %#v/%t/%v", replacement, found, err)
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", []string{"incident_metadata", "german_presentation"}, nil, now.Add(7*time.Minute+30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var translationStats *PostProcessingQueueStats
	for index := range snapshot.PostProcessing {
		if snapshot.PostProcessing[index].ProcessorKey == "translation" && snapshot.PostProcessing[index].ScopeKey == "en" {
			translationStats = &snapshot.PostProcessing[index]
			break
		}
	}
	if translationStats == nil || translationStats.Running != 1 || translationStats.QueueStartedAt == nil || !translationStats.QueueStartedAt.Equal(now.Add(6*time.Minute)) || translationStats.RunningStartedAt == nil || !translationStats.RunningStartedAt.Equal(now.Add(7*time.Minute)) {
		t.Fatalf("running translation stats = %#v", translationStats)
	}
	if err := database.CompletePostProcessingJob(ctx, replacement, []PipelineValue{{Kind: "title", Value: "Second title"}, {Kind: "summary", Value: "Second summary."}}, "translate:4b", replacement.InputHash, now.Add(8*time.Minute)); err != nil {
		t.Fatal(err)
	}
	visible, err = database.GetPresentationIncident(ctx, incidentID, PresentationScope{TranslationLanguage: "en"})
	if err != nil || visible.AITranslatedTitle != "Second title" {
		t.Fatalf("replacement selection = %#v/%v", visible, err)
	}
}

func TestPostProcessingQueueStatsExplainAutomaticRetryWaits(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "post-processing-retry-stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 31, 5, 45, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "retry-stats")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{
		"title_de": "Titel", "summary_de": "Zusammenfassung.", "category": "other",
	})
	plan := PostProcessingPlan{ProcessorKey: "translation", ScopeKey: "en", PromptVersion: "translation-v1", Model: "translate:4b", InputKinds: []string{"title_de", "summary_de"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "scheduled", false, now); err != nil || queued != 1 {
		t.Fatalf("queue scheduled retry fixture = %d/%v", queued, err)
	}
	job, found, err := database.ClaimPostProcessingJob(ctx, "translation", testPostProcessingContract("en", plan.PromptVersion, []string{"title", "summary"}, plan.InputKinds...), PostProcessingClaimOptions{AllowScheduled: true}, now.Add(time.Minute))
	if err != nil || !found {
		t.Fatalf("claim scheduled retry fixture = %#v/%t/%v", job, found, err)
	}
	retryAt := now.Add(10 * time.Minute)
	if err := database.FailPostProcessingJob(ctx, job, "pending", "output", &retryAt, now.Add(2*time.Minute), errors.New("invalid generated output")); err != nil {
		t.Fatal(err)
	}

	assertStats := func(at time.Time, wantReady int) {
		t.Helper()
		snapshot, err := database.PipelineSnapshot(ctx, "fixture", nil, nil, at)
		if err != nil {
			t.Fatal(err)
		}
		for _, stat := range snapshot.PostProcessing {
			if stat.ProcessorKey != "translation" || stat.ScopeKey != "en" {
				continue
			}
			if stat.Retrying != 1 || stat.AutomaticRetrying != 1 || stat.AutomaticRetryReady != wantReady || stat.NextRetryAt == nil || !stat.NextRetryAt.Equal(retryAt) || stat.RetryFailureKinds["output"] != 1 {
				t.Fatalf("retry stats at %s = %#v", at, stat)
			}
			return
		}
		t.Fatal("translation/en queue stats missing")
	}
	assertStats(now.Add(5*time.Minute), 0)
	assertStats(now.Add(11*time.Minute), 1)
	job, found, err = database.ClaimPostProcessingJob(ctx, "translation", testPostProcessingContract("en", plan.PromptVersion, []string{"title", "summary"}, plan.InputKinds...), PostProcessingClaimOptions{AllowScheduled: true}, now.Add(11*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim eligible retry fixture = %#v/%t/%v", job, found, err)
	}
	if err := database.FailPostProcessingJob(ctx, job, "needs_review", "output", nil, now.Add(12*time.Minute), errors.New("invalid generated output")); err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", nil, nil, now.Add(13*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, stat := range snapshot.PostProcessing {
		if stat.ProcessorKey == "translation" && stat.ScopeKey == "en" {
			if stat.Retrying != 0 || stat.NeedsReview != 1 || stat.AttentionFailureKinds["output"] != 1 {
				t.Fatalf("attention stats = %#v", stat)
			}
			return
		}
	}
	t.Fatal("translation/en attention stats missing")
}

func TestPostProcessingReadersRequireCompleteSuccessesAcrossProcessors(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "post-processing-complete-readers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 12, 30, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "complete-readers")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{
		"title_de": "Titel", "summary_de": "Zusammenfassung.", "category": "traffic",
		"report_kind": "incident", "public_assistance_status": "not_requested", "public_assistance_types": "[]",
	})
	if err := database.EnsurePostProcessingScopes(ctx, []PostProcessingScope{{ProcessorKey: "category_verification", ScopeKey: "default"}}, now); err != nil {
		t.Fatal(err)
	}
	insertSuccess := func(processor, scope, model string, completedAt time.Time, values ...PipelineValue) {
		t.Helper()
		formatted := formatTime(completedAt)
		result, err := database.db.ExecContext(ctx, `INSERT INTO post_processing_jobs(
			presentation_run_id,processor_key,scope_key,request_kind,status,model_identity,prompt_version,input_hash,
			attempt_count,started_at,completed_at,created_at,updated_at
		) VALUES(?,?,?,'manual','succeeded',?,'test-v1','hash',1,?,?,?,?)`, runID, processor, scope, model, formatted, formatted, formatted, formatted)
		if err != nil {
			t.Fatal(err)
		}
		jobID, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if _, err := database.db.ExecContext(ctx, `INSERT INTO post_processing_values(job_id,kind,value) VALUES(?,?,?)`, jobID, value.Kind, value.Value); err != nil {
				t.Fatal(err)
			}
		}
	}

	insertSuccess("category_verification", "default", "category-complete:4b", now.Add(time.Minute),
		PipelineValue{Kind: "is_correct", Value: "false"}, PipelineValue{Kind: "corrected_category", Value: "other"})
	insertSuccess("category_verification", "default", "category-incomplete:4b", now.Add(2*time.Minute),
		PipelineValue{Kind: "is_correct", Value: "false"})
	insertSuccess("translation", "en", "translation-complete:4b", now.Add(3*time.Minute),
		PipelineValue{Kind: "title", Value: "Complete title"}, PipelineValue{Kind: "summary", Value: "Complete summary."})
	insertSuccess("translation", "en", "translation-incomplete:4b", now.Add(4*time.Minute),
		PipelineValue{Kind: "title", Value: "Partial title"})
	insertSuccess("translation", "fr", "translation-incomplete:4b", now.Add(5*time.Minute),
		PipelineValue{Kind: "title", Value: "Titre partiel"})

	assertReaderProjectionParity(t, database)

	record, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{Language: "en", TranslationLanguage: "en", PublicOnly: true})
	if err != nil || record.AICategory != "other" || record.AICategoryVerificationModel != "category-complete:4b" || record.AITranslatedTitle != "Complete title" || record.AITranslatedSummary != "Complete summary." || record.AITranslationModel != "translation-complete:4b" {
		t.Fatalf("complete reader selection = %#v/%v", record, err)
	}
	if _, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{Language: "fr", TranslationLanguage: "fr", PublicOnly: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("incomplete-only translation became public: %v", err)
	}
	categories, err := database.ListAdminCategoryVerifications(ctx, []int64{incidentID})
	if err != nil || len(categories) != 1 || categories[0].EffectiveCategory != "other" || categories[0].Model != "category-complete:4b" {
		t.Fatalf("admin category complete selection = %#v/%v", categories, err)
	}
	translations, err := database.ListAdminTranslations(ctx, []int64{incidentID}, []string{"en"})
	if err != nil || len(translations) != 1 || translations[0].Title != "Complete title" || translations[0].Summary != "Complete summary." || translations[0].Model != "translation-complete:4b" || translations[0].Status != "succeeded" {
		t.Fatalf("admin translation complete selection = %#v/%v", translations, err)
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", nil, []PostProcessingCounterSpec{{
		ProcessorKey: "category_verification", ScopeKey: "default", CounterKey: "corrected",
		OutputKind: "is_correct", EqualsValue: "false", RequiredOutputKinds: []string{"is_correct", "corrected_category"},
	}}, now.Add(6*time.Minute))
	corrected := -1
	for _, stat := range snapshot.PostProcessing {
		if stat.ProcessorKey == "category_verification" && stat.ScopeKey == "default" {
			corrected = stat.Counters["corrected"]
		}
	}
	if err != nil || corrected != 1 {
		t.Fatalf("complete corrected counter = %#v/%v", snapshot.PostProcessing, err)
	}
	links, err := database.ListPublicIncidentLinks(ctx, "fixture", PresentationScope{Language: "en"})
	if err != nil || len(links) != 1 || !links[0].ModifiedAt.Equal(now.Add(3*time.Minute)) {
		t.Fatalf("complete modification time = %#v/%v", links, err)
	}
}

func TestPostProcessingRetryRecoveryAndTransactionalCompletion(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "post-processing-recovery.db"))
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
	job, found, err := database.ClaimPostProcessingJob(ctx, "test_processor", testPostProcessingContract("default", plan.PromptVersion, []string{"note"}, "title_de"), PostProcessingClaimOptions{}, now)
	if err != nil || !found {
		t.Fatalf("claim retry job = %#v/%t/%v", job, found, err)
	}
	retryAt := now.Add(time.Minute)
	if err := database.FailPostProcessingJob(ctx, job, "pending", "transient", &retryAt, now, errors.New("private provider failure")); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.ClaimPostProcessingJob(ctx, "test_processor", testPostProcessingContract("default", plan.PromptVersion, []string{"note"}, "title_de"), PostProcessingClaimOptions{}, now.Add(30*time.Second)); err != nil || found {
		t.Fatalf("early retry claim = %t/%v", found, err)
	}
	retried, found, err := database.ClaimPostProcessingJob(ctx, "test_processor", testPostProcessingContract("default", plan.PromptVersion, []string{"note"}, "title_de"), PostProcessingClaimOptions{}, retryAt)
	if err != nil || !found || retried.AttemptCount != 2 {
		t.Fatalf("due retry claim = %#v/%t/%v", retried, found, err)
	}
	if err := database.RecoverPostProcessing(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	recovered, found, err := database.ClaimPostProcessingJob(ctx, "test_processor", testPostProcessingContract("default", plan.PromptVersion, []string{"note"}, "title_de"), PostProcessingClaimOptions{}, now.Add(2*time.Minute))
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
	if err := database.CompletePostProcessingJob(ctx, recovered, []PipelineValue{{Kind: "note", Value: "valid"}}, recovered.ModelIdentity, "changed-hash", now.Add(3*time.Minute)); err == nil {
		t.Fatal("changed post-processing input hash unexpectedly committed")
	}
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM post_processing_jobs WHERE id=?`, recovered.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_values WHERE job_id=?`, recovered.ID).Scan(&valueCount); err != nil {
		t.Fatal(err)
	}
	if status != "running" || valueCount != 0 {
		t.Fatalf("hash mismatch was not transactional: status=%s values=%d", status, valueCount)
	}
}

func TestPostProcessingSkipsAndDeduplicatesUnavailableRequiredInput(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "post-processing-source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 13, 30, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "source")
	var incidentID int64
	var sourceHash, originalTitle string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash,title_de FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash, &originalTitle); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE incidents SET body_de='' WHERE id=?`, incidentID); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{
		"title_de": "Generated title", "summary_de": "Generated summary.", "category": "other",
		"public_assistance_status": "not_requested", "public_assistance_types": "[]",
	})
	plan := PostProcessingPlan{ProcessorKey: "public_assistance_verification", ScopeKey: "default", PromptVersion: "assistance-v1", Model: "verify:4b", InputKinds: []string{"original_title", "incident_body", "public_assistance_status", "public_assistance_types"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", false, now); err != nil || queued != 0 {
		t.Fatalf("skip raw-source verifier = %d/%v", queued, err)
	}
	expectedHash := HashPipelineInput(
		"processor", plan.ProcessorKey, "scope", plan.ScopeKey,
		"original_title", originalTitle, "incident_body", "",
		"public_assistance_status", "not_requested", "public_assistance_types", "[]",
		plan.PromptVersion, plan.Model,
	)
	var status, reason, detail, inputHash string
	var attempts int
	if err := database.db.QueryRowContext(ctx, `SELECT status,status_reason,status_detail,input_hash,attempt_count
		FROM post_processing_jobs WHERE presentation_run_id=? AND processor_key=? AND scope_key=?`, runID, plan.ProcessorKey, plan.ScopeKey).
		Scan(&status, &reason, &detail, &inputHash, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "skipped" || reason != PostProcessingStatusReasonMissingInput || detail != "incident_body" || inputHash != expectedHash || attempts != 0 {
		t.Fatalf("skip audit = %q/%q/%q/%q/%d, want missing incident body and hash %s", status, reason, detail, inputHash, attempts, expectedHash)
	}
	if job, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract("default", plan.PromptVersion, []string{"is_correct", "corrected_public_assistance_status", "corrected_public_assistance_types"}, plan.InputKinds...), PostProcessingClaimOptions{}, now); err != nil || found {
		t.Fatalf("skipped verifier became claimable = %#v/%t/%v", job, found, err)
	}
	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", false, now.Add(time.Minute)); err != nil || queued != 0 {
		t.Fatalf("deduplicate skipped verifier = %d/%v", queued, err)
	}
	var jobs int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_jobs WHERE presentation_run_id=? AND processor_key=?`, runID, plan.ProcessorKey).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("deduplicated skip count = %d/%v", jobs, err)
	}
	history, err := database.ListPipelineHistory(ctx, "fixture", 10, nil, nil)
	if err != nil || len(history.Entries) != 1 {
		t.Fatalf("skipped history = %#v/%v", history, err)
	}
	entry := history.Entries[0]
	if entry.Status != "skipped" || entry.StatusReason != PostProcessingStatusReasonMissingInput || entry.StatusDetail != "incident_body" || entry.AttemptCount != 0 {
		t.Fatalf("skipped history entry = %#v", entry)
	}
}

func TestPostProcessingInputHashKeepsStructuredCompatibilityAndSeparatesNativeAdapters(t *testing.T) {
	values := map[string]string{"title_de": "Titel", "summary_de": "Zusammenfassung."}
	kinds := []string{"title_de", "summary_de"}
	legacy := HashPipelineInput("processor", "translation", "scope", "en", "title_de", "Titel", "summary_de", "Zusammenfassung.", "prompt-v1", "model:4b")
	structured := postProcessingInputHash("translation", "en", kinds, values, "prompt-v1", "model:4b", "structured")
	native := postProcessingInputHash("translation", "en", kinds, values, "prompt-v1", "model:4b", "hy-mt2")
	if structured != legacy {
		t.Fatalf("structured hash = %q, want legacy %q", structured, legacy)
	}
	if native == structured {
		t.Fatal("native adapter did not affect input hash")
	}
}

func TestPostProcessingClaimSkipsInputThatBecameUnavailable(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "post-processing-late-skip.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 13, 40, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "late-skip")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{
		"title_de": "Generated title", "summary_de": "Generated summary.", "category": "other",
		"public_assistance_status": "not_requested", "public_assistance_types": "[]",
	})
	plan := PostProcessingPlan{ProcessorKey: "public_assistance_verification", ScopeKey: "default", PromptVersion: "assistance-v1", Model: "verify:4b", InputKinds: []string{"original_title", "incident_body", "public_assistance_status", "public_assistance_types"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", false, now); err != nil || queued != 1 {
		t.Fatalf("queue verifier before source removal = %d/%v", queued, err)
	}
	contract := testPostProcessingContract("default", plan.PromptVersion, []string{"is_correct", "corrected_public_assistance_status", "corrected_public_assistance_types"}, plan.InputKinds...)
	job, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, contract, PostProcessingClaimOptions{}, now.Add(time.Minute))
	if err != nil || !found {
		t.Fatalf("claim verifier before source removal = %#v/%t/%v", job, found, err)
	}
	retryAt := now.Add(2 * time.Minute)
	if err := database.FailPostProcessingJob(ctx, job, "pending", "transient", &retryAt, now.Add(time.Minute), errors.New("provider unavailable")); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE incidents SET body_de='' WHERE id=?`, incidentID); err != nil {
		t.Fatal(err)
	}
	if job, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, contract, PostProcessingClaimOptions{}, retryAt); err != nil || found {
		t.Fatalf("unavailable retrying verifier became claimable = %#v/%t/%v", job, found, err)
	}
	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", false, now.Add(3*time.Minute)); err != nil || queued != 0 {
		t.Fatalf("deduplicate late skip = %d/%v", queued, err)
	}
	var status, reason, detail, nextRetryAt, failureKind string
	var attempts, jobs int
	if err := database.db.QueryRowContext(ctx, `SELECT status,status_reason,status_detail,COALESCE(next_retry_at,''),COALESCE(failure_kind,''),attempt_count
		FROM post_processing_jobs WHERE presentation_run_id=?`, runID).Scan(&status, &reason, &detail, &nextRetryAt, &failureKind, &attempts); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_jobs WHERE presentation_run_id=?`, runID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if status != "skipped" || reason != PostProcessingStatusReasonMissingInput || detail != "incident_body" || nextRetryAt != "" || failureKind != "" || attempts != 1 || jobs != 1 {
		t.Fatalf("late skip audit = %q/%q/%q retry=%q failure=%q attempts=%d jobs=%d", status, reason, detail, nextRetryAt, failureKind, attempts, jobs)
	}
}

func TestPostProcessingSkipsAnyUnavailableDeclaredInput(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "post-processing-generic-skip.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 13, 50, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "generic-skip")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{"title_de": "Titel"})
	plan := PostProcessingPlan{ProcessorKey: "quality_note", ScopeKey: "default", PromptVersion: "quality-v1", Model: "quality:4b", InputKinds: []string{"title_de", "required_note"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", false, now); err != nil || queued != 0 {
		t.Fatalf("generic unavailable input queue = %d/%v", queued, err)
	}
	var status, reason, detail string
	if err := database.db.QueryRowContext(ctx, `SELECT status,status_reason,status_detail FROM post_processing_jobs WHERE presentation_run_id=?`, runID).Scan(&status, &reason, &detail); err != nil {
		t.Fatal(err)
	}
	if status != "skipped" || reason != PostProcessingStatusReasonMissingInput || detail != "required_note" {
		t.Fatalf("generic skip audit = %q/%q/%q", status, reason, detail)
	}
}

func TestPostProcessingClaimsStalePromptWithoutMaterializingCurrentInputs(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "post-processing-stale-prompt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 13, 45, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "stale-prompt")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{
		"title_de": "Titel", "summary_de": "Zusammenfassung.", "category": "other",
	})
	stalePlan := PostProcessingPlan{
		ProcessorKey: "future_processor", ScopeKey: "default", PromptVersion: "future-v1",
		Model: "verify:4b", InputKinds: []string{"category"},
	}
	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{stalePlan}, "manual", false, now); err != nil || queued != 1 {
		t.Fatalf("queue stale prompt = %d/%v", queued, err)
	}
	job, found, err := database.ClaimPostProcessingJob(ctx, stalePlan.ProcessorKey, testPostProcessingContract("default", "future-v2", []string{"new_output"}, "new_required_input"), PostProcessingClaimOptions{}, now)
	if err != nil || !found {
		t.Fatalf("claim stale prompt = %#v/%t/%v", job, found, err)
	}
	if job.PromptVersion != stalePlan.PromptVersion || len(job.InputValues) != 0 || len(job.OutputKinds) != 0 || job.AttemptCount != 1 {
		t.Fatalf("stale prompt claim = %#v", job)
	}
	if err := database.CompletePostProcessingJob(ctx, job, []PipelineValue{{Kind: "new_output", Value: "unsafe"}}, job.ModelIdentity, job.InputHash, now); err == nil {
		t.Fatal("stale prompt job unexpectedly completed with the current output contract")
	}
}

func TestPublicAssistanceVerificationKeepsLatestSuccessfulResult(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "public-assistance-selection.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "assistance-selection")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{
		"title_de": "Titel", "summary_de": "Zusammenfassung.", "category": "other",
		"public_assistance_status": "not_requested", "public_assistance_types": "[]",
	})
	plan := PostProcessingPlan{ProcessorKey: "public_assistance_verification", ScopeKey: "default", PromptVersion: "assistance-v1", Model: "verify:4b", InputKinds: []string{"original_title", "incident_body", "public_assistance_status", "public_assistance_types"}}

	assertEffective := func(status, types string) {
		t.Helper()
		record, err := database.GetPresentationIncident(ctx, incidentID, PresentationScope{Language: "de"})
		if err != nil {
			t.Fatal(err)
		}
		if record.AIPublicAssistanceStatus != status || record.AIPublicAssistanceTypes != types {
			t.Fatalf("effective assistance = %s/%s, want %s/%s", record.AIPublicAssistanceStatus, record.AIPublicAssistanceTypes, status, types)
		}
	}
	assertEffective("not_requested", "[]")

	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", false, now.Add(time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue first verification = %d/%v", queued, err)
	}
	first, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract("default", plan.PromptVersion, []string{"is_correct", "corrected_public_assistance_status", "corrected_public_assistance_types"}, plan.InputKinds...), PostProcessingClaimOptions{}, now.Add(2*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim first verification = %#v/%t/%v", first, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, first, []PipelineValue{
		{Kind: "is_correct", Value: "false"},
		{Kind: "corrected_public_assistance_status", Value: "requested"},
		{Kind: "corrected_public_assistance_types", Value: `["identify_person"]`},
	}, first.ModelIdentity, first.InputHash, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertEffective("requested", `["identify_person"]`)
	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", true, now.Add(3*time.Minute+10*time.Second)); err != nil || queued != 1 {
		t.Fatalf("queue incomplete replacement = %d/%v", queued, err)
	}
	incomplete, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract("default", plan.PromptVersion, []string{"is_correct", "corrected_public_assistance_status", "corrected_public_assistance_types"}, plan.InputKinds...), PostProcessingClaimOptions{}, now.Add(3*time.Minute+20*time.Second))
	if err != nil || !found {
		t.Fatalf("claim incomplete replacement = %#v/%t/%v", incomplete, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, incomplete, []PipelineValue{
		{Kind: "corrected_public_assistance_status", Value: "not_requested"},
	}, incomplete.ModelIdentity, incomplete.InputHash, now.Add(3*time.Minute+30*time.Second)); err == nil {
		t.Fatal("incomplete output contract unexpectedly succeeded")
	}
	var incompleteStatus string
	var incompleteValues int
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM post_processing_jobs WHERE id=?`, incomplete.ID).Scan(&incompleteStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_values WHERE job_id=?`, incomplete.ID).Scan(&incompleteValues); err != nil {
		t.Fatal(err)
	}
	if incompleteStatus != "running" || incompleteValues != 0 {
		t.Fatalf("incomplete completion was not transactional: status=%s values=%d", incompleteStatus, incompleteValues)
	}
	// Simulate an incomplete success left by an older implementation or manual
	// database corruption; readers must retain the prior complete result.
	if _, err := database.db.ExecContext(ctx, `INSERT INTO post_processing_values(job_id,kind,value) VALUES(?,?,?)`, incomplete.ID, "corrected_public_assistance_status", "not_requested"); err != nil {
		t.Fatal(err)
	}
	completedAt := formatTime(now.Add(3*time.Minute + 30*time.Second))
	if _, err := database.db.ExecContext(ctx, `UPDATE post_processing_jobs SET status='succeeded',completed_at=?,updated_at=? WHERE id=?`, completedAt, completedAt, incomplete.ID); err != nil {
		t.Fatal(err)
	}
	assertEffective("requested", `["identify_person"]`)

	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", true, now.Add(4*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue failed replacement = %d/%v", queued, err)
	}
	failed, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract("default", plan.PromptVersion, []string{"is_correct", "corrected_public_assistance_status", "corrected_public_assistance_types"}, plan.InputKinds...), PostProcessingClaimOptions{}, now.Add(5*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim failed replacement = %#v/%t/%v", failed, found, err)
	}
	if err := database.FailPostProcessingJob(ctx, failed, "needs_review", "invalid_response", nil, now.Add(6*time.Minute), errors.New("private model output")); err != nil {
		t.Fatal(err)
	}
	assertEffective("requested", `["identify_person"]`)

	admin, err := database.ListAdminPublicAssistanceVerifications(ctx, []int64{incidentID})
	if err != nil || len(admin) != 1 || admin[0].Status != "needs_review" || admin[0].EffectiveStatus != "requested" || admin[0].IsCorrect == nil || *admin[0].IsCorrect {
		t.Fatalf("admin fallback = %#v/%v", admin, err)
	}

	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", true, now.Add(7*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue later replacement = %d/%v", queued, err)
	}
	later, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract("default", plan.PromptVersion, []string{"is_correct", "corrected_public_assistance_status", "corrected_public_assistance_types"}, plan.InputKinds...), PostProcessingClaimOptions{}, now.Add(8*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim later replacement = %#v/%t/%v", later, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, later, []PipelineValue{
		{Kind: "is_correct", Value: "true"},
		{Kind: "corrected_public_assistance_status", Value: "not_requested"},
		{Kind: "corrected_public_assistance_types", Value: "[]"},
	}, later.ModelIdentity, later.InputHash, now.Add(9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertEffective("not_requested", "[]")
}

func TestPublicAssistanceVerificationCutoverRequiresManualHistoricalBackfill(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "public-assistance-cutover.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "old")
	var oldIncidentID int64
	var oldHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents WHERE title_de='Titel old'`).Scan(&oldIncidentID, &oldHash); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"title_de": "Titel", "summary_de": "Zusammenfassung.", "category": "other",
		"public_assistance_status": "not_requested", "public_assistance_types": "[]",
	}
	insertCompletedPresentationRun(t, ctx, database, oldIncidentID, oldHash, PipelineVersion, now, values)
	cutover := now.Add(time.Minute)
	if err := database.EnsurePostProcessingScopes(ctx, []PostProcessingScope{{ProcessorKey: "public_assistance_verification", ScopeKey: "default"}}, cutover); err != nil {
		t.Fatal(err)
	}
	plan := PostProcessingPlan{ProcessorKey: "public_assistance_verification", ScopeKey: "default", PromptVersion: "assistance-v1", Model: "verify:4b", InputKinds: []string{"original_title", "incident_body", "public_assistance_status", "public_assistance_types"}}
	if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, false, now.Add(2*time.Minute)); err != nil || queued != 0 {
		t.Fatalf("automatic historical queue = %d/%v, want 0", queued, err)
	}

	insertPipelineDocuments(t, ctx, database, now.Add(3*time.Minute), "new")
	var newIncidentID int64
	var newHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents WHERE title_de='Titel new'`).Scan(&newIncidentID, &newHash); err != nil {
		t.Fatal(err)
	}
	insertCompletedPresentationRun(t, ctx, database, newIncidentID, newHash, PipelineVersion, now.Add(4*time.Minute), values)
	if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, false, now.Add(5*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("automatic post-cutover queue = %d/%v, want 1", queued, err)
	}
	if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, true, now.Add(6*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("manual historical backfill queue = %d/%v, want 1", queued, err)
	}
	manual, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract("default", plan.PromptVersion, []string{"is_correct", "corrected_public_assistance_status", "corrected_public_assistance_types"}, plan.InputKinds...), PostProcessingClaimOptions{}, now.Add(7*time.Minute))
	if err != nil || !found || manual.IncidentID != oldIncidentID || manual.RequestKind != "manual" {
		t.Fatalf("historical manual claim = %#v/%t/%v", manual, found, err)
	}
}

func TestGenericAdminVerificationSelectionSupportsAnotherProcessor(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "generic-verification-selection.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 15, 30, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "generic")
	var incidentID int64
	var sourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&incidentID, &sourceHash); err != nil {
		t.Fatal(err)
	}
	runID := insertCompletedPresentationRun(t, ctx, database, incidentID, sourceHash, PipelineVersion, now, map[string]string{
		"title_de": "Titel", "summary_de": "Zusammenfassung.", "category": "other",
	})
	plan := PostProcessingPlan{ProcessorKey: "future_verification", ScopeKey: "default", PromptVersion: "future-v1", Model: "verify:4b", InputKinds: []string{"category"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, runID, []PostProcessingPlan{plan}, "manual", false, now); err != nil || queued != 1 {
		t.Fatalf("queue future verifier = %d/%v", queued, err)
	}
	job, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract("default", plan.PromptVersion, []string{"is_correct", "corrected_value"}, plan.InputKinds...), PostProcessingClaimOptions{}, now)
	if err != nil || !found {
		t.Fatalf("claim future verifier = %#v/%t/%v", job, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, job, []PipelineValue{
		{Kind: "is_correct", Value: "false"}, {Kind: "corrected_value", Value: "traffic"},
	}, job.ModelIdentity, job.InputHash, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	selected, err := database.listAdminVerificationSelections(ctx, []int64{incidentID}, plan.ProcessorKey, "default", "is_correct", []adminVerificationValuePair{{OriginalKind: "category", CorrectedKind: "corrected_value"}})
	if err != nil || len(selected) != 1 {
		t.Fatalf("generic verifier selection = %#v/%v", selected, err)
	}
	if selected[0].OriginalValues[0] != "other" || selected[0].EffectiveValues[0] != "traffic" || selected[0].IsCorrect == nil || *selected[0].IsCorrect || selected[0].Model != "verify:4b" {
		t.Fatalf("generic verifier state = %#v", selected[0])
	}

	if queued, err := database.QueueIncidentPostProcessing(ctx, incidentID, []PostProcessingPlan{plan}, now.Add(2*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue verifier replacement = %d/%v", queued, err)
	}
	replacement, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract("default", plan.PromptVersion, []string{"is_correct", "corrected_value"}, plan.InputKinds...), PostProcessingClaimOptions{}, now.Add(3*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim verifier replacement = %#v/%t/%v", replacement, found, err)
	}
	privateError := errors.New("<script>private malformed output</script>")
	if err := database.FailPostProcessingJob(ctx, replacement, "needs_review", "output", nil, now.Add(4*time.Minute), privateError); err != nil {
		t.Fatal(err)
	}

	insertPipelineDocuments(t, ctx, database, now.Add(5*time.Minute), "never-verified")
	var neverIncidentID int64
	var neverSourceHash string
	if err := database.db.QueryRowContext(ctx, `SELECT i.id,i.content_hash FROM incidents i JOIN source_documents d ON d.id=i.source_document_id WHERE d.external_id='never-verified'`).Scan(&neverIncidentID, &neverSourceHash); err != nil {
		t.Fatal(err)
	}
	insertCompletedPresentationRun(t, ctx, database, neverIncidentID, neverSourceHash, PipelineVersion, now.Add(5*time.Minute), map[string]string{
		"title_de": "Nie geprüft", "summary_de": "Zusammenfassung.", "category": "other",
	})

	spec := AdminVerificationSpec{
		ProcessorKey: plan.ProcessorKey, ScopeKey: plan.ScopeKey, VerdictKind: "is_correct",
		Fields: []AdminVerificationFieldSpec{{OriginalKind: "category", CorrectedKind: "corrected_value"}},
	}
	coverage, err := database.AdminVerificationCoverageFor(ctx, "fixture", spec)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.Current != 2 || coverage.Verified != 1 || coverage.Unverified != 1 || coverage.NeverQueued != 1 || coverage.Attention != 1 || coverage.Corrected != 1 || coverage.ReplacementAttention != 1 {
		t.Fatalf("verification coverage = %#v", coverage)
	}
	attention, total, err := database.ListAdminVerificationIncidents(ctx, 10, 0, "fixture", spec, AdminVerificationsAttention)
	if err != nil || total != 1 || len(attention) != 1 {
		t.Fatalf("attention verification list = %#v/%d/%v", attention, total, err)
	}
	item := attention[0]
	if item.IncidentID != incidentID || item.OriginalValues[0] != "other" || item.EffectiveValues[0] != "traffic" || item.IsCorrect == nil || *item.IsCorrect || item.Status != "needs_review" || item.Attempts != 1 || item.FailureKind != "output" || item.ErrorMessage != privateError.Error() || item.SuccessModel != "verify:4b" || item.AttemptModel != "verify:4b" {
		t.Fatalf("attention verification item = %#v", item)
	}
	corrected, correctedTotal, err := database.ListAdminVerificationIncidents(ctx, 1, 0, "fixture", spec, AdminVerificationsCorrected)
	if err != nil || correctedTotal != 1 || len(corrected) != 1 || corrected[0].IncidentID != incidentID {
		t.Fatalf("retained corrected verification list = %#v/%d/%v", corrected, correctedTotal, err)
	}
	neverQueued, neverTotal, err := database.ListAdminVerificationIncidents(ctx, 1, 0, "fixture", spec, AdminVerificationsNeverQueued)
	if err != nil || neverTotal != 1 || len(neverQueued) != 1 || neverQueued[0].IncidentID != neverIncidentID {
		t.Fatalf("never-queued verification list = %#v/%d/%v", neverQueued, neverTotal, err)
	}
}

func TestEnsurePostProcessingScopesIsAtomic(t *testing.T) {
	ctx := context.Background()
	database, err := openTestStore(ctx, filepath.Join(t.TempDir(), "post-processing-scopes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	processor := "atomic_test_processor"
	err = database.EnsurePostProcessingScopes(ctx, []PostProcessingScope{
		{ProcessorKey: processor, ScopeKey: "valid"},
		{ProcessorKey: processor, ScopeKey: ""},
	}, time.Now())
	if err == nil {
		t.Fatal("invalid scope initialization unexpectedly succeeded")
	}
	var count int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_processing_scopes WHERE processor_key=?`, processor).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("partial scope initialization committed %d rows", count)
	}
}
