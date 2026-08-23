package processing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

const maxAttempts = 5

type Repository interface {
	RecoverProcessingJobs(context.Context, string, time.Time) error
	QueueAndClaimProcessingJob(context.Context, string, time.Time) (store.ProcessingJob, bool, error)
	CompleteProcessingJob(context.Context, store.ProcessingJob, store.AIPresentation, string, string, time.Time) error
	FailProcessingJob(context.Context, store.ProcessingJob, int, time.Time, time.Time, error) error
	ProcessingQueueDepth(context.Context, string) (int, error)
}

type Observer interface {
	RecordProcessingAttempt()
	RecordProcessingSuccess()
	RecordProcessingFailure()
	SetProcessingQueueDepth(int)
}

type Worker struct {
	repository Repository
	generator  Generator
	observer   Observer
	logger     *slog.Logger
	interval   time.Duration
	clock      func() time.Time
	operation  string
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
		operation:  "incident-presentation/" + PromptVersion + "/" + generator.ModelIdentity(),
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
	for ctx.Err() == nil {
		job, found, err := w.repository.QueueAndClaimProcessingJob(ctx, w.operation, w.clock())
		if err != nil {
			w.logger.Error("claim AI processing job", "error", err)
			return
		}
		w.updateQueueDepth(ctx)
		if !found {
			return
		}
		w.process(ctx, job)
	}
}

func (w *Worker) process(ctx context.Context, job store.ProcessingJob) {
	if w.observer != nil {
		w.observer.RecordProcessingAttempt()
	}
	startedAt := w.clock()
	presentation, modelIdentity, err := w.generator.Generate(ctx, job.TitleDE, job.BodyDE)
	if err != nil {
		if w.observer != nil {
			w.observer.RecordProcessingFailure()
		}
		failedAt := w.clock()
		retryAt := failedAt.Add(retryDelay(job.AttemptCount))
		if recordErr := w.repository.FailProcessingJob(ctx, job, maxAttempts, retryAt, failedAt, err); recordErr != nil {
			w.logger.Error("record AI processing failure", "incident_id", job.IncidentID, "error", recordErr)
			return
		}
		w.logger.Warn("AI processing failed",
			"incident_id", job.IncidentID,
			"attempt", job.AttemptCount,
			"retry_at", retryAt,
			"error", err,
		)
		return
	}
	completedAt := w.clock()
	if err := w.repository.CompleteProcessingJob(ctx, job, presentation, modelIdentity, PromptVersion, completedAt); err != nil {
		if w.observer != nil {
			w.observer.RecordProcessingFailure()
		}
		retryAt := completedAt.Add(retryDelay(job.AttemptCount))
		if recordErr := w.repository.FailProcessingJob(ctx, job, maxAttempts, retryAt, completedAt, err); recordErr != nil {
			w.logger.Error("complete and reschedule AI processing job", "incident_id", job.IncidentID, "error", err, "record_error", recordErr)
			return
		}
		w.logger.Error("complete AI processing job", "incident_id", job.IncidentID, "retry_at", retryAt, "error", err)
		return
	}
	if w.observer != nil {
		w.observer.RecordProcessingSuccess()
	}
	w.logger.Info("AI processing completed",
		"incident_id", job.IncidentID,
		"model", modelIdentity,
		"prompt_version", PromptVersion,
		"duration_ms", completedAt.Sub(startedAt).Milliseconds(),
	)
	w.updateQueueDepth(ctx)
}

func (w *Worker) updateQueueDepth(ctx context.Context) {
	if w.observer == nil {
		return
	}
	depth, err := w.repository.ProcessingQueueDepth(ctx, w.operation)
	if err != nil {
		w.logger.Error("read AI processing queue depth", "error", err)
		return
	}
	w.observer.SetProcessingQueueDepth(depth)
}

func retryDelay(attempt int) time.Duration {
	delays := []time.Duration{15 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute}
	if attempt < 1 {
		return delays[0]
	}
	if attempt > len(delays) {
		return delays[len(delays)-1]
	}
	return delays[attempt-1]
}
