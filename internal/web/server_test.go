package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"io"
	"testing"

	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/source"
	"github.com/egekocabas/munichbrief/internal/store"
)

func legacyOperation(model string) string {
	return "incident-presentation/" + processing.LegacyBilingualPromptVersion + "/" + model
}

func fixtureStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "munichbrief.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { database.Close() })
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
	database *store.Store
	err      error
	status   *processing.PipelineModelStatus
}

func (p fakeProcessingRequester) RequestNow(ctx context.Context, sourceMode string, models map[string]string, incidentID *int64, reprocessAll bool) (store.PipelineRequestResult, error) {
	if p.err != nil {
		return store.PipelineRequestResult{}, p.err
	}
	plans, err := processing.StepPlans(models)
	if err != nil {
		return store.PipelineRequestResult{}, err
	}
	return p.database.CreateManualPipelineCycleWithPostProcessing(ctx, sourceMode, plans, models[processing.TranslationModelStep], models[processing.CategoryVerificationStep], incidentID, reprocessAll, time.Now())
}

func (p fakeProcessingRequester) ModelStatus(ctx context.Context) (processing.PipelineModelStatus, error) {
	if p.status != nil {
		return *p.status, nil
	}
	return processing.PipelineModelStatus{Steps: defaultTestStepStatus("qwen3.5:4b"), Translation: processing.StepModelStatus{Key: processing.TranslationModelStep, DisplayName: "Translations", Preferred: "qwen3.5:4b", PreferredAvailable: true}, CategoryVerification: processing.StepModelStatus{Key: processing.CategoryVerificationStep, DisplayName: "Category verification", Preferred: "qwen3.5:4b", PreferredAvailable: true}, Models: []string{"granite4:3b", "qwen3.5:4b"}, CatalogAvailable: true, Ready: true, TranslationReady: true, CategoryVerificationReady: true}, nil
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

func (p fakeProcessingRequester) RetryTranslation(ctx context.Context, incidentID int64, language, model string) (int, error) {
	definition, found := processing.TranslationByLanguage(language)
	if !found {
		return 0, store.ErrNotFound
	}
	return p.database.QueueIncidentTranslation(ctx, incidentID, store.TranslationPlan{Language: language, PromptVersion: definition.PromptVersion, Model: model}, time.Now())
}

func (p fakeProcessingRequester) BackfillTranslations(ctx context.Context, language, model string) (int, error) {
	definition, found := processing.TranslationByLanguage(language)
	if !found {
		return 0, store.ErrNotFound
	}
	return p.database.QueueMissingTranslations(ctx, "fixture", []store.TranslationPlan{{Language: language, PromptVersion: definition.PromptVersion, Model: model}}, true, time.Now())
}

func (p fakeProcessingRequester) RequestTranslations(ctx context.Context, incidentID *int64, languages []string, model string) (int, error) {
	plans := make([]store.TranslationPlan, 0, len(languages))
	for _, language := range languages {
		definition, found := processing.TranslationByLanguage(language)
		if !found {
			return 0, store.ErrNotFound
		}
		plans = append(plans, store.TranslationPlan{Language: language, PromptVersion: definition.PromptVersion, Model: model})
	}
	if incidentID != nil {
		return p.database.QueueIncidentTranslations(ctx, *incidentID, plans, true, time.Now())
	}
	return p.database.QueueAllTranslations(ctx, "fixture", plans, true, true, time.Now())
}

func (p fakeProcessingRequester) RetryCategoryVerification(ctx context.Context, incidentID int64, model string) (int, error) {
	return p.database.QueueIncidentCategoryVerification(ctx, incidentID, store.CategoryVerificationPlan{PromptVersion: processing.CategoryVerificationPromptVersion, Model: model}, time.Now())
}

func (p fakeProcessingRequester) BackfillCategoryVerifications(ctx context.Context, model string) (int, error) {
	return p.database.QueueMissingCategoryVerifications(ctx, "fixture", store.CategoryVerificationPlan{PromptVersion: processing.CategoryVerificationPromptVersion, Model: model}, true, time.Now())
}

func (p fakeProcessingRequester) RequestCategoryVerifications(ctx context.Context, incidentID *int64, model string) (int, error) {
	plan := store.CategoryVerificationPlan{PromptVersion: processing.CategoryVerificationPromptVersion, Model: model}
	if incidentID != nil {
		return p.database.QueueIncidentCategoryVerification(ctx, *incidentID, plan, time.Now())
	}
	return p.database.QueueAllCategoryVerifications(ctx, "fixture", plan, true, true, time.Now())
}

func (p fakeProcessingRequester) Status(ctx context.Context) (processing.PipelineRuntimeStatus, error) {
	models, err := p.ModelStatus(ctx)
	if err != nil {
		return processing.PipelineRuntimeStatus{}, err
	}
	queue, err := p.database.PipelineSnapshot(ctx, "fixture", processing.StepKeys(), time.Now())
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
		PromptVersion: processing.PipelineVersion,
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
		PromptVersion: processing.PipelineVersion,
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
