package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
)

const syncSourceName = "munich-police-rss"

type SourceDocumentRecord struct {
	ID              int64
	ExternalID      string
	SourceURL       string
	Title           string
	PublishedAt     time.Time
	FeedFingerprint string
	FetchStatus     string
	SourceHash      string
	LastFetchedAt   *time.Time
	ErrorMessage    string
}

type SyncState struct {
	ETag          string
	LastModified  string
	LastAttemptAt *time.Time
	LastSuccessAt *time.Time
	Result        string
}

// SyncRunResult is the sanitized, structured outcome persisted for one RSS
// synchronization. It deliberately excludes feed and article contents.
type SyncRunResult struct {
	NotModified     bool
	FeedDocuments   int
	Discovered      int
	Fetched         int
	FetchFailures   int
	ParserFailures  int
	Skipped         int
	DurationSeconds float64
	Summary         string
}

// RSSSyncHistoryEntry is one durable RSS synchronization attempt shown in the
// protected operations UI.
type RSSSyncHistoryEntry struct {
	ID              int64
	StartedAt       time.Time
	CompletedAt     *time.Time
	WindowStart     time.Time
	WindowEnd       time.Time
	Status          string
	NotModified     bool
	FeedDocuments   int
	Discovered      int
	Fetched         int
	FetchFailures   int
	ParserFailures  int
	Skipped         int
	DurationSeconds float64
	Summary         string
	ErrorMessage    string
}

// RSSSyncHistoryCursor identifies a stable boundary in reverse-chronological
// RSS synchronization history.
type RSSSyncHistoryCursor struct {
	StartedAt time.Time
	ID        int64
}

// RSSSyncHistoryPage contains one bounded page and navigation availability.
type RSSSyncHistoryPage struct {
	Entries  []RSSSyncHistoryEntry
	HasNewer bool
	HasOlder bool
}

func (s *Store) UpsertSourceMetadata(ctx context.Context, documents []domain.SourceDocument, observedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin source metadata ingestion: %w", err)
	}
	defer tx.Rollback()

	now := formatTime(observedAt.UTC())
	for _, document := range documents {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO source_documents (
				external_id, source_url, title, published_at, discovered_at, last_seen_at,
				feed_fingerprint, fetch_status, source_hash
			) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', '')
			ON CONFLICT(source_url) DO UPDATE SET
				external_id = excluded.external_id,
				title = excluded.title,
				published_at = excluded.published_at,
				last_seen_at = excluded.last_seen_at,
				fetch_status = CASE
					WHEN source_documents.feed_fingerprint <> excluded.feed_fingerprint THEN 'pending'
					ELSE source_documents.fetch_status
				END,
				last_fetched_at = CASE
					WHEN source_documents.feed_fingerprint <> excluded.feed_fingerprint THEN NULL
					ELSE source_documents.last_fetched_at
				END,
				feed_fingerprint = excluded.feed_fingerprint`,
			document.ExternalID,
			document.SourceURL,
			document.Title,
			formatTime(document.PublishedAt.UTC()),
			now,
			now,
			document.FeedFingerprint,
		); err != nil {
			return fmt.Errorf("upsert source metadata %s: %w", document.ExternalID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit source metadata ingestion: %w", err)
	}
	return nil
}

func (s *Store) ListDocumentsForFetch(ctx context.Context, start, end, refreshBefore time.Time, force bool) ([]SourceDocumentRecord, error) {
	condition := `(last_fetched_at IS NULL OR fetch_status = 'error' OR last_fetched_at <= ?)`
	if force {
		condition = "1 = 1"
	}
	query := `
		SELECT
			id, external_id, source_url, title, published_at, feed_fingerprint,
			fetch_status, source_hash, last_fetched_at, COALESCE(error_message, '')
		FROM source_documents
		WHERE fetch_status IN ('pending', 'fetched', 'error')
			AND published_at >= ? AND published_at < ?
			AND ` + condition + `
		ORDER BY published_at DESC`
	arguments := []any{
		formatTime(start.UTC()),
		formatTime(end.UTC()),
	}
	if !force {
		arguments = append(arguments, formatTime(refreshBefore.UTC()))
	}
	rows, err := s.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list documents for fetch: %w", err)
	}
	defer rows.Close()

	var documents []SourceDocumentRecord
	for rows.Next() {
		var document SourceDocumentRecord
		var publishedAt string
		var lastFetchedAt sql.NullString
		if err := rows.Scan(
			&document.ID,
			&document.ExternalID,
			&document.SourceURL,
			&document.Title,
			&publishedAt,
			&document.FeedFingerprint,
			&document.FetchStatus,
			&document.SourceHash,
			&lastFetchedAt,
			&document.ErrorMessage,
		); err != nil {
			return nil, fmt.Errorf("scan document for fetch: %w", err)
		}
		var err error
		document.PublishedAt, err = time.Parse(time.RFC3339Nano, publishedAt)
		if err != nil {
			return nil, fmt.Errorf("parse source publication time: %w", err)
		}
		if lastFetchedAt.Valid {
			parsed, err := time.Parse(time.RFC3339Nano, lastFetchedAt.String)
			if err != nil {
				return nil, fmt.Errorf("parse source fetch time: %w", err)
			}
			document.LastFetchedAt = &parsed
		}
		documents = append(documents, document)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate documents for fetch: %w", err)
	}
	return documents, nil
}

func (s *Store) ReplaceDocumentIncidents(ctx context.Context, documentID int64, sourceHash string, incidents []domain.Incident, fetchedAt time.Time) error {
	if len(incidents) == 0 {
		return errors.New("at least one incident is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin incident replacement: %w", err)
	}
	defer tx.Rollback()

	now := formatTime(fetchedAt.UTC())
	result, err := tx.ExecContext(ctx, `
		UPDATE source_documents
		SET fetch_status = 'fetched', source_hash = ?, last_fetched_at = ?, error_message = NULL
		WHERE id = ?`, sourceHash, now, documentID)
	if err != nil {
		return fmt.Errorf("mark source document fetched: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}

	for _, incident := range incidents {
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
			documentID,
			incident.Number,
			incident.Position,
			incident.TitleDE,
			incident.BodyDE,
			incident.ContentHash,
			now,
			now,
		); err != nil {
			return fmt.Errorf("upsert parsed incident %d/%d: %w", documentID, incident.Position, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM incidents WHERE source_document_id = ? AND position >= ?", documentID, len(incidents)); err != nil {
		return fmt.Errorf("remove stale parsed incidents: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit incident replacement: %w", err)
	}
	return nil
}

func (s *Store) MarkDocumentFetchFailed(ctx context.Context, documentID int64, fetchedAt time.Time, fetchError error) error {
	message := sanitizedError(fetchError)
	result, err := s.db.ExecContext(ctx, `
		UPDATE source_documents
		SET fetch_status = 'error', last_fetched_at = ?, error_message = ?
		WHERE id = ?`, formatTime(fetchedAt.UTC()), message, documentID)
	if err != nil {
		return fmt.Errorf("mark source document failed: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetSyncState(ctx context.Context) (SyncState, error) {
	var state SyncState
	var attempt sql.NullString
	var success sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(etag, ''), COALESCE(last_modified, ''), last_attempt_at, last_success_at, COALESCE(result, '')
		FROM sync_state WHERE source_name = ?`, syncSourceName).Scan(
		&state.ETag,
		&state.LastModified,
		&attempt,
		&success,
		&state.Result,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncState{}, nil
	}
	if err != nil {
		return SyncState{}, fmt.Errorf("get sync state: %w", err)
	}
	var parseErr error
	if state.LastAttemptAt, parseErr = parseOptionalTime(attempt); parseErr != nil {
		return SyncState{}, parseErr
	}
	if state.LastSuccessAt, parseErr = parseOptionalTime(success); parseErr != nil {
		return SyncState{}, parseErr
	}
	return state, nil
}

func (s *Store) RecordSyncAttempt(ctx context.Context, attemptedAt, windowStart, windowEnd time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin sync attempt: %w", err)
	}
	defer tx.Rollback()
	formattedAttempt := formatTime(attemptedAt.UTC())
	if _, err := tx.ExecContext(ctx, `
		UPDATE rss_sync_history
		SET completed_at=?,status='failed',
			duration_seconds=MAX(0,(julianday(?)-julianday(started_at))*86400.0),
			error_message='synchronization interrupted before completion'
		WHERE source_name=? AND status='running'`, formattedAttempt, formattedAttempt, syncSourceName); err != nil {
		return 0, fmt.Errorf("close interrupted sync history: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO sync_state(source_name, last_attempt_at, result)
		VALUES (?, ?, 'running')
		ON CONFLICT(source_name) DO UPDATE SET
			last_attempt_at = excluded.last_attempt_at,
			result = excluded.result`, syncSourceName, formattedAttempt)
	if err != nil {
		return 0, fmt.Errorf("record sync attempt state: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO rss_sync_history(source_name,started_at,window_start,window_end,status)
		VALUES(?,?,?,?,'running')`,
		syncSourceName,
		formattedAttempt,
		formatTime(windowStart.UTC()),
		formatTime(windowEnd.UTC()),
	)
	if err != nil {
		return 0, fmt.Errorf("record sync attempt history: %w", err)
	}
	attemptID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read sync attempt id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit sync attempt: %w", err)
	}
	return attemptID, nil
}

func (s *Store) RecordSyncSuccess(ctx context.Context, attemptID int64, etag, lastModified string, succeededAt time.Time, outcome SyncRunResult) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sync success: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO sync_state(source_name, etag, last_modified, last_attempt_at, last_success_at, result)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_name) DO UPDATE SET
			etag = excluded.etag,
			last_modified = excluded.last_modified,
			last_attempt_at = excluded.last_attempt_at,
			last_success_at = excluded.last_success_at,
			result = excluded.result`,
		syncSourceName,
		etag,
		lastModified,
		formatTime(succeededAt.UTC()),
		formatTime(succeededAt.UTC()),
		outcome.Summary,
	)
	if err != nil {
		return fmt.Errorf("record sync success state: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE rss_sync_history SET completed_at=?,status='succeeded',not_modified=?,
			feed_documents=?,discovered=?,fetched=?,fetch_failures=?,parser_failures=?,skipped=?,
			duration_seconds=?,summary=?,error_message=NULL
		WHERE id=? AND source_name=? AND status='running'`,
		formatTime(succeededAt.UTC()),
		boolInt(outcome.NotModified),
		outcome.FeedDocuments,
		outcome.Discovered,
		outcome.Fetched,
		outcome.FetchFailures,
		outcome.ParserFailures,
		outcome.Skipped,
		outcome.DurationSeconds,
		outcome.Summary,
		attemptID,
		syncSourceName,
	)
	if err != nil {
		return fmt.Errorf("record sync success history: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("record sync success history: attempt %d is not running", attemptID)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sync success: %w", err)
	}
	return nil
}

func (s *Store) RecordSyncFailure(ctx context.Context, attemptID int64, failedAt time.Time, outcome SyncRunResult, syncError error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sync failure: %w", err)
	}
	defer tx.Rollback()
	errorMessage := sanitizedError(syncError)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO sync_state(source_name, last_attempt_at, result)
		VALUES (?, ?, ?)
		ON CONFLICT(source_name) DO UPDATE SET
			last_attempt_at = excluded.last_attempt_at,
			result = excluded.result`, syncSourceName, formatTime(failedAt.UTC()), "error: "+errorMessage)
	if err != nil {
		return fmt.Errorf("record sync failure state: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE rss_sync_history SET completed_at=?,status='failed',not_modified=NULL,
			feed_documents=?,discovered=?,fetched=?,fetch_failures=?,parser_failures=?,skipped=?,
			duration_seconds=?,summary=?,error_message=?
		WHERE id=? AND source_name=? AND status='running'`,
		formatTime(failedAt.UTC()),
		outcome.FeedDocuments,
		outcome.Discovered,
		outcome.Fetched,
		outcome.FetchFailures,
		outcome.ParserFailures,
		outcome.Skipped,
		outcome.DurationSeconds,
		outcome.Summary,
		errorMessage,
		attemptID,
		syncSourceName,
	)
	if err != nil {
		return fmt.Errorf("record sync failure history: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("record sync failure history: attempt %d is not running", attemptID)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sync failure: %w", err)
	}
	return nil
}

// ListRSSSyncHistory returns a stable, reverse-chronological page of RSS
// synchronization attempts for the configured live source.
func (s *Store) ListRSSSyncHistory(ctx context.Context, limit int, before, after *RSSSyncHistoryCursor) (RSSSyncHistoryPage, error) {
	var page RSSSyncHistoryPage
	if limit < 1 {
		return page, errors.New("RSS sync history limit must be positive")
	}
	if before != nil && after != nil {
		return page, errors.New("RSS sync history accepts only one cursor")
	}

	entries, err := s.listRSSSyncHistoryEntries(ctx, limit, before, after)
	if err != nil {
		return page, err
	}
	page.Entries = entries
	if len(entries) == 0 {
		return page, nil
	}

	first := RSSSyncHistoryCursor{StartedAt: entries[0].StartedAt, ID: entries[0].ID}
	last := RSSSyncHistoryCursor{StartedAt: entries[len(entries)-1].StartedAt, ID: entries[len(entries)-1].ID}
	newer, err := s.listRSSSyncHistoryEntries(ctx, 1, nil, &first)
	if err != nil {
		return RSSSyncHistoryPage{}, err
	}
	older, err := s.listRSSSyncHistoryEntries(ctx, 1, &last, nil)
	if err != nil {
		return RSSSyncHistoryPage{}, err
	}
	page.HasNewer = len(newer) > 0
	page.HasOlder = len(older) > 0
	return page, nil
}

func (s *Store) listRSSSyncHistoryEntries(ctx context.Context, limit int, before, after *RSSSyncHistoryCursor) ([]RSSSyncHistoryEntry, error) {
	query := `SELECT id,started_at,completed_at,window_start,window_end,status,not_modified,
		feed_documents,discovered,fetched,fetch_failures,parser_failures,skipped,
		duration_seconds,summary,COALESCE(error_message,'')
		FROM rss_sync_history WHERE source_name=?`
	args := []any{syncSourceName}
	ascending := false
	if before != nil {
		formatted := formatTime(before.StartedAt.UTC())
		query += ` AND (julianday(started_at)<julianday(?) OR (julianday(started_at)=julianday(?) AND id<?))`
		args = append(args, formatted, formatted, before.ID)
	} else if after != nil {
		formatted := formatTime(after.StartedAt.UTC())
		query += ` AND (julianday(started_at)>julianday(?) OR (julianday(started_at)=julianday(?) AND id>?))`
		args = append(args, formatted, formatted, after.ID)
		ascending = true
	}
	if ascending {
		query += ` ORDER BY julianday(started_at),id LIMIT ?`
	} else {
		query += ` ORDER BY julianday(started_at) DESC,id DESC LIMIT ?`
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list RSS sync history: %w", err)
	}
	defer rows.Close()
	entries := make([]RSSSyncHistoryEntry, 0, limit)
	for rows.Next() {
		var entry RSSSyncHistoryEntry
		var startedAt, windowStart, windowEnd string
		var completedAt sql.NullString
		var notModified sql.NullBool
		if err := rows.Scan(
			&entry.ID,
			&startedAt,
			&completedAt,
			&windowStart,
			&windowEnd,
			&entry.Status,
			&notModified,
			&entry.FeedDocuments,
			&entry.Discovered,
			&entry.Fetched,
			&entry.FetchFailures,
			&entry.ParserFailures,
			&entry.Skipped,
			&entry.DurationSeconds,
			&entry.Summary,
			&entry.ErrorMessage,
		); err != nil {
			return nil, fmt.Errorf("scan RSS sync history: %w", err)
		}
		entry.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt)
		if err != nil {
			return nil, fmt.Errorf("parse RSS sync start: %w", err)
		}
		entry.WindowStart, err = time.Parse(time.RFC3339Nano, windowStart)
		if err != nil {
			return nil, fmt.Errorf("parse RSS sync window start: %w", err)
		}
		entry.WindowEnd, err = time.Parse(time.RFC3339Nano, windowEnd)
		if err != nil {
			return nil, fmt.Errorf("parse RSS sync window end: %w", err)
		}
		entry.CompletedAt, err = parseOptionalTime(completedAt)
		if err != nil {
			return nil, fmt.Errorf("parse RSS sync completion: %w", err)
		}
		entry.NotModified = notModified.Valid && notModified.Bool
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate RSS sync history: %w", err)
	}
	if ascending {
		for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
			entries[left], entries[right] = entries[right], entries[left]
		}
	}
	return entries, nil
}

func parseOptionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, fmt.Errorf("parse sync time: %w", err)
	}
	return &parsed, nil
}

func sanitizedError(err error) string {
	if err == nil {
		return "unknown error"
	}
	message := strings.Join(strings.Fields(err.Error()), " ")
	runes := []rune(message)
	if len(runes) > 500 {
		message = string(runes[:500])
	}
	return message
}
