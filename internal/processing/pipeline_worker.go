package processing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

type PipelineRepository interface {
	EnsurePipelineSteps(context.Context, []string, time.Time) error
	PipelineStepSettings(context.Context) ([]store.StepSetting, error)
	SetPipelineStepModel(context.Context, string, string, time.Time) error
	PreferredPipelineModels(context.Context, []string) (map[string]string, error)
	CreateManualPipelineCycle(context.Context, string, []store.PipelineStepPlan, *int64, bool, time.Time) (store.PipelineRequestResult, error)
	ActivateNextPipelineCycle(context.Context, string, []store.PipelineStepPlan, bool, time.Time) (store.PipelineCycle, bool, error)
	RecoverPipeline(context.Context, time.Time) error
	ClaimPipelineJob(context.Context, int64, int, time.Time) (store.PipelineJob, bool, error)
	CompletePipelineJob(context.Context, store.PipelineJob, []store.PipelineValue, string, string, time.Time) error
	FailPipelineJob(context.Context, store.PipelineJob, string, string, *time.Time, time.Time, error) error
	AdvancePipelineCycle(context.Context, store.PipelineCycle, int, time.Time) (store.AdvanceResult, error)
	HasQueuedManualCycle(context.Context) (bool, error)
	InterruptBlockedScheduledCycle(context.Context, int64, time.Time) (int64, error)
	PipelineSnapshot(context.Context, string, []string, time.Time) (store.PipelineSnapshot, error)
	PipelineStepModel(context.Context, int64, int) (string, string, error)
}

type PipelineObserver interface {
	RecordPipelineAttempt(step string)
	RecordPipelineSuccess(step string, at time.Time)
	RecordPipelineFailure(step, kind string)
	RecordPipelineDuration(step string, duration time.Duration)
	SetPipelineSnapshot(store.PipelineSnapshot)
	SetProcessorAvailable(bool)
	SetProcessingWindowOpen(bool)
}

type StepModelStatus struct {
	Key                string `json:"key"`
	DisplayName        string `json:"display_name"`
	PromptVersion      string `json:"prompt_version"`
	Preferred          string `json:"preferred"`
	PreferredAvailable bool   `json:"preferred_available"`
}

type PipelineModelStatus struct {
	Steps            []StepModelStatus `json:"steps"`
	Models           []string          `json:"models"`
	CatalogAvailable bool              `json:"catalog_available"`
	CatalogError     string            `json:"catalog_error,omitempty"`
	Ready            bool              `json:"ready"`
}

type PipelineRuntimeStatus struct {
	GeneratedAt        time.Time              `json:"generated_at"`
	WindowOpen         bool                   `json:"window_open"`
	ScheduledReady     bool                   `json:"scheduled_ready"`
	ProcessorAvailable bool                   `json:"processor_available"`
	Models             PipelineModelStatus    `json:"models"`
	Queue              store.PipelineSnapshot `json:"queue"`
}

type PipelineWorker struct {
	repository PipelineRepository
	providers  StepGeneratorProvider
	catalog    ModelCatalog
	observer   PipelineObserver
	logger     *slog.Logger
	interval   time.Duration
	clock      func() time.Time
	schedule   Schedule
	sourceMode string
	wake       chan struct{}
	mu         sync.RWMutex
	available  bool
	circuits   map[string]time.Time
}

func NewPipelineWorker(repository PipelineRepository, providers StepGeneratorProvider, catalog ModelCatalog, observer PipelineObserver, logger *slog.Logger, interval time.Duration, clock func() time.Time, schedule Schedule, sourceMode string) (*PipelineWorker, error) {
	if repository == nil || providers == nil || catalog == nil || logger == nil {
		return nil, errors.New("pipeline repository, generator provider, catalog, and logger are required")
	}
	if interval <= 0 {
		return nil, errors.New("pipeline interval must be positive")
	}
	if sourceMode != "fixture" && sourceMode != "live" {
		return nil, errors.New("pipeline source mode must be fixture or live")
	}
	if clock == nil {
		clock = time.Now
	}
	if !schedule.Immediate && (schedule.Location == nil || schedule.Start < 0 || schedule.Start >= 24*time.Hour || schedule.End < 0 || schedule.End >= 24*time.Hour || schedule.Start == schedule.End) {
		return nil, errors.New("a valid AI processing schedule is required")
	}
	if err := repository.EnsurePipelineSteps(context.Background(), StepKeys(), clock()); err != nil {
		return nil, err
	}
	return &PipelineWorker{repository: repository, providers: providers, catalog: catalog, observer: observer, logger: logger, interval: interval, clock: clock, schedule: schedule, sourceMode: sourceMode, wake: make(chan struct{}, 1), available: true, circuits: make(map[string]time.Time)}, nil
}

func (w *PipelineWorker) RequestNow(ctx context.Context, sourceMode string, models map[string]string, incidentID *int64, reprocessAll bool) (store.PipelineRequestResult, error) {
	if sourceMode != w.sourceMode {
		return store.PipelineRequestResult{}, errors.New("manual request source mode does not match worker")
	}
	plans, err := StepPlans(models)
	if err != nil {
		return store.PipelineRequestResult{}, err
	}
	snapshot := w.catalog.Snapshot()
	if !snapshot.Available() {
		return store.PipelineRequestResult{}, ErrModelUnavailable
	}
	for _, plan := range plans {
		if !snapshot.Has(plan.Model) {
			return store.PipelineRequestResult{}, fmt.Errorf("%w: %s", ErrModelUnavailable, plan.Model)
		}
	}
	result, err := w.repository.CreateManualPipelineCycle(ctx, sourceMode, plans, incidentID, reprocessAll, w.clock())
	if err == nil && result.Requested > 0 {
		w.signal()
	}
	return result, err
}

func (w *PipelineWorker) SetPreferredStepModel(ctx context.Context, stepKey, model string) error {
	if _, found := StepByKey(stepKey); !found {
		return store.ErrNotFound
	}
	snapshot := w.catalog.Snapshot()
	if !snapshot.Available() || !snapshot.Has(model) {
		return fmt.Errorf("%w: %s", ErrModelUnavailable, model)
	}
	if err := w.repository.SetPipelineStepModel(ctx, stepKey, model, w.clock()); err != nil {
		return err
	}
	w.signal()
	return nil
}

func (w *PipelineWorker) ModelStatus(ctx context.Context) (PipelineModelStatus, error) {
	settings, err := w.repository.PipelineStepSettings(ctx)
	if err != nil {
		return PipelineModelStatus{}, err
	}
	byKey := make(map[string]string, len(settings))
	for _, setting := range settings {
		byKey[setting.StepKey] = setting.PreferredModel
	}
	catalog := w.catalog.Snapshot()
	status := PipelineModelStatus{Models: append([]string(nil), catalog.Models...), CatalogAvailable: catalog.Available(), Ready: catalog.Available()}
	if catalog.Err != nil {
		status.CatalogError = catalog.Err.Error()
	}
	for _, step := range RegisteredSteps() {
		model := byKey[step.Key]
		item := StepModelStatus{Key: step.Key, DisplayName: step.DisplayName, PromptVersion: step.PromptVersion, Preferred: model, PreferredAvailable: model != "" && catalog.Available() && catalog.Has(model)}
		status.Steps = append(status.Steps, item)
		status.Ready = status.Ready && item.PreferredAvailable
	}
	return status, nil
}

func (w *PipelineWorker) Status(ctx context.Context) (PipelineRuntimeStatus, error) {
	models, err := w.ModelStatus(ctx)
	if err != nil {
		return PipelineRuntimeStatus{}, err
	}
	queue, err := w.repository.PipelineSnapshot(ctx, w.sourceMode, StepKeys(), w.clock())
	if err != nil {
		return PipelineRuntimeStatus{}, err
	}
	w.mu.RLock()
	available := w.available
	w.mu.RUnlock()
	window := w.schedule.Allows(w.clock())
	return PipelineRuntimeStatus{GeneratedAt: w.clock(), WindowOpen: window, ScheduledReady: window && models.Ready, ProcessorAvailable: available && models.CatalogAvailable, Models: models, Queue: queue}, nil
}

func (w *PipelineWorker) Run(ctx context.Context) {
	if err := w.repository.RecoverPipeline(ctx, w.clock()); err != nil {
		w.logger.Error("recover staged AI processing", "error", err)
	}
	w.processAvailable(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processAvailable(ctx)
		case <-w.wake:
			w.processAvailable(ctx)
		}
	}
}

func (w *PipelineWorker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *PipelineWorker) processAvailable(ctx context.Context) {
	for ctx.Err() == nil {
		now := w.clock()
		windowOpen := w.schedule.Allows(now)
		if w.observer != nil {
			w.observer.SetProcessingWindowOpen(windowOpen)
		}
		models, modelsErr := w.repository.PreferredPipelineModels(ctx, StepKeys())
		plans, plansErr := StepPlans(models)
		catalog := w.catalog.Snapshot()
		scheduledReady := modelsErr == nil && plansErr == nil && catalog.Available()
		if scheduledReady {
			for _, plan := range plans {
				scheduledReady = scheduledReady && catalog.Has(plan.Model)
			}
		}
		cycle, found, err := w.repository.ActivateNextPipelineCycle(ctx, w.sourceMode, plans, windowOpen && scheduledReady, now)
		if err != nil {
			w.logger.Error("activate staged AI cycle", "error", err)
			return
		}
		if !found {
			w.publishSnapshot(ctx)
			return
		}
		if !w.processCycle(ctx, cycle) {
			w.publishSnapshot(ctx)
			return
		}
	}
}

func (w *PipelineWorker) processCycle(ctx context.Context, cycle store.PipelineCycle) bool {
	steps := RegisteredSteps()
	for ctx.Err() == nil {
		stepKey, model, err := w.repository.PipelineStepModel(ctx, cycle.ID, cycle.ActiveStep)
		if err != nil {
			w.logger.Error("read staged AI cycle model", "cycle_id", cycle.ID, "step_order", cycle.ActiveStep, "error", err)
			return false
		}
		if w.circuitOpen(stepKey, model, w.clock()) {
			return w.yieldBlockedForManual(ctx, cycle)
		}
		job, found, err := w.repository.ClaimPipelineJob(ctx, cycle.ID, cycle.ActiveStep, w.clock())
		if err != nil {
			w.logger.Error("claim staged AI job", "cycle_id", cycle.ID, "error", err)
			return false
		}
		if found {
			if !w.catalog.Snapshot().Has(job.ModelIdentity) {
				now := w.clock()
				retry := now.Add(30 * time.Second)
				_ = w.repository.FailPipelineJob(ctx, job, "pending", string(ErrorConfiguration), &retry, now, fmt.Errorf("selected Ollama model is unavailable"))
				w.openCircuit(job.StepKey, job.ModelIdentity, retry)
				return w.yieldBlockedForManual(ctx, cycle)
			}
			if !w.processJob(ctx, job) {
				return false
			}
			continue
		}
		advance, err := w.repository.AdvancePipelineCycle(ctx, cycle, len(steps), w.clock())
		if err != nil {
			w.logger.Error("advance staged AI cycle", "cycle_id", cycle.ID, "error", err)
			return false
		}
		if advance.Completed {
			w.publishSnapshot(ctx)
			return true
		}
		if advance.Advanced {
			cycle.ActiveStep++
			w.publishSnapshot(ctx)
			continue
		}
		if advance.Waiting {
			return w.yieldBlockedForManual(ctx, cycle)
		}
		return false
	}
	return false
}

func (w *PipelineWorker) yieldBlockedForManual(ctx context.Context, cycle store.PipelineCycle) bool {
	if cycle.Kind != "scheduled" {
		return false
	}
	manual, err := w.repository.HasQueuedManualCycle(ctx)
	if err != nil || !manual {
		return false
	}
	continuation, err := w.repository.InterruptBlockedScheduledCycle(ctx, cycle.ID, w.clock())
	if err != nil {
		w.logger.Error("interrupt blocked scheduled AI cycle", "cycle_id", cycle.ID, "error", err)
		return false
	}
	w.logger.Warn("blocked scheduled AI cycle yielded to manual work", "cycle_id", cycle.ID, "continuation_cycle_id", continuation)
	return true
}

func (w *PipelineWorker) processJob(ctx context.Context, job store.PipelineJob) bool {
	step, found := StepByKey(job.StepKey)
	if !found {
		return w.handleJobFailure(ctx, job, errorOf(ErrorConfiguration, "unknown pipeline step %q", job.StepKey))
	}
	started := w.clock()
	if w.observer != nil {
		w.observer.RecordPipelineAttempt(step.Key)
	}
	w.logger.Info("staged AI request started", "cycle_id", job.CycleID, "job_id", job.ID, "incident_id", job.IncidentID, "step", step.Key, "model", job.ModelIdentity, "attempt", job.AttemptCount)
	generator, err := w.providers.StepGenerator(job.ModelIdentity)
	if err != nil {
		return w.handleJobFailure(ctx, job, errorOf(ErrorConfiguration, "create step generator: %v", err))
	}
	input := StepInput{OriginalTitle: job.OriginalTitle, IncidentBody: job.OriginalBody, TitleDE: job.TitleDE, SummaryDE: job.SummaryDE}
	output, modelIdentity, err := generator.GenerateStep(ctx, step, input)
	if err != nil {
		return w.handleJobFailure(ctx, job, err)
	}
	values, err := PipelineValues(step.Key, output)
	if err != nil {
		return w.handleJobFailure(ctx, job, errorOf(ErrorOutput, "%v", err))
	}
	inputHash := store.HashPipelineInput(job.SourceHash, step.PromptVersion, job.ModelIdentity)
	if step.Key == EnglishTranslationStep {
		inputHash = store.HashPipelineInput(job.TitleDE, job.SummaryDE, step.PromptVersion, job.ModelIdentity)
	}
	completed := w.clock()
	if err := w.repository.CompletePipelineJob(ctx, job, values, modelIdentity, inputHash, completed); err != nil {
		return w.handleJobFailure(ctx, job, err)
	}
	w.setAvailable(true)
	w.closeCircuit(step.Key, job.ModelIdentity)
	if w.observer != nil {
		w.observer.RecordPipelineSuccess(step.Key, completed)
		w.observer.RecordPipelineDuration(step.Key, completed.Sub(started))
	}
	w.logger.Info("staged AI request completed", "cycle_id", job.CycleID, "job_id", job.ID, "incident_id", job.IncidentID, "step", step.Key, "model", modelIdentity, "duration", completed.Sub(started).Round(time.Millisecond))
	w.publishSnapshot(ctx)
	return true
}

func (w *PipelineWorker) handleJobFailure(ctx context.Context, job store.PipelineJob, processingError error) bool {
	kind := KindOf(processingError)
	now := w.clock()
	status := "pending"
	var retryAt *time.Time
	if (kind == ErrorOutput || kind == ErrorPrivacy) && job.AttemptCount >= contentMaxAttempts {
		status = "needs_review"
	} else {
		delay := contentRetryDelay(job.AttemptCount)
		if kind == ErrorTransient {
			delay = transientRetryDelay(job.AttemptCount)
		}
		if kind == ErrorConfiguration {
			delay = configurationRetryDelay(job.AttemptCount)
		}
		next := now.Add(jitter(delay, job.ID, job.AttemptCount))
		retryAt = &next
	}
	if err := w.repository.FailPipelineJob(ctx, job, status, string(kind), retryAt, now, processingError); err != nil {
		w.logger.Error("record staged AI failure", "job_id", job.ID, "error", err)
		return false
	}
	if kind == ErrorTransient || kind == ErrorConfiguration {
		w.setAvailable(false)
		if retryAt != nil {
			w.openCircuit(job.StepKey, job.ModelIdentity, *retryAt)
		}
	}
	if w.observer != nil {
		w.observer.RecordPipelineFailure(job.StepKey, string(kind))
	}
	w.logger.Warn("staged AI request failed", "cycle_id", job.CycleID, "job_id", job.ID, "incident_id", job.IncidentID, "step", job.StepKey, "failure_kind", kind, "status", status, "retry_at", retryAt)
	return kind == ErrorOutput || kind == ErrorPrivacy
}

func pipelineCircuitKey(step, model string) string { return step + "\x00" + model }

func (w *PipelineWorker) circuitOpen(step, model string, now time.Time) bool {
	w.mu.RLock()
	until := w.circuits[pipelineCircuitKey(step, model)]
	w.mu.RUnlock()
	return now.Before(until)
}

func (w *PipelineWorker) openCircuit(step, model string, until time.Time) {
	w.mu.Lock()
	w.circuits[pipelineCircuitKey(step, model)] = until
	w.mu.Unlock()
}

func (w *PipelineWorker) closeCircuit(step, model string) {
	w.mu.Lock()
	delete(w.circuits, pipelineCircuitKey(step, model))
	w.mu.Unlock()
}

func (w *PipelineWorker) setAvailable(value bool) {
	w.mu.Lock()
	w.available = value
	w.mu.Unlock()
	if w.observer != nil {
		w.observer.SetProcessorAvailable(value)
	}
}

func (w *PipelineWorker) publishSnapshot(ctx context.Context) {
	if w.observer == nil {
		return
	}
	snapshot, err := w.repository.PipelineSnapshot(ctx, w.sourceMode, StepKeys(), w.clock())
	if err != nil {
		w.logger.Error("read staged pipeline snapshot", "error", err)
		return
	}
	w.observer.SetPipelineSnapshot(snapshot)
}

func ModelsFromForm(values map[string]string) map[string]string {
	models := make(map[string]string, len(registeredSteps))
	for _, step := range registeredSteps {
		models[step.Key] = strings.TrimSpace(values[step.Key])
	}
	return models
}
