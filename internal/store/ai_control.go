package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AIControl returns the durable automatic-processing gate. Manual requests do
// not consult this gate.
func (s *Store) AIControl(ctx context.Context) (AIControlState, error) {
	var state AIControlState
	var enabled int
	var updatedAt string
	if err := s.db.QueryRowContext(ctx, `SELECT automatic_processing_enabled,updated_at FROM ai_runtime_control WHERE id=1`).Scan(&enabled, &updatedAt); err != nil {
		return state, fmt.Errorf("read AI runtime control: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return state, fmt.Errorf("parse AI runtime control update time: %w", err)
	}
	state.AutomaticProcessingEnabled = enabled == 1
	state.UpdatedAt = parsed
	return state, nil
}

// SetAutomaticProcessing changes only automatic work eligibility. Existing
// queues and manual work are preserved.
func (s *Store) SetAutomaticProcessing(ctx context.Context, enabled bool, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE ai_runtime_control SET automatic_processing_enabled=?,updated_at=? WHERE id=1`, boolInt(enabled), formatTime(now.UTC()))
	if err != nil {
		return fmt.Errorf("set automatic AI processing: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// SuspendAutomaticCycle releases the single-running-cycle lease at a safe job
// boundary while retaining the frozen cycle and every accepted result.
func (s *Store) SuspendAutomaticCycle(ctx context.Context, cycleID int64, now time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE processing_cycles SET status='queued',updated_at=?
		WHERE id=? AND status='running' AND kind IN ('scheduled','continuation')`, formatTime(now.UTC()), cycleID)
	if err != nil {
		return false, fmt.Errorf("suspend automatic AI cycle: %w", err)
	}
	affected, _ := result.RowsAffected()
	return affected == 1, nil
}

// CancelAllAIWork atomically disables automatic processing and terminalizes
// all unfinished canonical and independent work without deleting audit rows or
// changing completed presentations.
func (s *Store) CancelAllAIWork(ctx context.Context, now time.Time) (PipelineCancellationResult, error) {
	var canceled PipelineCancellationResult
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return canceled, err
	}
	defer tx.Rollback()
	formatted := formatTime(now.UTC())
	if _, err := tx.ExecContext(ctx, `UPDATE ai_runtime_control SET automatic_processing_enabled=0,updated_at=? WHERE id=1`, formatted); err != nil {
		return canceled, fmt.Errorf("disable automatic AI processing: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE processing_step_jobs SET status='superseded',next_retry_at=NULL,
		failure_kind=?,error_message=NULL,completed_at=?,updated_at=?
		WHERE status IN ('waiting','pending','running')`, ProcessingStatusReasonOperatorCanceled, formatted, formatted)
	if err != nil {
		return canceled, fmt.Errorf("cancel canonical AI jobs: %w", err)
	}
	canceled.CanonicalJobs = rowsAffected(result)
	if _, err := tx.ExecContext(ctx, `UPDATE processing_cycle_items SET status='superseded',updated_at=? WHERE status='pending'`, formatted); err != nil {
		return canceled, fmt.Errorf("cancel canonical AI items: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE presentation_runs SET status='superseded',completed_at=? WHERE status='processing'`, formatted); err != nil {
		return canceled, fmt.Errorf("cancel processing presentation runs: %w", err)
	}
	result, err = tx.ExecContext(ctx, `UPDATE processing_cycles SET status='superseded',failure_kind=?,completed_at=?,updated_at=?
		WHERE status IN ('queued','running')`, ProcessingStatusReasonOperatorCanceled, formatted, formatted)
	if err != nil {
		return canceled, fmt.Errorf("cancel canonical AI cycles: %w", err)
	}
	canceled.Cycles = rowsAffected(result)
	result, err = tx.ExecContext(ctx, `UPDATE post_processing_jobs SET status='superseded',status_reason=?,status_detail=NULL,
		next_retry_at=NULL,failure_kind=NULL,error_message=NULL,completed_at=?,updated_at=?
		WHERE status IN ('pending','running')`, ProcessingStatusReasonOperatorCanceled, formatted, formatted)
	if err != nil {
		return canceled, fmt.Errorf("cancel AI post-processing jobs: %w", err)
	}
	canceled.PostProcessingJobs = rowsAffected(result)
	if err := tx.Commit(); err != nil {
		return PipelineCancellationResult{}, err
	}
	return canceled, nil
}

func rowsAffected(result sql.Result) int {
	count, err := result.RowsAffected()
	if err != nil || count < 1 {
		return 0
	}
	return int(count)
}

func automaticProcessingEnabledTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT automatic_processing_enabled FROM ai_runtime_control WHERE id=1`).Scan(&enabled); err != nil {
		return false, err
	}
	return enabled == 1, nil
}
