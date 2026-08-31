package processing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

// PipelineRepository is the transactional state-machine surface required by the
// worker. Implementations must make claim, completion, and advancement atomic.
type PipelineRepository interface {
	EnsurePipelineSteps(context.Context, []string, time.Time) error
	PipelineStepSettings(context.Context) ([]store.StepSetting, error)
	SetPipelineStepModel(context.Context, string, string, time.Time) error
	PreferredPipelineModels(context.Context, []string) (map[string]string, error)
	CreateManualPipelineCycle(context.Context, string, []store.PipelineStepPlan, []store.PostProcessingPlan, *int64, bool, time.Time) (store.PipelineRequestResult, error)
	ActivateNextPipelineCycle(context.Context, string, []store.PipelineStepPlan, bool, time.Time) (store.PipelineCycle, bool, error)
	RecoverPipeline(context.Context, time.Time) error
	ClaimPipelineJob(context.Context, int64, int, time.Time) (store.PipelineJob, bool, error)
	CompletePipelineJob(context.Context, store.PipelineJob, []store.PipelineValue, string, string, time.Time) error
	FailPipelineJob(context.Context, store.PipelineJob, string, string, *time.Time, time.Time, error) error
	AdvancePipelineCycle(context.Context, store.PipelineCycle, int, time.Time) (store.AdvanceResult, error)
	HasQueuedManualCycle(context.Context) (bool, error)
	InterruptBlockedAutomaticCycle(context.Context, int64, time.Time) (int64, error)
	PipelineSnapshot(context.Context, string, []string, []store.PostProcessingCounterSpec, time.Time) (store.PipelineSnapshot, error)
	PipelineStepModel(context.Context, int64, int) (string, string, error)
	EnsurePostProcessingScopes(context.Context, []store.PostProcessingScope, time.Time) error
	QueuePostProcessingForRun(context.Context, int64, []store.PostProcessingPlan, string, bool, time.Time) (int, error)
	QueueIncidentPostProcessing(context.Context, int64, []store.PostProcessingPlan, time.Time) (int, error)
	QueuePostProcessingForAll(context.Context, string, []store.PostProcessingPlan, bool, time.Time) (int, error)
	QueueUnpublishedPostProcessingForAll(context.Context, string, []store.PostProcessingPlan, time.Time) (int, error)
	ClaimPostProcessingJob(context.Context, string, store.PostProcessingContract, bool, []string, time.Time) (store.PostProcessingJob, bool, error)
	CompletePostProcessingJob(context.Context, store.PostProcessingJob, []store.PipelineValue, string, string, time.Time) error
	FailPostProcessingJob(context.Context, store.PostProcessingJob, string, string, *time.Time, time.Time, error) error
	RecoverPostProcessing(context.Context, time.Time) error
	CyclePostProcessingPlans(context.Context, int64) ([]store.PostProcessingPlan, error)
	AIControl(context.Context) (store.AIControlState, error)
	SetAutomaticProcessing(context.Context, bool, time.Time) error
	SuspendAutomaticCycle(context.Context, int64, time.Time) (bool, error)
	CancelAllAIWork(context.Context, time.Time) (store.PipelineCancellationResult, error)
}

// PostProcessingRequest describes one explicit processor-only request.
type PostProcessingRequest struct {
	ProcessorKey string
	ScopeKeys    []string
	IncidentID   *int64
	Model        string
	Selection    PostProcessingSelection
}

// PostProcessingSelection controls whether a bulk manual request replaces
// successful work or fills only currently unpublished scopes.
type PostProcessingSelection string

const (
	PostProcessingSelectionAll         PostProcessingSelection = "all"
	PostProcessingSelectionUnpublished PostProcessingSelection = "unpublished"
)

// RequestPostProcessing queues a manual incident rerun or applies the selected
// bulk policy to every current eligible v2 presentation.
func (w *PipelineWorker) RequestPostProcessing(ctx context.Context, request PostProcessingRequest) (int, error) {
	selection := request.Selection
	if selection == "" {
		selection = PostProcessingSelectionAll
	}
	if selection != PostProcessingSelectionAll && selection != PostProcessingSelectionUnpublished {
		return 0, store.ErrNotFound
	}
	if request.IncidentID != nil && selection != PostProcessingSelectionAll {
		return 0, store.ErrNotFound
	}
	if selection == PostProcessingSelectionUnpublished && request.ProcessorKey != TranslationModelStep {
		return 0, store.ErrNotFound
	}
	definition, found := w.postProcessors.Definition(request.ProcessorKey)
	if !found || !definition.Manual {
		return 0, store.ErrNotFound
	}
	model := strings.TrimSpace(request.Model)
	if !w.catalog.Snapshot().Has(model) {
		return 0, fmt.Errorf("%w: %s", ErrModelUnavailable, model)
	}
	plans, err := w.postProcessors.Plans(request.ProcessorKey, request.ScopeKeys, model)
	if err != nil {
		return 0, err
	}
	w.executionMu.Lock()
	defer w.executionMu.Unlock()
	var queued int
	if request.IncidentID == nil {
		if selection == PostProcessingSelectionUnpublished {
			queued, err = w.repository.QueueUnpublishedPostProcessingForAll(ctx, w.sourceMode, plans, w.clock())
		} else {
			queued, err = w.repository.QueuePostProcessingForAll(ctx, w.sourceMode, plans, true, w.clock())
		}
	} else {
		queued, err = w.repository.QueueIncidentPostProcessing(ctx, *request.IncidentID, plans, w.clock())
	}
	if err == nil && queued > 0 {
		target := "all"
		if request.IncidentID != nil {
			target = "incident"
		}
		scopes := make([]string, 0, len(plans))
		for _, plan := range plans {
			scopes = append(scopes, plan.ScopeKey)
		}
		w.logger.Info("AI post-processing jobs queued", "processor", request.ProcessorKey, "scopes", scopes, "target", target, "selection", selection, "incident_id", request.IncidentID, "model", model, "jobs", queued, "request_kind", "manual")
		w.signal()
	}
	return queued, err
}

// PipelineObserver receives bounded operational state; incident text and model
// output must never be included in observations.
type PipelineObserver interface {
	RecordPipelineAttempt(step string)
	RecordPipelineSuccess(step string, at time.Time)
	RecordPipelineFailure(step, kind string)
	RecordPipelineDuration(step string, duration time.Duration)
	SetPipelineSnapshot(store.PipelineSnapshot)
	SetProcessorAvailable(bool)
	SetProcessingWindowOpen(bool)
}

// StepModelStatus describes one step's configured and currently available model.
type StepModelStatus struct {
	Number             int    `json:"number"`
	Key                string `json:"key"`
	DisplayName        string `json:"display_name"`
	PromptVersion      string `json:"prompt_version"`
	Preferred          string `json:"preferred"`
	PreferredAvailable bool   `json:"preferred_available"`
}

// PipelineModelStatus is the review-facing model configuration snapshot.
type PipelineModelStatus struct {
	Steps            []StepModelStatus          `json:"steps"`
	PostProcessors   []PostProcessorModelStatus `json:"post_processors"`
	Models           []string                   `json:"models"`
	CatalogAvailable bool                       `json:"catalog_available"`
	CatalogError     string                     `json:"catalog_error,omitempty"`
	Ready            bool                       `json:"ready"`
}

type PostProcessorScopeStatus struct {
	Key           string `json:"key"`
	DisplayName   string `json:"display_name"`
	StepKey       string `json:"step_key"`
	PromptVersion string `json:"prompt_version"`
}

type PostProcessorModelStatus struct {
	Key                string                     `json:"key"`
	DisplayName        string                     `json:"display_name"`
	Description        string                     `json:"description"`
	ModelSettingKey    string                     `json:"model_setting_key"`
	Manual             bool                       `json:"manual"`
	Preferred          string                     `json:"preferred"`
	PreferredAvailable bool                       `json:"preferred_available"`
	Scopes             []PostProcessorScopeStatus `json:"scopes"`
	Verification       *PostProcessorVerification `json:"verification,omitempty"`
}

// PipelineRuntimeStatus combines model, schedule, worker, and queue readiness.
type PipelineRuntimeStatus struct {
	GeneratedAt                  time.Time              `json:"generated_at"`
	WindowOpen                   bool                   `json:"window_open"`
	ScheduledReady               bool                   `json:"scheduled_ready"`
	AutomaticProcessingEnabled   bool                   `json:"automatic_processing_enabled"`
	AutomaticProcessingUpdatedAt time.Time              `json:"automatic_processing_updated_at"`
	ProcessorAvailable           bool                   `json:"processor_available"`
	Models                       PipelineModelStatus    `json:"models"`
	Queue                        store.PipelineSnapshot `json:"queue"`
}

// PipelineWorker serially executes persisted cycles. Database claims protect
// correctness across restarts; the worker mutex protects only in-memory status.
type PipelineWorker struct {
	repository     PipelineRepository
	providers      StepGeneratorProvider
	catalog        ModelCatalog
	observer       PipelineObserver
	logger         *slog.Logger
	interval       time.Duration
	clock          func() time.Time
	schedule       Schedule
	sourceMode     string
	postProcessors *PostProcessorRegistry
	wake           chan struct{}
	mu             sync.RWMutex
	available      bool
	circuits       map[string]time.Time
	// executionMu linearizes claims, manual queue mutations, automatic-cycle
	// yielding, runtime switch changes, and cancel-all around the in-flight call.
	executionMu         sync.Mutex
	executionGeneration uint64
	currentCancel       context.CancelFunc
}

// NewPipelineWorker validates dependencies and ensures all registered step
// settings exist before any background processing begins.
func NewPipelineWorker(repository PipelineRepository, providers StepGeneratorProvider, catalog ModelCatalog, registry *PostProcessorRegistry, observer PipelineObserver, logger *slog.Logger, interval time.Duration, clock func() time.Time, schedule Schedule, sourceMode string) (*PipelineWorker, error) {
	if repository == nil || providers == nil || catalog == nil || registry == nil || logger == nil {
		return nil, errors.New("pipeline repository, generator provider, catalog, post-processor registry, and logger are required")
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
	settingKeys := append(StepKeys(), registry.ModelSettingKeys()...)
	if err := repository.EnsurePipelineSteps(context.Background(), settingKeys, clock()); err != nil {
		return nil, err
	}
	if err := repository.EnsurePostProcessingScopes(context.Background(), registry.StoreScopes(), clock()); err != nil {
		return nil, err
	}
	return &PipelineWorker{repository: repository, providers: providers, catalog: catalog, postProcessors: registry, observer: observer, logger: logger, interval: interval, clock: clock, schedule: schedule, sourceMode: sourceMode, wake: make(chan struct{}, 1), available: true, circuits: make(map[string]time.Time)}, nil
}

// RequestNow queues manual work after verifying every requested model against
// the most recent catalog snapshot.
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
	var postPlans []store.PostProcessingPlan
	for _, definition := range w.postProcessors.Definitions() {
		if !definition.Manual {
			continue
		}
		model := strings.TrimSpace(models[definition.ModelSettingKey])
		if model == "" {
			continue
		}
		if !snapshot.Has(model) {
			return store.PipelineRequestResult{}, fmt.Errorf("%w: %s", ErrModelUnavailable, model)
		}
		processorPlans, err := w.postProcessors.Plans(definition.Key, nil, model)
		if err != nil {
			return store.PipelineRequestResult{}, err
		}
		postPlans = append(postPlans, processorPlans...)
	}
	w.executionMu.Lock()
	result, err := w.repository.CreateManualPipelineCycle(ctx, sourceMode, plans, postPlans, incidentID, reprocessAll, w.clock())
	w.executionMu.Unlock()
	if err == nil && result.Requested > 0 {
		w.signal()
	}
	return result, err
}

// SetPreferredStepModel changes the model used when future work is frozen.
func (w *PipelineWorker) SetPreferredStepModel(ctx context.Context, stepKey, model string) error {
	if _, found := StepByKey(stepKey); !found {
		registered := false
		for _, definition := range w.postProcessors.Definitions() {
			registered = registered || definition.ModelSettingKey == stepKey
		}
		if !registered {
			return store.ErrNotFound
		}
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

// SetAutomaticProcessing changes the durable automatic-work gate. Manual
// requests remain eligible, and disabling takes effect at the next job boundary.
func (w *PipelineWorker) SetAutomaticProcessing(ctx context.Context, enabled bool) error {
	w.executionMu.Lock()
	defer w.executionMu.Unlock()
	now := w.clock()
	if err := w.repository.SetAutomaticProcessing(ctx, enabled, now); err != nil {
		return err
	}
	w.logger.Info("automatic AI processing changed", "enabled", enabled)
	w.signal()
	return nil
}

// CancelAll disables automatic processing, terminalizes every unfinished job,
// and then interrupts the current provider request. Holding executionMu across
// the transaction closes the gap between a database claim and cancel setup.
func (w *PipelineWorker) CancelAll(ctx context.Context) (store.PipelineCancellationResult, error) {
	w.executionMu.Lock()
	defer w.executionMu.Unlock()
	result, err := w.repository.CancelAllAIWork(ctx, w.clock())
	if err != nil {
		return store.PipelineCancellationResult{}, err
	}
	w.executionGeneration++
	if w.currentCancel != nil {
		w.currentCancel()
		w.currentCancel = nil
	}
	w.logger.Warn("unfinished AI processing canceled", "cycles", result.Cycles, "canonical_jobs", result.CanonicalJobs, "post_processing_jobs", result.PostProcessingJobs)
	w.signal()
	return result, nil
}

// ModelStatus reports canonical and post-processing readiness independently.
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
	for index, step := range RegisteredSteps() {
		model := byKey[step.Key]
		item := StepModelStatus{Number: index + 1, Key: step.Key, DisplayName: step.DisplayName, PromptVersion: step.PromptVersion, Preferred: model, PreferredAvailable: model != "" && catalog.Available() && catalog.Has(model)}
		status.Steps = append(status.Steps, item)
		status.Ready = status.Ready && item.PreferredAvailable
	}
	for _, definition := range w.postProcessors.Definitions() {
		model := byKey[definition.ModelSettingKey]
		item := PostProcessorModelStatus{Key: definition.Key, DisplayName: definition.DisplayName, Description: definition.Description, ModelSettingKey: definition.ModelSettingKey, Manual: definition.Manual, Preferred: model, PreferredAvailable: model != "" && catalog.Available() && catalog.Has(model), Verification: clonePostProcessorVerification(definition.Verification)}
		for _, scope := range definition.Scopes {
			item.Scopes = append(item.Scopes, PostProcessorScopeStatus{Key: scope.Key, DisplayName: scope.DisplayName, StepKey: scope.Step.Key, PromptVersion: scope.Step.PromptVersion})
		}
		status.PostProcessors = append(status.PostProcessors, item)
	}
	return status, nil
}

// Status combines persisted queue state with current catalog and window state.
func (w *PipelineWorker) Status(ctx context.Context) (PipelineRuntimeStatus, error) {
	models, err := w.ModelStatus(ctx)
	if err != nil {
		return PipelineRuntimeStatus{}, err
	}
	queue, err := w.repository.PipelineSnapshot(ctx, w.sourceMode, StepKeys(), w.postProcessingCounterSpecs(), w.clock())
	if err != nil {
		return PipelineRuntimeStatus{}, err
	}
	queue.PostProcessing = w.postProcessors.OrderedQueueStats(queue.PostProcessing)
	control, err := w.repository.AIControl(ctx)
	if err != nil {
		return PipelineRuntimeStatus{}, err
	}
	w.mu.RLock()
	available := w.available
	w.mu.RUnlock()
	window := w.schedule.Allows(w.clock())
	return PipelineRuntimeStatus{
		GeneratedAt: w.clock(), WindowOpen: window,
		ScheduledReady:               window && control.AutomaticProcessingEnabled && models.Ready,
		AutomaticProcessingEnabled:   control.AutomaticProcessingEnabled,
		AutomaticProcessingUpdatedAt: control.UpdatedAt,
		ProcessorAvailable:           available && models.CatalogAvailable, Models: models, Queue: queue,
	}, nil
}

// Run recovers interrupted work, processes immediately available cycles, and
// continues until ctx is cancelled. Only one Run call is supported per worker.
func (w *PipelineWorker) Run(ctx context.Context) {
	if err := w.repository.RecoverPipeline(ctx, w.clock()); err != nil {
		w.logger.Error("recover staged AI processing", "error", err)
	}
	if err := w.repository.RecoverPostProcessing(ctx, w.clock()); err != nil {
		w.logger.Error("recover AI post-processing", "error", err)
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

// beginExecutionLocked registers the only in-flight provider call. The caller
// must hold executionMu until the corresponding database claim is committed.
func (w *PipelineWorker) beginExecutionLocked(ctx context.Context) (context.Context, func()) {
	requestCtx, cancel := context.WithCancel(ctx)
	generation := w.executionGeneration
	w.currentCancel = cancel
	return requestCtx, func() {
		cancel()
		w.executionMu.Lock()
		if w.executionGeneration == generation {
			w.currentCancel = nil
		}
		w.executionMu.Unlock()
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
		control, controlErr := w.repository.AIControl(ctx)
		if controlErr != nil {
			w.logger.Error("read automatic AI processing state", "error", controlErr)
			return
		}
		canonicalReady := modelsErr == nil && plansErr == nil && catalog.Available()
		if canonicalReady {
			for _, plan := range plans {
				canonicalReady = canonicalReady && catalog.Has(plan.Model)
			}
		}
		cycle, found, err := w.repository.ActivateNextPipelineCycle(ctx, w.sourceMode, plans, control.AutomaticProcessingEnabled && windowOpen && canonicalReady, now)
		if err != nil {
			w.logger.Error("activate staged AI cycle", "error", err)
			return
		}
		if found {
			processed := w.processCycle(ctx, cycle)
			if processed || w.suspendDisabledAutomaticCycle(ctx, cycle) {
				continue
			}
		}

		processedPostJob := false
		for _, definition := range w.postProcessors.Definitions() {
			contracts := make(store.PostProcessingContract, len(definition.Scopes))
			for _, scope := range definition.Scopes {
				contracts[scope.Key] = store.PostProcessingScopeContract{
					PromptVersion: scope.Step.PromptVersion,
					InputKinds:    append([]string(nil), scope.Step.InputKinds...),
					OutputKinds:   append([]string(nil), scope.Step.OutputKinds...),
				}
			}
			preferred, preferredErr := w.repository.PreferredPipelineModels(ctx, []string{definition.ModelSettingKey})
			model := preferred[definition.ModelSettingKey]
			if definition.Automatic && control.AutomaticProcessingEnabled && preferredErr == nil && catalog.Available() && catalog.Has(model) && windowOpen {
				plans, err := w.postProcessors.Plans(definition.Key, nil, model)
				if err != nil {
					w.logger.Error("build scheduled AI post-processing plans", "processor", definition.Key, "error", err)
					return
				}
				queued, err := w.repository.QueuePostProcessingForAll(ctx, w.sourceMode, plans, false, now)
				if err != nil {
					w.logger.Error("queue missing AI post-processing", "processor", definition.Key, "error", err)
					return
				}
				if queued > 0 {
					w.logger.Info("AI post-processing jobs discovered", "processor", definition.Key, "jobs", queued, "request_kind", "scheduled")
				}
			}
			w.executionMu.Lock()
			job, found, err := w.repository.ClaimPostProcessingJob(ctx, definition.Key, contracts, control.AutomaticProcessingEnabled && windowOpen, w.blockedModels(definition.Key, now), now)
			if err != nil {
				w.executionMu.Unlock()
				w.logger.Error("claim AI post-processing job", "processor", definition.Key, "error", err)
				return
			}
			if !found {
				w.executionMu.Unlock()
				continue
			}
			requestCtx, finishExecution := w.beginExecutionLocked(ctx)
			w.executionMu.Unlock()
			processedPostJob = true
			if !catalog.Has(job.ModelIdentity) {
				retry := now.Add(30 * time.Second)
				if err := w.repository.FailPostProcessingJob(ctx, job, "pending", string(ErrorConfiguration), &retry, now, fmt.Errorf("selected post-processing model is unavailable")); err != nil {
					if errors.Is(err, store.ErrJobNotRunning) {
						finishExecution()
						w.publishSnapshot(ctx)
						return
					}
					w.logger.Error("defer unavailable AI post-processing model", "job_id", job.ID, "processor", job.ProcessorKey, "error", err)
				} else {
					if w.observer != nil {
						w.observer.RecordPipelineAttempt(job.ProcessorKey + "/" + job.ScopeKey)
						w.observer.RecordPipelineFailure(job.ProcessorKey+"/"+job.ScopeKey, string(ErrorConfiguration))
					}
					w.logger.Warn("AI post-processing job deferred", "job_id", job.ID, "incident_id", job.IncidentID, "processor", job.ProcessorKey, "scope", job.ScopeKey, "model", job.ModelIdentity, "failure_kind", ErrorConfiguration, "retry_at", retry)
				}
				w.openCircuit(job.ProcessorKey, job.ModelIdentity, retry)
				w.publishSnapshot(ctx)
				finishExecution()
				break
			}
			if !w.processPostProcessingJob(ctx, requestCtx, job) {
				finishExecution()
				w.publishSnapshot(ctx)
				return
			}
			finishExecution()
			break
		}
		if processedPostJob {
			continue
		}
		w.publishSnapshot(ctx)
		return
	}
}

func (w *PipelineWorker) processCycle(ctx context.Context, cycle store.PipelineCycle) bool {
	steps := RegisteredSteps()
	for ctx.Err() == nil {
		if w.suspendDisabledAutomaticCycle(ctx, cycle) {
			return true
		}
		stepKey, model, err := w.repository.PipelineStepModel(ctx, cycle.ID, cycle.ActiveStep)
		if err != nil {
			w.logger.Error("read staged AI cycle model", "cycle_id", cycle.ID, "step_order", cycle.ActiveStep, "error", err)
			return false
		}
		if w.circuitOpen(stepKey, model, w.clock()) {
			return w.yieldBlockedForManual(ctx, cycle)
		}
		w.executionMu.Lock()
		job, found, err := w.repository.ClaimPipelineJob(ctx, cycle.ID, cycle.ActiveStep, w.clock())
		if err != nil {
			w.executionMu.Unlock()
			w.logger.Error("claim staged AI job", "cycle_id", cycle.ID, "error", err)
			return false
		}
		if found {
			requestCtx, finishExecution := w.beginExecutionLocked(ctx)
			w.executionMu.Unlock()
			if !w.catalog.Snapshot().Has(job.ModelIdentity) {
				now := w.clock()
				retry := now.Add(30 * time.Second)
				if err := w.repository.FailPipelineJob(ctx, job, "pending", string(ErrorConfiguration), &retry, now, fmt.Errorf("selected ollama model is unavailable")); err != nil {
					if errors.Is(err, store.ErrJobNotRunning) {
						finishExecution()
						return false
					}
					w.logger.Error("defer unavailable staged AI model", "cycle_id", cycle.ID, "job_id", job.ID, "error", err)
					finishExecution()
					return false
				}
				if w.observer != nil {
					w.observer.RecordPipelineAttempt(job.StepKey)
					w.observer.RecordPipelineFailure(job.StepKey, string(ErrorConfiguration))
				}
				w.logger.Warn("staged AI job deferred", "cycle_id", cycle.ID, "job_id", job.ID, "incident_id", job.IncidentID, "step", job.StepKey, "model", job.ModelIdentity, "failure_kind", ErrorConfiguration, "retry_at", retry)
				w.openCircuit(job.StepKey, job.ModelIdentity, retry)
				finishExecution()
				return w.yieldBlockedForManual(ctx, cycle)
			}
			if !w.processJob(ctx, requestCtx, job) {
				finishExecution()
				return false
			}
			finishExecution()
			continue
		}
		w.executionMu.Unlock()
		if w.suspendDisabledAutomaticCycle(ctx, cycle) {
			return true
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

// suspendDisabledAutomaticCycle enforces the durable switch after every way a
// cycle can leave a job boundary, including provider failure and circuit paths.
func (w *PipelineWorker) suspendDisabledAutomaticCycle(ctx context.Context, cycle store.PipelineCycle) bool {
	if cycle.Kind == "manual" || ctx.Err() != nil {
		return false
	}
	control, err := w.repository.AIControl(ctx)
	if err != nil {
		w.logger.Error("read automatic AI processing state", "cycle_id", cycle.ID, "error", err)
		return false
	}
	if control.AutomaticProcessingEnabled {
		return false
	}
	suspended, err := w.repository.SuspendAutomaticCycle(ctx, cycle.ID, w.clock())
	if err != nil {
		w.logger.Error("suspend automatic AI cycle", "cycle_id", cycle.ID, "error", err)
		return false
	}
	if suspended {
		w.logger.Info("automatic AI cycle suspended", "cycle_id", cycle.ID, "kind", cycle.Kind)
	}
	return true
}

func (w *PipelineWorker) yieldBlockedForManual(ctx context.Context, cycle store.PipelineCycle) bool {
	if cycle.Kind != "scheduled" && cycle.Kind != "continuation" {
		return false
	}
	w.executionMu.Lock()
	defer w.executionMu.Unlock()
	manual, err := w.repository.HasQueuedManualCycle(ctx)
	if err != nil || !manual {
		return false
	}
	continuation, err := w.repository.InterruptBlockedAutomaticCycle(ctx, cycle.ID, w.clock())
	if err != nil {
		w.logger.Error("interrupt blocked automatic AI cycle", "cycle_id", cycle.ID, "kind", cycle.Kind, "error", err)
		return false
	}
	w.logger.Warn("blocked automatic AI cycle yielded to manual work", "cycle_id", cycle.ID, "kind", cycle.Kind, "continuation_cycle_id", continuation)
	return true
}

func (w *PipelineWorker) processJob(ctx, requestCtx context.Context, job store.PipelineJob) bool {
	step, found := StepByKey(job.StepKey)
	if !found {
		return w.handleJobFailure(ctx, job, errorOf(ErrorConfiguration, "unknown pipeline step %q", job.StepKey))
	}
	started := w.clock()
	if w.observer != nil {
		w.observer.RecordPipelineAttempt(step.Key)
		defer func() { w.observer.RecordPipelineDuration(step.Key, w.clock().Sub(started)) }()
	}
	w.logger.Info("staged AI request started", "cycle_id", job.CycleID, "job_id", job.ID, "incident_id", job.IncidentID, "step", step.Key, "model", job.ModelIdentity, "attempt", job.AttemptCount)
	generator, err := w.providers.StepGenerator(job.ModelIdentity)
	if err != nil {
		return w.handleJobFailure(ctx, job, errorOf(ErrorConfiguration, "create step generator: %v", err))
	}
	input, inputHash := stepInputAndHash(job, step)
	output, modelIdentity, err := generator.GenerateStep(requestCtx, step, input)
	if err != nil {
		if errors.Is(err, context.Canceled) && requestCtx.Err() != nil && ctx.Err() == nil {
			return false
		}
		return w.handleJobFailure(ctx, job, err)
	}
	values, err := PipelineValues(step.Key, output)
	if err != nil {
		return w.handleJobFailure(ctx, job, errorOf(ErrorOutput, "%v", err))
	}
	completed := w.clock()
	finalCanonical := job.StepKey == GermanPresentationStep
	if err := w.repository.CompletePipelineJob(ctx, job, values, modelIdentity, inputHash, completed); err != nil {
		if errors.Is(err, store.ErrJobNotRunning) {
			return false
		}
		return w.handleJobFailure(ctx, job, err)
	}
	if finalCanonical {
		// German publication is already committed. Independent enqueueing is
		// best-effort so post-processing cannot roll it back.
		requestKind := "manual"
		var postPlans []store.PostProcessingPlan
		var postErr error
		if job.CycleKind != "scheduled" {
			postPlans, postErr = w.repository.CyclePostProcessingPlans(ctx, job.CycleID)
		}
		automaticCycle := job.CycleKind == "scheduled" || job.CycleKind == "continuation"
		windowOpen := w.schedule.Allows(completed)
		if job.CycleKind == "scheduled" {
			requestKind = "scheduled"
			if windowOpen {
				for _, definition := range w.postProcessors.Definitions() {
					if !definition.Automatic {
						continue
					}
					models, err := w.repository.PreferredPipelineModels(ctx, []string{definition.ModelSettingKey})
					if err != nil || !w.catalog.Snapshot().Has(models[definition.ModelSettingKey]) {
						continue
					}
					plans, err := w.postProcessors.Plans(definition.Key, nil, models[definition.ModelSettingKey])
					if err != nil {
						postErr = err
						break
					}
					postPlans = append(postPlans, plans...)
				}
			}
		} else if job.CycleKind == "continuation" {
			requestKind = "scheduled"
			if !windowOpen {
				postPlans = nil
				postErr = nil
			} else if postErr == nil {
				postPlans, postErr = w.hydratePostProcessingPlans(postPlans)
			}
		} else if postErr == nil {
			postPlans, postErr = w.hydratePostProcessingPlans(postPlans)
		}
		if automaticCycle && !windowOpen {
			w.logger.Info("scheduled AI post-processing discovery deferred outside window", "cycle_id", job.CycleID, "cycle_kind", job.CycleKind, "presentation_run_id", job.PresentationRunID)
		}
		if postErr != nil {
			w.logger.Error("resolve AI post-processing plans", "cycle_id", job.CycleID, "error", postErr)
		} else if len(postPlans) > 0 {
			// Serialize continuation enqueueing with cancel-all. If cancellation
			// happens first, do not create new work after its database transaction;
			// if enqueueing happens first, that transaction will terminalize it.
			w.executionMu.Lock()
			if requestCtx.Err() != nil {
				w.executionMu.Unlock()
				return false
			}
			queued, err := w.repository.QueuePostProcessingForRun(ctx, job.PresentationRunID, postPlans, requestKind, false, completed)
			w.executionMu.Unlock()
			if err != nil {
				w.logger.Error("queue presentation AI post-processing", "presentation_run_id", job.PresentationRunID, "error", err)
			} else if queued > 0 {
				w.logger.Info("AI post-processing jobs queued", "cycle_id", job.CycleID, "presentation_run_id", job.PresentationRunID, "jobs", queued, "request_kind", requestKind)
			}
		}
	}
	w.setAvailable(true)
	w.closeCircuit(step.Key, job.ModelIdentity)
	if w.observer != nil {
		w.observer.RecordPipelineSuccess(step.Key, completed)
	}
	duration := completed.Sub(started)
	w.logger.Info("staged AI request completed", "cycle_id", job.CycleID, "job_id", job.ID, "incident_id", job.IncidentID, "step", step.Key, "model", modelIdentity, "duration", duration.Round(time.Millisecond), "duration_seconds", duration.Seconds())
	w.publishSnapshot(ctx)
	return true
}

func (w *PipelineWorker) hydratePostProcessingPlans(plans []store.PostProcessingPlan) ([]store.PostProcessingPlan, error) {
	for index, plan := range plans {
		_, scope, found := w.postProcessors.Scope(plan.ProcessorKey, plan.ScopeKey)
		if !found || scope.Step.PromptVersion != plan.PromptVersion {
			return nil, fmt.Errorf("unknown frozen post-processing plan %s/%s", plan.ProcessorKey, plan.ScopeKey)
		}
		plans[index].InputKinds = append([]string(nil), scope.Step.InputKinds...)
	}
	return plans, nil
}

func (w *PipelineWorker) processPostProcessingJob(ctx, requestCtx context.Context, job store.PostProcessingJob) bool {
	_, scope, found := w.postProcessors.Scope(job.ProcessorKey, job.ScopeKey)
	if !found || scope.Step.PromptVersion != job.PromptVersion {
		return w.handlePostProcessingFailure(ctx, job, errorOf(ErrorConfiguration, "unknown post-processing definition %s/%s", job.ProcessorKey, job.ScopeKey))
	}
	executionKey := job.ProcessorKey + "/" + job.ScopeKey
	started := w.clock()
	if w.observer != nil {
		w.observer.RecordPipelineAttempt(executionKey)
		defer func() { w.observer.RecordPipelineDuration(executionKey, w.clock().Sub(started)) }()
	}
	w.logger.Info("AI post-processing request started", "job_id", job.ID, "incident_id", job.IncidentID, "processor", job.ProcessorKey, "scope", job.ScopeKey, "model", job.ModelIdentity, "attempt", job.AttemptCount)
	generator, err := w.providers.StepGenerator(job.ModelIdentity)
	if err != nil {
		return w.handlePostProcessingFailure(ctx, job, errorOf(ErrorConfiguration, "create post-processing generator: %v", err))
	}
	inputValues := make(map[string]string, len(scope.Step.InputKinds))
	hashParts := []string{"processor", job.ProcessorKey, "scope", job.ScopeKey}
	for _, kind := range scope.Step.InputKinds {
		value := job.InputValues[kind]
		inputValues[kind] = value
		hashParts = append(hashParts, kind, value)
	}
	hashParts = append(hashParts, job.PromptVersion, job.ModelIdentity)
	inputHash := store.HashPipelineInput(hashParts...)
	output, modelIdentity, err := generator.GenerateStep(requestCtx, scope.Step, StepInput{Values: inputValues})
	if err != nil {
		if errors.Is(err, context.Canceled) && requestCtx.Err() != nil && ctx.Err() == nil {
			return false
		}
		return w.handlePostProcessingFailure(ctx, job, err)
	}
	values, err := scope.Step.OutputValues(output)
	if err != nil {
		return w.handlePostProcessingFailure(ctx, job, errorOf(ErrorOutput, "%v", err))
	}
	if err := requireOutputKinds(values, scope.Step.OutputKinds); err != nil {
		return w.handlePostProcessingFailure(ctx, job, errorOf(ErrorOutput, "%v", err))
	}
	completed := w.clock()
	if err := w.repository.CompletePostProcessingJob(ctx, job, values, modelIdentity, inputHash, completed); err != nil {
		if errors.Is(err, store.ErrJobNotRunning) {
			return false
		}
		return w.handlePostProcessingFailure(ctx, job, err)
	}
	w.closeCircuit(job.ProcessorKey, job.ModelIdentity)
	if w.observer != nil {
		w.observer.RecordPipelineSuccess(executionKey, completed)
	}
	duration := completed.Sub(started)
	w.logger.Info("AI post-processing request completed", "job_id", job.ID, "incident_id", job.IncidentID, "processor", job.ProcessorKey, "scope", job.ScopeKey, "model", modelIdentity, "duration", duration.Round(time.Millisecond), "duration_seconds", duration.Seconds())
	w.publishSnapshot(ctx)
	return true
}

func (w *PipelineWorker) handlePostProcessingFailure(ctx context.Context, job store.PostProcessingJob, processingError error) bool {
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
		} else if kind == ErrorConfiguration {
			delay = configurationRetryDelay(job.AttemptCount)
		}
		next := now.Add(jitter(delay, job.ID, job.AttemptCount))
		retryAt = &next
	}
	if err := w.repository.FailPostProcessingJob(ctx, job, status, string(kind), retryAt, now, processingError); err != nil {
		if errors.Is(err, store.ErrJobNotRunning) {
			return false
		}
		w.logger.Error("record AI post-processing failure", "job_id", job.ID, "error", err)
		return false
	}
	if kind == ErrorTransient || kind == ErrorConfiguration {
		if retryAt != nil {
			w.openCircuit(job.ProcessorKey, job.ModelIdentity, *retryAt)
		}
	}
	if w.observer != nil {
		executionKey := job.ProcessorKey + "/" + job.ScopeKey
		w.observer.RecordPipelineFailure(executionKey, string(kind))
	}
	w.logger.Warn("AI post-processing request failed", "job_id", job.ID, "incident_id", job.IncidentID, "processor", job.ProcessorKey, "scope", job.ScopeKey, "failure_kind", kind, "status", status, "retry_at", retryAt)
	return kind == ErrorOutput || kind == ErrorPrivacy
}

func stepInputAndHash(job store.PipelineJob, step StepDefinition) (StepInput, string) {
	inputValues := make(map[string]string, len(step.InputKinds))
	hashValues := make([]string, 0, len(step.InputKinds)*2+2)
	for _, kind := range step.InputKinds {
		value := job.InputValues[kind]
		inputValues[kind] = value
		hashValues = append(hashValues, kind, value)
	}
	hashValues = append(hashValues, step.PromptVersion, job.ModelIdentity)
	return StepInput{Values: inputValues}, store.HashPipelineInput(hashValues...)
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
		if errors.Is(err, store.ErrJobNotRunning) {
			return false
		}
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

// blockedModels returns models for one independent processor whose systemic
// failure backoff is still active. Other manual overrides remain claimable.
func (w *PipelineWorker) blockedModels(step string, now time.Time) []string {
	prefix := step + "\x00"
	w.mu.RLock()
	models := make([]string, 0, len(w.circuits))
	for key, until := range w.circuits {
		if strings.HasPrefix(key, prefix) && now.Before(until) {
			models = append(models, strings.TrimPrefix(key, prefix))
		}
	}
	w.mu.RUnlock()
	sort.Strings(models)
	return models
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
	snapshot, err := w.repository.PipelineSnapshot(ctx, w.sourceMode, StepKeys(), w.postProcessingCounterSpecs(), w.clock())
	if err != nil {
		w.logger.Error("read staged pipeline snapshot", "error", err)
		return
	}
	snapshot.PostProcessing = w.postProcessors.OrderedQueueStats(snapshot.PostProcessing)
	w.observer.SetPipelineSnapshot(snapshot)
}

func (w *PipelineWorker) postProcessingCounterSpecs() []store.PostProcessingCounterSpec {
	var specs []store.PostProcessingCounterSpec
	for _, definition := range w.postProcessors.Definitions() {
		for _, scope := range definition.Scopes {
			for _, counter := range definition.Counters {
				specs = append(specs, store.PostProcessingCounterSpec{
					ProcessorKey: definition.Key, ScopeKey: scope.Key, CounterKey: counter.Key,
					OutputKind: counter.OutputKind, EqualsValue: counter.EqualsValue,
					RequiredOutputKinds: append([]string(nil), scope.Step.OutputKinds...),
				})
			}
		}
	}
	return specs
}
