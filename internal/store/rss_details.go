package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/parser"
)

// RSSIncidentOutcome describes the database transition at this check, not today.
type RSSIncidentOutcome struct {
	Position   int
	IncidentID int64
	Status     string
	Number     string
}

type RSSDocumentDetail struct {
	ID, CheckID, DocumentID                   int64
	SourceURL, ExternalID, Title, PublishedAt string
	InWindow, Existed, FromFeed               bool
	FetchReason, FetchStatus, ErrorMessage    string
	Inserted, Updated, Unchanged, Removed     int
	SnapshotID                                int64
	ExtractedText                             string
	Incidents                                 []domain.Incident
	Outcomes                                  []RSSIncidentOutcome
}

type RSSCheckDetails struct {
	ID        int64
	Status    string
	Recorded  bool
	Documents []RSSDocumentDetail
	HasOlder  bool
}

// ObserveRSSDocuments classifies against pre-ingestion state and writes metadata
// in the same transaction. Duplicate URLs produce one observation per check.
func (s *Store) ObserveRSSDocuments(ctx context.Context, checkID int64, documents []domain.SourceDocument, now, start, end time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, d := range documents {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM source_documents WHERE source_url=?`, d.SourceURL).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		existed := err == nil
		inWindow := !d.PublishedAt.Before(start) && d.PublishedAt.Before(end)
		status := "outside_window"
		if inWindow {
			status = "pending"
		}
		// Insert observation first so a duplicate cannot change the original classification.
		_, err = tx.ExecContext(ctx, `INSERT INTO rss_sync_documents(check_id,source_document_id,source_url,external_id,title,published_at,in_window,existed,from_feed,fetch_status)
   VALUES(?,NULLIF(?,0),?,?,?,?,?,?,1,?) ON CONFLICT(check_id,source_url) DO NOTHING`, checkID, id, d.SourceURL, d.ExternalID, d.Title, formatTime(d.PublishedAt.UTC()), boolInt(inWindow), boolInt(existed), status)
		if err != nil {
			return err
		}
		if inWindow {
			if err := upsertSourceMetadata(ctx, tx, []domain.SourceDocument{d}, now); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE rss_sync_documents SET source_document_id=(SELECT id FROM source_documents WHERE source_url=?) WHERE check_id=? AND source_url=?`, d.SourceURL, checkID, d.SourceURL); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// QueueRSSFetches includes retries/refreshes absent from a 304 response.
func (s *Store) QueueRSSFetches(ctx context.Context, checkID int64, documents []SourceDocumentRecord, force bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Finish selection atomically so an interrupted selection never claims a fetch was unnecessary.
	if _, err := tx.ExecContext(ctx, `UPDATE rss_sync_documents SET fetch_status='not_needed' WHERE check_id=? AND fetch_status='pending'`, checkID); err != nil {
		return err
	}
	for _, d := range documents {
		reason := "refresh_due"
		if d.FetchStatus == "error" {
			reason = "retry"
		} else if d.LastFetchedAt == nil {
			reason = "pending_or_metadata_changed"
		} else if force {
			reason = "feed_changed"
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO rss_sync_documents(check_id,source_document_id,source_url,external_id,title,published_at,in_window,existed,from_feed,fetch_reason,fetch_status)
   VALUES(?,?,?,?,?,?,1,1,0,?,'pending') ON CONFLICT(check_id,source_url) DO UPDATE SET fetch_reason=excluded.fetch_reason,fetch_status='pending'`, checkID, d.ID, d.SourceURL, d.ExternalID, d.Title, formatTime(d.PublishedAt.UTC()), reason)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func snapshotRSS(ctx context.Context, tx *sql.Tx, parsed parser.ParsedRelease) (int64, error) {
	payload, err := json.Marshal(parsed.Incidents)
	if err != nil {
		return 0, err
	}
	// JSON encoding avoids delimiter collisions and includes both text and parser output.
	identity, err := json.Marshal([]string{parsed.ExtractedText, string(payload)})
	if err != nil {
		return 0, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(identity))
	if _, err := tx.ExecContext(ctx, `INSERT INTO rss_source_snapshots(content_hash,extracted_text,parsed_json) VALUES(?,?,?) ON CONFLICT(content_hash) DO NOTHING`, hash, parsed.ExtractedText, string(payload)); err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM rss_source_snapshots WHERE content_hash=?`, hash).Scan(&id)
	return id, err
}

func refreshRSSArticleCounts(ctx context.Context, tx *sql.Tx, checkID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE rss_sync_history SET
 fetched=(SELECT COUNT(*) FROM rss_sync_documents WHERE check_id=? AND fetch_status='stored'),
 fetch_failures=(SELECT COUNT(*) FROM rss_sync_documents WHERE check_id=? AND fetch_status='fetch_failed'),
 parser_failures=(SELECT COUNT(*) FROM rss_sync_documents WHERE check_id=? AND fetch_status='parse_failed') WHERE id=?`, checkID, checkID, checkID, checkID)
	return err
}

// StoreRSSFetch commits incident changes, immutable input/output, and outcomes atomically.
func (s *Store) StoreRSSFetch(ctx context.Context, checkID, documentID int64, parsed parser.ParsedRelease, now time.Time) error {
	if len(parsed.Incidents) == 0 {
		return errors.New("at least one incident is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type previous struct {
		id           int64
		hash, number string
	}
	existing := map[int]previous{}
	rows, err := tx.QueryContext(ctx, `SELECT id,position,content_hash,incident_number FROM incidents WHERE source_document_id=? ORDER BY position`, documentID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var pos int
		var p previous
		if err := rows.Scan(&p.id, &pos, &p.hash, &p.number); err != nil {
			rows.Close()
			return err
		}
		existing[pos] = p
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var outcomes []RSSIncidentOutcome
	inserted, updated, unchanged, removed := 0, 0, 0, 0
	for _, i := range parsed.Incidents {
		p, exists := existing[i.Position]
		status := "inserted"
		if !exists {
			inserted++
		} else if p.hash == i.ContentHash {
			status = "unchanged"
			unchanged++
		} else {
			status = "updated"
			updated++
		}
		outcomes = append(outcomes, RSSIncidentOutcome{Position: i.Position, IncidentID: p.id, Status: status, Number: i.Number})
	}
	// Removal follows the existing position-based replacement semantics exactly.
	var removedPositions []int
	for pos := range existing {
		if pos >= len(parsed.Incidents) {
			removedPositions = append(removedPositions, pos)
		}
	}
	slices.Sort(removedPositions)
	for _, pos := range removedPositions {
		p := existing[pos]
		removed++
		outcomes = append(outcomes, RSSIncidentOutcome{Position: pos, IncidentID: p.id, Status: "removed", Number: p.number})
	}
	if err := replaceDocumentIncidents(ctx, tx, documentID, parsed.SourceHash, parsed.Incidents, now); err != nil {
		return err
	}
	for n := range parsed.Incidents {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM incidents WHERE source_document_id=? AND position=?`, documentID, outcomes[n].Position).Scan(&outcomes[n].IncidentID); err != nil {
			return err
		}
	}
	snapshotID, err := snapshotRSS(ctx, tx, parsed)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(outcomes)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE rss_sync_documents SET fetch_status='stored',snapshot_id=?,outcomes_json=?,inserted=?,updated=?,unchanged=?,removed=?,error_message='' WHERE check_id=? AND source_document_id=? AND fetch_status='pending'`, snapshotID, string(payload), inserted, updated, unchanged, removed, checkID, documentID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	if err := refreshRSSArticleCounts(ctx, tx, checkID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FailRSSFetch(ctx context.Context, checkID, documentID int64, status string, parsed parser.ParsedRelease, now time.Time, failure error) error {
	if status != "fetch_failed" && status != "parse_failed" && status != "storage_failed" {
		return errors.New("invalid fetch failure status")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var snapshotID int64
	if parsed.ExtractedText != "" || len(parsed.Incidents) > 0 {
		snapshotID, err = snapshotRSS(ctx, tx, parsed)
		if err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE rss_sync_documents SET fetch_status=?,error_message=?,snapshot_id=NULLIF(?,0) WHERE check_id=? AND source_document_id=? AND fetch_status='pending'`, status, sanitizedError(failure), snapshotID, checkID, documentID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE source_documents SET fetch_status='error',last_fetched_at=?,error_message=? WHERE id=?`, formatTime(now.UTC()), sanitizedError(failure), documentID); err != nil {
		return err
	}
	if err := refreshRSSArticleCounts(ctx, tx, checkID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecordRSSFeedProgress(ctx context.Context, checkID int64, outcome SyncRunResult) error {
	_, err := s.db.ExecContext(ctx, `UPDATE rss_sync_history SET not_modified=?,feed_documents=?,discovered=?,skipped=? WHERE id=? AND status='running'`, boolInt(outcome.NotModified), outcome.FeedDocuments, outcome.Discovered, outcome.Skipped, checkID)
	return err
}

func (s *Store) RSSCheckDetails(ctx context.Context, checkID int64, limit int, after int64) (RSSCheckDetails, error) {
	result := RSSCheckDetails{ID: checkID}
	if limit < 1 || after < 0 {
		return result, errors.New("invalid detail pagination")
	}
	err := s.db.QueryRowContext(ctx, `SELECT status,details_recorded FROM rss_sync_history WHERE id=? AND source_name=?`, checkID, syncSourceName).Scan(&result.Status, &result.Recorded)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,check_id,COALESCE(source_document_id,0),source_url,external_id,title,published_at,in_window,existed,from_feed,fetch_reason,fetch_status,error_message,COALESCE(snapshot_id,0),inserted,updated,unchanged,removed FROM rss_sync_documents WHERE check_id=? AND id>? ORDER BY id LIMIT ?`, checkID, after, limit+1)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var d RSSDocumentDetail
		if err := rows.Scan(&d.ID, &d.CheckID, &d.DocumentID, &d.SourceURL, &d.ExternalID, &d.Title, &d.PublishedAt, &d.InWindow, &d.Existed, &d.FromFeed, &d.FetchReason, &d.FetchStatus, &d.ErrorMessage, &d.SnapshotID, &d.Inserted, &d.Updated, &d.Unchanged, &d.Removed); err != nil {
			return result, err
		}
		result.Documents = append(result.Documents, d)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if len(result.Documents) > limit {
		result.HasOlder = true
		result.Documents = result.Documents[:limit]
	}
	return result, nil
}

func (s *Store) RSSDocumentSnapshot(ctx context.Context, checkID, detailID int64) (RSSDocumentDetail, error) {
	var d RSSDocumentDetail
	var parsed, outcomes string
	err := s.db.QueryRowContext(ctx, `SELECT d.id,d.check_id,COALESCE(d.source_document_id,0),d.title,d.source_url,d.fetch_status,d.error_message,COALESCE(d.snapshot_id,0),COALESCE(s.extracted_text,''),COALESCE(s.parsed_json,'[]'),d.outcomes_json
 FROM rss_sync_documents d JOIN rss_sync_history h ON h.id=d.check_id LEFT JOIN rss_source_snapshots s ON s.id=d.snapshot_id WHERE d.check_id=? AND d.id=? AND h.source_name=?`, checkID, detailID, syncSourceName).Scan(&d.ID, &d.CheckID, &d.DocumentID, &d.Title, &d.SourceURL, &d.FetchStatus, &d.ErrorMessage, &d.SnapshotID, &d.ExtractedText, &parsed, &outcomes)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal([]byte(parsed), &d.Incidents); err != nil {
		return d, err
	}
	if err := json.Unmarshal([]byte(outcomes), &d.Outcomes); err != nil {
		return d, err
	}
	return d, nil
}
