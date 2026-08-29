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
	CreateManualPipelineCycleWithPostProcessing(context.Context, string, []store.PipelineStepPlan, string, string, *int64, bool, time.Time) (store.PipelineRequestResult, error)
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
	EnsureTranslationLanguages(context.Context, []string, time.Time) error
	QueueTranslationsForRun(context.Context, int64, []store.TranslationPlan, string, time.Time) (int, error)
	QueueIncidentTranslation(context.Context, int64, store.TranslationPlan, time.Time) (int, error)
	RequeueIncidentTranslations(context.Context, int64, []store.TranslationPlan, time.Time) (int, error)
	QueueMissingTranslations(context.Context, string, []store.TranslationPlan, bool, time.Time) (int, error)
	RequeueAllTranslations(context.Context, string, []store.TranslationPlan, time.Time) (int, error)
	ClaimTranslationJob(context.Context, bool, []string, time.Time) (store.TranslationJob, bool, error)
	CompleteTranslationJob(context.Context, store.TranslationJob, string, string, string, string, time.Time) error
	FailTranslationJob(context.Context, store.TranslationJob, string, string, *time.Time, time.Time, error) error
	RecoverTranslations(context.Context, time.Time) error
	QueueCategoryVerificationForRun(context.Context, int64, store.CategoryVerificationPlan, string, bool, time.Time) (int, error)
	QueueIncidentCategoryVerification(context.Context, int64, store.CategoryVerificationPlan, time.Time) (int, error)
	QueueMissingCategoryVerifications(context.Context, string, store.CategoryVerificationPlan, bool, time.Time) (int, error)
	RequeueAllCategoryVerifications(context.Context, string, store.CategoryVerificationPlan, time.Time) (int, error)
	ClaimCategoryVerificationJob(context.Context, bool, []string, time.Time) (store.CategoryVerificationJob, bool, error)
	CompleteCategoryVerificationJob(context.Context, store.CategoryVerificationJob, bool, string, string, string, time.Time) error
	FailCategoryVerificationJob(context.Context, store.CategoryVerificationJob, string, string, *time.Time, time.Time, error) error
	RecoverCategoryVerifications(context.Context, time.Time) error
}

// RequestCategoryVerifications queues only category-verification work. A nil
// incident ID targets every eligible presentation; completed checks are rerun.
func (w *PipelineWorker) RequestCategoryVerifications(ctx context.Context, incidentID *int64, model string) (int, error) {
	if !w.catalog.Snapshot().Has(model) {
		return 0, fmt.Errorf("%w: %s", ErrModelUnavailable, model)
	}
	plan := store.CategoryVerificationPlan{PromptVersion: CategoryVerificationPromptVersion, Model: model}
	var queued int
	var err error
	if incidentID == nil {
		queued, err = w.repository.RequeueAllCategoryVerifications(ctx, w.sourceMode, plan, w.clock())
	} else {
		queued, err = w.repository.QueueIncidentCategoryVerification(ctx, *incidentID, plan, w.clock())
	}
	if err == nil && queued > 0 {
		w.signal()
	}
	return queued, err
}

// RequestTranslations queues only translation work for the requested
// languages. A nil incident ID targets every current eligible incident;
// completed translations are deliberately rerun for this explicit action.
func (w *PipelineWorker) RequestTranslations(ctx context.Context, incidentID *int64, languages []string, model string) (int, error) {
	if !w.catalog.Snapshot().Has(model) {
		return 0, fmt.Errorf("%w: %s", ErrModelUnavailable, model)
	}
	plans := make([]store.TranslationPlan, 0, len(languages))
	seen := make(map[string]struct{}, len(languages))
	for _, language := range languages {
		translation, found := TranslationByLanguage(language)
		if !found {
			return 0, store.ErrNotFound
		}
		if _, duplicate := seen[language]; duplicate {
			continue
		}
		seen[language] = struct{}{}
		plans = append(plans, store.TranslationPlan{Language: language, PromptVersion: translation.PromptVersion, Model: model})
	}
	if len(plans) == 0 {
		return 0, store.ErrNotFound
	}
	var queued int
	var err error
	if incidentID == nil {
		queued, err = w.repository.RequeueAllTranslations(ctx, w.sourceMode, plans, w.clock())
	} else {
		queued, err = w.repository.RequeueIncidentTranslations(ctx, *incidentID, plans, w.clock())
	}
	if err == nil && queued > 0 {
		w.signal()
	}
	return queued, err
}

func (w *PipelineWorker) RetryCategoryVerification(ctx context.Context, incidentID int64, model string) (int, error) {
	if !w.catalog.Snapshot().Has(model) {
		return 0, fmt.Errorf("%w: %s", ErrModelUnavailable, model)
	}
	queued, err := w.repository.QueueIncidentCategoryVerification(ctx, incidentID, store.CategoryVerificationPlan{
		PromptVersion: CategoryVerificationPromptVersion, Model: model,
	}, w.clock())
	if err == nil && queued > 0 {
		w.signal()
	}
	return queued, err
}

// RetryTranslation queues one immediate translation attempt for the incident's
// newest supported canonical presentation.
func (w *PipelineWorker) RetryTranslation(ctx context.Context, incidentID int64, language, model string) (int, error) {
	translation, found := TranslationByLanguage(language)
	if !found {
		return 0, store.ErrNotFound
	}
	if !w.catalog.Snapshot().Has(model) {
		return 0, fmt.Errorf("%w: %s", ErrModelUnavailable, model)
	}
	queued, err := w.repository.QueueIncidentTranslation(ctx, incidentID, store.TranslationPlan{Language: language, PromptVersion: translation.PromptVersion, Model: model}, w.clock())
	if err == nil && queued > 0 {
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
	Steps                     []StepModelStatus `json:"steps"`
	Translation               StepModelStatus   `json:"translation"`
	CategoryVerification      StepModelStatus   `json:"category_verification"`
	Models                    []string          `json:"models"`
	CatalogAvailable          bool              `json:"catalog_available"`
	CatalogError              string            `json:"catalog_error,omitempty"`
	Ready                     bool              `json:"ready"`
	TranslationReady          bool              `json:"translation_ready"`
	CategoryVerificationReady bool              `json:"category_verification_ready"`
}

// PipelineRuntimeStatus combines model, schedule, worker, and queue readiness.
type PipelineRuntimeStatus struct {
	GeneratedAt        time.Time              `json:"generated_at"`
	WindowOpen         bool                   `json:"window_open"`
	ScheduledReady     bool                   `json:"scheduled_ready"`
	ProcessorAvailable bool                   `json:"processor_available"`
	Models             PipelineModelStatus    `json:"models"`
	Queue              store.PipelineSnapshot `json:"queue"`
}

// PipelineWorker serially executes persisted cycles. Database claims protect
// correctness across restarts; the worker mutex protects only in-memory status.
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

// NewPipelineWorker validates dependencies and ensures all registered step
// settings exist before any background processing begins.
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
	if err := repository.EnsurePipelineSteps(context.Background(), ModelSettingKeys(), clock()); err != nil {
		return nil, err
	}
	languages := make([]string, 0, len(RegisteredTranslations()))
	for _, translation := range RegisteredTranslations() {
		languages = append(languages, translation.Language)
	}
	if err := repository.EnsureTranslationLanguages(context.Background(), languages, clock()); err != nil {
		return nil, err
	}
	return &PipelineWorker{repository: repository, providers: providers, catalog: catalog, observer: observer, logger: logger, interval: interval, clock: clock, schedule: schedule, sourceMode: sourceMode, wake: make(chan struct{}, 1), available: true, circuits: make(map[string]time.Time)}, nil
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
	translationModel := models[TranslationModelStep]
	if translationModel != "" && !snapshot.Has(translationModel) {
		return store.PipelineRequestResult{}, fmt.Errorf("%w: %s", ErrModelUnavailable, translationModel)
	}
	categoryModel := models[CategoryVerificationStep]
	if categoryModel != "" && !snapshot.Has(categoryModel) {
		return store.PipelineRequestResult{}, fmt.Errorf("%w: %s", ErrModelUnavailable, categoryModel)
	}
	result, err := w.repository.CreateManualPipelineCycleWithPostProcessing(ctx, sourceMode, plans, translationModel, categoryModel, incidentID, reprocessAll, w.clock())
	if err == nil && result.Requested > 0 {
		w.signal()
	}
	return result, err
}

// SetPreferredStepModel changes the model used when future work is frozen.
func (w *PipelineWorker) SetPreferredStepModel(ctx context.Context, stepKey, model string) error {
	if _, found := StepByKey(stepKey); !found && stepKey != TranslationModelStep && stepKey != CategoryVerificationStep {
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
	translationModel := byKey[TranslationModelStep]
	status.Translation = StepModelStatus{Key: TranslationModelStep, DisplayName: "Translations", PromptVersion: "per language", Preferred: translationModel, PreferredAvailable: translationModel != "" && catalog.Available() && catalog.Has(translationModel)}
	status.TranslationReady = status.Translation.PreferredAvailable
	categoryModel := byKey[CategoryVerificationStep]
	status.CategoryVerification = StepModelStatus{Key: CategoryVerificationStep, DisplayName: "Category verification", PromptVersion: CategoryVerificationPromptVersion, Preferred: categoryModel, PreferredAvailable: categoryModel != "" && catalog.Available() && catalog.Has(categoryModel)}
	status.CategoryVerificationReady = status.CategoryVerification.PreferredAvailable
	return status, nil
}

// Status combines persisted queue state with current catalog and window state.
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

// Run recovers interrupted work, processes immediately available cycles, and
// continues until ctx is cancelled. Only one Run call is supported per worker.
func (w *PipelineWorker) Run(ctx context.Context) {
	if err := w.repository.RecoverPipeline(ctx, w.clock()); err != nil {
		w.logger.Error("recover staged AI processing", "error", err)
	}
	if err := w.repository.RecoverTranslations(ctx, w.clock()); err != nil {
		w.logger.Error("recover translations", "error", err)
	}
	if err := w.repository.RecoverCategoryVerifications(ctx, w.clock()); err != nil {
		w.logger.Error("recover category verifications", "error", err)
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
		canonicalReady := modelsErr == nil && plansErr == nil && catalog.Available()
		if canonicalReady {
			for _, plan := range plans {
				canonicalReady = canonicalReady && catalog.Has(plan.Model)
			}
		}
		cycle, found, err := w.repository.ActivateNextPipelineCycle(ctx, w.sourceMode, plans, windowOpen && canonicalReady, now)
		if err != nil {
			w.logger.Error("activate staged AI cycle", "error", err)
			return
		}
		if found && w.processCycle(ctx, cycle) {
			continue
		}

		// Canonical work is always checked first. Category verification has the
		// first independent slot so corrections converge before translations.
		categoryModels, categoryModelsErr := w.repository.PreferredPipelineModels(ctx, []string{CategoryVerificationStep})
		categoryModel := categoryModels[CategoryVerificationStep]
		if categoryModelsErr == nil && catalog.Available() && catalog.Has(categoryModel) && windowOpen {
			if _, err := w.repository.QueueMissingCategoryVerifications(ctx, w.sourceMode, store.CategoryVerificationPlan{PromptVersion: CategoryVerificationPromptVersion, Model: categoryModel}, false, now); err != nil {
				w.logger.Error("queue missing category verifications", "error", err)
				return
			}
		}
		categoryJob, categoryFound, err := w.repository.ClaimCategoryVerificationJob(ctx, windowOpen, w.blockedModels(CategoryVerificationStep, now), now)
		if err != nil {
			w.logger.Error("claim category verification job", "error", err)
			return
		}
		if categoryFound {
			if !catalog.Has(categoryJob.ModelIdentity) {
				retry := now.Add(30 * time.Second)
				if err := w.repository.FailCategoryVerificationJob(ctx, categoryJob, "pending", string(ErrorConfiguration), &retry, now, fmt.Errorf("selected category verification model is unavailable")); err != nil {
					w.logger.Error("defer unavailable category verification model", "verification_id", categoryJob.ID, "error", err)
				}
				w.openCircuit(CategoryVerificationStep, categoryJob.ModelIdentity, retry)
				w.publishSnapshot(ctx)
				continue
			}
			if !w.processCategoryVerificationJob(ctx, categoryJob) {
				w.publishSnapshot(ctx)
				return
			}
			continue
		}

		// Reaching this point means canonical and category work are absent or
		// waiting, so one translation may run before the next canonical check.
		translationModels, translationErr := w.repository.PreferredPipelineModels(ctx, []string{TranslationModelStep})
		translationModel := translationModels[TranslationModelStep]
		if translationErr == nil && catalog.Available() && catalog.Has(translationModel) && windowOpen {
			if _, err := w.repository.QueueMissingTranslations(ctx, w.sourceMode, translationPlans(translationModel), false, now); err != nil {
				w.logger.Error("queue missing translations", "error", err)
				return
			}
		}
		translationJob, translationFound, err := w.repository.ClaimTranslationJob(ctx, windowOpen, w.blockedModels(TranslationModelStep, now), now)
		if err != nil {
			w.logger.Error("claim translation job", "error", err)
			return
		}
		if translationFound {
			if !catalog.Has(translationJob.ModelIdentity) {
				retry := now.Add(30 * time.Second)
				if err := w.repository.FailTranslationJob(ctx, translationJob, "pending", string(ErrorConfiguration), &retry, now, fmt.Errorf("selected translation model is unavailable")); err != nil {
					w.logger.Error("defer unavailable translation model", "translation_id", translationJob.ID, "error", err)
				}
				w.openCircuit(TranslationModelStep, translationJob.ModelIdentity, retry)
				w.publishSnapshot(ctx)
				return
			}
			if !w.processTranslationJob(ctx, translationJob) {
				w.publishSnapshot(ctx)
				return
			}
			continue
		}
		w.publishSnapshot(ctx)
		return
	}
}

func translationPlans(model string) []store.TranslationPlan {
	translations := RegisteredTranslations()
	plans := make([]store.TranslationPlan, 0, len(translations))
	for _, translation := range translations {
		plans = append(plans, store.TranslationPlan{Language: translation.Language, PromptVersion: translation.PromptVersion, Model: model})
	}
	return plans
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
				if err := w.repository.FailPipelineJob(ctx, job, "pending", string(ErrorConfiguration), &retry, now, fmt.Errorf("selected ollama model is unavailable")); err != nil {
					w.logger.Error("defer unavailable staged AI model", "cycle_id", cycle.ID, "job_id", job.ID, "error", err)
					return false
				}
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
	input, inputHash := stepInputAndHash(job, step)
	output, modelIdentity, err := generator.GenerateStep(ctx, step, input)
	if err != nil {
		return w.handleJobFailure(ctx, job, err)
	}
	values, err := PipelineValues(step.Key, output)
	if err != nil {
		return w.handleJobFailure(ctx, job, errorOf(ErrorOutput, "%v", err))
	}
	completed := w.clock()
	finalCanonical := job.StepKey == GermanPresentationStep
	if err := w.repository.CompletePipelineJob(ctx, job, values, modelIdentity, inputHash, completed); err != nil {
		return w.handleJobFailure(ctx, job, err)
	}
	if finalCanonical {
		// German publication is already committed. Independent enqueueing is
		// best-effort so neither verifier nor translation failures can roll it back.
		categoryModel := job.CategoryVerificationModel
		categoryRequestKind := "manual"
		if job.CycleKind != "manual" {
			categoryRequestKind = "scheduled"
			models, err := w.repository.PreferredPipelineModels(ctx, []string{CategoryVerificationStep})
			if err == nil && w.catalog.Snapshot().Has(models[CategoryVerificationStep]) {
				categoryModel = models[CategoryVerificationStep]
			}
		}
		if categoryModel != "" {
			if _, err := w.repository.QueueCategoryVerificationForRun(ctx, job.PresentationRunID, store.CategoryVerificationPlan{PromptVersion: CategoryVerificationPromptVersion, Model: categoryModel}, categoryRequestKind, false, completed); err != nil {
				w.logger.Error("queue category verification", "presentation_run_id", job.PresentationRunID, "error", err)
			}
		}
		translationModel := job.TranslationModel
		requestKind := "manual"
		if job.CycleKind != "manual" {
			requestKind = "scheduled"
			models, err := w.repository.PreferredPipelineModels(ctx, []string{TranslationModelStep})
			if err == nil && w.catalog.Snapshot().Has(models[TranslationModelStep]) {
				translationModel = models[TranslationModelStep]
			}
		}
		if translationModel != "" {
			if _, err := w.repository.QueueTranslationsForRun(ctx, job.PresentationRunID, translationPlans(translationModel), requestKind, completed); err != nil {
				w.logger.Error("queue presentation translations", "presentation_run_id", job.PresentationRunID, "error", err)
			}
		}
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

func (w *PipelineWorker) processCategoryVerificationJob(ctx context.Context, job store.CategoryVerificationJob) bool {
	definition := CategoryVerificationDefinition()
	if definition.PromptVersion != job.PromptVersion {
		return w.handleCategoryVerificationFailure(ctx, job, errorOf(ErrorConfiguration, "unknown category verification prompt %q", job.PromptVersion))
	}
	started := w.clock()
	if w.observer != nil {
		w.observer.RecordPipelineAttempt(CategoryVerificationStep)
	}
	w.logger.Info("category verification started", "verification_id", job.ID, "incident_id", job.IncidentID, "model", job.ModelIdentity, "attempt", job.AttemptCount)
	generator, err := w.providers.StepGenerator(job.ModelIdentity)
	if err != nil {
		return w.handleCategoryVerificationFailure(ctx, job, errorOf(ErrorConfiguration, "create category verification generator: %v", err))
	}
	input := StepInput{Values: map[string]string{"title_de": job.TitleDE, "summary_de": job.SummaryDE, "category": job.InputCategory}}
	inputHash := store.HashPipelineInput("title_de", job.TitleDE, "summary_de", job.SummaryDE, "category", job.InputCategory, job.PromptVersion, job.ModelIdentity)
	output, modelIdentity, err := generator.GenerateStep(ctx, definition, input)
	if err != nil {
		return w.handleCategoryVerificationFailure(ctx, job, err)
	}
	if output.CategoryVerification == nil {
		return w.handleCategoryVerificationFailure(ctx, job, errorOf(ErrorOutput, "category verification returned no result"))
	}
	completed := w.clock()
	if err := w.repository.CompleteCategoryVerificationJob(ctx, job, output.CategoryVerification.IsCorrect, output.CategoryVerification.CorrectedCategory, modelIdentity, inputHash, completed); err != nil {
		return w.handleCategoryVerificationFailure(ctx, job, err)
	}
	w.closeCircuit(CategoryVerificationStep, job.ModelIdentity)
	if w.observer != nil {
		w.observer.RecordPipelineSuccess(CategoryVerificationStep, completed)
		w.observer.RecordPipelineDuration(CategoryVerificationStep, completed.Sub(started))
	}
	w.logger.Info("category verification completed", "verification_id", job.ID, "incident_id", job.IncidentID, "model", modelIdentity, "correct", output.CategoryVerification.IsCorrect, "duration", completed.Sub(started).Round(time.Millisecond))
	w.publishSnapshot(ctx)
	return true
}

func (w *PipelineWorker) handleCategoryVerificationFailure(ctx context.Context, job store.CategoryVerificationJob, processingError error) bool {
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
	if err := w.repository.FailCategoryVerificationJob(ctx, job, status, string(kind), retryAt, now, processingError); err != nil {
		w.logger.Error("record category verification failure", "verification_id", job.ID, "error", err)
		return false
	}
	if kind == ErrorTransient || kind == ErrorConfiguration {
		if retryAt != nil {
			w.openCircuit(CategoryVerificationStep, job.ModelIdentity, *retryAt)
		}
	}
	if w.observer != nil {
		w.observer.RecordPipelineFailure(CategoryVerificationStep, string(kind))
	}
	w.logger.Warn("category verification failed", "verification_id", job.ID, "incident_id", job.IncidentID, "failure_kind", kind, "status", status, "retry_at", retryAt)
	return kind == ErrorOutput || kind == ErrorPrivacy
}

func (w *PipelineWorker) processTranslationJob(ctx context.Context, job store.TranslationJob) bool {
	translation, found := TranslationByLanguage(job.Language)
	if !found || translation.PromptVersion != job.PromptVersion {
		return w.handleTranslationFailure(ctx, job, errorOf(ErrorConfiguration, "unknown translation definition %q", job.Language))
	}
	started := w.clock()
	if w.observer != nil {
		w.observer.RecordPipelineAttempt(translation.Step.Key)
	}
	w.logger.Info("translation request started", "translation_id", job.ID, "incident_id", job.IncidentID, "language", job.Language, "model", job.ModelIdentity, "attempt", job.AttemptCount)
	generator, err := w.providers.StepGenerator(job.ModelIdentity)
	if err != nil {
		return w.handleTranslationFailure(ctx, job, errorOf(ErrorConfiguration, "create translation generator: %v", err))
	}
	inputValues := map[string]string{"title_de": job.TitleDE, "summary_de": job.SummaryDE}
	input := StepInput{Values: inputValues}
	inputHash := store.HashPipelineInput("title_de", job.TitleDE, "summary_de", job.SummaryDE, "language", job.Language, job.PromptVersion, job.ModelIdentity)
	output, modelIdentity, err := generator.GenerateStep(ctx, translation.Step, input)
	if err != nil {
		return w.handleTranslationFailure(ctx, job, err)
	}
	completed := w.clock()
	if output.Translation == nil {
		return w.handleTranslationFailure(ctx, job, errorOf(ErrorOutput, "translation returned no presentation"))
	}
	if err := w.repository.CompleteTranslationJob(ctx, job, output.Translation.Title, output.Translation.Summary, modelIdentity, inputHash, completed); err != nil {
		return w.handleTranslationFailure(ctx, job, err)
	}
	w.closeCircuit(TranslationModelStep, job.ModelIdentity)
	if w.observer != nil {
		w.observer.RecordPipelineSuccess(translation.Step.Key, completed)
		w.observer.RecordPipelineDuration(translation.Step.Key, completed.Sub(started))
	}
	w.logger.Info("translation request completed", "translation_id", job.ID, "incident_id", job.IncidentID, "language", job.Language, "model", modelIdentity, "duration", completed.Sub(started).Round(time.Millisecond))
	w.publishSnapshot(ctx)
	return true
}

func (w *PipelineWorker) handleTranslationFailure(ctx context.Context, job store.TranslationJob, processingError error) bool {
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
	if err := w.repository.FailTranslationJob(ctx, job, status, string(kind), retryAt, now, processingError); err != nil {
		w.logger.Error("record translation failure", "translation_id", job.ID, "error", err)
		return false
	}
	if kind == ErrorTransient || kind == ErrorConfiguration {
		if retryAt != nil {
			w.openCircuit(TranslationModelStep, job.ModelIdentity, *retryAt)
		}
	}
	if w.observer != nil {
		w.observer.RecordPipelineFailure("translation/"+job.Language, string(kind))
	}
	w.logger.Warn("translation request failed", "translation_id", job.ID, "incident_id", job.IncidentID, "language", job.Language, "failure_kind", kind, "status", status, "retry_at", retryAt)
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

// blockedTranslationModels is retained for focused worker tests and callers
// migrating to the generic independent-processor circuit helper.
func (w *PipelineWorker) blockedTranslationModels(now time.Time) []string {
	return w.blockedModels(TranslationModelStep, now)
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
