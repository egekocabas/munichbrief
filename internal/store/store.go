package store

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

	"github.com/egekocabas/munichbrief/internal/domain"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var ErrNotFound = errors.New("incident not found")

// Store is the application's single SQLite persistence boundary.
type Store struct {
	db   *sql.DB
	path string
}

// IncidentRecord is a query model used by review and presentation code. Public
// visibility is decided by scoped presentation queries, not by this type.
type IncidentRecord struct {
	ID                                  int64
	SourceDocumentID                    int64
	HasIncident                         bool
	Number                              string
	Position                            int
	TitleDE                             string
	BodyDE                              string
	ContentHash                         string
	SourceTitle                         string
	SourceURL                           string
	SourceExternalID                    string
	PublishedAt                         time.Time
	UpdatedAt                           time.Time
	FetchStatus                         string
	ErrorMessage                        string
	AITitleDE                           string
	AISummaryDE                         string
	AITranslatedTitle                   string
	AITranslatedSummary                 string
	AICategory                          string
	AIOriginalCategory                  string
	AIAreaName                          string
	AIAreaType                          string
	AIEventStartDate                    string
	AIEventStartTime                    string
	AIEventDayPart                      string
	AIReportKind                        string
	AIPublicAssistanceStatus            string
	AIPublicAssistanceTypes             string
	AIMetadataModel                     string
	AIMetadataPromptVersion             string
	AIMetadataGeneratedAt               *time.Time
	AICategoryVerificationModel         string
	AICategoryVerificationPromptVersion string
	AICategoryVerificationGeneratedAt   *time.Time
	AIModel                             string
	AIPromptVersion                     string
	AIGeneratedAt                       *time.Time
	AITranslationModel                  string
	AITranslationPromptVersion          string
	AITranslationGeneratedAt            *time.Time
	AITranslationStatus                 string
	AITranslationAttempts               int
	AITranslationNextRetryAt            *time.Time
	AITranslationFailureKind            string
	AITranslationFallback               bool
	AIPipelineVersion                   string
	HasAI                               bool
	ProcessingStatus                    string
	ProcessingAttempts                  int
	ProcessingNextRetryAt               *time.Time
	ProcessingFailureKind               string
}

func (r IncidentRecord) ProcessingState() string {
	if r.HasAI {
		return "ready"
	}
	switch r.ProcessingStatus {
	case "running":
		return "running"
	case "pending":
		if r.ProcessingAttempts > 0 {
			return "retrying"
		}
		return "queued"
	case "needs_review":
		return "needs_review"
	case "failed":
		return "failed"
	default:
		return "not_processed"
	}
}

const incidentAIColumns = `
			COALESCE((SELECT value FROM derivations WHERE incident_id = i.id AND source_hash = i.content_hash AND kind = 'title_de' ORDER BY generated_at DESC LIMIT 1), ''),
			COALESCE((SELECT value FROM derivations WHERE incident_id = i.id AND source_hash = i.content_hash AND kind = 'summary_de' ORDER BY generated_at DESC LIMIT 1), ''),
			COALESCE((SELECT value FROM derivations WHERE incident_id = i.id AND source_hash = i.content_hash AND kind = 'title_en' ORDER BY generated_at DESC LIMIT 1), ''),
			COALESCE((SELECT value FROM derivations WHERE incident_id = i.id AND source_hash = i.content_hash AND kind = 'summary_en' ORDER BY generated_at DESC LIMIT 1), ''),
			COALESCE((SELECT model_identity FROM derivations WHERE incident_id = i.id AND source_hash = i.content_hash AND kind = 'summary_de' ORDER BY generated_at DESC LIMIT 1), ''),
			COALESCE((SELECT prompt_version FROM derivations WHERE incident_id = i.id AND source_hash = i.content_hash AND kind = 'summary_de' ORDER BY generated_at DESC LIMIT 1), ''),
			COALESCE((SELECT generated_at FROM derivations WHERE incident_id = i.id AND source_hash = i.content_hash AND kind = 'summary_de' ORDER BY generated_at DESC LIMIT 1), '')`

// Open creates or opens a SQLite database, applies required pragmas, and runs
// all embedded migrations before returning it to callers.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// One connection keeps SQLite transaction ordering predictable and ensures
	// in-memory databases do not split into independent per-connection stores.
	db.SetMaxOpenConns(1)

	store := &Store{db: db, path: path}
	if err := store.configure(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) configure(ctx context.Context) error {
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure sqlite with %q: %w", statement, err)
		}
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}

	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		versionText, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.Atoi(versionText)
		if err != nil {
			return fmt.Errorf("parse migration version %q: %w", versionText, err)
		}

		var applied int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %d: %w", version, err)
		}
		if applied > 0 {
			continue
		}

		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %d: %w", version, err)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, string(contents)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", version, formatTime(time.Now().UTC())); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// Ready verifies the database connection and the embedded migration state.
func (s *Store) Ready(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite: %w", err)
	}
	var migrations int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&migrations); err != nil {
		return fmt.Errorf("read migration state: %w", err)
	}
	if migrations == 0 {
		return errors.New("database has no applied migrations")
	}
	return nil
}

func (s *Store) UpsertDocuments(ctx context.Context, documents []domain.SourceDocument, observedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin fixture ingestion: %w", err)
	}
	defer tx.Rollback()

	now := formatTime(observedAt.UTC())
	for _, document := range documents {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO source_documents (
				external_id, source_url, title, published_at, discovered_at, last_seen_at,
				feed_fingerprint, fetch_status, source_hash, last_fetched_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, 'fixture', ?, ?)
			ON CONFLICT(source_url) DO UPDATE SET
				external_id = excluded.external_id,
				title = excluded.title,
				published_at = excluded.published_at,
				last_seen_at = excluded.last_seen_at,
				feed_fingerprint = excluded.feed_fingerprint,
				fetch_status = excluded.fetch_status,
				source_hash = excluded.source_hash,
				last_fetched_at = excluded.last_fetched_at,
				error_message = NULL`,
			document.ExternalID,
			document.SourceURL,
			document.Title,
			formatTime(document.PublishedAt.UTC()),
			now,
			now,
			document.FeedFingerprint,
			document.SourceHash,
			now,
		); err != nil {
			return fmt.Errorf("upsert source document %s: %w", document.ExternalID, err)
		}

		var sourceDocumentID int64
		if err := tx.QueryRowContext(ctx, "SELECT id FROM source_documents WHERE source_url = ?", document.SourceURL).Scan(&sourceDocumentID); err != nil {
			return fmt.Errorf("read source document %s: %w", document.ExternalID, err)
		}

		for _, incident := range document.Incidents {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO incidents (
					source_document_id, incident_number, position, title_de, body_de,
					content_hash, created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(source_document_id, position) DO UPDATE SET
					incident_number = excluded.incident_number,
					title_de = excluded.title_de,
					body_de = excluded.body_de,
					content_hash = excluded.content_hash,
					updated_at = excluded.updated_at`,
				sourceDocumentID,
				incident.Number,
				incident.Position,
				incident.TitleDE,
				incident.BodyDE,
				incident.ContentHash,
				now,
				now,
			); err != nil {
				return fmt.Errorf("upsert incident %s/%d: %w", document.ExternalID, incident.Position, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit fixture ingestion: %w", err)
	}
	return nil
}

func (s *Store) ListIncidents(ctx context.Context, limit, offset int) ([]IncidentRecord, int, error) {
	if limit < 1 {
		return nil, 0, errors.New("limit must be positive")
	}
	if offset < 0 {
		return nil, 0, errors.New("offset must not be negative")
	}

	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM incidents").Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count incidents: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			i.id, d.id, 1, i.incident_number, i.position, i.title_de, COALESCE(i.body_de, ''),
			i.content_hash, d.title, d.source_url, d.external_id, d.published_at, i.updated_at,
			d.fetch_status, COALESCE(d.error_message, ''),`+incidentAIColumns+`
		FROM incidents i
		JOIN source_documents d ON d.id = i.source_document_id
		ORDER BY d.published_at DESC, i.position ASC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()

	records := make([]IncidentRecord, 0, min(limit, total))
	for rows.Next() {
		record, err := scanIncident(rows)
		if err != nil {
			return nil, 0, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate incidents: %w", err)
	}
	return records, total, nil
}

func (s *Store) GetIncident(ctx context.Context, id int64) (IncidentRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			i.id, d.id, 1, i.incident_number, i.position, i.title_de, COALESCE(i.body_de, ''),
			i.content_hash, d.title, d.source_url, d.external_id, d.published_at, i.updated_at,
			d.fetch_status, COALESCE(d.error_message, ''),`+incidentAIColumns+`
		FROM incidents i
		JOIN source_documents d ON d.id = i.source_document_id
		WHERE i.id = ?`, id)

	record, err := scanIncident(row)
	if errors.Is(err, sql.ErrNoRows) {
		return IncidentRecord{}, ErrNotFound
	}
	if err != nil {
		return IncidentRecord{}, err
	}
	return record, nil
}

type scanner interface {
	Scan(...any) error
}

func scanIncident(row scanner) (IncidentRecord, error) {
	var record IncidentRecord
	var publishedAt string
	var updatedAt string
	var aiGeneratedAt string
	if err := row.Scan(
		&record.ID,
		&record.SourceDocumentID,
		&record.HasIncident,
		&record.Number,
		&record.Position,
		&record.TitleDE,
		&record.BodyDE,
		&record.ContentHash,
		&record.SourceTitle,
		&record.SourceURL,
		&record.SourceExternalID,
		&publishedAt,
		&updatedAt,
		&record.FetchStatus,
		&record.ErrorMessage,
		&record.AITitleDE,
		&record.AISummaryDE,
		&record.AITranslatedTitle,
		&record.AITranslatedSummary,
		&record.AIModel,
		&record.AIPromptVersion,
		&aiGeneratedAt,
	); err != nil {
		return IncidentRecord{}, err
	}

	var err error
	record.PublishedAt, err = time.Parse(time.RFC3339Nano, publishedAt)
	if err != nil {
		return IncidentRecord{}, fmt.Errorf("parse publication time: %w", err)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return IncidentRecord{}, fmt.Errorf("parse update time: %w", err)
	}
	if aiGeneratedAt != "" {
		generatedAt, err := time.Parse(time.RFC3339Nano, aiGeneratedAt)
		if err != nil {
			return IncidentRecord{}, fmt.Errorf("parse AI generation time: %w", err)
		}
		record.AIGeneratedAt = &generatedAt
	}
	record.HasAI = record.AITitleDE != "" && record.AISummaryDE != "" && record.AITranslatedTitle != "" && record.AITranslatedSummary != ""
	return record, nil
}

func formatTime(value time.Time) string {
	return value.Format(time.RFC3339Nano)
}
