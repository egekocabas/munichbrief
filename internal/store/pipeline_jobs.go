package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) PipelineStepModel(ctx context.Context, cycleID int64, stepOrder int) (string, string, error) {
	var step, model string
	if err := s.db.QueryRowContext(ctx, `SELECT step_key, model_identity FROM cycle_step_models WHERE cycle_id=? AND step_order=?`, cycleID, stepOrder).Scan(&step, &model); err != nil {
		return "", "", err
	}
	return step, model, nil
}

func (s *Store) ClaimPipelineJob(ctx context.Context, cycleID int64, stepOrder int, now time.Time) (PipelineJob, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PipelineJob{}, false, err
	}
	defer tx.Rollback()
	formatted := formatTime(now.UTC())
	if _, err := tx.ExecContext(ctx, `
		UPDATE processing_step_jobs SET status = 'superseded', updated_at = ?
		WHERE cycle_item_id IN (
			SELECT ci.id FROM processing_cycle_items ci JOIN incidents i ON i.id = ci.incident_id
			WHERE ci.cycle_id = ? AND ci.source_hash <> i.content_hash
		) AND status IN ('waiting', 'pending')`, formatted, cycleID); err != nil {
		return PipelineJob{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE processing_cycle_items SET status = 'superseded', updated_at = ?
		WHERE cycle_id = ? AND EXISTS (SELECT 1 FROM incidents i WHERE i.id = incident_id AND i.content_hash <> source_hash)`, formatted, cycleID); err != nil {
		return PipelineJob{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE presentation_runs SET status='superseded', completed_at=?
		WHERE status='processing' AND id IN (SELECT presentation_run_id FROM processing_cycle_items WHERE cycle_id=? AND status='superseded')`, formatted, cycleID); err != nil {
		return PipelineJob{}, false, err
	}
	var job PipelineJob
	err = tx.QueryRowContext(ctx, `
		SELECT j.id, c.id, c.kind, ci.id, ci.presentation_run_id, ci.incident_id, ci.source_hash,
			j.step_key, j.step_order, j.model_identity, j.prompt_version, j.input_hash, j.attempt_count,
			i.title_de, i.body_de,
			COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ci.presentation_run_id AND kind = 'title_de'), ''),
			COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ci.presentation_run_id AND kind = 'summary_de'), '')
		FROM processing_step_jobs j
		JOIN processing_cycle_items ci ON ci.id = j.cycle_item_id
		JOIN processing_cycles c ON c.id = ci.cycle_id
		JOIN incidents i ON i.id = ci.incident_id
		JOIN source_documents d ON d.id = i.source_document_id
		WHERE c.id = ? AND c.status = 'running' AND j.step_order = ? AND j.status = 'pending'
			AND (j.next_retry_at IS NULL OR j.next_retry_at <= ?) AND ci.source_hash = i.content_hash
		ORDER BY d.published_at DESC, i.position ASC, j.id ASC LIMIT 1`, cycleID, stepOrder, formatted).Scan(
		&job.ID, &job.CycleID, &job.CycleKind, &job.CycleItemID, &job.PresentationRunID, &job.IncidentID, &job.SourceHash,
		&job.StepKey, &job.StepOrder, &job.ModelIdentity, &job.PromptVersion, &job.InputHash, &job.AttemptCount,
		&job.OriginalTitle, &job.OriginalBody, &job.TitleDE, &job.SummaryDE,
	)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return PipelineJob{}, false, err
		}
		return PipelineJob{}, false, nil
	}
	if err != nil {
		return PipelineJob{}, false, fmt.Errorf("select pipeline job: %w", err)
	}
	job.AttemptCount++
	result, err := tx.ExecContext(ctx, `UPDATE processing_step_jobs SET status = 'running', attempt_count = ?, next_retry_at = NULL, failure_kind = NULL, error_message = NULL, started_at = ?, updated_at = ? WHERE id = ? AND status = 'pending'`, job.AttemptCount, formatted, formatted, job.ID)
	if err != nil {
		return PipelineJob{}, false, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return PipelineJob{}, false, errors.New("pipeline job was claimed concurrently")
	}
	if err := tx.Commit(); err != nil {
		return PipelineJob{}, false, err
	}
	return job, true, nil
}

func (s *Store) CompletePipelineJob(ctx context.Context, job PipelineJob, values []PipelineValue, modelIdentity, inputHash string, completedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	formatted := formatTime(completedAt.UTC())
	for _, value := range values {
		if strings.TrimSpace(value.Kind) == "" || strings.TrimSpace(value.Value) == "" {
			return errors.New("pipeline output kind and value are required")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO presentation_values(presentation_run_id, kind, value, model_identity, prompt_version, generated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(presentation_run_id, kind) DO UPDATE SET value = excluded.value, model_identity = excluded.model_identity,
			prompt_version = excluded.prompt_version, generated_at = excluded.generated_at`, job.PresentationRunID, value.Kind, value.Value, modelIdentity, job.PromptVersion, formatted); err != nil {
			return fmt.Errorf("store pipeline output %s: %w", value.Kind, err)
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE processing_step_jobs SET status = 'succeeded', input_hash = ?, completed_at = ?, updated_at = ? WHERE id = ? AND status = 'running'`, inputHash, formatted, formatted, job.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("pipeline job is no longer running")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) FailPipelineJob(ctx context.Context, job PipelineJob, status, failureKind string, retryAt *time.Time, failedAt time.Time, processingError error) error {
	if status != "pending" && status != "needs_review" && status != "failed" {
		return fmt.Errorf("invalid pipeline failure status %q", status)
	}
	var retry any
	if retryAt != nil {
		retry = formatTime(retryAt.UTC())
	}
	result, err := s.db.ExecContext(ctx, `UPDATE processing_step_jobs SET status = ?, next_retry_at = ?, failure_kind = ?, error_message = ?, completed_at = CASE WHEN ? IN ('failed','needs_review') THEN ? ELSE completed_at END, updated_at = ? WHERE id = ? AND status = 'running'`, status, retry, failureKind, sanitizedError(processingError), status, formatTime(failedAt.UTC()), formatTime(failedAt.UTC()), job.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("pipeline job is no longer running")
	}
	return nil
}
