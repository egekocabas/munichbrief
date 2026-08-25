package processing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

const contentMaxAttempts = 3

func Operation(model string) string {
	return "incident-presentation/" + PromptVersion + "/" + model
}

type Repository interface {
	RecoverProcessingJobsForPrompt(context.Context, string, time.Time) error
	QueueAndClaimProcessingJob(context.Context, string, time.Time) (store.ProcessingJob, bool, error)
	ClaimAnyManualProcessingJob(context.Context, string, time.Time) (store.ProcessingJob, bool, error)
	RequestProcessingJobs(context.Context, string, store.PresentationScope, *int64, time.Time) (store.ProcessingRequestResult, error)
	CompleteProcessingJob(context.Context, store.ProcessingJob, store.AIPresentation, string, string, time.Time) error
	FailProcessingJob(context.Context, store.ProcessingJob, string, string, *time.Time, time.Time, error) error
	DeferUnavailableModelJob(context.Context, store.ProcessingJob, time.Time, time.Time) error
	ProcessingQueueStatsForPrompt(context.Context, string, time.Time) (store.ProcessingStats, error)
	PreferredModel(context.Context) (string, error)
	SetPreferredModel(context.Context, string, string, string, time.Time) error
}

type Observer interface {
	RecordProcessingAttempt()
	RecordProcessingSuccess(time.Time)
	RecordProcessingFailure(string)
	RecordProcessingDuration(time.Duration)
	SetProcessingStats(store.ProcessingStats)
	SetProcessorAvailable(bool)
	SetProcessingWindowOpen(bool)
}

type Worker struct {
	repository      Repository
	providers       GeneratorProvider
	catalog         ModelCatalog
	observer        Observer
	logger          *slog.Logger
	interval        time.Duration
	clock           func() time.Time
	schedule        Schedule
	circuitUntil    time.Time
	circuitFailures int
	wake            chan struct{}
}

type Schedule struct {
	Immediate bool
	Location  *time.Location
	Start     time.Duration
	End       time.Duration
}

func (s Schedule) Allows(value time.Time) bool {
	if s.Immediate {
		return true
	}
	local := value.In(s.Location)
	current := time.Duration(local.Hour())*time.Hour + time.Duration(local.Minute())*time.Minute + time.Duration(local.Second())*time.Second
	if s.Start < s.End {
		return current >= s.Start && current < s.End
	}
	return current >= s.Start || current < s.End
}

func NewWorker(repository Repository, providers GeneratorProvider, catalog ModelCatalog, observer Observer, logger *slog.Logger, interval time.Duration, clock func() time.Time, schedule Schedule) (*Worker, error) {
	if repository == nil || providers == nil || catalog == nil || logger == nil {
		return nil, fmt.Errorf("processing repository, generator provider, model catalog, and logger are required")
	}
	if interval <= 0 {
		return nil, fmt.Errorf("processing interval must be positive")
	}
	if clock == nil {
		clock = time.Now
	}
	if !schedule.Immediate && (schedule.Location == nil || schedule.Start < 0 || schedule.Start >= 24*time.Hour || schedule.End < 0 || schedule.End >= 24*time.Hour || schedule.Start == schedule.End) {
		return nil, fmt.Errorf("a valid AI processing schedule is required")
	}
	return &Worker{
		repository: repository,
		providers:  providers,
		catalog:    catalog,
		observer:   observer,
		logger:     logger,
		interval:   interval,
		clock:      clock,
		schedule:   schedule,
		wake:       make(chan struct{}, 1),
	}, nil
}

// RequestNow persists explicit administrator intent and wakes the worker. The
// request returns after durable queueing; generation remains asynchronous.
func (w *Worker) RequestNow(ctx context.Context, sourceMode, model string, incidentID *int64) (store.ProcessingRequestResult, error) {
	snapshot := w.catalog.Snapshot()
	if !snapshot.Available() || !snapshot.Has(model) {
		return store.ProcessingRequestResult{}, fmt.Errorf("%w: %s", ErrModelUnavailable, model)
	}
	result, err := w.repository.RequestProcessingJobs(ctx, sourceMode, store.PresentationScope{
		Operation:     Operation(model),
		ModelIdentity: model,
		PromptVersion: PromptVersion,
	}, incidentID, w.clock())
	if err != nil {
		return store.ProcessingRequestResult{}, err
	}
	if result.Requested > 0 {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
	return result, nil
}

type ModelStatus struct {
	Preferred          string
	Models             []string
	CatalogAvailable   bool
	PreferredAvailable bool
	CatalogError       string
}

func (w *Worker) ModelStatus(ctx context.Context) (ModelStatus, error) {
	preferred, err := w.repository.PreferredModel(ctx)
	if err != nil {
		return ModelStatus{}, err
	}
	snapshot := w.catalog.Snapshot()
	status := ModelStatus{
		Preferred: preferred, Models: append([]string(nil), snapshot.Models...),
		CatalogAvailable: snapshot.Available(), PreferredAvailable: snapshot.Available() && snapshot.Has(preferred),
	}
	if snapshot.Err != nil {
		status.CatalogError = snapshot.Err.Error()
	}
	return status, nil
}

func OperationPrefix() string { return "incident-presentation/" + PromptVersion + "/" }

func (w *Worker) SetPreferredModel(ctx context.Context, model string) error {
	snapshot := w.catalog.Snapshot()
	if !snapshot.Available() || !snapshot.Has(model) {
		return fmt.Errorf("%w: %s", ErrModelUnavailable, model)
	}
	if err := w.repository.SetPreferredModel(ctx, model, OperationPrefix(), Operation(model), w.clock()); err != nil {
		return err
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return nil
}

func (w *Worker) Run(ctx context.Context) {
	if err := w.repository.RecoverProcessingJobsForPrompt(ctx, PromptVersion, w.clock()); err != nil {
		w.logger.Error("recover AI processing jobs", "error", err)
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

func (w *Worker) processAvailable(ctx context.Context) {
	windowOpen := w.schedule.Allows(w.clock())
	if w.observer != nil {
		w.observer.SetProcessingWindowOpen(windowOpen)
	}
	if w.clock().Before(w.circuitUntil) {
		w.updateStats(ctx)
		return
	}
	snapshot := w.catalog.Snapshot()
	if !snapshot.Available() {
		if w.observer != nil {
			w.observer.SetProcessorAvailable(false)
		}
		w.updateStats(ctx)
		return
	}
	if w.observer != nil {
		w.observer.SetProcessorAvailable(true)
	}
	for ctx.Err() == nil {
		job, found, err := w.repository.ClaimAnyManualProcessingJob(ctx, PromptVersion, w.clock())
		if err != nil {
			w.logger.Error("claim manually requested AI processing job", "error", err)
			return
		}
		w.updateStats(ctx)
		if !found {
			break
		}
		if !snapshot.Has(job.ModelIdentity) {
			now := w.clock()
			if err := w.repository.DeferUnavailableModelJob(ctx, job, now.Add(30*time.Second), now); err != nil {
				w.logger.Error("defer unavailable manually requested model", "model", job.ModelIdentity, "error", err)
				return
			}
			continue
		}
		if !w.process(ctx, job) {
			return
		}
	}
	if !windowOpen {
		w.updateStats(ctx)
		return
	}
	preferred, err := w.repository.PreferredModel(ctx)
	if err != nil {
		w.logger.Error("read preferred AI model", "error", err)
		return
	}
	if !snapshot.Has(preferred) {
		if w.observer != nil {
			w.observer.SetProcessorAvailable(false)
		}
		w.updateStats(ctx)
		return
	}
	operation := Operation(preferred)
	for ctx.Err() == nil {
		windowOpen = w.schedule.Allows(w.clock())
		if w.observer != nil {
			w.observer.SetProcessingWindowOpen(windowOpen)
		}
		if !windowOpen {
			w.updateStats(ctx)
			return
		}
		job, found, err := w.repository.QueueAndClaimProcessingJob(ctx, operation, w.clock())
		if err != nil {
			w.logger.Error("claim AI processing job", "error", err)
			return
		}
		w.updateStats(ctx)
		if !found {
			return
		}
		if !w.process(ctx, job) {
			return
		}
	}
}

func (w *Worker) process(ctx context.Context, job store.ProcessingJob) bool {
	if w.observer != nil {
		w.observer.RecordProcessingAttempt()
	}
	startedAt := w.clock()
	if w.observer != nil {
		defer func() { w.observer.RecordProcessingDuration(w.clock().Sub(startedAt)) }()
	}
	w.logger.Info("AI request started",
		"job_id", job.ID,
		"incident_id", job.IncidentID,
		"attempt", job.AttemptCount,
		"model", job.ModelIdentity,
		"prompt_version", PromptVersion,
	)
	generator, err := w.providers.Generator(job.ModelIdentity)
	if err != nil {
		return w.handleFailure(ctx, job, startedAt, errorOf(ErrorConfiguration, "create model generator: %v", err))
	}
	presentation, modelIdentity, err := generator.Generate(ctx, job.TitleDE, job.BodyDE)
	if err != nil {
		return w.handleFailure(ctx, job, startedAt, err)
	}
	responseAt := w.clock()
	responseDuration := responseAt.Sub(startedAt)
	w.logger.Info("AI response received and validated",
		"job_id", job.ID,
		"incident_id", job.IncidentID,
		"attempt", job.AttemptCount,
		"model", modelIdentity,
		"prompt_version", PromptVersion,
		"duration", responseDuration.Round(time.Millisecond).String(),
		"duration_seconds", responseDuration.Seconds(),
	)
	completedAt := w.clock()
	if err := w.repository.CompleteProcessingJob(ctx, job, presentation, modelIdentity, PromptVersion, completedAt); err != nil {
		return w.handleFailure(ctx, job, startedAt, err)
	}
	w.circuitFailures = 0
	w.circuitUntil = time.Time{}
	if w.observer != nil {
		w.observer.RecordProcessingSuccess(completedAt)
		w.observer.SetProcessorAvailable(true)
	}
	totalDuration := completedAt.Sub(startedAt)
	w.logger.Info("AI processing completed and persisted",
		"job_id", job.ID,
		"incident_id", job.IncidentID,
		"attempt", job.AttemptCount,
		"model", modelIdentity,
		"prompt_version", PromptVersion,
		"duration", totalDuration.Round(time.Millisecond).String(),
		"duration_seconds", totalDuration.Seconds(),
	)
	w.updateStats(ctx)
	return true
}

func (w *Worker) handleFailure(ctx context.Context, job store.ProcessingJob, startedAt time.Time, processingError error) bool {
	kind := KindOf(processingError)
	failedAt := w.clock()
	status := "pending"
	var retryAt *time.Time
	continueQueue := true

	switch kind {
	case ErrorOutput, ErrorPrivacy:
		if job.AttemptCount >= contentMaxAttempts {
			status = "needs_review"
		} else {
			next := failedAt.Add(contentRetryDelay(job.AttemptCount))
			retryAt = &next
		}
	default:
		w.circuitFailures++
		delay := transientRetryDelay(job.AttemptCount)
		if kind == ErrorConfiguration {
			delay = configurationRetryDelay(w.circuitFailures)
		}
		delay = jitter(delay, job.ID, job.AttemptCount)
		next := failedAt.Add(delay)
		retryAt = &next
		w.circuitUntil = next
		continueQueue = false
		if w.observer != nil {
			w.observer.SetProcessorAvailable(false)
		}
	}

	if recordErr := w.repository.FailProcessingJob(ctx, job, status, string(kind), retryAt, failedAt, processingError); recordErr != nil {
		w.logger.Error("record AI processing failure", "incident_id", job.IncidentID, "failure_kind", kind, "error", recordErr)
		return false
	}
	if w.observer != nil {
		w.observer.RecordProcessingFailure(string(kind))
	}
	attributes := []any{
		"job_id", job.ID,
		"incident_id", job.IncidentID,
		"attempt", job.AttemptCount,
		"failure_kind", kind,
		"status", status,
		"duration", failedAt.Sub(startedAt).Round(time.Millisecond).String(),
		"duration_seconds", failedAt.Sub(startedAt).Seconds(),
	}
	if retryAt != nil {
		attributes = append(attributes, "retry_at", *retryAt)
	}
	w.logger.Warn("AI request finished with failure", attributes...)
	w.updateStats(ctx)
	return continueQueue
}

func (w *Worker) updateStats(ctx context.Context) {
	if w.observer == nil {
		return
	}
	stats, err := w.repository.ProcessingQueueStatsForPrompt(ctx, PromptVersion, w.clock())
	if err != nil {
		w.logger.Error("read AI processing queue stats", "error", err)
		return
	}
	w.observer.SetProcessingStats(stats)
}

func transientRetryDelay(attempt int) time.Duration {
	delays := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour}
	return delayAt(delays, attempt)
}

func configurationRetryDelay(attempt int) time.Duration {
	delays := []time.Duration{10 * time.Minute, 30 * time.Minute, time.Hour}
	return delayAt(delays, attempt)
}

func contentRetryDelay(attempt int) time.Duration {
	delays := []time.Duration{time.Minute, 10 * time.Minute, time.Hour}
	return delayAt(delays, attempt)
}

func delayAt(delays []time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > len(delays) {
		attempt = len(delays)
	}
	return delays[attempt-1]
}

// jitter deterministically varies endpoint-wide retries by no more than 20%.
func jitter(delay time.Duration, jobID int64, attempt int) time.Duration {
	percentage := ((jobID*31+int64(attempt)*17)%41 - 20)
	return delay + time.Duration(int64(delay)*percentage/100)
}
