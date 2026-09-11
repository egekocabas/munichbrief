package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TranslationLanguageSetting is the durable production route for one reader
// language. The adapter is stored independently from the Ollama model name so
// routing never depends on mutable naming conventions.
type TranslationLanguageSetting struct {
	LanguageCode   string    `json:"language_code"`
	PreferredModel string    `json:"preferred_model"`
	AdapterKey     string    `json:"adapter_key"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// EnsureTranslationLanguageSettings adds newly registered languages without
// changing existing choices. Only the pre-migration translation target
// inherits the former shared model under the structured adapter; genuinely new
// languages remain paused until an operator selects a reviewed route.
func (s *Store) EnsureTranslationLanguageSettings(ctx context.Context, languageCodes []string, legacyLanguageCode string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin translation settings initialization: %w", err)
	}
	defer tx.Rollback()
	var sharedModel string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(preferred_model, '') FROM ai_step_settings WHERE step_key='translation'`).Scan(&sharedModel); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read legacy translation model: %w", err)
	}
	formatted := formatTime(now.UTC())
	legacyLanguageCode = strings.TrimSpace(legacyLanguageCode)
	seen := make(map[string]struct{}, len(languageCodes))
	for _, code := range languageCodes {
		code = strings.TrimSpace(code)
		if code == "" {
			return errors.New("translation language code is required")
		}
		if _, duplicate := seen[code]; duplicate {
			continue
		}
		seen[code] = struct{}{}
		var model any
		if code == legacyLanguageCode && strings.TrimSpace(sharedModel) != "" {
			model = strings.TrimSpace(sharedModel)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO translation_language_settings(language_code,preferred_model,adapter_key,updated_at)
			VALUES (?,?,'structured',?) ON CONFLICT(language_code) DO NOTHING`, code, model, formatted); err != nil {
			return fmt.Errorf("ensure translation setting %s: %w", code, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit translation settings initialization: %w", err)
	}
	return nil
}

func (s *Store) TranslationLanguageSettings(ctx context.Context) ([]TranslationLanguageSetting, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT language_code,COALESCE(preferred_model,''),adapter_key,updated_at FROM translation_language_settings ORDER BY language_code`)
	if err != nil {
		return nil, fmt.Errorf("list translation language settings: %w", err)
	}
	defer rows.Close()
	var settings []TranslationLanguageSetting
	for rows.Next() {
		var setting TranslationLanguageSetting
		var updated string
		if err := rows.Scan(&setting.LanguageCode, &setting.PreferredModel, &setting.AdapterKey, &updated); err != nil {
			return nil, fmt.Errorf("scan translation language setting: %w", err)
		}
		setting.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, fmt.Errorf("parse translation language setting time: %w", err)
		}
		settings = append(settings, setting)
	}
	return settings, rows.Err()
}

func (s *Store) SetTranslationLanguageSetting(ctx context.Context, languageCode, model, adapter string, now time.Time) error {
	languageCode, model, adapter = strings.TrimSpace(languageCode), strings.TrimSpace(model), strings.TrimSpace(adapter)
	if languageCode == "" || model == "" || adapter == "" {
		return errors.New("translation language, model, and adapter are required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE translation_language_settings SET preferred_model=?,adapter_key=?,updated_at=? WHERE language_code=?`, model, adapter, formatTime(now.UTC()), languageCode)
	if err != nil {
		return fmt.Errorf("set translation language setting: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}
