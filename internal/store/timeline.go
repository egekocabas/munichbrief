package store

import (
	"context"
	"errors"
	"fmt"
)

// ListTimelineEntries returns parsed incidents plus one metadata-only entry for
// each source document that has no parsed incidents.
func (s *Store) ListTimelineEntries(ctx context.Context, limit, offset int, sourceMode string) ([]IncidentRecord, int, error) {
	if limit < 1 {
		return nil, 0, errors.New("limit must be positive")
	}
	if offset < 0 {
		return nil, 0, errors.New("offset must not be negative")
	}
	statusCondition := "d.fetch_status = 'fixture'"
	if sourceMode == "live" {
		statusCondition = "d.fetch_status IN ('pending', 'fetched', 'error')"
	} else if sourceMode != "fixture" {
		return nil, 0, errors.New("source mode must be fixture or live")
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM incidents i JOIN source_documents d ON d.id = i.source_document_id WHERE `+statusCondition+`) +
			(SELECT COUNT(*) FROM source_documents d WHERE `+statusCondition+` AND NOT EXISTS (
				SELECT 1 FROM incidents i WHERE i.source_document_id = d.id
			))`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count timeline entries: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT * FROM (
			SELECT
				i.id AS incident_id,
				d.id AS source_document_id,
				1 AS has_incident,
				i.incident_number,
				i.position,
				i.title_de,
				COALESCE(i.body_de, '') AS body_de,
				i.content_hash,
				d.title AS source_title,
				d.source_url,
				d.external_id,
				d.published_at,
				i.updated_at,
				d.fetch_status,
				COALESCE(d.error_message, '') AS error_message,`+incidentAIColumns+`
			FROM incidents i
			JOIN source_documents d ON d.id = i.source_document_id
			WHERE `+statusCondition+`

			UNION ALL

			SELECT
				0 AS incident_id,
				d.id AS source_document_id,
				0 AS has_incident,
				'' AS incident_number,
				0 AS position,
				d.title AS title_de,
				'' AS body_de,
				'' AS content_hash,
				d.title AS source_title,
				d.source_url,
				d.external_id,
				d.published_at,
				d.last_seen_at AS updated_at,
				d.fetch_status,
				COALESCE(d.error_message, '') AS error_message,
				'' AS ai_title_de,
				'' AS ai_summary_de,
				'' AS ai_title_en,
				'' AS ai_summary_en,
				'' AS ai_model,
				'' AS ai_prompt_version,
				'' AS ai_generated_at
			FROM source_documents d
			WHERE `+statusCondition+` AND NOT EXISTS (
				SELECT 1 FROM incidents i WHERE i.source_document_id = d.id
			)
		)
		ORDER BY published_at DESC, position ASC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list timeline entries: %w", err)
	}
	defer rows.Close()

	records := make([]IncidentRecord, 0, min(limit, total))
	for rows.Next() {
		record, err := scanIncident(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan timeline entry: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate timeline entries: %w", err)
	}
	return records, total, nil
}
