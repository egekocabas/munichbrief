package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CreateManualPipelineCycle atomically freezes targets, canonical step models,
// and optional independent post-processing plans. An identical queued request
// coalesces into the existing cycle.
func (s *Store) CreateManualPipelineCycle(ctx context.Context, sourceMode string, steps []PipelineStepPlan, postPlans []PostProcessingPlan, incidentID *int64, reprocessAll bool, now time.Time) (PipelineRequestResult, error) {
	if err := validateStepPlans(steps); err != nil {
		return PipelineRequestResult{}, err
	}
	for _, plan := range postPlans {
		if err := validatePostProcessingPlan(plan); err != nil {
			return PipelineRequestResult{}, err
		}
	}
	condition, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return PipelineRequestResult{}, err
	}
	condition += " AND COALESCE(i.body_de, '') <> ''"
	args := []any{}
	if incidentID != nil {
		if *incidentID < 1 {
			return PipelineRequestResult{}, errors.New("incident ID must be positive")
		}
		condition += " AND i.id = ?"
		args = append(args, *incidentID)
	} else if !reprocessAll {
		condition += ` AND NOT EXISTS (
			SELECT 1 FROM presentation_runs r WHERE r.incident_id = i.id AND r.source_hash = i.content_hash
			AND r.status = 'complete'
		)`
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PipelineRequestResult{}, fmt.Errorf("begin manual pipeline cycle: %w", err)
	}
	defer tx.Rollback()
	requestKey, targetCount, err := manualRequestKeyTx(ctx, tx, sourceMode, condition, args, steps, postPlans)
	if err != nil {
		return PipelineRequestResult{}, err
	}
	var existingID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM processing_cycles WHERE kind='manual' AND status='queued' AND request_key=? LIMIT 1`, requestKey).Scan(&existingID); err == nil {
		if err := tx.Commit(); err != nil {
			return PipelineRequestResult{}, err
		}
		return PipelineRequestResult{CycleID: existingID, Current: targetCount}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return PipelineRequestResult{}, err
	}
	cycleID, count, err := createPipelineCycleTx(ctx, tx, "manual", sourceMode, true, requestKey, condition, args, steps, postPlans, now)
	if err != nil {
		return PipelineRequestResult{}, err
	}
	if incidentID != nil && count == 0 {
		return PipelineRequestResult{}, ErrNotFound
	}
	if cycleID != 0 {
		if err := supersedeQueuedAutomaticTargetsTx(ctx, tx, cycleID, now); err != nil {
			return PipelineRequestResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return PipelineRequestResult{}, fmt.Errorf("commit manual pipeline cycle: %w", err)
	}
	return PipelineRequestResult{CycleID: cycleID, Requested: count}, nil
}

// ActivateNextPipelineCycle returns the running cycle, promotes queued manual
// work, or creates scheduled work when the caller authorizes the window.
func (s *Store) ActivateNextPipelineCycle(ctx context.Context, sourceMode string, steps []PipelineStepPlan, allowScheduled bool, now time.Time) (PipelineCycle, bool, error) {
	if allowScheduled {
		if err := validateStepPlans(steps); err != nil {
			return PipelineCycle{}, false, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PipelineCycle{}, false, fmt.Errorf("begin pipeline cycle activation: %w", err)
	}
	defer tx.Rollback()
	automaticEnabled, err := automaticProcessingEnabledTx(ctx, tx)
	if err != nil {
		return PipelineCycle{}, false, fmt.Errorf("read automatic AI processing state: %w", err)
	}
	cycle, found, err := selectCycleTx(ctx, tx, `status = 'running'`, nil)
	if err != nil {
		return PipelineCycle{}, false, err
	}
	if found {
		if automaticEnabled || cycle.Kind == "manual" {
			if err := tx.Commit(); err != nil {
				return PipelineCycle{}, false, err
			}
			return cycle, true, nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE processing_cycles SET status='queued',updated_at=? WHERE id=? AND status='running'`, formatTime(now.UTC()), cycle.ID); err != nil {
			return PipelineCycle{}, false, fmt.Errorf("suspend disabled automatic AI cycle: %w", err)
		}
	}
	queuedCondition := `status = 'queued'`
	if !automaticEnabled {
		queuedCondition += ` AND kind = 'manual'`
	}
	cycle, found, err = selectCycleTx(ctx, tx, queuedCondition, nil)
	if err != nil {
		return PipelineCycle{}, false, err
	}
	if !found && allowScheduled && automaticEnabled {
		condition, err := sourceStatusCondition(sourceMode)
		if err != nil {
			return PipelineCycle{}, false, err
		}
		condition += ` AND COALESCE(i.body_de, '') <> ''
			AND julianday(i.created_at) > julianday((SELECT scheduled_after FROM pipeline_cutovers WHERE pipeline_version = '` + PipelineVersion + `'))
			AND NOT EXISTS (
			SELECT 1 FROM presentation_runs r WHERE r.incident_id = i.id AND r.source_hash = i.content_hash
			AND r.pipeline_version = '` + PipelineVersion + `' AND r.status IN ('complete','failed')
		)`
		cycleID, count, err := createPipelineCycleTx(ctx, tx, "scheduled", sourceMode, true, "", condition, nil, steps, nil, now)
		if err != nil {
			return PipelineCycle{}, false, err
		}
		if count > 0 {
			cycle, found, err = selectCycleTx(ctx, tx, `id = ?`, []any{cycleID})
			if err != nil {
				return PipelineCycle{}, false, err
			}
		}
	}
	if !found {
		if err := tx.Commit(); err != nil {
			return PipelineCycle{}, false, err
		}
		return PipelineCycle{}, false, nil
	}
	formatted := formatTime(now.UTC())
	if _, err := tx.ExecContext(ctx, `UPDATE processing_cycles SET status = 'running', started_at = COALESCE(started_at, ?), updated_at = ? WHERE id = ? AND status = 'queued'`, formatted, formatted, cycle.ID); err != nil {
		return PipelineCycle{}, false, fmt.Errorf("activate pipeline cycle: %w", err)
	}
	cycle.Status = "running"
	started := now.UTC()
	if cycle.StartedAt == nil {
		cycle.StartedAt = &started
	}
	if err := tx.Commit(); err != nil {
		return PipelineCycle{}, false, err
	}
	return cycle, true, nil
}

// RecoverPipeline makes jobs interrupted by process termination claimable again.
func (s *Store) RecoverPipeline(ctx context.Context, now time.Time) error {
	formatted := formatTime(now.UTC())
	_, err := s.db.ExecContext(ctx, `
		UPDATE processing_step_jobs SET status = 'pending', next_retry_at = ?, failure_kind = 'transient',
		error_message = 'worker restarted during processing', updated_at = ? WHERE status = 'running'`, formatted, formatted)
	if err != nil {
		return fmt.Errorf("recover staged processing jobs: %w", err)
	}
	return nil
}

func (s *Store) HasQueuedManualCycle(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_cycles WHERE status = 'queued' AND kind = 'manual'`).Scan(&count); err != nil {
		return false, fmt.Errorf("count queued manual cycles: %w", err)
	}
	return count > 0, nil
}

// InterruptBlockedAutomaticCycle yields automatic work to a queued manual cycle
// without losing unfinished targets. A scheduled cycle freezes its unfinished
// work into a continuation; an existing continuation releases its running lease.
func (s *Store) InterruptBlockedAutomaticCycle(ctx context.Context, cycleID int64, now time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	formatted := formatTime(now.UTC())
	var kind, status string
	var activeStep int
	if err := tx.QueryRowContext(ctx, `SELECT kind, status, active_step FROM processing_cycles WHERE id = ?`, cycleID).Scan(&kind, &status, &activeStep); err != nil {
		return 0, err
	}
	if (kind != "scheduled" && kind != "continuation") || status != "running" {
		return 0, errors.New("only a running automatic cycle may be interrupted")
	}
	if kind == "continuation" {
		result, err := tx.ExecContext(ctx, `UPDATE processing_cycles SET status='queued',updated_at=? WHERE id=? AND status='running' AND kind='continuation'`, formatted, cycleID)
		if err != nil {
			return 0, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return 0, errors.New("continuation cycle was interrupted concurrently")
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return cycleID, nil
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO processing_cycles(kind, source_mode, status, active_step, window_authorized, created_at, updated_at) SELECT 'continuation', source_mode, 'queued', ?, 1, ?, ? FROM processing_cycles WHERE id = ?`, activeStep, formatted, formatted, cycleID)
	if err != nil {
		return 0, err
	}
	continuationID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read continuation cycle ID: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cycle_step_models(cycle_id, step_key, step_order, model_identity, prompt_version) SELECT ?, step_key, step_order, model_identity, prompt_version FROM cycle_step_models WHERE cycle_id = ?`, continuationID, cycleID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cycle_post_processing_plans(cycle_id,processor_key,scope_key,model_identity,prompt_version) SELECT ?,processor_key,scope_key,model_identity,prompt_version FROM cycle_post_processing_plans WHERE cycle_id=?`, continuationID, cycleID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE processing_cycle_items SET cycle_id = ?, updated_at = ? WHERE cycle_id = ? AND status = 'pending'`, continuationID, formatted, cycleID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE processing_cycles SET status = 'failed', failure_kind = 'blocked_for_manual', completed_at = ?, updated_at = ? WHERE id = ?`, formatted, formatted, cycleID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return continuationID, nil
}

// AdvancePipelineCycle moves to the next stage only when every active-stage job
// is terminal, or marks the cycle complete after its last stage.
func (s *Store) AdvancePipelineCycle(ctx context.Context, cycle PipelineCycle, stepCount int, now time.Time) (AdvanceResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdvanceResult{}, err
	}
	defer tx.Rollback()
	formatted := formatTime(now.UTC())
	var ready, running, retrying int
	var nextRetry string
	err = tx.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN j.status = 'pending' AND (j.next_retry_at IS NULL OR j.next_retry_at <= ?) THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN j.status = 'running' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN j.status = 'pending' AND j.next_retry_at > ? THEN 1 ELSE 0 END),0),
		COALESCE(MIN(CASE WHEN j.status = 'pending' AND j.next_retry_at > ? THEN j.next_retry_at END),'')
		FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id = j.cycle_item_id
		WHERE ci.cycle_id = ? AND j.step_order = ?`, formatted, formatted, formatted, cycle.ID, cycle.ActiveStep).Scan(&ready, &running, &retrying, &nextRetry)
	if err != nil {
		return AdvanceResult{}, err
	}
	if ready > 0 || running > 0 || retrying > 0 {
		result := AdvanceResult{Waiting: ready == 0 && running == 0}
		if nextRetry != "" {
			value, err := time.Parse(time.RFC3339Nano, nextRetry)
			if err != nil {
				return AdvanceResult{}, err
			}
			result.RetryAt = &value
		}
		if err := tx.Commit(); err != nil {
			return AdvanceResult{}, err
		}
		return result, nil
	}
	if cycle.ActiveStep+1 < stepCount {
		next := cycle.ActiveStep + 1
		if _, err := tx.ExecContext(ctx, `
			UPDATE processing_step_jobs SET status = CASE WHEN EXISTS (
				SELECT 1 FROM processing_step_jobs previous WHERE previous.cycle_item_id = processing_step_jobs.cycle_item_id
				AND previous.step_order = ? AND previous.status = 'succeeded'
			) THEN 'pending' ELSE 'skipped' END, updated_at = ?
			WHERE step_order = ? AND status = 'waiting' AND cycle_item_id IN (SELECT id FROM processing_cycle_items WHERE cycle_id = ?)`, cycle.ActiveStep, formatted, next, cycle.ID); err != nil {
			return AdvanceResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE processing_cycles SET active_step = ?, updated_at = ? WHERE id = ? AND status = 'running'`, next, formatted, cycle.ID); err != nil {
			return AdvanceResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return AdvanceResult{}, err
		}
		return AdvanceResult{Advanced: true}, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE processing_cycle_items SET status = CASE WHEN EXISTS (
			SELECT 1 FROM processing_step_jobs j WHERE j.cycle_item_id = processing_cycle_items.id
			AND j.step_order = ? AND j.status = 'succeeded'
		) THEN 'succeeded' WHEN status <> 'superseded' THEN 'failed' ELSE status END, updated_at = ? WHERE cycle_id = ?`, stepCount-1, formatted, cycle.ID); err != nil {
		return AdvanceResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE presentation_runs SET status = CASE WHEN id IN (
			SELECT presentation_run_id FROM processing_cycle_items WHERE cycle_id = ? AND status = 'succeeded'
		) THEN 'complete' WHEN status <> 'superseded' THEN 'failed' ELSE status END, completed_at = ?
			WHERE id IN (SELECT presentation_run_id FROM processing_cycle_items WHERE cycle_id = ?)`, cycle.ID, formatted, cycle.ID); err != nil {
		return AdvanceResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE processing_cycles SET
		status = CASE WHEN EXISTS (SELECT 1 FROM processing_cycle_items WHERE cycle_id = ? AND status = 'failed') THEN 'failed' ELSE 'succeeded' END,
		failure_kind = CASE WHEN EXISTS (SELECT 1 FROM processing_cycle_items WHERE cycle_id = ? AND status = 'failed') THEN 'partial_failure' ELSE NULL END,
		completed_at = ?, updated_at = ? WHERE id = ? AND status = 'running'`, cycle.ID, cycle.ID, formatted, formatted, cycle.ID); err != nil {
		return AdvanceResult{}, err
	}
	if cycle.Kind == "manual" {
		if err := supersedeCompletedManualTargetsTx(ctx, tx, cycle.ID, formatted); err != nil {
			return AdvanceResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AdvanceResult{}, err
	}
	return AdvanceResult{Completed: true}, nil
}

func manualRequestKeyTx(ctx context.Context, tx *sql.Tx, sourceMode, condition string, args []any, steps []PipelineStepPlan, postPlans []PostProcessingPlan) (string, int, error) {
	hash := sha256.New()
	fmt.Fprintf(hash, "manual\x00%s", sourceMode)
	for _, step := range steps {
		fmt.Fprintf(hash, "\x00%s\x00%d\x00%s\x00%s", step.Key, step.Order, step.PromptVersion, step.Model)
	}
	for _, plan := range postPlans {
		fmt.Fprintf(hash, "\x00post\x00%s\x00%s\x00%s\x00%s", plan.ProcessorKey, plan.ScopeKey, plan.PromptVersion, plan.Model)
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.id, i.content_hash FROM incidents i JOIN source_documents d ON d.id=i.source_document_id WHERE `+condition+` ORDER BY i.id`, args...)
	if err != nil {
		return "", 0, fmt.Errorf("select manual request signature targets: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var sourceHash string
		if err := rows.Scan(&id, &sourceHash); err != nil {
			return "", 0, err
		}
		fmt.Fprintf(hash, "\x00%d\x00%s", id, sourceHash)
		count++
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), count, nil
}

// supersedeQueuedAutomaticTargetsTx prevents an older scheduled snapshot or
// continuation from repeating work that a newly queued priority cycle will
// rerun from the first step. Running cycles are deliberately left untouched.
func supersedeQueuedAutomaticTargetsTx(ctx context.Context, tx *sql.Tx, manualCycleID int64, now time.Time) error {
	formatted := formatTime(now.UTC())
	targets := `SELECT newer.incident_id, newer.source_hash FROM processing_cycle_items newer WHERE newer.cycle_id = ?`
	if _, err := tx.ExecContext(ctx, `
		UPDATE processing_step_jobs SET status = 'superseded', updated_at = ?
		WHERE status IN ('waiting','pending') AND cycle_item_id IN (
			SELECT older.id FROM processing_cycle_items older
			JOIN processing_cycles c ON c.id = older.cycle_id
			WHERE c.status = 'queued' AND c.kind IN ('scheduled','continuation')
			AND (older.incident_id, older.source_hash) IN (`+targets+`)
		)`, formatted, manualCycleID); err != nil {
		return fmt.Errorf("supersede queued automatic jobs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE processing_cycle_items SET status = 'superseded', updated_at = ?
		WHERE cycle_id IN (SELECT id FROM processing_cycles WHERE status = 'queued' AND kind IN ('scheduled','continuation'))
		AND (incident_id, source_hash) IN (`+targets+`)`, formatted, manualCycleID); err != nil {
		return fmt.Errorf("supersede queued automatic items: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE presentation_runs SET status = 'superseded', completed_at = ?
		WHERE id IN (SELECT presentation_run_id FROM processing_cycle_items WHERE status = 'superseded')
		AND status = 'processing'`, formatted); err != nil {
		return fmt.Errorf("supersede queued automatic runs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE processing_cycles SET status = 'superseded', completed_at = ?, updated_at = ?
		WHERE status = 'queued' AND kind IN ('scheduled','continuation') AND NOT EXISTS (
			SELECT 1 FROM processing_cycle_items ci WHERE ci.cycle_id = processing_cycles.id AND ci.status <> 'superseded'
		)`, formatted, formatted); err != nil {
		return fmt.Errorf("close empty automatic cycles: %w", err)
	}
	return nil
}

func createPipelineCycleTx(ctx context.Context, tx *sql.Tx, kind, sourceMode string, windowAuthorized bool, requestKey, targetCondition string, targetArgs []any, steps []PipelineStepPlan, postPlans []PostProcessingPlan, now time.Time) (int64, int, error) {
	formatted := formatTime(now.UTC())
	requested := any(nil)
	if kind == "manual" {
		requested = formatted
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO processing_cycles(kind, source_mode, status, active_step, window_authorized, requested_at, request_key, created_at, updated_at)
		VALUES (?, ?, 'queued', 0, ?, ?, ?, ?, ?)`, kind, sourceMode, boolInt(windowAuthorized), requested, nullableString(requestKey), formatted, formatted)
	if err != nil {
		return 0, 0, fmt.Errorf("create pipeline cycle: %w", err)
	}
	cycleID, err := result.LastInsertId()
	if err != nil {
		return 0, 0, fmt.Errorf("read pipeline cycle ID: %w", err)
	}
	for _, step := range steps {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cycle_step_models(cycle_id, step_key, step_order, model_identity, prompt_version)
			VALUES (?, ?, ?, ?, ?)`, cycleID, step.Key, step.Order, step.Model, step.PromptVersion); err != nil {
			return 0, 0, fmt.Errorf("store cycle step %s: %w", step.Key, err)
		}
	}
	for _, plan := range postPlans {
		if _, err := tx.ExecContext(ctx, `INSERT INTO cycle_post_processing_plans(cycle_id,processor_key,scope_key,model_identity,prompt_version) VALUES(?,?,?,?,?)`, cycleID, plan.ProcessorKey, plan.ScopeKey, plan.Model, plan.PromptVersion); err != nil {
			return 0, 0, fmt.Errorf("store cycle post-processing plan %s/%s: %w", plan.ProcessorKey, plan.ScopeKey, err)
		}
	}
	query := `SELECT i.id, i.content_hash FROM incidents i JOIN source_documents d ON d.id = i.source_document_id WHERE ` + targetCondition + ` ORDER BY d.published_at DESC, i.position ASC`
	rows, err := tx.QueryContext(ctx, query, targetArgs...)
	if err != nil {
		return 0, 0, fmt.Errorf("select pipeline cycle targets: %w", err)
	}
	type target struct {
		id   int64
		hash string
	}
	var targets []target
	for rows.Next() {
		var value target
		if err := rows.Scan(&value.id, &value.hash); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("scan pipeline target: %w", err)
		}
		targets = append(targets, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, fmt.Errorf("iterate pipeline cycle targets: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, 0, err
	}
	for _, target := range targets {
		run, err := tx.ExecContext(ctx, `
			INSERT INTO presentation_runs(incident_id, source_hash, pipeline_version, status, legacy, created_at)
			VALUES (?, ?, ?, 'processing', 0, ?)`, target.id, target.hash, PipelineVersion, formatted)
		if err != nil {
			return 0, 0, fmt.Errorf("create presentation run: %w", err)
		}
		runID, err := run.LastInsertId()
		if err != nil {
			return 0, 0, fmt.Errorf("read presentation run ID: %w", err)
		}
		item, err := tx.ExecContext(ctx, `
			INSERT INTO processing_cycle_items(cycle_id, incident_id, source_hash, presentation_run_id, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'pending', ?, ?)`, cycleID, target.id, target.hash, runID, formatted, formatted)
		if err != nil {
			return 0, 0, fmt.Errorf("create pipeline cycle item: %w", err)
		}
		itemID, err := item.LastInsertId()
		if err != nil {
			return 0, 0, fmt.Errorf("read pipeline cycle item ID: %w", err)
		}
		for _, step := range steps {
			status := "waiting"
			if step.Order == 0 {
				status = "pending"
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO processing_step_jobs(cycle_item_id, step_key, step_order, model_identity, prompt_version, status, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, itemID, step.Key, step.Order, step.Model, step.PromptVersion, status, formatted, formatted); err != nil {
				return 0, 0, fmt.Errorf("create pipeline step job: %w", err)
			}
		}
	}
	if len(targets) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM processing_cycles WHERE id = ?`, cycleID); err != nil {
			return 0, 0, err
		}
		return 0, 0, nil
	}
	return cycleID, len(targets), nil
}

func selectCycleTx(ctx context.Context, tx *sql.Tx, condition string, args []any) (PipelineCycle, bool, error) {
	query := `SELECT id, kind, status, active_step, window_authorized, COALESCE(started_at, ''), COALESCE(completed_at, '')
		FROM processing_cycles WHERE ` + condition + `
		ORDER BY CASE kind WHEN 'manual' THEN 0 WHEN 'continuation' THEN 1 ELSE 2 END,
			COALESCE(requested_at, created_at), id LIMIT 1`
	var cycle PipelineCycle
	var authorized int
	var started, completed string
	err := tx.QueryRowContext(ctx, query, args...).Scan(&cycle.ID, &cycle.Kind, &cycle.Status, &cycle.ActiveStep, &authorized, &started, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return PipelineCycle{}, false, nil
	}
	if err != nil {
		return PipelineCycle{}, false, fmt.Errorf("select pipeline cycle: %w", err)
	}
	cycle.WindowAuthorized = authorized == 1
	if started != "" {
		value, err := time.Parse(time.RFC3339Nano, started)
		if err != nil {
			return PipelineCycle{}, false, err
		}
		cycle.StartedAt = &value
	}
	if completed != "" {
		value, err := time.Parse(time.RFC3339Nano, completed)
		if err != nil {
			return PipelineCycle{}, false, err
		}
		cycle.CompletedAt = &value
	}
	return cycle, true, nil
}

func supersedeCompletedManualTargetsTx(ctx context.Context, tx *sql.Tx, manualCycleID int64, formatted string) error {
	completeTargets := `SELECT ci.incident_id, ci.source_hash FROM processing_cycle_items ci WHERE ci.cycle_id = ? AND ci.status = 'succeeded'`
	if _, err := tx.ExecContext(ctx, `
		UPDATE processing_step_jobs SET status = 'superseded', updated_at = ?
		WHERE status IN ('waiting','pending') AND cycle_item_id IN (
			SELECT ci.id FROM processing_cycle_items ci JOIN processing_cycles c ON c.id = ci.cycle_id
			WHERE c.kind IN ('scheduled','continuation') AND c.status IN ('queued','running')
			AND (ci.incident_id, ci.source_hash) IN (`+completeTargets+`)
		)`, formatted, manualCycleID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE processing_cycle_items SET status = 'superseded', updated_at = ?
		WHERE cycle_id IN (SELECT id FROM processing_cycles WHERE kind IN ('scheduled','continuation') AND status IN ('queued','running'))
		AND status = 'pending' AND (incident_id, source_hash) IN (`+completeTargets+`)`, formatted, manualCycleID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE presentation_runs SET status = 'superseded', completed_at = ? WHERE status = 'processing'
		AND id IN (SELECT presentation_run_id FROM processing_cycle_items WHERE status = 'superseded')`, formatted); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE processing_cycles SET status = 'superseded', completed_at = ?, updated_at = ?
		WHERE kind IN ('scheduled','continuation') AND status IN ('queued','running') AND NOT EXISTS (
			SELECT 1 FROM processing_cycle_items ci WHERE ci.cycle_id = processing_cycles.id AND ci.status = 'pending'
		)`, formatted, formatted); err != nil {
		return err
	}
	return nil
}
