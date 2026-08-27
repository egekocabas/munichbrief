package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PresentationScope pins queries to the expected prompt/model provenance and
// determines whether only complete public output may be returned.
type PresentationScope struct {
	Operation     string
	ModelIdentity string
	PromptVersion string
	PublicOnly    bool
}

// AdminIncidentFilter selects review queue subsets without changing visibility.
type AdminIncidentFilter string

// PublicIncidentLink is the minimal record required to build a sitemap.
type PublicIncidentLink struct {
	ID         int64
	ModifiedAt time.Time
}

const (
	AdminIncidentsAll         AdminIncidentFilter = "all"
	AdminIncidentsUnprocessed AdminIncidentFilter = "unprocessed"
)

const latestPresentationRun = `(SELECT r.id
	FROM presentation_runs r
	WHERE r.incident_id = i.id AND r.source_hash = i.content_hash AND r.status = 'complete'
		AND (@prompt = '` + PipelineVersion + `' OR r.pipeline_version LIKE 'legacy/' || @prompt || '/%')
	ORDER BY (r.pipeline_version = '` + PipelineVersion + `') DESC, r.completed_at DESC, r.id DESC
	LIMIT 1)`

const scopedAIColumns = `
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'title_de' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_de' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'title_en' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_en' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'category' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'area_name' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'area_type' LIMIT 1), ''),
				COALESCE((SELECT model_identity FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_de' LIMIT 1), ''),
				COALESCE((SELECT prompt_version FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_de' LIMIT 1), ''),
				COALESCE((SELECT generated_at FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_de' LIMIT 1), ''),
				COALESCE((SELECT model_identity FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_en' LIMIT 1), ''),
				COALESCE((SELECT prompt_version FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_en' LIMIT 1), ''),
				COALESCE((SELECT generated_at FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_en' LIMIT 1), ''),
				COALESCE((SELECT pipeline_version FROM presentation_runs WHERE id = ` + latestPresentationRun + `), ''),
				COALESCE((SELECT legacy FROM presentation_runs WHERE id = ` + latestPresentationRun + `), 0),
				COALESCE((SELECT j.status FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id = j.cycle_item_id WHERE ci.incident_id = i.id AND ci.source_hash = i.content_hash ORDER BY j.updated_at DESC, j.id DESC LIMIT 1), ''),
				COALESCE((SELECT j.attempt_count FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id = j.cycle_item_id WHERE ci.incident_id = i.id AND ci.source_hash = i.content_hash ORDER BY j.updated_at DESC, j.id DESC LIMIT 1), 0),
				COALESCE((SELECT j.next_retry_at FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id = j.cycle_item_id WHERE ci.incident_id = i.id AND ci.source_hash = i.content_hash ORDER BY j.updated_at DESC, j.id DESC LIMIT 1), ''),
				COALESCE((SELECT j.failure_kind FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id = j.cycle_item_id WHERE ci.incident_id = i.id AND ci.source_hash = i.content_hash ORDER BY j.updated_at DESC, j.id DESC LIMIT 1), '')`

const publicReadyCondition = `EXISTS (
		SELECT 1 FROM presentation_runs r
		WHERE r.incident_id = i.id AND r.source_hash = i.content_hash AND r.status = 'complete'
			AND (@prompt = '` + PipelineVersion + `' OR r.pipeline_version LIKE 'legacy/' || @prompt || '/%')
)`

const stagedReadyCondition = `EXISTS (
		SELECT 1 FROM presentation_runs r
		WHERE r.incident_id = i.id AND r.source_hash = i.content_hash
			AND r.pipeline_version = '` + PipelineVersion + `' AND r.status = 'complete'
)`

func presentationArgs(scope PresentationScope) []any {
	return []any{
		sql.Named("prompt", scope.PromptVersion),
		sql.Named("operation_prefix", "incident-presentation/"+scope.PromptVersion+"/"),
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

// ListPresentationEntries returns timeline records within the requested scope.
// Public scopes fail closed by requiring a complete matching presentation run.
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
				'', '', '', '', '', '', '', '', '', '', '', '', '', '', 0, '', 0, '', ''
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

// ListPublicIncidentLinks returns the stable public incident identity and
// modification time needed by discovery documents without loading article
// bodies or presentation values.
func (s *Store) ListPublicIncidentLinks(ctx context.Context, sourceMode string, scope PresentationScope) ([]PublicIncidentLink, error) {
	statusCondition, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return nil, err
	}
	scope.PublicOnly = true
	query := `
		SELECT i.id,
			COALESCE((SELECT r.completed_at FROM presentation_runs r WHERE r.id = ` + latestPresentationRun + `), i.updated_at)
		FROM incidents i
		JOIN source_documents d ON d.id = i.source_document_id
		WHERE ` + statusCondition + ` AND ` + publicReadyCondition + `
		ORDER BY i.id ASC`
	rows, err := s.db.QueryContext(ctx, query, presentationArgs(scope)...)
	if err != nil {
		return nil, fmt.Errorf("list public incident links: %w", err)
	}
	defer rows.Close()
	links := make([]PublicIncidentLink, 0)
	for rows.Next() {
		var link PublicIncidentLink
		var modifiedAt string
		if err := rows.Scan(&link.ID, &modifiedAt); err != nil {
			return nil, fmt.Errorf("scan public incident link: %w", err)
		}
		link.ModifiedAt, err = time.Parse(time.RFC3339Nano, modifiedAt)
		if err != nil {
			return nil, fmt.Errorf("parse public incident modification time: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate public incident links: %w", err)
	}
	return links, nil
}

// ListAdminIncidents returns parsed incidents for protected review views. AI
// output is scoped to the active source content and prompt. Any model may
// provide the newest complete presentation, while stale or incomplete output
// remains visible as unprocessed work.
func (s *Store) ListAdminIncidents(ctx context.Context, limit, offset int, sourceMode string, scope PresentationScope, filter AdminIncidentFilter) ([]IncidentRecord, int, error) {
	if limit < 1 {
		return nil, 0, errors.New("limit must be positive")
	}
	if offset < 0 {
		return nil, 0, errors.New("offset must not be negative")
	}
	if filter != AdminIncidentsAll && filter != AdminIncidentsUnprocessed {
		return nil, 0, errors.New("admin incident filter must be all or unprocessed")
	}
	statusCondition, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return nil, 0, err
	}
	filterCondition := ""
	if filter == AdminIncidentsUnprocessed {
		readyCondition := publicReadyCondition
		if scope.PromptVersion == PipelineVersion {
			readyCondition = stagedReadyCondition
		}
		filterCondition = " AND NOT (" + readyCondition + ")"
	}
	args := presentationArgs(scope)

	var total int
	countQuery := `
		SELECT COUNT(*)
		FROM incidents i
		JOIN source_documents d ON d.id = i.source_document_id
		WHERE ` + statusCondition + filterCondition
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count admin incidents: %w", err)
	}

	query := `
		SELECT
			i.id, d.id, 1, i.incident_number, i.position, i.title_de, COALESCE(i.body_de, ''),
			i.content_hash, d.title, d.source_url, d.external_id, d.published_at, i.updated_at,
			d.fetch_status, COALESCE(d.error_message, ''),` + scopedAIColumns + `
		FROM incidents i
		JOIN source_documents d ON d.id = i.source_document_id
		WHERE ` + statusCondition + filterCondition + `
		ORDER BY d.published_at DESC, i.position ASC
		LIMIT @limit OFFSET @offset`
	args = append(args, sql.Named("limit", limit), sql.Named("offset", offset))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list admin incidents: %w", err)
	}
	defer rows.Close()

	records := make([]IncidentRecord, 0, min(limit, total))
	for rows.Next() {
		record, err := scanPresentationIncident(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan admin incident: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate admin incidents: %w", err)
	}
	return records, total, nil
}

// GetPresentationIncident returns one incident only when it satisfies the same
// provenance and public-readiness rules as the timeline.
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
	var publishedAt, updatedAt, aiGeneratedAt, aiTranslationGeneratedAt, nextRetryAt string
	var aiLegacy int
	if err := row.Scan(
		&record.ID, &record.SourceDocumentID, &record.HasIncident, &record.Number, &record.Position,
		&record.TitleDE, &record.BodyDE, &record.ContentHash, &record.SourceTitle, &record.SourceURL,
		&record.SourceExternalID, &publishedAt, &updatedAt, &record.FetchStatus, &record.ErrorMessage,
		&record.AITitleDE, &record.AISummaryDE, &record.AITitleEN, &record.AISummaryEN,
		&record.AICategory, &record.AIAreaName, &record.AIAreaType,
		&record.AIModel, &record.AIPromptVersion, &aiGeneratedAt,
		&record.AITranslationModel, &record.AITranslationPromptVersion, &aiTranslationGeneratedAt,
		&record.AIPipelineVersion, &aiLegacy,
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
	if aiTranslationGeneratedAt != "" {
		generatedAt, err := time.Parse(time.RFC3339Nano, aiTranslationGeneratedAt)
		if err != nil {
			return IncidentRecord{}, fmt.Errorf("parse AI translation generation time: %w", err)
		}
		record.AITranslationGeneratedAt = &generatedAt
	}
	record.AILegacy = aiLegacy == 1
	if nextRetryAt != "" {
		next, err := time.Parse(time.RFC3339Nano, nextRetryAt)
		if err != nil {
			return IncidentRecord{}, fmt.Errorf("parse processing retry time: %w", err)
		}
		record.ProcessingNextRetryAt = &next
	}
	record.HasAI = record.AITitleDE != "" && record.AISummaryDE != "" && record.AITitleEN != "" && record.AISummaryEN != ""
	return record, nil
}
