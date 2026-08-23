package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type ProcessingJob struct {
	ID           int64
	IncidentID   int64
	SourceHash   string
	TitleDE      string
	BodyDE       string
	AttemptCount int
}

type AIPresentation struct {
	TitleDE   string `json:"title_de"`
	SummaryDE string `json:"summary_de"`
	TitleEN   string `json:"title_en"`
	SummaryEN string `json:"summary_en"`
}

func (s *Store) RecoverProcessingJobs(ctx context.Context, operation string, recoveredAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE processing_jobs
		SET status = 'pending', next_retry_at = ?, error_message = 'worker restarted during processing', updated_at = ?
		WHERE operation = ? AND status = 'running'`,
		formatTime(recoveredAt.UTC()), formatTime(recoveredAt.UTC()), operation)
	if err != nil {
		return fmt.Errorf("recover processing jobs: %w", err)
	}
	return nil
}

// QueueAndClaimProcessingJob creates jobs for new incident content and claims
// the oldest ready job. The transaction keeps the single-worker claim durable.
func (s *Store) QueueAndClaimProcessingJob(ctx context.Context, operation string, now time.Time) (ProcessingJob, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProcessingJob{}, false, fmt.Errorf("begin processing job claim: %w", err)
	}
	defer tx.Rollback()

	formattedNow := formatTime(now.UTC())
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO processing_jobs (
			incident_id, operation, source_hash, status, attempt_count,
			next_retry_at, created_at, updated_at
		)
		SELECT i.id, ?, i.content_hash, 'pending', 0, ?, ?, ?
		FROM incidents i
		WHERE COALESCE(i.body_de, '') <> ''
			AND NOT EXISTS (
				SELECT 1 FROM processing_jobs j
				WHERE j.incident_id = i.id AND j.operation = ? AND j.source_hash = i.content_hash
			)`, operation, formattedNow, formattedNow, formattedNow, operation); err != nil {
		return ProcessingJob{}, false, fmt.Errorf("queue processing jobs: %w", err)
	}

	var job ProcessingJob
	err = tx.QueryRowContext(ctx, `
		SELECT j.id, j.incident_id, j.source_hash, i.title_de, i.body_de, j.attempt_count
		FROM processing_jobs j
		JOIN incidents i ON i.id = j.incident_id
		WHERE j.operation = ? AND j.status = 'pending'
			AND (j.next_retry_at IS NULL OR j.next_retry_at <= ?)
		ORDER BY j.created_at, j.id
		LIMIT 1`, operation, formattedNow).Scan(
		&job.ID, &job.IncidentID, &job.SourceHash, &job.TitleDE, &job.BodyDE, &job.AttemptCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return ProcessingJob{}, false, fmt.Errorf("commit empty processing claim: %w", err)
		}
		return ProcessingJob{}, false, nil
	}
	if err != nil {
		return ProcessingJob{}, false, fmt.Errorf("select processing job: %w", err)
	}

	job.AttemptCount++
	result, err := tx.ExecContext(ctx, `
		UPDATE processing_jobs
		SET status = 'running', attempt_count = ?, next_retry_at = NULL,
			error_message = NULL, updated_at = ?
		WHERE id = ? AND status = 'pending'`, job.AttemptCount, formattedNow, job.ID)
	if err != nil {
		return ProcessingJob{}, false, fmt.Errorf("claim processing job: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ProcessingJob{}, false, errors.New("processing job was claimed concurrently")
	}
	if err := tx.Commit(); err != nil {
		return ProcessingJob{}, false, fmt.Errorf("commit processing claim: %w", err)
	}
	return job, true, nil
}

func (s *Store) CompleteProcessingJob(
	ctx context.Context,
	job ProcessingJob,
	presentation AIPresentation,
	modelIdentity, promptVersion string,
	generatedAt time.Time,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin processing completion: %w", err)
	}
	defer tx.Rollback()

	formattedTime := formatTime(generatedAt.UTC())
	values := []struct {
		kind  string
		value string
	}{
		{kind: "title_de", value: presentation.TitleDE},
		{kind: "summary_de", value: presentation.SummaryDE},
		{kind: "title_en", value: presentation.TitleEN},
		{kind: "summary_en", value: presentation.SummaryEN},
	}
	for _, derivation := range values {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO derivations (
				incident_id, kind, value, source_hash, model_identity, prompt_version, generated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(incident_id, kind, source_hash, model_identity, prompt_version) DO UPDATE SET
				value = excluded.value,
				generated_at = excluded.generated_at`,
			job.IncidentID, derivation.kind, derivation.value, job.SourceHash,
			modelIdentity, promptVersion, formattedTime,
		); err != nil {
			return fmt.Errorf("store %s derivation: %w", derivation.kind, err)
		}
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE processing_jobs
		SET status = 'succeeded', next_retry_at = NULL, error_message = NULL, updated_at = ?
		WHERE id = ? AND status = 'running'`, formattedTime, job.ID)
	if err != nil {
		return fmt.Errorf("complete processing job: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("processing job is no longer running")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit processing completion: %w", err)
	}
	return nil
}

func (s *Store) FailProcessingJob(ctx context.Context, job ProcessingJob, maxAttempts int, retryAt, failedAt time.Time, processingError error) error {
	status := "pending"
	var nextRetry any = formatTime(retryAt.UTC())
	if job.AttemptCount >= maxAttempts {
		status = "failed"
		nextRetry = nil
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE processing_jobs
		SET status = ?, next_retry_at = ?, error_message = ?, updated_at = ?
		WHERE id = ? AND status = 'running'`,
		status, nextRetry, sanitizedError(processingError), formatTime(failedAt.UTC()), job.ID)
	if err != nil {
		return fmt.Errorf("fail processing job: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("processing job is no longer running")
	}
	return nil
}

func (s *Store) ProcessingQueueDepth(ctx context.Context, operation string) (int, error) {
	var depth int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM processing_jobs
		WHERE operation = ? AND status IN ('pending', 'running')`, operation).Scan(&depth); err != nil {
		return 0, fmt.Errorf("read processing queue depth: %w", err)
	}
	return depth, nil
}
