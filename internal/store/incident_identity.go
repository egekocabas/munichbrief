package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/parser"
)

// PreviewDocumentRepair uses exactly the same identity matching as ingestion.
func (s *Store) PreviewDocumentRepair(ctx context.Context, documentID int64, incidents []domain.Incident) ([]RSSIncidentOutcome, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_documents WHERE id=?`, documentID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists != 1 {
		return nil, ErrNotFound
	}
	return documentIncidentChanges(ctx, tx, documentID, incidents)
}

func documentIncidentChanges(ctx context.Context, tx *sql.Tx, documentID int64, incidents []domain.Incident) ([]RSSIncidentOutcome, error) {
	if len(incidents) == 0 {
		return nil, fmt.Errorf("empty incident replacement")
	}
	type previous struct {
		id                        int64
		position                  int
		number, hash, contextHash string
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,position,incident_number,content_hash,context_hash FROM incidents WHERE source_document_id=? ORDER BY position`, documentID)
	if err != nil {
		return nil, err
	}
	var old []previous
	byNumber := map[string]previous{}
	for rows.Next() {
		var p previous
		if err := rows.Scan(&p.id, &p.position, &p.number, &p.hash, &p.contextHash); err != nil {
			rows.Close()
			return nil, err
		}
		if _, exists := byNumber[p.number]; exists {
			rows.Close()
			return nil, fmt.Errorf("ambiguous existing report identity in document %d", documentID)
		}
		old = append(old, p)
		byNumber[p.number] = p
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	positions := map[int]bool{}
	var changes []RSSIncidentOutcome
	for _, incident := range incidents {
		if seen[incident.Number] || (incident.Number == "" && len(incidents) != 1) || incident.Position < 0 || incident.Position >= len(incidents) || positions[incident.Position] {
			return nil, fmt.Errorf("ambiguous incoming report identity or position")
		}
		seen[incident.Number] = true
		positions[incident.Position] = true
		p, exists := byNumber[incident.Number]
		status := "inserted"
		if exists {
			status = "updated"
			if p.hash == incident.ContentHash && p.contextHash == incident.ContextHash {
				status = "unchanged"
			}
		}
		changes = append(changes, RSSIncidentOutcome{IncidentID: p.id, Position: incident.Position, Number: incident.Number, Status: status})
	}
	// A transition between a standalone release and a numbered bundle has no
	// trustworthy automatic identity mapping.
	if len(old) > 0 && ((old[0].number == "") != (incidents[0].Number == "")) {
		return nil, fmt.Errorf("standalone/report identity changed")
	}
	for _, p := range old {
		if !seen[p.number] {
			changes = append(changes, RSSIncidentOutcome{IncidentID: p.id, Position: p.position, Number: p.number, Status: "removed"})
		}
	}
	return changes, nil
}

// ApplyDocumentRepair records a protected snapshot and performs a compare-and-
// swap source replacement so a stale preview cannot overwrite a newer release.
func (s *Store) ApplyDocumentRepair(ctx context.Context, documentID int64, expected string, parsed parser.ParsedRelease, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT source_hash FROM source_documents WHERE id=?`, documentID).Scan(&current); err != nil {
		return err
	}
	if current != expected {
		return fmt.Errorf("source changed since repair preview")
	}
	if _, err := snapshotRSS(ctx, tx, parsed); err != nil {
		return err
	}
	if err := replaceDocumentIncidents(ctx, tx, documentID, parsed.SourceHash, parsed.Incidents, now); err != nil {
		return err
	}
	return tx.Commit()
}
