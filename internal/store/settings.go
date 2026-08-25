package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrSettingsNotInitialized = errors.New("AI settings are not initialized")

func (s *Store) InitializePreferredModel(ctx context.Context, configuredModel string, now time.Time) (string, error) {
	configuredModel = strings.TrimSpace(configuredModel)
	if configuredModel == "" {
		return "", errors.New("configured preferred model is required")
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO ai_settings (id, preferred_model, updated_at)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO NOTHING`, configuredModel, formatTime(now.UTC())); err != nil {
		return "", fmt.Errorf("initialize preferred model: %w", err)
	}
	return s.PreferredModel(ctx)
}

func (s *Store) PreferredModel(ctx context.Context) (string, error) {
	var model string
	if err := s.db.QueryRowContext(ctx, `SELECT preferred_model FROM ai_settings WHERE id = 1`).Scan(&model); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrSettingsNotInitialized
		}
		return "", fmt.Errorf("read preferred model: %w", err)
	}
	return model, nil
}

// SetPreferredModel stores the new automatic-processing default and retargets
// only pending, non-manual work for the current prompt. Running and explicit
// manual jobs keep the model selected when they were requested.
func (s *Store) SetPreferredModel(ctx context.Context, model, operationPrefix, operation string, now time.Time) error {
	model = strings.TrimSpace(model)
	if model == "" || strings.TrimSpace(operationPrefix) == "" || strings.TrimSpace(operation) == "" {
		return errors.New("preferred model, operation prefix, and operation are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin preferred model update: %w", err)
	}
	defer tx.Rollback()

	formattedNow := formatTime(now.UTC())
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM processing_jobs
		WHERE status = 'pending' AND manual_requested_at IS NULL
			AND operation LIKE ? || '%' AND operation <> ?
			AND EXISTS (
				SELECT 1 FROM processing_jobs target
				WHERE target.incident_id = processing_jobs.incident_id
					AND target.source_hash = processing_jobs.source_hash
					AND target.operation = ?
			)`, operationPrefix, operation, operation); err != nil {
		return fmt.Errorf("remove duplicate pending preferred-model jobs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE processing_jobs
		SET operation = ?, model_identity = ?, attempt_count = 0,
			next_retry_at = ?, failure_kind = NULL, error_message = NULL, updated_at = ?
		WHERE status = 'pending' AND manual_requested_at IS NULL
			AND operation LIKE ? || '%' AND operation <> ?`,
		operation, model, formattedNow, formattedNow, operationPrefix, operation); err != nil {
		return fmt.Errorf("retarget pending preferred-model jobs: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE ai_settings SET preferred_model = ?, updated_at = ? WHERE id = 1`, model, formattedNow)
	if err != nil {
		return fmt.Errorf("store preferred model: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrSettingsNotInitialized
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit preferred model update: %w", err)
	}
	return nil
}
