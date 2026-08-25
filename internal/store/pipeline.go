package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const PipelineVersion = "incident-pipeline-v1"

var ErrPipelineUnconfigured = errors.New("AI pipeline models are not configured")

type PipelineStepPlan struct {
	Key           string
	Order         int
	PromptVersion string
	Model         string
}

type StepSetting struct {
	StepKey        string    `json:"step_key"`
	PreferredModel string    `json:"preferred_model"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type PipelineCycle struct {
	ID               int64      `json:"id"`
	Kind             string     `json:"kind"`
	Status           string     `json:"status"`
	ActiveStep       int        `json:"active_step"`
	WindowAuthorized bool       `json:"window_authorized"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

type PipelineJob struct {
	ID                int64
	CycleID           int64
	CycleKind         string
	CycleItemID       int64
	PresentationRunID int64
	IncidentID        int64
	SourceHash        string
	StepKey           string
	StepOrder         int
	ModelIdentity     string
	PromptVersion     string
	InputHash         string
	AttemptCount      int
	OriginalTitle     string
	OriginalBody      string
	TitleDE           string
	SummaryDE         string
}

type PipelineValue struct {
	Kind  string
	Value string
}

type PipelineRequestResult struct {
	CycleID   int64
	Requested int
	Current   int
}

type StepQueueStats struct {
	StepKey                string        `json:"step_key"`
	Queued                 int           `json:"queued"`
	Running                int           `json:"running"`
	Retrying               int           `json:"retrying"`
	NeedsReview            int           `json:"needs_review"`
	Failed                 int           `json:"failed"`
	Succeeded              int           `json:"succeeded"`
	AverageDuration        time.Duration `json:"-"`
	AverageDurationSeconds float64       `json:"average_duration_seconds"`
	LastSuccess            *time.Time    `json:"last_success,omitempty"`
}

type PipelineSnapshot struct {
	ActiveCycle         *PipelineCycle   `json:"active_cycle,omitempty"`
	ActiveStepKey       string           `json:"active_step_key"`
	ActiveModel         string           `json:"active_model"`
	CurrentIncidentID   int64            `json:"current_incident_id,omitempty"`
	CycleCompleted      int              `json:"cycle_completed"`
	CycleTotal          int              `json:"cycle_total"`
	ManualCycles        int              `json:"manual_cycles"`
	ContinuationCycles  int              `json:"continuation_cycles"`
	ScheduledCandidates int              `json:"scheduled_candidates"`
	Steps               []StepQueueStats `json:"steps"`
	RecentEvents        []PipelineEvent  `json:"recent_events"`
}

type PipelineEvent struct {
	At          time.Time `json:"at"`
	CycleID     int64     `json:"cycle_id"`
	IncidentID  int64     `json:"incident_id"`
	StepKey     string    `json:"step_key"`
	Status      string    `json:"status"`
	FailureKind string    `json:"failure_kind,omitempty"`
}

type AdvanceResult struct {
	Advanced  bool
	Completed bool
	Waiting   bool
	RetryAt   *time.Time
}

func validateStepPlans(steps []PipelineStepPlan) error {
	if len(steps) == 0 {
		return errors.New("at least one pipeline step is required")
	}
	seen := make(map[string]bool, len(steps))
	for index, step := range steps {
		if strings.TrimSpace(step.Key) == "" || strings.TrimSpace(step.PromptVersion) == "" || strings.TrimSpace(step.Model) == "" {
			return errors.New("pipeline step key, prompt version, and model are required")
		}
		if step.Order != index || seen[step.Key] {
			return errors.New("pipeline steps must have unique keys and contiguous order")
		}
		seen[step.Key] = true
	}
	return nil
}

func (s *Store) EnsurePipelineSteps(ctx context.Context, stepKeys []string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pipeline settings initialization: %w", err)
	}
	defer tx.Rollback()
	for _, key := range stepKeys {
		if strings.TrimSpace(key) == "" {
			return errors.New("pipeline step key is required")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ai_step_settings(step_key, preferred_model, updated_at)
			VALUES (?, NULL, ?) ON CONFLICT(step_key) DO NOTHING`, key, formatTime(now.UTC())); err != nil {
			return fmt.Errorf("initialize pipeline step %s: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pipeline settings initialization: %w", err)
	}
	return nil
}

func (s *Store) PipelineStepSettings(ctx context.Context) ([]StepSetting, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT step_key, COALESCE(preferred_model, ''), updated_at FROM ai_step_settings ORDER BY step_key`)
	if err != nil {
		return nil, fmt.Errorf("list pipeline step settings: %w", err)
	}
	defer rows.Close()
	var settings []StepSetting
	for rows.Next() {
		var setting StepSetting
		var updated string
		if err := rows.Scan(&setting.StepKey, &setting.PreferredModel, &updated); err != nil {
			return nil, fmt.Errorf("scan pipeline step setting: %w", err)
		}
		setting.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, fmt.Errorf("parse pipeline setting time: %w", err)
		}
		settings = append(settings, setting)
	}
	return settings, rows.Err()
}

func (s *Store) SetPipelineStepModel(ctx context.Context, stepKey, model string, now time.Time) error {
	model = strings.TrimSpace(model)
	if strings.TrimSpace(stepKey) == "" || model == "" {
		return errors.New("pipeline step and model are required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE ai_step_settings SET preferred_model = ?, updated_at = ? WHERE step_key = ?`, model, formatTime(now.UTC()), stepKey)
	if err != nil {
		return fmt.Errorf("set pipeline step model: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) PreferredPipelineModels(ctx context.Context, stepKeys []string) (map[string]string, error) {
	settings, err := s.PipelineStepSettings(ctx)
	if err != nil {
		return nil, err
	}
	models := make(map[string]string, len(settings))
	for _, setting := range settings {
		models[setting.StepKey] = setting.PreferredModel
	}
	for _, key := range stepKeys {
		if strings.TrimSpace(models[key]) == "" {
			return models, fmt.Errorf("%w: %s", ErrPipelineUnconfigured, key)
		}
	}
	return models, nil
}

func (s *Store) CreateManualPipelineCycle(ctx context.Context, sourceMode string, steps []PipelineStepPlan, incidentID *int64, reprocessAll bool, now time.Time) (PipelineRequestResult, error) {
	if err := validateStepPlans(steps); err != nil {
		return PipelineRequestResult{}, err
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
			AND r.pipeline_version = '` + PipelineVersion + `' AND r.status = 'complete'
		)`
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PipelineRequestResult{}, fmt.Errorf("begin manual pipeline cycle: %w", err)
	}
	defer tx.Rollback()
	requestKey, targetCount, err := manualRequestKeyTx(ctx, tx, sourceMode, condition, args, steps)
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
	cycleID, count, err := createPipelineCycleTx(ctx, tx, "manual", sourceMode, true, requestKey, condition, args, steps, now)
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

func manualRequestKeyTx(ctx context.Context, tx *sql.Tx, sourceMode, condition string, args []any, steps []PipelineStepPlan) (string, int, error) {
	hash := sha256.New()
	fmt.Fprintf(hash, "manual\x00%s", sourceMode)
	for _, step := range steps {
		fmt.Fprintf(hash, "\x00%s\x00%d\x00%s\x00%s", step.Key, step.Order, step.PromptVersion, step.Model)
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

func createPipelineCycleTx(ctx context.Context, tx *sql.Tx, kind, sourceMode string, windowAuthorized bool, requestKey, targetCondition string, targetArgs []any, steps []PipelineStepPlan, now time.Time) (int64, int, error) {
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
		runID, _ := run.LastInsertId()
		item, err := tx.ExecContext(ctx, `
			INSERT INTO processing_cycle_items(cycle_id, incident_id, source_hash, presentation_run_id, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'pending', ?, ?)`, cycleID, target.id, target.hash, runID, formatted, formatted)
		if err != nil {
			return 0, 0, fmt.Errorf("create pipeline cycle item: %w", err)
		}
		itemID, _ := item.LastInsertId()
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
	cycle, found, err := selectCycleTx(ctx, tx, `status = 'running'`, nil)
	if err != nil {
		return PipelineCycle{}, false, err
	}
	if found {
		if err := tx.Commit(); err != nil {
			return PipelineCycle{}, false, err
		}
		return cycle, true, nil
	}
	cycle, found, err = selectCycleTx(ctx, tx, `status = 'queued'`, nil)
	if err != nil {
		return PipelineCycle{}, false, err
	}
	if !found && allowScheduled {
		condition, err := sourceStatusCondition(sourceMode)
		if err != nil {
			return PipelineCycle{}, false, err
		}
		condition += ` AND COALESCE(i.body_de, '') <> '' AND NOT EXISTS (
			SELECT 1 FROM presentation_runs r WHERE r.incident_id = i.id AND r.source_hash = i.content_hash
			AND r.pipeline_version = '` + PipelineVersion + `' AND r.status = 'complete'
		)`
		cycleID, count, err := createPipelineCycleTx(ctx, tx, "scheduled", sourceMode, true, "", condition, nil, steps, now)
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

func (s *Store) PipelineStepModel(ctx context.Context, cycleID int64, stepOrder int) (string, string, error) {
	var step, model string
	if err := s.db.QueryRowContext(ctx, `SELECT step_key, model_identity FROM cycle_step_models WHERE cycle_id=? AND step_order=?`, cycleID, stepOrder).Scan(&step, &model); err != nil {
		return "", "", err
	}
	return step, model, nil
}

// InterruptBlockedScheduledCycle closes a blocked scheduled cycle while moving
// every unfinished item, its accepted outputs, and its remaining jobs into a
// continuation cycle. The continuation retains the original window lease.
func (s *Store) InterruptBlockedScheduledCycle(ctx context.Context, cycleID int64, now time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	formatted := formatTime(now.UTC())
	var sourceMode, kind, status string
	var activeStep int
	if err := tx.QueryRowContext(ctx, `SELECT source_mode, kind, status, active_step FROM processing_cycles WHERE id = ?`, cycleID).Scan(&sourceMode, &kind, &status, &activeStep); err != nil {
		return 0, err
	}
	if kind != "scheduled" || status != "running" {
		return 0, errors.New("only a running scheduled cycle may be interrupted")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO processing_cycles(kind, source_mode, status, active_step, window_authorized, created_at, updated_at) VALUES ('continuation', ?, 'queued', ?, 1, ?, ?)`, sourceMode, activeStep, formatted, formatted)
	if err != nil {
		return 0, err
	}
	continuationID, _ := result.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO cycle_step_models(cycle_id, step_key, step_order, model_identity, prompt_version) SELECT ?, step_key, step_order, model_identity, prompt_version FROM cycle_step_models WHERE cycle_id = ?`, continuationID, cycleID); err != nil {
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

func (s *Store) PipelineSnapshot(ctx context.Context, sourceMode string, stepKeys []string, now time.Time) (PipelineSnapshot, error) {
	var snapshot PipelineSnapshot
	var cycle PipelineCycle
	var authorized int
	var started, completed string
	err := s.db.QueryRowContext(ctx, `SELECT id, kind, status, active_step, window_authorized, COALESCE(started_at,''), COALESCE(completed_at,'') FROM processing_cycles WHERE status = 'running' LIMIT 1`).Scan(&cycle.ID, &cycle.Kind, &cycle.Status, &cycle.ActiveStep, &authorized, &started, &completed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return snapshot, err
	}
	if err == nil {
		cycle.WindowAuthorized = authorized == 1
		if started != "" {
			value, _ := time.Parse(time.RFC3339Nano, started)
			cycle.StartedAt = &value
		}
		snapshot.ActiveCycle = &cycle
		_ = s.db.QueryRowContext(ctx, `SELECT step_key, model_identity FROM cycle_step_models WHERE cycle_id = ? AND step_order = ?`, cycle.ID, cycle.ActiveStep).Scan(&snapshot.ActiveStepKey, &snapshot.ActiveModel)
		_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN j.status IN ('succeeded','failed','needs_review','skipped','superseded') THEN 1 ELSE 0 END),0), COUNT(*)
			FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id=j.cycle_item_id WHERE ci.cycle_id = ?`, cycle.ID).Scan(&snapshot.CycleCompleted, &snapshot.CycleTotal)
		_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(ci.incident_id,0) FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id=j.cycle_item_id WHERE ci.cycle_id=? AND j.status='running' LIMIT 1`, cycle.ID).Scan(&snapshot.CurrentIncidentID)
	}
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_cycles WHERE status='queued' AND kind='manual'`).Scan(&snapshot.ManualCycles)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_cycles WHERE status='queued' AND kind='continuation'`).Scan(&snapshot.ContinuationCycles)
	condition, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return snapshot, err
	}
	query := `SELECT COUNT(*) FROM incidents i JOIN source_documents d ON d.id=i.source_document_id WHERE ` + condition + ` AND COALESCE(i.body_de,'')<>'' AND NOT EXISTS (SELECT 1 FROM presentation_runs r WHERE r.incident_id=i.id AND r.source_hash=i.content_hash AND r.pipeline_version=? AND r.status='complete')`
	_ = s.db.QueryRowContext(ctx, query, PipelineVersion).Scan(&snapshot.ScheduledCandidates)
	for _, key := range stepKeys {
		stat := StepQueueStats{StepKey: key}
		var durationSeconds float64
		var durationCount int
		var last string
		err := s.db.QueryRowContext(ctx, `SELECT
			COALESCE(SUM(CASE WHEN status='pending' AND attempt_count=0 THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='running' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='pending' AND attempt_count>0 THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='needs_review' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='succeeded' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN completed_at<>'' AND started_at<>'' THEN (julianday(completed_at)-julianday(started_at))*86400 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN completed_at<>'' AND started_at<>'' THEN 1 ELSE 0 END),0),
			COALESCE(MAX(CASE WHEN status='succeeded' THEN completed_at END),'')
			FROM processing_step_jobs WHERE step_key=?`, key).Scan(&stat.Queued, &stat.Running, &stat.Retrying, &stat.NeedsReview, &stat.Failed, &stat.Succeeded, &durationSeconds, &durationCount, &last)
		if err != nil {
			return snapshot, err
		}
		if durationCount > 0 {
			stat.AverageDuration = time.Duration(durationSeconds / float64(durationCount) * float64(time.Second))
			stat.AverageDurationSeconds = stat.AverageDuration.Seconds()
		}
		if last != "" {
			value, _ := time.Parse(time.RFC3339Nano, last)
			stat.LastSuccess = &value
		}
		snapshot.Steps = append(snapshot.Steps, stat)
	}
	eventRows, err := s.db.QueryContext(ctx, `SELECT j.updated_at, c.id, ci.incident_id, j.step_key, j.status, COALESCE(j.failure_kind,'')
		FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id=j.cycle_item_id
		JOIN processing_cycles c ON c.id=ci.cycle_id WHERE j.status <> 'waiting'
		ORDER BY j.updated_at DESC, j.id DESC LIMIT 12`)
	if err != nil {
		return snapshot, err
	}
	defer eventRows.Close()
	for eventRows.Next() {
		var event PipelineEvent
		var at string
		if err := eventRows.Scan(&at, &event.CycleID, &event.IncidentID, &event.StepKey, &event.Status, &event.FailureKind); err != nil {
			return snapshot, err
		}
		event.At, _ = time.Parse(time.RFC3339Nano, at)
		snapshot.RecentEvents = append(snapshot.RecentEvents, event)
	}
	if err := eventRows.Err(); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func HashPipelineInput(values ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return fmt.Sprintf("%x", hash[:])
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func SortStepSettings(settings []StepSetting, order map[string]int) {
	sort.SliceStable(settings, func(i, j int) bool { return order[settings[i].StepKey] < order[settings[j].StepKey] })
}
