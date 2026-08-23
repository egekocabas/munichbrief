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
	RecoverProcessingJobs(context.Context, string, time.Time) error
	QueueAndClaimProcessingJob(context.Context, string, time.Time) (store.ProcessingJob, bool, error)
	CompleteProcessingJob(context.Context, store.ProcessingJob, store.AIPresentation, string, string, time.Time) error
	FailProcessingJob(context.Context, store.ProcessingJob, string, string, *time.Time, time.Time, error) error
	ProcessingQueueStats(context.Context, string, time.Time) (store.ProcessingStats, error)
}

type Observer interface {
	RecordProcessingAttempt()
	RecordProcessingSuccess()
	RecordProcessingFailure(string)
	SetProcessingStats(store.ProcessingStats)
	SetProcessorAvailable(bool)
}

type Worker struct {
	repository      Repository
	generator       Generator
	observer        Observer
	logger          *slog.Logger
	interval        time.Duration
	clock           func() time.Time
	operation       string
	circuitUntil    time.Time
	circuitFailures int
}

func NewWorker(repository Repository, generator Generator, observer Observer, logger *slog.Logger, interval time.Duration, clock func() time.Time) (*Worker, error) {
	if repository == nil || generator == nil || logger == nil {
		return nil, fmt.Errorf("processing repository, generator, and logger are required")
	}
	if interval <= 0 {
		return nil, fmt.Errorf("processing interval must be positive")
	}
	if clock == nil {
		clock = time.Now
	}
	return &Worker{
		repository: repository,
		generator:  generator,
		observer:   observer,
		logger:     logger,
		interval:   interval,
		clock:      clock,
		operation:  Operation(generator.ModelIdentity()),
	}, nil
}

func (w *Worker) Run(ctx context.Context) {
	if err := w.repository.RecoverProcessingJobs(ctx, w.operation, w.clock()); err != nil {
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
		}
	}
}

func (w *Worker) processAvailable(ctx context.Context) {
	if w.clock().Before(w.circuitUntil) {
		w.updateStats(ctx)
		return
	}
	if w.observer != nil {
		w.observer.SetProcessorAvailable(true)
	}
	for ctx.Err() == nil {
		job, found, err := w.repository.QueueAndClaimProcessingJob(ctx, w.operation, w.clock())
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
	presentation, modelIdentity, err := w.generator.Generate(ctx, job.TitleDE, job.BodyDE)
	if err != nil {
		return w.handleFailure(ctx, job, startedAt, err)
	}
	completedAt := w.clock()
	if err := w.repository.CompleteProcessingJob(ctx, job, presentation, modelIdentity, PromptVersion, completedAt); err != nil {
		return w.handleFailure(ctx, job, startedAt, err)
	}
	w.circuitFailures = 0
	w.circuitUntil = time.Time{}
	if w.observer != nil {
		w.observer.RecordProcessingSuccess()
		w.observer.SetProcessorAvailable(true)
	}
	w.logger.Info("AI processing completed",
		"incident_id", job.IncidentID,
		"model", modelIdentity,
		"prompt_version", PromptVersion,
		"duration_ms", completedAt.Sub(startedAt).Milliseconds(),
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
		"incident_id", job.IncidentID,
		"attempt", job.AttemptCount,
		"failure_kind", kind,
		"status", status,
		"duration_ms", failedAt.Sub(startedAt).Milliseconds(),
	}
	if retryAt != nil {
		attributes = append(attributes, "retry_at", *retryAt)
	}
	w.logger.Warn("AI processing failed", attributes...)
	w.updateStats(ctx)
	return continueQueue
}

func (w *Worker) updateStats(ctx context.Context) {
	if w.observer == nil {
		return
	}
	stats, err := w.repository.ProcessingQueueStats(ctx, w.operation, w.clock())
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
