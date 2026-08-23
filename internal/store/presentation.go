package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type PresentationScope struct {
	Operation     string
	ModelIdentity string
	PromptVersion string
	PublicOnly    bool
}

const scopedAIColumns = `
			COALESCE((SELECT value FROM derivations WHERE incident_id = i.id AND source_hash = i.content_hash AND model_identity = @model AND prompt_version = @prompt AND kind = 'title_de' ORDER BY generated_at DESC LIMIT 1), ''),
			COALESCE((SELECT value FROM derivations WHERE incident_id = i.id AND source_hash = i.content_hash AND model_identity = @model AND prompt_version = @prompt AND kind = 'summary_de' ORDER BY generated_at DESC LIMIT 1), ''),
			COALESCE((SELECT value FROM derivations WHERE incident_id = i.id AND source_hash = i.content_hash AND model_identity = @model AND prompt_version = @prompt AND kind = 'title_en' ORDER BY generated_at DESC LIMIT 1), ''),
			COALESCE((SELECT value FROM derivations WHERE incident_id = i.id AND source_hash = i.content_hash AND model_identity = @model AND prompt_version = @prompt AND kind = 'summary_en' ORDER BY generated_at DESC LIMIT 1), ''),
			COALESCE((SELECT model_identity FROM derivations WHERE incident_id = i.id AND source_hash = i.content_hash AND model_identity = @model AND prompt_version = @prompt AND kind = 'summary_de' ORDER BY generated_at DESC LIMIT 1), ''),
			COALESCE((SELECT prompt_version FROM derivations WHERE incident_id = i.id AND source_hash = i.content_hash AND model_identity = @model AND prompt_version = @prompt AND kind = 'summary_de' ORDER BY generated_at DESC LIMIT 1), ''),
			COALESCE((SELECT generated_at FROM derivations WHERE incident_id = i.id AND source_hash = i.content_hash AND model_identity = @model AND prompt_version = @prompt AND kind = 'summary_de' ORDER BY generated_at DESC LIMIT 1), ''),
			COALESCE((SELECT status FROM processing_jobs WHERE incident_id = i.id AND source_hash = i.content_hash AND operation = @operation ORDER BY id DESC LIMIT 1), ''),
			COALESCE((SELECT attempt_count FROM processing_jobs WHERE incident_id = i.id AND source_hash = i.content_hash AND operation = @operation ORDER BY id DESC LIMIT 1), 0),
			COALESCE((SELECT next_retry_at FROM processing_jobs WHERE incident_id = i.id AND source_hash = i.content_hash AND operation = @operation ORDER BY id DESC LIMIT 1), ''),
			COALESCE((SELECT failure_kind FROM processing_jobs WHERE incident_id = i.id AND source_hash = i.content_hash AND operation = @operation ORDER BY id DESC LIMIT 1), '')`

const publicReadyCondition = `EXISTS (
	SELECT 1 FROM processing_jobs j
	WHERE j.incident_id = i.id AND j.source_hash = i.content_hash
		AND j.operation = @operation AND j.status = 'succeeded'
) AND EXISTS (
	SELECT 1 FROM derivations x
	WHERE x.incident_id = i.id AND x.source_hash = i.content_hash
		AND x.model_identity = @model AND x.prompt_version = @prompt
	GROUP BY x.incident_id
	HAVING COUNT(DISTINCT CASE WHEN x.kind IN ('title_de', 'summary_de', 'title_en', 'summary_en') THEN x.kind END) = 4
)`

func presentationArgs(scope PresentationScope) []any {
	return []any{
		sql.Named("operation", scope.Operation),
		sql.Named("model", scope.ModelIdentity),
		sql.Named("prompt", scope.PromptVersion),
	}
}

func sourceStatusCondition(sourceMode string) (string, error) {
	switch sourceMode {
	case "fixture":
		return "d.fetch_status = 'fixture'", nil
	case "live":
		return "d.fetch_status IN ('pending', 'fetched', 'error')", nil
	default:
		return "", errors.New("source mode must be fixture or live")
	}
}

func (s *Store) ListPresentationEntries(ctx context.Context, limit, offset int, sourceMode string, scope PresentationScope) ([]IncidentRecord, int, error) {
	if limit < 1 {
		return nil, 0, errors.New("limit must be positive")
	}
	if offset < 0 {
		return nil, 0, errors.New("offset must not be negative")
	}
	statusCondition, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return nil, 0, err
	}
	args := presentationArgs(scope)

	countQuery := `SELECT COUNT(*) FROM incidents i JOIN source_documents d ON d.id = i.source_document_id WHERE ` + statusCondition
	if scope.PublicOnly {
		countQuery += " AND " + publicReadyCondition
	} else {
		countQuery = `SELECT
			(SELECT COUNT(*) FROM incidents i JOIN source_documents d ON d.id = i.source_document_id WHERE ` + statusCondition + `) +
			(SELECT COUNT(*) FROM source_documents d WHERE ` + statusCondition + ` AND NOT EXISTS (
				SELECT 1 FROM incidents i WHERE i.source_document_id = d.id
			))`
	}
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count presentation entries: %w", err)
	}

	incidentSelect := `
		SELECT
			i.id, d.id, 1, i.incident_number, i.position, i.title_de, COALESCE(i.body_de, ''),
			i.content_hash, d.title, d.source_url, d.external_id, d.published_at, i.updated_at,
			d.fetch_status, COALESCE(d.error_message, ''),` + scopedAIColumns + `
		FROM incidents i
		JOIN source_documents d ON d.id = i.source_document_id
		WHERE ` + statusCondition
	if scope.PublicOnly {
		incidentSelect += " AND " + publicReadyCondition
	}

	query := incidentSelect + ` ORDER BY d.published_at DESC, i.position ASC LIMIT @limit OFFSET @offset`
	if !scope.PublicOnly {
		query = `SELECT * FROM (` + incidentSelect + `
			UNION ALL
			SELECT
				0, d.id, 0, '', 0, d.title, '', '', d.title, d.source_url, d.external_id,
				d.published_at, d.last_seen_at, d.fetch_status, COALESCE(d.error_message, ''),
				'', '', '', '', '', '', '', '', 0, '', ''
			FROM source_documents d
			WHERE ` + statusCondition + ` AND NOT EXISTS (
				SELECT 1 FROM incidents i WHERE i.source_document_id = d.id
			)
		) ORDER BY published_at DESC, position ASC LIMIT @limit OFFSET @offset`
	}
	args = append(args, sql.Named("limit", limit), sql.Named("offset", offset))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list presentation entries: %w", err)
	}
	defer rows.Close()
	records := make([]IncidentRecord, 0, min(limit, total))
	for rows.Next() {
		record, err := scanPresentationIncident(rows)
		if err != nil {
			return nil, 0, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate presentation entries: %w", err)
	}
	return records, total, nil
}

func (s *Store) GetPresentationIncident(ctx context.Context, id int64, scope PresentationScope) (IncidentRecord, error) {
	query := `
		SELECT
			i.id, d.id, 1, i.incident_number, i.position, i.title_de, COALESCE(i.body_de, ''),
			i.content_hash, d.title, d.source_url, d.external_id, d.published_at, i.updated_at,
			d.fetch_status, COALESCE(d.error_message, ''),` + scopedAIColumns + `
		FROM incidents i JOIN source_documents d ON d.id = i.source_document_id
		WHERE i.id = @id`
	if scope.PublicOnly {
		query += " AND " + publicReadyCondition
	}
	args := append(presentationArgs(scope), sql.Named("id", id))
	record, err := scanPresentationIncident(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return IncidentRecord{}, ErrNotFound
	}
	return record, err
}

func scanPresentationIncident(row scanner) (IncidentRecord, error) {
	var record IncidentRecord
	var publishedAt, updatedAt, aiGeneratedAt, nextRetryAt string
	if err := row.Scan(
		&record.ID, &record.SourceDocumentID, &record.HasIncident, &record.Number, &record.Position,
		&record.TitleDE, &record.BodyDE, &record.ContentHash, &record.SourceTitle, &record.SourceURL,
		&record.SourceExternalID, &publishedAt, &updatedAt, &record.FetchStatus, &record.ErrorMessage,
		&record.AITitleDE, &record.AISummaryDE, &record.AITitleEN, &record.AISummaryEN,
		&record.AIModel, &record.AIPromptVersion, &aiGeneratedAt,
		&record.ProcessingStatus, &record.ProcessingAttempts, &nextRetryAt, &record.ProcessingFailureKind,
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
	if nextRetryAt != "" {
		next, err := time.Parse(time.RFC3339Nano, nextRetryAt)
		if err != nil {
			return IncidentRecord{}, fmt.Errorf("parse processing retry time: %w", err)
		}
		record.ProcessingNextRetryAt = &next
	}
	record.HasAI = record.ProcessingStatus == "succeeded" && record.AITitleDE != "" && record.AISummaryDE != "" && record.AITitleEN != "" && record.AISummaryEN != ""
	return record, nil
}
