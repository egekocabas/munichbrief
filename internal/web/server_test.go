package web

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/source"
	"github.com/egekocabas/munichbrief/internal/store"
)

type testPresentation struct {
	TitleDE, SummaryDE, TitleEN, SummaryEN string
}

type testPresentationJob struct {
	IncidentID        int64
	PresentationRunID int64
	TitleDE           string
	BodyDE            string
}

func testPostProcessingContract(scope, promptVersion string, outputKinds []string, inputKinds ...string) store.PostProcessingContract {
	return store.PostProcessingContract{scope: {PromptVersion: promptVersion, InputKinds: inputKinds, OutputKinds: outputKinds}}
}

func seedV2Presentation(t *testing.T, database *store.Store, presentation testPresentation, now time.Time) testPresentationJob {
	t.Helper()
	ctx := context.Background()
	records, _, err := database.ListAdminIncidents(ctx, 1, 0, "fixture", store.PresentationScope{Language: "de"}, store.AdminIncidentsUnprocessed)
	if err != nil || len(records) != 1 {
		t.Fatalf("find unprocessed presentation target = %d/%v", len(records), err)
	}
	incidentID := records[0].ID
	plans, err := processing.StepPlans(map[string]string{processing.IncidentMetadataStep: "qwen3.5:4b", processing.GermanPresentationStep: "qwen3.5:4b"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, nil, &incidentID, false, now)
	if err != nil {
		t.Fatal(err)
	}
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", nil, false, now)
	if err != nil || !found || cycle.ID != request.CycleID {
		t.Fatalf("activate test presentation cycle = %#v/%t/%v", cycle, found, err)
	}
	metadata, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now)
	if err != nil || !found {
		t.Fatalf("claim test metadata = %#v/%t/%v", metadata, found, err)
	}
	metadataValues := []store.PipelineValue{{Kind: "category", Value: "other"}, {Kind: "report_kind", Value: "incident"}, {Kind: "public_assistance_status", Value: "not_requested"}, {Kind: "public_assistance_types", Value: "[]"}}
	if err := database.CompletePipelineJob(ctx, metadata, metadataValues, metadata.ModelIdentity, store.HashPipelineInput("metadata", metadata.SourceHash), now); err != nil {
		t.Fatal(err)
	}
	if advanced, err := database.AdvancePipelineCycle(ctx, cycle, len(plans), now); err != nil || !advanced.Advanced {
		t.Fatalf("advance test metadata = %#v/%v", advanced, err)
	}
	cycle.ActiveStep = 1
	german, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 1, now)
	if err != nil || !found {
		t.Fatalf("claim test German presentation = %#v/%t/%v", german, found, err)
	}
	values := []store.PipelineValue{{Kind: "title_de", Value: presentation.TitleDE}, {Kind: "summary_de", Value: presentation.SummaryDE}, {Kind: "privacy_status", Value: "safe"}, {Kind: "privacy_flags", Value: "[]"}}
	if err := database.CompletePipelineJob(ctx, german, values, german.ModelIdentity, store.HashPipelineInput("german", german.SourceHash), now); err != nil {
		t.Fatal(err)
	}
	if completed, err := database.AdvancePipelineCycle(ctx, cycle, len(plans), now); err != nil || !completed.Completed {
		t.Fatalf("complete test presentation cycle = %#v/%v", completed, err)
	}
	translationPlan := store.PostProcessingPlan{ProcessorKey: "translation", ScopeKey: "en", PromptVersion: processing.EnglishTranslationPromptVersion, Model: "qwen3.5:4b", InputKinds: []string{"title_de", "summary_de"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, german.PresentationRunID, []store.PostProcessingPlan{translationPlan}, "manual", false, now); err != nil || queued != 1 {
		t.Fatalf("queue test translation = %d/%v", queued, err)
	}
	translation, found, err := database.ClaimPostProcessingJob(ctx, "translation", testPostProcessingContract("en", translationPlan.PromptVersion, []string{"title", "summary"}, "title_de", "summary_de"), false, nil, now)
	if err != nil || !found {
		t.Fatalf("claim test translation = %#v/%t/%v", translation, found, err)
	}
	translated := []store.PipelineValue{{Kind: "title", Value: presentation.TitleEN}, {Kind: "summary", Value: presentation.SummaryEN}}
	if err := database.CompletePostProcessingJob(ctx, translation, translated, translation.ModelIdentity, translation.InputHash, now); err != nil {
		t.Fatal(err)
	}
	return testPresentationJob{IncidentID: incidentID, PresentationRunID: german.PresentationRunID, TitleDE: records[0].TitleDE, BodyDE: records[0].BodyDE}
}

func fixtureStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	database := openTestStore(t)
	documents, err := source.NewFixtureProvider().Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := database.UpsertDocuments(ctx, documents, time.Now()); err != nil {
		t.Fatalf("UpsertDocuments() error = %v", err)
	}
	return database
}

type fakeProcessingRequester struct {
	database    *store.Store
	err         error
	status      *processing.PipelineModelStatus
	postRequest func(context.Context, processing.PostProcessingRequest) (int, error)
}

func (p fakeProcessingRequester) RequestNow(ctx context.Context, sourceMode string, models map[string]string, incidentID *int64, reprocessAll bool) (store.PipelineRequestResult, error) {
	if p.err != nil {
		return store.PipelineRequestResult{}, p.err
	}
	plans, err := processing.StepPlans(models)
	if err != nil {
		return store.PipelineRequestResult{}, err
	}
	var postPlans []store.PostProcessingPlan
	registry := processing.DefaultPostProcessorRegistry()
	for _, definition := range registry.Definitions() {
		model := models[definition.ModelSettingKey]
		if model == "" {
			continue
		}
		selected, err := registry.Plans(definition.Key, nil, model)
		if err != nil {
			return store.PipelineRequestResult{}, err
		}
		postPlans = append(postPlans, selected...)
	}
	return p.database.CreateManualPipelineCycle(ctx, sourceMode, plans, postPlans, incidentID, reprocessAll, time.Now())
}

func (p fakeProcessingRequester) ModelStatus(ctx context.Context) (processing.PipelineModelStatus, error) {
	if p.status != nil {
		return *p.status, nil
	}
	postProcessors := []processing.PostProcessorModelStatus{
		{Key: processing.PublicAssistanceVerificationStep, DisplayName: "Public assistance verification", Description: "Verify public assistance metadata.", ModelSettingKey: processing.PublicAssistanceVerificationStep, Manual: true, Preferred: "qwen3.5:4b", PreferredAvailable: true, Scopes: []processing.PostProcessorScopeStatus{{Key: "default", DisplayName: "Default"}}},
		{Key: processing.CategoryVerificationStep, DisplayName: "Category verification", Description: "Verify categories.", ModelSettingKey: processing.CategoryVerificationStep, Manual: true, Preferred: "qwen3.5:4b", PreferredAvailable: true, Scopes: []processing.PostProcessorScopeStatus{{Key: "default", DisplayName: "Default"}}},
		{Key: processing.TranslationModelStep, DisplayName: "Translations", Description: "Translate presentations.", ModelSettingKey: processing.TranslationModelStep, Manual: true, Preferred: "qwen3.5:4b", PreferredAvailable: true, Scopes: []processing.PostProcessorScopeStatus{{Key: "en", DisplayName: "English"}}},
	}
	return processing.PipelineModelStatus{Steps: defaultTestStepStatus("qwen3.5:4b"), PostProcessors: postProcessors, Models: []string{"granite4:3b", "qwen3.5:4b"}, CatalogAvailable: true, Ready: true}, nil
}

func (p fakeProcessingRequester) SetPreferredStepModel(ctx context.Context, step, model string) error {
	if model != "qwen3.5:4b" && model != "granite4:3b" {
		return processing.ErrModelUnavailable
	}
	if err := p.database.EnsurePipelineSteps(ctx, processing.ModelSettingKeys(), time.Now()); err != nil {
		return err
	}
	return p.database.SetPipelineStepModel(ctx, step, model, time.Now())
}

func (p fakeProcessingRequester) RequestPostProcessing(ctx context.Context, request processing.PostProcessingRequest) (int, error) {
	if p.postRequest != nil {
		return p.postRequest(ctx, request)
	}
	plans, err := processing.DefaultPostProcessorRegistry().Plans(request.ProcessorKey, request.ScopeKeys, request.Model)
	if err != nil {
		return 0, err
	}
	if request.IncidentID != nil {
		return p.database.QueueIncidentPostProcessing(ctx, *request.IncidentID, plans, time.Now())
	}
	return p.database.QueuePostProcessingForAll(ctx, "fixture", plans, true, time.Now())
}

func (p fakeProcessingRequester) Status(ctx context.Context) (processing.PipelineRuntimeStatus, error) {
	models, err := p.ModelStatus(ctx)
	if err != nil {
		return processing.PipelineRuntimeStatus{}, err
	}
	queue, err := p.database.PipelineSnapshot(ctx, "fixture", processing.StepKeys(), nil, time.Now())
	queue.PostProcessing = processing.DefaultPostProcessorRegistry().OrderedQueueStats(queue.PostProcessing)
	return processing.PipelineRuntimeStatus{GeneratedAt: time.Now(), WindowOpen: true, ScheduledReady: models.Ready, ProcessorAvailable: true, Models: models, Queue: queue}, err
}

func defaultTestStepStatus(model string) []processing.StepModelStatus {
	steps := make([]processing.StepModelStatus, 0, len(processing.RegisteredSteps()))
	for index, step := range processing.RegisteredSteps() {
		steps = append(steps, processing.StepModelStatus{Number: index + 1, Key: step.Key, DisplayName: step.DisplayName, PromptVersion: step.PromptVersion, Preferred: model, PreferredAvailable: model != ""})
	}
	return steps
}

func testServer(t *testing.T, database *store.Store) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	server, err := NewWithOptions(database, logger, Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review",
	})
	if err != nil {
		t.Fatalf("NewWithOptions() error = %v", err)
	}
	return server
}

func adminTestServer(t *testing.T, database *store.Store, publicHosts []string) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	server, err := NewWithOptions(database, logger, Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review",
		SecureCookies: true, AdminEnabled: true, PublicHosts: publicHosts,
		CanonicalOrigin: canonicalOriginForHosts(publicHosts),
		Processor:       fakeProcessingRequester{database: database},
	})
	if err != nil {
		t.Fatalf("NewWithOptions() error = %v", err)
	}
	return server
}

func canonicalOriginForHosts(publicHosts []string) string {
	if len(publicHosts) == 0 {
		return ""
	}
	return "https://munichbrief.de"
}

func formatID(id int64) string {
	return strconv.FormatInt(id, 10)
}

func englishRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("Accept-Language", "en")
	return request
}

func formRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://example.com")
	return request
}

func stagedModelForm(body string) string {
	return body + "&model_incident_metadata=qwen3.5%3A4b&model_german_presentation=qwen3.5%3A4b&model_translation=qwen3.5%3A4b"
}
