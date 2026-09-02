package gazetteer

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct {
	db   *sql.DB
	path string
}

func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("gazetteer database path is required")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, fmt.Errorf("create gazetteer database directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open gazetteer sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, path: path}
	for _, statement := range []string{"PRAGMA foreign_keys = ON", "PRAGMA journal_mode = WAL", "PRAGMA busy_timeout = 5000"} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure gazetteer sqlite: %w", err)
		}
	}
	if err := store.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("invalid gazetteer migration %q", entry.Name())
		}
		var applied int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&applied); err != nil {
			return err
		}
		if applied != 0 {
			continue
		}
		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(contents)); err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, version, formatTime(time.Now()))
		}
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("apply gazetteer migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ready(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) Status(ctx context.Context) (Status, error) {
	var result Status
	var active sql.NullInt64
	var attempt, success, next sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT active_generation_id, last_attempt_at, last_success_at, next_refresh_at FROM gazetteer_state WHERE singleton = 1`).Scan(&active, &attempt, &success, &next)
	if err != nil {
		return result, err
	}
	result.ActiveGeneration = active.Int64
	result.LastAttempt = parseTime(attempt.String)
	result.LastSuccess = parseTime(success.String)
	result.NextRefresh = parseTime(next.String)
	if active.Valid {
		err = s.db.QueryRowContext(ctx, `SELECT entry_count FROM gazetteer_generations WHERE id = ?`, active.Int64).Scan(&result.EntryCount)
	}
	return result, err
}

func (s *Store) ActiveEntries(ctx context.Context) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.value, n.kind, n.priority, n.requires_context
		FROM gazetteer_names n
		JOIN gazetteer_state s ON s.active_generation_id = n.generation_id
		WHERE s.singleton = 1
		ORDER BY n.normalized_value`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.Name, &entry.Kind, &entry.Priority, &entry.RequiresContext); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) SourceValidators(ctx context.Context, key string) (etag, modified, hash, sourceURL, contractVersion string, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT etag, last_modified, content_sha256, source_url, contract_version FROM gazetteer_sources WHERE source_key = ?`, key).Scan(&etag, &modified, &hash, &sourceURL, &contractVersion)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}

func (s *Store) ActiveSourceEntries(ctx context.Context, key string) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT n.value, ns.source_kind, ns.source_priority, ns.requires_context, ns.external_id
		FROM gazetteer_names n
		JOIN gazetteer_state st ON st.active_generation_id = n.generation_id AND st.singleton = 1
		JOIN gazetteer_name_sources ns ON ns.name_id = n.id
		WHERE ns.source_key = ? ORDER BY n.value`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		var entry Entry
		var externalID string
		if err := rows.Scan(&entry.Name, &entry.Kind, &entry.Priority, &entry.RequiresContext, &externalID); err != nil {
			return nil, err
		}
		entry.Sources = []EntrySource{{Key: key, ExternalID: externalID, Kind: entry.Kind, Priority: entry.Priority, RequiresContext: entry.RequiresContext}}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) RecordAttempt(ctx context.Context, at, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE gazetteer_state SET last_attempt_at = ?, next_refresh_at = ? WHERE singleton = 1`, formatTime(at), formatTime(next))
	return err
}

func (s *Store) SetNextRefresh(ctx context.Context, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE gazetteer_state SET next_refresh_at = ? WHERE singleton = 1`, formatTime(next))
	return err
}

func (s *Store) Overrides(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT normalized_value, action FROM gazetteer_overrides`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var name, action string
		if err := rows.Scan(&name, &action); err != nil {
			return nil, err
		}
		result[name] = action
	}
	return result, rows.Err()
}

func (s *Store) RecordSourceFailure(ctx context.Context, definition SourceDefinition, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO gazetteer_sources(source_key, display_name, source_url, license, attribution, last_checked_at, consecutive_failures)
		VALUES (?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT(source_key) DO UPDATE SET
		etag=CASE WHEN gazetteer_sources.source_url=excluded.source_url THEN gazetteer_sources.etag ELSE '' END,
		last_modified=CASE WHEN gazetteer_sources.source_url=excluded.source_url THEN gazetteer_sources.last_modified ELSE '' END,
		content_sha256=CASE WHEN gazetteer_sources.source_url=excluded.source_url THEN gazetteer_sources.content_sha256 ELSE '' END,
		display_name=excluded.display_name, source_url=excluded.source_url,
		license=excluded.license, attribution=excluded.attribution, last_checked_at=excluded.last_checked_at,
		consecutive_failures=gazetteer_sources.consecutive_failures+1`, definition.Key, definition.DisplayName, definition.URL, definition.License, definition.Attribution, formatTime(at))
	return err
}

func (s *Store) Activate(ctx context.Context, snapshots []SourceSnapshot, entries []Entry, aggregateHash string, at, next time.Time) (int64, bool, error) {
	status, err := s.Status(ctx)
	if err != nil {
		return 0, false, err
	}
	if status.ActiveGeneration != 0 {
		var current string
		if err := s.db.QueryRowContext(ctx, `SELECT aggregate_sha256 FROM gazetteer_generations WHERE id = ?`, status.ActiveGeneration).Scan(&current); err != nil {
			return 0, false, err
		}
		if current == aggregateHash {
			if err := s.updateSuccessfulSources(ctx, snapshots, at, next); err != nil {
				return 0, false, err
			}
			return status.ActiveGeneration, false, nil
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO gazetteer_generations(status, aggregate_sha256, created_at, entry_count) VALUES ('candidate', ?, ?, ?)`, aggregateHash, formatTime(at), len(entries))
	if err != nil {
		return 0, false, err
	}
	generationID, err := result.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	for _, snapshot := range snapshots {
		definition := snapshot.Definition
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO gazetteer_sources(source_key, display_name, source_url, license, attribution, etag, last_modified, content_sha256, contract_version, last_checked_at, last_success_at, consecutive_failures)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
			ON CONFLICT(source_key) DO UPDATE SET display_name=excluded.display_name, source_url=excluded.source_url,
			license=excluded.license, attribution=excluded.attribution, etag=excluded.etag, last_modified=excluded.last_modified,
			content_sha256=excluded.content_sha256, contract_version=excluded.contract_version, last_checked_at=excluded.last_checked_at, last_success_at=excluded.last_success_at, consecutive_failures=0`,
			definition.Key, definition.DisplayName, definition.URL, definition.License, definition.Attribution, snapshot.ETag, snapshot.LastModified, snapshot.ContentHash, sourceContractVersion, formatTime(at), formatTime(at)); err != nil {
			return 0, false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gazetteer_generation_sources(generation_id, source_key, content_sha256, etag, last_modified, fetched_at, row_count) VALUES (?, ?, ?, ?, ?, ?, ?)`, generationID, definition.Key, snapshot.ContentHash, snapshot.ETag, snapshot.LastModified, formatTime(snapshot.FetchedAt), len(snapshot.Entries)); err != nil {
			return 0, false, err
		}
	}
	for _, entry := range entries {
		result, err := tx.ExecContext(ctx, `INSERT INTO gazetteer_names(generation_id, value, normalized_value, kind, priority, requires_context) VALUES (?, ?, ?, ?, ?, ?)`, generationID, entry.Name, entry.Name, entry.Kind, entry.Priority, entry.RequiresContext)
		if err != nil {
			return 0, false, err
		}
		nameID, _ := result.LastInsertId()
		for _, source := range entry.Sources {
			priority := source.Priority
			if priority == 0 {
				priority = entry.Priority
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO gazetteer_name_sources(name_id, source_key, external_id, source_kind, source_priority, requires_context) VALUES (?, ?, ?, ?, ?, ?)`, nameID, source.Key, source.ExternalID, source.Kind, priority, source.RequiresContext); err != nil {
				return 0, false, err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gazetteer_generations SET status = 'superseded' WHERE status = 'active'`); err != nil {
		return 0, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gazetteer_generations SET status = 'active', activated_at = ? WHERE id = ?`, formatTime(at), generationID); err != nil {
		return 0, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gazetteer_state SET active_generation_id = ?, last_attempt_at = ?, last_success_at = ?, next_refresh_at = ? WHERE singleton = 1`, generationID, formatTime(at), formatTime(at), formatTime(next)); err != nil {
		return 0, false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gazetteer_generations WHERE id NOT IN (SELECT id FROM gazetteer_generations WHERE status IN ('active','superseded') ORDER BY id DESC LIMIT 2)`); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return generationID, true, nil
}

func (s *Store) updateSuccessfulSources(ctx context.Context, snapshots []SourceSnapshot, at, next time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, snapshot := range snapshots {
		definition := snapshot.Definition
		if _, err := tx.ExecContext(ctx, `INSERT INTO gazetteer_sources(source_key, display_name, source_url, license, attribution, etag, last_modified, content_sha256, contract_version, last_checked_at, last_success_at, consecutive_failures) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0) ON CONFLICT(source_key) DO UPDATE SET display_name=excluded.display_name, source_url=excluded.source_url, license=excluded.license, attribution=excluded.attribution, etag=excluded.etag, last_modified=excluded.last_modified, content_sha256=excluded.content_sha256, contract_version=excluded.contract_version, last_checked_at=excluded.last_checked_at, last_success_at=excluded.last_success_at, consecutive_failures=0`, definition.Key, definition.DisplayName, definition.URL, definition.License, definition.Attribution, snapshot.ETag, snapshot.LastModified, snapshot.ContentHash, sourceContractVersion, formatTime(at), formatTime(at)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gazetteer_state SET last_attempt_at=?, last_success_at=?, next_refresh_at=? WHERE singleton=1`, formatTime(at), formatTime(at), formatTime(next)); err != nil {
		return err
	}
	return tx.Commit()
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}
