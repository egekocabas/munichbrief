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
	return p.database.CreateManualPipelineCycle(ctx, sourceMode, plans, incidentID, reprocessAll, time.Now())
}

func (p fakeProcessingRequester) ModelStatus(ctx context.Context) (processing.PipelineModelStatus, error) {
	if p.status != nil {
		return *p.status, nil
	}
	return processing.PipelineModelStatus{Steps: defaultTestStepStatus("qwen3.5:4b"), Models: []string{"granite4:3b", "qwen3.5:4b"}, CatalogAvailable: true, Ready: true}, nil
}

func (p fakeProcessingRequester) SetPreferredStepModel(ctx context.Context, step, model string) error {
	if model != "qwen3.5:4b" && model != "granite4:3b" {
		return processing.ErrModelUnavailable
	}
	if err := p.database.EnsurePipelineSteps(ctx, processing.StepKeys(), time.Now()); err != nil {
		return err
	}
	return p.database.SetPipelineStepModel(ctx, step, model, time.Now())
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
	for _, step := range processing.RegisteredSteps() {
		steps = append(steps, processing.StepModelStatus{Key: step.Key, DisplayName: step.DisplayName, PromptVersion: step.PromptVersion, Preferred: model, PreferredAvailable: model != ""})
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
	return body + "&model_german_analysis=qwen3.5%3A4b&model_english_translation=qwen3.5%3A4b"
}
