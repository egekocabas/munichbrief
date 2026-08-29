package processing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/store"
)

type pipelineTestProvider struct {
	mu              sync.Mutex
	events          []string
	onGerman        func()
	onTranslation   func()
	calls           map[string]int
	fail            func(string, int) error
	generate        func(StepDefinition, StepInput) (StepOutput, bool)
	generateContext func(context.Context, StepDefinition, StepInput) (StepOutput, bool, error)
}

type testModelCatalog struct{ snapshot ModelCatalogSnapshot }

func (c testModelCatalog) Snapshot() ModelCatalogSnapshot { return c.snapshot }

type blockingManualRepository struct {
	*store.Store
	manualEntered chan struct{}
	manualRelease chan struct{}
}

func (r *blockingManualRepository) CreateManualPipelineCycle(ctx context.Context, sourceMode string, plans []store.PipelineStepPlan, postPlans []store.PostProcessingPlan, incidentID *int64, reprocessAll bool, now time.Time) (store.PipelineRequestResult, error) {
	close(r.manualEntered)
	<-r.manualRelease
	return r.Store.CreateManualPipelineCycle(ctx, sourceMode, plans, postPlans, incidentID, reprocessAll, now)
}

type pipelineTestObserver struct {
	attempts  map[string]int
	failures  map[string]int
	durations map[string]int
}

func (o *pipelineTestObserver) RecordPipelineAttempt(step string) {
	if o.attempts == nil {
		o.attempts = make(map[string]int)
	}
	o.attempts[step]++
}

func (o *pipelineTestObserver) RecordPipelineSuccess(string, time.Time) {}

func (o *pipelineTestObserver) RecordPipelineFailure(step, _ string) {
	if o.failures == nil {
		o.failures = make(map[string]int)
	}
	o.failures[step]++
}

func (o *pipelineTestObserver) RecordPipelineDuration(step string, _ time.Duration) {
	if o.durations == nil {
		o.durations = make(map[string]int)
	}
	o.durations[step]++
}

func (*pipelineTestObserver) SetPipelineSnapshot(store.PipelineSnapshot) {}
func (*pipelineTestObserver) SetProcessorAvailable(bool)                 {}
func (*pipelineTestObserver) SetProcessingWindowOpen(bool)               {}

func (p *pipelineTestProvider) StepGenerator(model string) (StepGenerator, error) {
	return pipelineTestGenerator{model: model, provider: p}, nil
}

type pipelineTestGenerator struct {
	model    string
	provider *pipelineTestProvider
}

func (g pipelineTestGenerator) ModelIdentity() string { return g.model }

func (g pipelineTestGenerator) GenerateStep(ctx context.Context, step StepDefinition, input StepInput) (StepOutput, string, error) {
	g.provider.mu.Lock()
	g.provider.events = append(g.provider.events, step.Key+":"+g.model+":"+input.Value("original_title"))
	if g.provider.calls == nil {
		g.provider.calls = make(map[string]int)
	}
	g.provider.calls[step.Key]++
	call := g.provider.calls[step.Key]
	var failure error
	if g.provider.fail != nil {
		failure = g.provider.fail(step.Key, call)
	}
	generate := g.provider.generate
	generateContext := g.provider.generateContext
	callback := g.provider.onGerman
	if step.Key == GermanPresentationStep {
		g.provider.onGerman = nil
	}
	translationCallback := g.provider.onTranslation
	if step.Key == EnglishTranslationStep {
		g.provider.onTranslation = nil
	}
	g.provider.mu.Unlock()
	if failure != nil {
		return StepOutput{}, "", failure
	}
	if generateContext != nil {
		if output, handled, err := generateContext(ctx, step, input); handled || err != nil {
			return output, g.model, err
		}
	}
	if generate != nil {
		if output, handled := generate(step, input); handled {
			return output, g.model, nil
		}
	}
	if step.Key == IncidentMetadataStep {
		return StepOutput{
			Category: "other", ReportKind: "incident", PublicAssistanceStatus: "not_requested", PublicAssistanceTypes: []string{},
		}, g.model, nil
	}
	if step.Key == GermanPresentationStep {
		if callback != nil {
			callback()
		}
		return StepOutput{TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.", PrivacyStatus: "safe", PrivacyFlags: []string{}}, g.model, nil
	}
	if step.Key == PublicAssistanceVerificationStep {
		if !strings.HasPrefix(input.Value("original_title"), "Titel ") || !strings.HasPrefix(input.Value("incident_body"), "Text für ") || input.Value("public_assistance_status") != "not_requested" || input.Value("public_assistance_types") != "[]" || input.Value("title_de") != "" || input.Value("summary_de") != "" {
			return StepOutput{}, "", fmt.Errorf("public assistance verifier received invalid inputs")
		}
		return StepOutput{Values: map[string]string{"is_correct": "true", "corrected_public_assistance_status": "not_requested", "corrected_public_assistance_types": "[]"}}, g.model, nil
	}
	if step.Key == CategoryVerificationStep {
		if input.Value("title_de") != "Sicherer Titel" || input.Value("summary_de") != "Sichere Zusammenfassung." || input.Value("category") != "other" || input.Value("incident_body") != "" {
			return StepOutput{}, "", fmt.Errorf("category verifier received invalid inputs")
		}
		return StepOutput{Values: map[string]string{"is_correct": "true", "corrected_category": "other"}}, g.model, nil
	}
	if input.Value("title_de") != "Sicherer Titel" || input.Value("summary_de") != "Sichere Zusammenfassung." {
		return StepOutput{}, "", fmt.Errorf("translation received incomplete German presentation")
	}
	if translationCallback != nil {
		translationCallback()
	}
	return StepOutput{Values: map[string]string{"title": "Safe title", "summary": "Safe summary."}}, g.model, nil
}

func TestAutomaticProcessingDisableFinishesCurrentRequestAndSuspendsCycle(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker-automatic-control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	provider := &pipelineTestProvider{generateContext: func(_ context.Context, step StepDefinition, _ StepInput) (StepOutput, bool, error) {
		if step.Key != IncidentMetadataStep {
			return StepOutput{}, false, nil
		}
		once.Do(func() { close(started) })
		<-release
		return StepOutput{Category: "other", ReportKind: "incident", PublicAssistanceStatus: "not_requested", PublicAssistanceTypes: []string{}}, true, nil
	}}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		worker.processAvailable(ctx)
		close(done)
	}()
	<-started
	if err := worker.SetAutomaticProcessing(ctx, false); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop at automatic job boundary")
	}
	if provider.callCount(IncidentMetadataStep) != 1 || provider.callCount(GermanPresentationStep) != 0 {
		t.Fatalf("calls while disabled: metadata=%d german=%d", provider.callCount(IncidentMetadataStep), provider.callCount(GermanPresentationStep))
	}
	status, err := worker.Status(ctx)
	if err != nil || status.AutomaticProcessingEnabled || status.Queue.ActiveCycle != nil {
		t.Fatalf("disabled worker status = %#v/%v", status, err)
	}
	if err := worker.SetAutomaticProcessing(ctx, true); err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if provider.callCount(GermanPresentationStep) != 1 {
		t.Fatalf("German calls after resume = %d, want 1", provider.callCount(GermanPresentationStep))
	}
}

func TestAutomaticProcessingDisableSuspendsCycleAfterCurrentRequestFails(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker-automatic-failure-control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 16, 15, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	provider := &pipelineTestProvider{generateContext: func(_ context.Context, step StepDefinition, _ StepInput) (StepOutput, bool, error) {
		if step.Key != IncidentMetadataStep {
			return StepOutput{}, false, nil
		}
		once.Do(func() { close(started) })
		<-release
		return StepOutput{}, true, errorOf(ErrorTransient, "synthetic provider failure")
	}}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		worker.processAvailable(ctx)
		close(done)
	}()
	<-started
	if err := worker.SetAutomaticProcessing(ctx, false); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not return after failed automatic request")
	}
	status, err := worker.Status(ctx)
	if err != nil || status.Queue.ActiveCycle != nil || status.Queue.Steps[0].Retrying != 1 {
		t.Fatalf("failed request boundary status = active:%#v steps:%#v err:%v", status.Queue.ActiveCycle, status.Queue.Steps, err)
	}
}

func TestCancelAllInterruptsCurrentRequestWithoutRetryFailure(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker-cancel-all.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 16, 30, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan struct{})
	var once sync.Once
	provider := &pipelineTestProvider{generateContext: func(requestCtx context.Context, step StepDefinition, _ StepInput) (StepOutput, bool, error) {
		if step.Key != IncidentMetadataStep {
			return StepOutput{}, false, nil
		}
		once.Do(func() { close(started) })
		<-requestCtx.Done()
		return StepOutput{}, true, requestCtx.Err()
	}}
	observer := &pipelineTestObserver{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), observer, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		worker.processAvailable(ctx)
		close(done)
	}()
	<-started
	canceled, err := worker.CancelAll(ctx)
	if err != nil || canceled.Cycles != 1 || canceled.CanonicalJobs != 2 {
		t.Fatalf("worker cancel all = %#v/%v", canceled, err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled provider request did not exit")
	}
	if observer.failures[IncidentMetadataStep] != 0 {
		t.Fatalf("operator cancellation recorded as failure: %#v", observer.failures)
	}
	status, err := worker.Status(ctx)
	if err != nil || status.AutomaticProcessingEnabled || status.Queue.ActiveCycle != nil || status.Queue.Steps[0].Retrying != 0 {
		t.Fatalf("canceled worker status = %#v/%v", status, err)
	}
}

func TestCancelAllInterruptsCurrentPostProcessingRequestWithoutRetryFailure(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker-cancel-all-post.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 16, 32, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	started := make(chan struct{})
	var once sync.Once
	provider := &pipelineTestProvider{}
	observer := &pipelineTestObserver{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), observer, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.SetAutomaticProcessing(ctx, false); err != nil {
		t.Fatal(err)
	}
	if result, err := worker.RequestNow(ctx, "fixture", map[string]string{IncidentMetadataStep: "qwen:4b", GermanPresentationStep: "qwen:4b"}, nil, false); err != nil || result.Requested != 1 {
		t.Fatalf("manual canonical request = %#v/%v", result, err)
	}
	worker.processAvailable(ctx)
	if queued, err := worker.RequestPostProcessing(ctx, PostProcessingRequest{ProcessorKey: TranslationModelStep, Model: "translate:4b"}); err != nil || queued != 1 {
		t.Fatalf("manual post-processing request = %d/%v", queued, err)
	}
	provider.generateContext = func(requestCtx context.Context, step StepDefinition, _ StepInput) (StepOutput, bool, error) {
		if step.Key != EnglishTranslationStep {
			return StepOutput{}, false, nil
		}
		once.Do(func() { close(started) })
		<-requestCtx.Done()
		return StepOutput{}, true, requestCtx.Err()
	}
	done := make(chan struct{})
	go func() {
		worker.processAvailable(ctx)
		close(done)
	}()
	<-started
	canceled, err := worker.CancelAll(ctx)
	if err != nil || canceled.Cycles != 0 || canceled.CanonicalJobs != 0 || canceled.PostProcessingJobs != 1 {
		t.Fatalf("post-processing cancel all = %#v/%v", canceled, err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled post-processing provider request did not exit")
	}
	executionKey := TranslationModelStep + "/en"
	if observer.failures[executionKey] != 0 || worker.circuitOpen(TranslationModelStep, "translate:4b", now.Add(time.Second)) {
		t.Fatalf("post-processing operator cancellation recorded failure/circuit: failures=%#v", observer.failures)
	}
	status, err := worker.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, queue := range status.Queue.PostProcessing {
		if queue.Pending != 0 || queue.Running != 0 || queue.Retrying != 0 {
			t.Fatalf("post-processing queue after cancellation = %#v", status.Queue.PostProcessing)
		}
	}
}

func TestManualSubmissionIsLinearizedBeforeConcurrentCancelAll(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker-cancel-manual-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 16, 35, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	repository := &blockingManualRepository{Store: database, manualEntered: make(chan struct{}), manualRelease: make(chan struct{})}
	worker, err := NewPipelineWorker(repository, &pipelineTestProvider{}, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan error, 1)
	go func() {
		_, requestErr := worker.RequestNow(ctx, "fixture", map[string]string{IncidentMetadataStep: "qwen:4b", GermanPresentationStep: "qwen:4b"}, nil, false)
		requestDone <- requestErr
	}()
	<-repository.manualEntered
	if worker.executionMu.TryLock() {
		worker.executionMu.Unlock()
		t.Fatal("manual queue mutation was not serialized with cancel-all")
	}
	cancelDone := make(chan store.PipelineCancellationResult, 1)
	go func() {
		result, cancelErr := worker.CancelAll(ctx)
		if cancelErr != nil {
			t.Errorf("cancel all: %v", cancelErr)
		}
		cancelDone <- result
	}()
	close(repository.manualRelease)
	if err := <-requestDone; err != nil {
		t.Fatal(err)
	}
	if canceled := <-cancelDone; canceled.Cycles != 1 || canceled.CanonicalJobs != 2 {
		t.Fatalf("concurrent cancellation = %#v", canceled)
	}
	status, err := worker.Status(ctx)
	if err != nil || status.AutomaticProcessingEnabled || status.Queue.ActiveCycle != nil {
		t.Fatalf("status after linearized cancellation = %#v/%v", status, err)
	}
}

func TestCancelWinningBeforeCanonicalFailureBookkeepingIsNotAProcessingFailure(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker-cancel-validation-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 16, 40, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{}
	observer := &pipelineTestObserver{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), observer, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	provider.generateContext = func(_ context.Context, step StepDefinition, _ StepInput) (StepOutput, bool, error) {
		if step.Key != IncidentMetadataStep {
			return StepOutput{}, false, nil
		}
		if _, err := worker.CancelAll(ctx); err != nil {
			return StepOutput{}, true, err
		}
		return StepOutput{}, true, nil
	}
	worker.processAvailable(ctx)
	if observer.failures[IncidentMetadataStep] != 0 {
		t.Fatalf("cancellation race recorded as canonical failure: %#v", observer.failures)
	}
	if worker.circuitOpen(IncidentMetadataStep, "qwen:4b", now.Add(time.Second)) {
		t.Fatal("cancellation race opened the canonical circuit")
	}
	status, err := worker.Status(ctx)
	if err != nil || status.Queue.Steps[0].Retrying != 0 {
		t.Fatalf("canonical cancellation race status = %#v/%v", status.Queue.Steps, err)
	}
}

func TestCancelWinningBeforePostProcessingFailureBookkeepingIsNotAProcessingFailure(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker-cancel-post-validation-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 16, 42, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	provider := &pipelineTestProvider{}
	observer := &pipelineTestObserver{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), observer, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.SetAutomaticProcessing(ctx, false); err != nil {
		t.Fatal(err)
	}
	if result, err := worker.RequestNow(ctx, "fixture", map[string]string{IncidentMetadataStep: "qwen:4b", GermanPresentationStep: "qwen:4b"}, nil, false); err != nil || result.Requested != 1 {
		t.Fatalf("manual canonical request = %#v/%v", result, err)
	}
	worker.processAvailable(ctx)
	if queued, err := worker.RequestPostProcessing(ctx, PostProcessingRequest{ProcessorKey: TranslationModelStep, Model: "translate:4b"}); err != nil || queued != 1 {
		t.Fatalf("manual post-processing request = %d/%v", queued, err)
	}
	provider.generateContext = func(_ context.Context, step StepDefinition, _ StepInput) (StepOutput, bool, error) {
		if step.Key != EnglishTranslationStep {
			return StepOutput{}, false, nil
		}
		if _, err := worker.CancelAll(ctx); err != nil {
			return StepOutput{}, true, err
		}
		return StepOutput{Values: map[string]string{"title": "incomplete"}}, true, nil
	}
	worker.processAvailable(ctx)
	executionKey := TranslationModelStep + "/en"
	if observer.failures[executionKey] != 0 {
		t.Fatalf("cancellation race recorded as post-processing failure: %#v", observer.failures)
	}
	if worker.circuitOpen(TranslationModelStep, "translate:4b", now.Add(time.Second)) {
		t.Fatal("cancellation race opened the post-processing circuit")
	}
	status, err := worker.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, queue := range status.Queue.PostProcessing {
		if queue.Pending != 0 || queue.Running != 0 || queue.Retrying != 0 {
			t.Fatalf("post-processing cancellation race queue = %#v", status.Queue.PostProcessing)
		}
	}
}

func TestProviderCanceledWithoutWorkerCancellationRetriesCanonicalJob(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker-provider-canceled-canonical.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 16, 43, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	for step, model := range pipelineTestModels() {
		if err := database.EnsurePipelineSteps(ctx, []string{step}, now); err != nil {
			t.Fatal(err)
		}
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{fail: func(step string, _ int) error {
		if step == IncidentMetadataStep {
			return context.Canceled
		}
		return nil
	}}
	observer := &pipelineTestObserver{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), observer, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	status, err := worker.Status(ctx)
	if err != nil || status.Queue.Steps[0].Retrying != 1 || observer.failures[IncidentMetadataStep] != 1 {
		t.Fatalf("provider cancellation canonical retry = queue:%#v failures:%#v err:%v", status.Queue.Steps, observer.failures, err)
	}
	if !worker.circuitOpen(IncidentMetadataStep, "qwen:4b", now.Add(time.Second)) {
		t.Fatal("provider cancellation did not open the canonical transient circuit")
	}
}

func TestProviderCanceledWithoutWorkerCancellationRetriesPostProcessingJob(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker-provider-canceled-post.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 16, 44, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	provider := &pipelineTestProvider{}
	observer := &pipelineTestObserver{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), observer, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.SetAutomaticProcessing(ctx, false); err != nil {
		t.Fatal(err)
	}
	if result, err := worker.RequestNow(ctx, "fixture", map[string]string{IncidentMetadataStep: "qwen:4b", GermanPresentationStep: "qwen:4b"}, nil, false); err != nil || result.Requested != 1 {
		t.Fatalf("manual canonical request = %#v/%v", result, err)
	}
	worker.processAvailable(ctx)
	if queued, err := worker.RequestPostProcessing(ctx, PostProcessingRequest{ProcessorKey: TranslationModelStep, Model: "translate:4b"}); err != nil || queued != 1 {
		t.Fatalf("manual post-processing request = %d/%v", queued, err)
	}
	provider.fail = func(step string, _ int) error {
		if step == EnglishTranslationStep {
			return context.Canceled
		}
		return nil
	}
	worker.processAvailable(ctx)
	status, err := worker.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	executionKey := TranslationModelStep + "/en"
	translationRetrying := 0
	for _, queue := range status.Queue.PostProcessing {
		if queue.ProcessorKey == TranslationModelStep && queue.ScopeKey == EnglishLanguage {
			translationRetrying = queue.Retrying
		}
	}
	if translationRetrying != 1 || observer.failures[executionKey] != 1 {
		t.Fatalf("provider cancellation post-processing retry = queue:%#v failures:%#v", status.Queue.PostProcessing, observer.failures)
	}
	if !worker.circuitOpen(TranslationModelStep, "translate:4b", now.Add(time.Second)) {
		t.Fatal("provider cancellation did not open the post-processing transient circuit")
	}
}

func TestAutomaticProcessingDisableFinishesCurrentPostProcessingOnly(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker-post-control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 16, 45, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	insertWorkerDocument(t, ctx, database, now.Add(time.Second), "two")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	provider := &pipelineTestProvider{generateContext: func(_ context.Context, step StepDefinition, _ StepInput) (StepOutput, bool, error) {
		if step.Key != EnglishTranslationStep {
			return StepOutput{}, false, nil
		}
		once.Do(func() { close(started) })
		<-release
		return StepOutput{Values: map[string]string{"title": "Safe title", "summary": "Safe summary."}}, true, nil
	}}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		worker.processAvailable(ctx)
		close(done)
	}()
	<-started
	if err := worker.SetAutomaticProcessing(ctx, false); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("post-processing worker did not stop after current request")
	}
	if provider.callCount(EnglishTranslationStep) != 1 {
		t.Fatalf("translation calls while disabled = %d, want 1", provider.callCount(EnglishTranslationStep))
	}
	status, err := worker.Status(ctx)
	if err != nil || status.Queue.PostProcessing[len(status.Queue.PostProcessing)-1].Pending != 1 {
		t.Fatalf("disabled post-processing status = %#v/%v", status.Queue.PostProcessing, err)
	}
}

func (p *pipelineTestProvider) callCount(step string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[step]
}

func TestPipelineWorkerGroupsModelsFreezesTargetsAndStartsNextCycle(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	insertWorkerDocument(t, ctx, database, now.Add(time.Second), "two")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{}
	provider.onGerman = func() { insertWorkerDocument(t, ctx, database, now.Add(2*time.Second), "three") }
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)

	provider.mu.Lock()
	events := append([]string(nil), provider.events...)
	provider.mu.Unlock()
	if len(events) != 9 {
		t.Fatalf("events = %v", events)
	}
	want := []struct{ step, model string }{
		{IncidentMetadataStep, "qwen:4b"}, {IncidentMetadataStep, "qwen:4b"},
		{GermanPresentationStep, "qwen:4b"}, {GermanPresentationStep, "qwen:4b"},
		{IncidentMetadataStep, "qwen:4b"}, {GermanPresentationStep, "qwen:4b"},
		{EnglishTranslationStep, "translate:4b"}, {EnglishTranslationStep, "translate:4b"}, {EnglishTranslationStep, "translate:4b"},
	}
	for index, event := range events {
		wantStep, wantModel := want[index].step, want[index].model
		if event[:len(wantStep)+len(wantModel)+2] != wantStep+":"+wantModel+":" {
			t.Fatalf("event %d = %q, want %s/%s grouped order", index, event, wantStep, wantModel)
		}
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", StepKeys(), nil, now)
	if err != nil || snapshot.ActiveCycle != nil || snapshot.ScheduledCandidates != 0 {
		t.Fatalf("completed pipeline snapshot = %#v, err=%v", snapshot, err)
	}
}

func TestPipelineWorkerPrioritizesPublicAssistanceThenCategoryBeforeTranslation(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker-category-priority.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.SetPipelineStepModel(ctx, CategoryVerificationStep, "verify:4b", now); err != nil {
		t.Fatal(err)
	}
	if err := database.SetPipelineStepModel(ctx, PublicAssistanceVerificationStep, "assist:4b", now); err != nil {
		t.Fatal(err)
	}
	provider := &pipelineTestProvider{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"assist:4b", "qwen:4b", "translate:4b", "verify:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	provider.mu.Lock()
	events := append([]string(nil), provider.events...)
	provider.mu.Unlock()
	want := []string{IncidentMetadataStep, GermanPresentationStep, PublicAssistanceVerificationStep, CategoryVerificationStep, EnglishTranslationStep}
	if len(events) != len(want) {
		status, _ := worker.Status(ctx)
		t.Fatalf("events = %v; models=%#v queue=%#v", events, status.Models.PostProcessors, status.Queue.PostProcessing)
	}
	for index, step := range want {
		if !strings.HasPrefix(events[index], step+":") {
			t.Fatalf("event %d = %q, want %s", index, events[index], step)
		}
	}
	status, err := worker.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	queueOrder := []string{
		PublicAssistanceVerificationStep + "/" + DefaultPostProcessingScope,
		CategoryVerificationStep + "/" + DefaultPostProcessingScope,
		TranslationModelStep + "/" + EnglishLanguage,
	}
	if len(status.Queue.PostProcessing) != len(queueOrder) {
		t.Fatalf("runtime queue stats = %#v, want %d scopes", status.Queue.PostProcessing, len(queueOrder))
	}
	for index, stat := range status.Queue.PostProcessing {
		if key := stat.ProcessorKey + "/" + stat.ScopeKey; key != queueOrder[index] {
			t.Fatalf("runtime queue stat %d = %q, want %q", index, key, queueOrder[index])
		}
	}
}

func TestInjectedPostProcessorUsesGenericSchedulingManualExecutionStatusAndHistory(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker-injected-processor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 10, 30, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")

	definitions := DefaultPostProcessorRegistry().Definitions()
	definitions = append(definitions, PostProcessorDefinition{
		Key: "quality_note", DisplayName: "Quality note", Description: "Test-only independent review note.", Priority: 30,
		ModelSettingKey: "quality_note", Automatic: true, Manual: true,
		Scopes: []PostProcessorScope{{Key: DefaultPostProcessingScope, DisplayName: "Default", Step: StepDefinition{
			Key: "quality_note_step", DisplayName: "Quality note", PromptVersion: "quality-note-v1",
			InputKinds: []string{"title_de", "summary_de"}, OutputKinds: []string{"note"}, OutputValues: postProcessingOutputValues,
		}}},
	})
	registry, err := NewPostProcessorRegistry(definitions...)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.EnsurePostProcessingScopes(ctx, registry.StoreScopes(), now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	for step, model := range map[string]string{
		IncidentMetadataStep: "qwen:4b", GermanPresentationStep: "qwen:4b",
		TranslationModelStep: "translate:4b", CategoryVerificationStep: "verify:4b", "quality_note": "quality:4b",
	} {
		if err := database.EnsurePipelineSteps(ctx, []string{step}, now); err != nil {
			t.Fatal(err)
		}
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{generate: func(step StepDefinition, input StepInput) (StepOutput, bool) {
		if step.Key != "quality_note_step" {
			return StepOutput{}, false
		}
		if input.Value("title_de") == "" || input.Value("summary_de") == "" {
			return StepOutput{Values: map[string]string{}}, true
		}
		return StepOutput{Values: map[string]string{"note": "checked"}}, true
	}}
	catalog := testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"quality:4b", "qwen:4b", "translate:4b", "verify:4b"}, CheckedAt: now}}
	worker, err := NewPipelineWorker(database, provider, catalog, registry, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if provider.callCount("quality_note_step") != 1 {
		status, _ := worker.Status(ctx)
		t.Fatalf("scheduled injected processor calls = %d, want 1; events=%v models=%#v queue=%#v", provider.callCount("quality_note_step"), provider.events, status.Models.PostProcessors, status.Queue.PostProcessing)
	}
	status, err := worker.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Models.PostProcessors) != 4 || len(status.Queue.PostProcessing) != 4 {
		t.Fatalf("injected processor status = %#v / %#v", status.Models.PostProcessors, status.Queue.PostProcessing)
	}
	records, _, err := database.ListIncidents(ctx, 1, 0)
	if err != nil || len(records) != 1 {
		t.Fatalf("list injected processor incident = %d/%v", len(records), err)
	}
	if queued, err := worker.RequestPostProcessing(ctx, PostProcessingRequest{ProcessorKey: "quality_note", IncidentID: &records[0].ID, Model: "quality:4b"}); err != nil || queued != 1 {
		t.Fatalf("manual injected processor request = %d/%v", queued, err)
	}
	worker.processAvailable(ctx)
	if provider.callCount("quality_note_step") != 2 {
		t.Fatalf("manual injected processor calls = %d, want 2", provider.callCount("quality_note_step"))
	}
	history, err := database.ListPipelineHistory(ctx, "fixture", 20, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var automatic, manual bool
	for _, entry := range history.Entries {
		if entry.ProcessorKey != "quality_note" || entry.ExecutionKey != "quality_note/default" || entry.Status != "succeeded" {
			continue
		}
		automatic = automatic || entry.RequestKind == "scheduled"
		manual = manual || entry.RequestKind == "manual"
	}
	if !automatic || !manual {
		t.Fatalf("injected processor history missing scheduled/manual successes: %#v", history.Entries)
	}
}

func TestPipelineWorkerRequestsFocusedPostProcessing(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker-focused-post-processing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 11, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.SetPipelineStepModel(ctx, CategoryVerificationStep, "verify:4b", now); err != nil {
		t.Fatal(err)
	}
	provider := &pipelineTestProvider{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b", "verify:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	records, _, err := database.ListIncidents(ctx, 1, 0)
	if err != nil || len(records) != 1 {
		t.Fatalf("list focused incident = %d/%v", len(records), err)
	}
	if queued, err := worker.RequestPostProcessing(ctx, PostProcessingRequest{ProcessorKey: TranslationModelStep, ScopeKeys: []string{"en"}, IncidentID: &records[0].ID, Model: "translate:4b"}); err != nil || queued != 1 {
		t.Fatalf("focused translation request = %d/%v", queued, err)
	}
	if queued, err := worker.RequestPostProcessing(ctx, PostProcessingRequest{ProcessorKey: CategoryVerificationStep, Model: "verify:4b"}); err != nil || queued != 1 {
		t.Fatalf("focused category request = %d/%v", queued, err)
	}
	if _, err := worker.RequestPostProcessing(ctx, PostProcessingRequest{ProcessorKey: TranslationModelStep, ScopeKeys: []string{"en"}, Model: "missing:4b"}); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("unavailable focused model error = %v", err)
	}
	worker.processAvailable(ctx)
	if provider.callCount(EnglishTranslationStep) != 2 || provider.callCount(CategoryVerificationStep) != 2 {
		t.Fatalf("focused post-processing calls = translation:%d category:%d", provider.callCount(EnglishTranslationStep), provider.callCount(CategoryVerificationStep))
	}
}

func TestPipelineWorkerFinishesAuthorizedCycleAfterWindowButDoesNotStartAnother(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "window.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	current := time.Date(2026, 8, 25, 7, 59, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, current, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), current); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, current); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{}
	provider.onGerman = func() {
		current = time.Date(2026, 8, 25, 8, 1, 0, 0, time.UTC)
		insertWorkerDocument(t, ctx, database, current, "two")
	}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: current}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return current }, Schedule{Location: time.UTC, Start: 3 * time.Hour, End: 8 * time.Hour}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if len(provider.events) != 2 || provider.events[0][:len(IncidentMetadataStep)] != IncidentMetadataStep || provider.events[1][:len(GermanPresentationStep)] != GermanPresentationStep {
		t.Fatalf("authorized cycle events = %v", provider.events)
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", StepKeys(), nil, current)
	if err != nil || snapshot.ActiveCycle != nil || snapshot.ScheduledCandidates != 1 {
		t.Fatalf("closed-window snapshot = %#v, err=%v", snapshot, err)
	}
	for _, queue := range snapshot.PostProcessing {
		if queue.Pending != 0 || queue.Running != 0 || queue.Retrying != 0 {
			t.Fatalf("scheduled post-processing was discovered outside the window: %#v", snapshot.PostProcessing)
		}
	}

	records, _, err := database.ListIncidents(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	var secondID int64
	for _, record := range records {
		if record.TitleDE == "Titel two" {
			secondID = record.ID
		}
	}
	models := pipelineTestModels()
	if _, err := worker.RequestNow(ctx, "fixture", models, &secondID, false); err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if len(provider.events) != 5 {
		t.Fatalf("outside-window manual cycle did not run: %v", provider.events)
	}
}

func TestPipelineWorkerDoesNotCallModelsWhileRequiredStepIsUnset(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "unset.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	provider := &pipelineTestProvider{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if len(provider.events) != 0 {
		t.Fatalf("unconfigured pipeline called a model: %v", provider.events)
	}
	status, err := worker.Status(ctx)
	if err != nil || status.ScheduledReady || status.Models.Ready {
		t.Fatalf("unconfigured status = %#v, err=%v", status, err)
	}
}

func TestMissingTranslationModelDoesNotBlockCanonicalGerman(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "canonical-without-translation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{IncidentMetadataStep, GermanPresentationStep} {
		if err := database.SetPipelineStepModel(ctx, step, "qwen:4b", now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if provider.callCount(IncidentMetadataStep) != 1 || provider.callCount(GermanPresentationStep) != 1 || provider.callCount(EnglishTranslationStep) != 0 {
		t.Fatalf("calls with translation unset = metadata:%d german:%d translation:%d", provider.callCount(IncidentMetadataStep), provider.callCount(GermanPresentationStep), provider.callCount(EnglishTranslationStep))
	}
	records, _, err := database.ListIncidents(ctx, 1, 0)
	if err != nil || len(records) != 1 {
		t.Fatal(err)
	}
	if _, err := database.GetPresentationIncident(ctx, records[0].ID, store.PresentationScope{Language: "de", PublicOnly: true}); err != nil {
		t.Fatalf("canonical German was not public: %v", err)
	}
	if _, err := database.GetPresentationIncident(ctx, records[0].ID, store.PresentationScope{Language: "en", PublicOnly: true}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("English was visible without a translation: %v", err)
	}
	status, err := worker.ModelStatus(ctx)
	if err != nil || !status.Ready || len(status.PostProcessors) != 3 || status.PostProcessors[2].PreferredAvailable {
		t.Fatalf("split model readiness = %#v/%v", status, err)
	}
}

func TestCanonicalWorkPreemptsTranslationsAtJobBoundaries(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "canonical-preemption.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{}
	provider.onTranslation = func() { insertWorkerDocument(t, ctx, database, now.Add(time.Second), "two") }
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)

	provider.mu.Lock()
	events := append([]string(nil), provider.events...)
	provider.mu.Unlock()
	want := []string{IncidentMetadataStep, GermanPresentationStep, EnglishTranslationStep, IncidentMetadataStep, GermanPresentationStep, EnglishTranslationStep}
	if len(events) != len(want) {
		t.Fatalf("events = %v", events)
	}
	for index, step := range want {
		if !strings.HasPrefix(events[index], step+":") {
			t.Fatalf("event %d = %q, want %s", index, events[index], step)
		}
	}
}

func TestTranslationFailureDoesNotChangeCanonicalCompletion(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "translation-failure.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{fail: func(step string, _ int) error {
		if step == EnglishTranslationStep {
			return errorOf(ErrorOutput, "synthetic malformed translation")
		}
		return nil
	}}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	now = now.Add(2 * time.Minute)
	worker.processAvailable(ctx)
	now = now.Add(11 * time.Minute)
	worker.processAvailable(ctx)

	records, _, err := database.ListIncidents(ctx, 1, 0)
	if err != nil || len(records) != 1 {
		t.Fatalf("incidents = %d, err=%v", len(records), err)
	}
	if _, err := database.GetPresentationIncident(ctx, records[0].ID, store.PresentationScope{Language: "de", PublicOnly: true}); err != nil {
		t.Fatalf("canonical German was not public after translation failure: %v", err)
	}
	if _, err := database.GetPresentationIncident(ctx, records[0].ID, store.PresentationScope{Language: "en", PublicOnly: true}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("failed English translation became public: %v", err)
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", StepKeys(), nil, now)
	var translationStats *store.PostProcessingQueueStats
	for index := range snapshot.PostProcessing {
		if snapshot.PostProcessing[index].ProcessorKey == TranslationModelStep {
			translationStats = &snapshot.PostProcessing[index]
		}
	}
	if err != nil || len(snapshot.PostProcessing) != 3 || translationStats == nil || translationStats.NeedsReview != 1 {
		t.Fatalf("translation failure snapshot = %#v, err=%v", snapshot.PostProcessing, err)
	}
}

func TestPublicAssistanceVerificationFailureDoesNotBlockOtherProcessors(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "public-assistance-failure.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 29, 17, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	for step, model := range map[string]string{PublicAssistanceVerificationStep: "assist:4b", CategoryVerificationStep: "verify:4b"} {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{fail: func(step string, _ int) error {
		if step == PublicAssistanceVerificationStep {
			return errorOf(ErrorOutput, "synthetic malformed assistance verification")
		}
		return nil
	}}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"assist:4b", "qwen:4b", "translate:4b", "verify:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	now = now.Add(2 * time.Minute)
	worker.processAvailable(ctx)
	now = now.Add(11 * time.Minute)
	worker.processAvailable(ctx)

	if provider.callCount(PublicAssistanceVerificationStep) != 3 || provider.callCount(CategoryVerificationStep) != 1 || provider.callCount(EnglishTranslationStep) != 1 {
		t.Fatalf("independent processor calls = assistance:%d category:%d translation:%d", provider.callCount(PublicAssistanceVerificationStep), provider.callCount(CategoryVerificationStep), provider.callCount(EnglishTranslationStep))
	}
	records, _, err := database.ListIncidents(ctx, 1, 0)
	if err != nil || len(records) != 1 {
		t.Fatalf("incidents = %d, err=%v", len(records), err)
	}
	for _, language := range []string{"de", "en"} {
		record, err := database.GetPresentationIncident(ctx, records[0].ID, store.PresentationScope{Language: language, TranslationLanguage: language, PublicOnly: true})
		if err != nil {
			t.Fatalf("%s publication blocked by assistance verifier: %v", language, err)
		}
		if record.AIPublicAssistanceStatus != "not_requested" || record.AIPublicAssistanceTypes != "[]" {
			t.Fatalf("%s fallback assistance = %s/%s", language, record.AIPublicAssistanceStatus, record.AIPublicAssistanceTypes)
		}
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", StepKeys(), nil, now)
	var assistanceStats *store.PostProcessingQueueStats
	for index := range snapshot.PostProcessing {
		if snapshot.PostProcessing[index].ProcessorKey == PublicAssistanceVerificationStep {
			assistanceStats = &snapshot.PostProcessing[index]
		}
	}
	if err != nil || assistanceStats == nil || assistanceStats.NeedsReview != 1 {
		t.Fatalf("assistance failure snapshot = %#v, err=%v", snapshot.PostProcessing, err)
	}
}

func TestPipelineWorkerRetriesPrivacyFailurePerStepThenRequiresReview(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "privacy-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "privacy")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{fail: func(step string, _ int) error {
		if step == GermanPresentationStep {
			return errorOf(ErrorPrivacy, "synthetic privacy uncertainty")
		}
		return nil
	}}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	now = now.Add(2 * time.Minute)
	worker.processAvailable(ctx)
	now = now.Add(11 * time.Minute)
	worker.processAvailable(ctx)

	if provider.callCount(IncidentMetadataStep) != 1 || provider.callCount(GermanPresentationStep) != contentMaxAttempts || provider.callCount(EnglishTranslationStep) != 0 {
		t.Fatalf("step calls = metadata:%d german:%d translation:%d", provider.callCount(IncidentMetadataStep), provider.callCount(GermanPresentationStep), provider.callCount(EnglishTranslationStep))
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", StepKeys(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveCycle != nil || snapshot.ScheduledCandidates != 0 || len(snapshot.Steps) != 2 || snapshot.Steps[1].NeedsReview != 1 {
		t.Fatalf("privacy retry snapshot = %#v", snapshot)
	}
}

func TestPipelineWorkerFailsClosedWhenMetadataRemainsInvalid(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "metadata-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "metadata")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{fail: func(step string, _ int) error {
		if step == IncidentMetadataStep {
			return errorOf(ErrorOutput, "synthetic malformed metadata")
		}
		return nil
	}}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	now = now.Add(2 * time.Minute)
	worker.processAvailable(ctx)
	now = now.Add(11 * time.Minute)
	worker.processAvailable(ctx)

	if provider.callCount(IncidentMetadataStep) != contentMaxAttempts || provider.callCount(GermanPresentationStep) != 0 || provider.callCount(EnglishTranslationStep) != 0 {
		t.Fatalf("step calls = metadata:%d german:%d translation:%d", provider.callCount(IncidentMetadataStep), provider.callCount(GermanPresentationStep), provider.callCount(EnglishTranslationStep))
	}
	snapshot, err := database.PipelineSnapshot(ctx, "fixture", StepKeys(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveCycle != nil || len(snapshot.Steps) != 2 || snapshot.Steps[0].NeedsReview != 1 {
		t.Fatalf("metadata fail-closed snapshot = %#v", snapshot)
	}
}

func TestPipelineWorkerTransientFailureOpensPerStepModelCircuit(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "circuit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "circuit")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{fail: func(step string, _ int) error {
		if step == GermanPresentationStep {
			return errorOf(ErrorTransient, "synthetic endpoint outage")
		}
		return nil
	}}
	observer := &pipelineTestObserver{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), observer, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if provider.callCount(IncidentMetadataStep) != 1 || provider.callCount(GermanPresentationStep) != 1 || !worker.circuitOpen(GermanPresentationStep, "qwen:4b", now) {
		t.Fatalf("first transient failure calls/circuit = metadata:%d german:%d/%t", provider.callCount(IncidentMetadataStep), provider.callCount(GermanPresentationStep), worker.circuitOpen(GermanPresentationStep, "qwen:4b", now))
	}
	if observer.attempts[GermanPresentationStep] != 1 || observer.failures[GermanPresentationStep] != 1 || observer.durations[GermanPresentationStep] != 1 {
		t.Fatalf("failed canonical observability = attempts:%d failures:%d durations:%d", observer.attempts[GermanPresentationStep], observer.failures[GermanPresentationStep], observer.durations[GermanPresentationStep])
	}
	worker.processAvailable(ctx)
	if provider.callCount(GermanPresentationStep) != 1 {
		t.Fatalf("open circuit made %d model calls", provider.callCount(GermanPresentationStep))
	}
	now = now.Add(time.Minute)
	worker.processAvailable(ctx)
	if provider.callCount(GermanPresentationStep) != 2 {
		t.Fatalf("expired circuit calls = %d, want 2", provider.callCount(GermanPresentationStep))
	}
}

func TestTranslationTransientFailureBacksOffFailedModel(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "translation-circuit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	insertWorkerDocument(t, ctx, database, now, "one")
	if err := database.EnsurePipelineSteps(ctx, ModelSettingKeys(), now); err != nil {
		t.Fatal(err)
	}
	for step, model := range pipelineTestModels() {
		if err := database.SetPipelineStepModel(ctx, step, model, now); err != nil {
			t.Fatal(err)
		}
	}
	provider := &pipelineTestProvider{fail: func(step string, _ int) error {
		if step == EnglishTranslationStep {
			return errorOf(ErrorTransient, "synthetic translation endpoint outage")
		}
		return nil
	}}
	observer := &pipelineTestObserver{}
	worker, err := NewPipelineWorker(database, provider, testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"qwen:4b", "translate:4b"}, CheckedAt: now}}, DefaultPostProcessorRegistry(), observer, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	if provider.callCount(EnglishTranslationStep) != 1 || len(worker.blockedModels(TranslationModelStep, now)) != 1 {
		t.Fatalf("translation circuit = calls:%d blocked:%v", provider.callCount(EnglishTranslationStep), worker.blockedModels(TranslationModelStep, now))
	}
	if observer.attempts[TranslationModelStep+"/en"] != 1 || observer.failures[TranslationModelStep+"/en"] != 1 || observer.durations[TranslationModelStep+"/en"] != 1 {
		t.Fatalf("failed post-processing observability = attempts:%d failures:%d durations:%d", observer.attempts[TranslationModelStep+"/en"], observer.failures[TranslationModelStep+"/en"], observer.durations[TranslationModelStep+"/en"])
	}
	worker.processAvailable(ctx)
	if provider.callCount(EnglishTranslationStep) != 1 {
		t.Fatalf("open translation circuit made %d model calls", provider.callCount(EnglishTranslationStep))
	}
	now = now.Add(time.Minute)
	worker.processAvailable(ctx)
	if provider.callCount(EnglishTranslationStep) != 2 {
		t.Fatalf("expired translation circuit calls = %d, want 2", provider.callCount(EnglishTranslationStep))
	}
}

func pipelineTestModels() map[string]string {
	return map[string]string{
		IncidentMetadataStep: "qwen:4b", GermanPresentationStep: "qwen:4b", TranslationModelStep: "translate:4b",
	}
}

func insertWorkerDocument(t *testing.T, ctx context.Context, database *store.Store, now time.Time, id string) {
	t.Helper()
	document := domain.SourceDocument{
		ExternalID: id, SourceURL: "https://fixture.invalid/" + id, Title: "Release " + id,
		PublishedAt: now, FeedFingerprint: "feed-" + id, SourceHash: "source-" + id,
		Incidents: []domain.Incident{{Number: "1", Position: 0, TitleDE: "Titel " + id, BodyDE: fmt.Sprintf("Text für %s in München.", id), ContentHash: "content-" + id}},
	}
	// created_at must be newer than the real migration-time v2 cutover; the
	// document publication time still follows the deterministic worker clock.
	if err := database.UpsertDocuments(ctx, []domain.SourceDocument{document}, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}
