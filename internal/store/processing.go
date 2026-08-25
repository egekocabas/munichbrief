package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

type ProcessingStats struct {
	Queued           int
	Running          int
	Retrying         int
	NeedsReview      int
	Failed           int
	OldestPendingAge time.Duration
}

type ProcessingRequestResult struct {
	Requested      int
	AlreadyRunning int
	AlreadyCurrent int
}

type AIPresentation struct {
	TitleDE       string   `json:"title_de"`
	SummaryDE     string   `json:"summary_de"`
	TitleEN       string   `json:"title_en"`
	SummaryEN     string   `json:"summary_en"`
	PrivacyStatus string   `json:"privacy_status"`
	PrivacyFlags  []string `json:"privacy_flags"`
}

func (s *Store) RecoverProcessingJobs(ctx context.Context, operation string, recoveredAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE processing_jobs
		SET status = 'pending', next_retry_at = ?, failure_kind = 'transient',
			error_message = 'worker restarted during processing', updated_at = ?
		WHERE operation = ? AND status = 'running'`,
		formatTime(recoveredAt.UTC()), formatTime(recoveredAt.UTC()), operation)
	if err != nil {
		return fmt.Errorf("recover processing jobs: %w", err)
	}
	return nil
}

// QueueAndClaimProcessingJob creates jobs for all unprocessed incident content
// and claims the newest published ready incident. The transaction keeps the
// single-worker claim durable.
func (s *Store) QueueAndClaimProcessingJob(ctx context.Context, operation string, now time.Time) (ProcessingJob, bool, error) {
	return s.queueAndClaimProcessingJob(ctx, operation, now, true, false)
}

// ClaimManualProcessingJob claims only work explicitly requested by an
// administrator. It does not discover scheduled work, which lets the worker
// safely use it outside the configured processing window.
func (s *Store) ClaimManualProcessingJob(ctx context.Context, operation string, now time.Time) (ProcessingJob, bool, error) {
	return s.queueAndClaimProcessingJob(ctx, operation, now, false, true)
}

func (s *Store) queueAndClaimProcessingJob(ctx context.Context, operation string, now time.Time, discover, manualOnly bool) (ProcessingJob, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProcessingJob{}, false, fmt.Errorf("begin processing job claim: %w", err)
	}
	defer tx.Rollback()

	formattedNow := formatTime(now.UTC())
	if discover {
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
	}

	manualCondition := ""
	if manualOnly {
		manualCondition = " AND j.manual_requested_at IS NOT NULL"
	}
	var job ProcessingJob
	err = tx.QueryRowContext(ctx, `
		SELECT j.id, j.incident_id, j.source_hash, i.title_de, i.body_de, j.attempt_count
		FROM processing_jobs j
		JOIN incidents i ON i.id = j.incident_id
		JOIN source_documents d ON d.id = i.source_document_id
		WHERE j.operation = ? AND j.status = 'pending'
			AND (j.next_retry_at IS NULL OR j.next_retry_at <= ?)
			`+manualCondition+`
		ORDER BY (j.manual_requested_at IS NULL) ASC, j.manual_requested_at ASC,
			d.published_at DESC, i.position ASC, j.id ASC
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
			failure_kind = NULL, error_message = NULL, updated_at = ?
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
	if presentation.PrivacyStatus != "safe" {
		return errors.New("refuse to persist AI presentation without safe privacy status")
	}
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
		SET status = 'succeeded', next_retry_at = NULL, failure_kind = NULL,
			error_message = NULL, manual_requested_at = NULL, updated_at = ?
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

func (s *Store) FailProcessingJob(ctx context.Context, job ProcessingJob, status, failureKind string, retryAt *time.Time, failedAt time.Time, processingError error) error {
	if status != "pending" && status != "failed" && status != "needs_review" {
		return fmt.Errorf("invalid processing failure status %q", status)
	}
	var nextRetry any
	if retryAt != nil {
		nextRetry = formatTime(retryAt.UTC())
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE processing_jobs
		SET status = ?, next_retry_at = ?, failure_kind = ?, error_message = ?,
			manual_requested_at = NULL, updated_at = ?
		WHERE id = ? AND status = 'running'`,
		status, nextRetry, failureKind, sanitizedError(processingError), formatTime(failedAt.UTC()), job.ID)
	if err != nil {
		return fmt.Errorf("fail processing job: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("processing job is no longer running")
	}
	return nil
}

func (s *Store) ProcessingQueueStats(ctx context.Context, operation string, now time.Time) (ProcessingStats, error) {
	var stats ProcessingStats
	var oldest string
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN status = 'pending' AND attempt_count = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'pending' AND attempt_count > 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'needs_review' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0),
			COALESCE(MIN(CASE WHEN status IN ('pending', 'running') THEN created_at END), '')
		FROM processing_jobs WHERE operation = ?`, operation).Scan(
		&stats.Queued, &stats.Running, &stats.Retrying, &stats.NeedsReview, &stats.Failed, &oldest,
	); err != nil {
		return ProcessingStats{}, fmt.Errorf("read processing queue stats: %w", err)
	}
	if oldest != "" {
		oldestAt, err := time.Parse(time.RFC3339Nano, oldest)
		if err != nil {
			return ProcessingStats{}, fmt.Errorf("parse oldest processing job time: %w", err)
		}
		stats.OldestPendingAge = max(now.UTC().Sub(oldestAt), 0)
	}
	return stats, nil
}

func (s *Store) ProcessingQueueDepth(ctx context.Context, operation string) (int, error) {
	stats, err := s.ProcessingQueueStats(ctx, operation, time.Now())
	if err != nil {
		return 0, err
	}
	return stats.Queued + stats.Running + stats.Retrying, nil
}

// RequestProcessingJobs persists immediate manual work for one incident or
// every unprocessed incident in the active source mode. A presentation is
// current only when all four derivations and the successful job match the
// active source hash, model, and prompt.
func (s *Store) RequestProcessingJobs(ctx context.Context, sourceMode string, scope PresentationScope, incidentID *int64, requestedAt time.Time) (ProcessingRequestResult, error) {
	statusCondition, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return ProcessingRequestResult{}, err
	}
	if strings.TrimSpace(scope.Operation) == "" || strings.TrimSpace(scope.ModelIdentity) == "" || strings.TrimSpace(scope.PromptVersion) == "" {
		return ProcessingRequestResult{}, errors.New("processing operation, model, and prompt are required")
	}

	targetCondition := statusCondition + " AND COALESCE(i.body_de, '') <> ''"
	args := presentationArgs(scope)
	if incidentID != nil {
		if *incidentID < 1 {
			return ProcessingRequestResult{}, errors.New("incident ID must be positive")
		}
		targetCondition += " AND i.id = @incident_id"
		args = append(args, sql.Named("incident_id", *incidentID))
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProcessingRequestResult{}, fmt.Errorf("begin manual processing request: %w", err)
	}
	defer tx.Rollback()

	if incidentID != nil {
		var exists int
		query := `SELECT COUNT(*) FROM incidents i JOIN source_documents d ON d.id = i.source_document_id WHERE ` + targetCondition
		if err := tx.QueryRowContext(ctx, query, args...).Scan(&exists); err != nil {
			return ProcessingRequestResult{}, fmt.Errorf("find manual processing incident: %w", err)
		}
		if exists == 0 {
			return ProcessingRequestResult{}, ErrNotFound
		}
	}

	base := ` FROM incidents i JOIN source_documents d ON d.id = i.source_document_id WHERE ` + targetCondition
	var result ProcessingRequestResult
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)`+base+` AND (`+publicReadyCondition+`)`, args...).Scan(&result.AlreadyCurrent); err != nil {
		return ProcessingRequestResult{}, fmt.Errorf("count current manual processing incidents: %w", err)
	}
	runningCondition := `EXISTS (
		SELECT 1 FROM processing_jobs r
		WHERE r.incident_id = i.id AND r.operation = @operation
			AND r.source_hash = i.content_hash AND r.status = 'running'
	)`
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)`+base+` AND NOT (`+publicReadyCondition+`) AND `+runningCondition, args...).Scan(&result.AlreadyRunning); err != nil {
		return ProcessingRequestResult{}, fmt.Errorf("count running manual processing incidents: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)`+base+` AND NOT (`+publicReadyCondition+`) AND NOT `+runningCondition, args...).Scan(&result.Requested); err != nil {
		return ProcessingRequestResult{}, fmt.Errorf("count requested manual processing incidents: %w", err)
	}

	formattedNow := formatTime(requestedAt.UTC())
	mutationArgs := append(append([]any{}, args...), sql.Named("requested_at", formattedNow))
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO processing_jobs (
			incident_id, operation, source_hash, status, attempt_count,
			next_retry_at, created_at, updated_at, manual_requested_at
		)
		SELECT i.id, @operation, i.content_hash, 'pending', 0,
			@requested_at, @requested_at, @requested_at, @requested_at
		`+base+`
			AND NOT (`+publicReadyCondition+`)
			AND NOT `+runningCondition+`
			AND NOT EXISTS (
				SELECT 1 FROM processing_jobs j
				WHERE j.incident_id = i.id AND j.operation = @operation AND j.source_hash = i.content_hash
			)`, mutationArgs...); err != nil {
		return ProcessingRequestResult{}, fmt.Errorf("create manual processing jobs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE processing_jobs
		SET status = 'pending', attempt_count = 0, next_retry_at = @requested_at,
			failure_kind = NULL, error_message = NULL, manual_requested_at = @requested_at,
			updated_at = @requested_at
		WHERE id IN (
			SELECT j.id FROM processing_jobs j
			JOIN incidents i ON i.id = j.incident_id
			JOIN source_documents d ON d.id = i.source_document_id
			WHERE `+targetCondition+`
				AND j.operation = @operation AND j.source_hash = i.content_hash
				AND j.status <> 'running'
				AND NOT (`+publicReadyCondition+`)
		)`, mutationArgs...); err != nil {
		return ProcessingRequestResult{}, fmt.Errorf("reset manual processing jobs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ProcessingRequestResult{}, fmt.Errorf("commit manual processing request: %w", err)
	}
	return result, nil
}
