package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// EnsurePipelineSteps idempotently creates settings rows for registered steps.
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

// SetPipelineStepModel changes the preference used by future cycles. Active and
// queued cycle plans remain frozen.
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

// PreferredPipelineModels returns all requested preferences or fails closed when
// any registered step is unconfigured.
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
