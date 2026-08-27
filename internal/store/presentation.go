package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PresentationScope pins queries to the expected prompt/model provenance and
// determines whether only complete public output may be returned.
type PresentationScope struct {
	Operation           string
	ModelIdentity       string
	PromptVersion       string
	Language            string
	TranslationLanguage string
	PublicOnly          bool
}

// AdminIncidentFilter selects review queue subsets without changing visibility.
type AdminIncidentFilter string

// PublicIncidentLink is the minimal record required to build a sitemap.
type PublicIncidentLink struct {
	ID         int64
	ModifiedAt time.Time
}

// AdminTranslation is the selected translated presentation plus the latest
// attempt state for one incident and registered translation language.
type AdminTranslation struct {
	IncidentID    int64
	Language      string
	Title         string
	Summary       string
	Model         string
	PromptVersion string
	GeneratedAt   *time.Time
	Status        string
	Attempts      int
	NextRetryAt   *time.Time
	FailureKind   string
	Fallback      bool
}

const (
	AdminIncidentsAll         AdminIncidentFilter = "all"
	AdminIncidentsUnprocessed AdminIncidentFilter = "unprocessed"
)

const latestPresentationRun = `(SELECT r.id
	FROM presentation_runs r
	WHERE r.incident_id = i.id AND r.source_hash = i.content_hash AND r.status = 'complete'
		AND ((@prompt = '` + PipelineVersion + `' AND (r.pipeline_version IN ('` + PipelineVersion + `', '` + PreviousPipelineVersion + `') OR r.legacy = 1))
			OR r.pipeline_version LIKE 'legacy/' || @prompt || '/%')
		AND (@language = 'de' OR EXISTS (
			SELECT 1 FROM presentation_translations translated
			WHERE translated.presentation_run_id = r.id AND translated.language_code = @language AND translated.status = 'succeeded'
		))
	ORDER BY CASE r.pipeline_version WHEN '` + PipelineVersion + `' THEN 0 WHEN '` + PreviousPipelineVersion + `' THEN 1 ELSE 2 END,
		r.completed_at DESC, r.id DESC
	LIMIT 1)`

const latestCanonicalPresentationRun = `(SELECT r.id
	FROM presentation_runs r
	WHERE r.incident_id = i.id AND r.source_hash = i.content_hash AND r.status = 'complete'
		AND ((@prompt = '` + PipelineVersion + `' AND (r.pipeline_version IN ('` + PipelineVersion + `', '` + PreviousPipelineVersion + `') OR r.legacy = 1))
			OR r.pipeline_version LIKE 'legacy/' || @prompt || '/%')
	ORDER BY CASE r.pipeline_version WHEN '` + PipelineVersion + `' THEN 0 WHEN '` + PreviousPipelineVersion + `' THEN 1 ELSE 2 END,
		r.completed_at DESC, r.id DESC
	LIMIT 1)`

const scopedAIColumns = `
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'title_de' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_de' LIMIT 1), ''),
				COALESCE((SELECT title FROM presentation_translations WHERE presentation_run_id = ` + latestPresentationRun + ` AND language_code = @translation_language AND status = 'succeeded' ORDER BY completed_at DESC, id DESC LIMIT 1), ''),
				COALESCE((SELECT summary FROM presentation_translations WHERE presentation_run_id = ` + latestPresentationRun + ` AND language_code = @translation_language AND status = 'succeeded' ORDER BY completed_at DESC, id DESC LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'category' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'area_name' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'area_type' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'event_start_date' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'event_start_time' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'event_day_part' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'report_kind' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'public_assistance_status' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'public_assistance_types' LIMIT 1), ''),
				COALESCE((SELECT model_identity FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'category' LIMIT 1), ''),
				COALESCE((SELECT prompt_version FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'category' LIMIT 1), ''),
				COALESCE((SELECT generated_at FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'category' LIMIT 1), ''),
				COALESCE((SELECT model_identity FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_de' LIMIT 1), ''),
				COALESCE((SELECT prompt_version FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_de' LIMIT 1), ''),
				COALESCE((SELECT generated_at FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_de' LIMIT 1), ''),
				COALESCE((SELECT model_identity FROM presentation_translations WHERE presentation_run_id = ` + latestPresentationRun + ` AND language_code = @translation_language AND status = 'succeeded' ORDER BY completed_at DESC, id DESC LIMIT 1), ''),
				COALESCE((SELECT prompt_version FROM presentation_translations WHERE presentation_run_id = ` + latestPresentationRun + ` AND language_code = @translation_language AND status = 'succeeded' ORDER BY completed_at DESC, id DESC LIMIT 1), ''),
				COALESCE((SELECT completed_at FROM presentation_translations WHERE presentation_run_id = ` + latestPresentationRun + ` AND language_code = @translation_language AND status = 'succeeded' ORDER BY completed_at DESC, id DESC LIMIT 1), ''),
				COALESCE((SELECT pipeline_version FROM presentation_runs WHERE id = ` + latestPresentationRun + `), ''),
				COALESCE((SELECT legacy FROM presentation_runs WHERE id = ` + latestPresentationRun + `), 0),
				COALESCE((SELECT j.status FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id = j.cycle_item_id WHERE ci.incident_id = i.id AND ci.source_hash = i.content_hash ORDER BY j.updated_at DESC, j.id DESC LIMIT 1), ''),
				COALESCE((SELECT j.attempt_count FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id = j.cycle_item_id WHERE ci.incident_id = i.id AND ci.source_hash = i.content_hash ORDER BY j.updated_at DESC, j.id DESC LIMIT 1), 0),
				COALESCE((SELECT j.next_retry_at FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id = j.cycle_item_id WHERE ci.incident_id = i.id AND ci.source_hash = i.content_hash ORDER BY j.updated_at DESC, j.id DESC LIMIT 1), ''),
				COALESCE((SELECT j.failure_kind FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id = j.cycle_item_id WHERE ci.incident_id = i.id AND ci.source_hash = i.content_hash ORDER BY j.updated_at DESC, j.id DESC LIMIT 1), ''),
				COALESCE((SELECT status FROM presentation_translations WHERE presentation_run_id = ` + latestCanonicalPresentationRun + ` AND language_code = @translation_language ORDER BY created_at DESC, id DESC LIMIT 1), ''),
				COALESCE((SELECT attempt_count FROM presentation_translations WHERE presentation_run_id = ` + latestCanonicalPresentationRun + ` AND language_code = @translation_language ORDER BY created_at DESC, id DESC LIMIT 1), 0),
				COALESCE((SELECT next_retry_at FROM presentation_translations WHERE presentation_run_id = ` + latestCanonicalPresentationRun + ` AND language_code = @translation_language ORDER BY created_at DESC, id DESC LIMIT 1), ''),
				COALESCE((SELECT failure_kind FROM presentation_translations WHERE presentation_run_id = ` + latestCanonicalPresentationRun + ` AND language_code = @translation_language ORDER BY created_at DESC, id DESC LIMIT 1), ''),
				CASE WHEN NOT EXISTS (
					SELECT 1 FROM presentation_translations current_translation
					WHERE current_translation.presentation_run_id = ` + latestCanonicalPresentationRun + `
						AND current_translation.language_code=@translation_language AND current_translation.status='succeeded'
				) AND EXISTS (
					SELECT 1 FROM presentation_runs fallback_run
					JOIN presentation_translations fallback_translation ON fallback_translation.presentation_run_id=fallback_run.id
					WHERE fallback_run.incident_id=i.id AND fallback_run.source_hash=i.content_hash AND fallback_run.status='complete'
						AND fallback_translation.language_code=@translation_language AND fallback_translation.status='succeeded'
						AND ((@prompt = '` + PipelineVersion + `' AND (fallback_run.pipeline_version IN ('` + PipelineVersion + `', '` + PreviousPipelineVersion + `') OR fallback_run.legacy = 1))
							OR fallback_run.pipeline_version LIKE 'legacy/' || @prompt || '/%')
				) THEN 1 ELSE 0 END`

const publicReadyCondition = `EXISTS (
		SELECT 1 FROM presentation_runs r
		WHERE r.incident_id = i.id AND r.source_hash = i.content_hash AND r.status = 'complete'
			AND ((@prompt = '` + PipelineVersion + `' AND (r.pipeline_version IN ('` + PipelineVersion + `', '` + PreviousPipelineVersion + `') OR r.legacy = 1))
				OR r.pipeline_version LIKE 'legacy/' || @prompt || '/%')
			AND (@language = 'de' OR EXISTS (
				SELECT 1 FROM presentation_translations translated
				WHERE translated.presentation_run_id = r.id AND translated.language_code = @language AND translated.status = 'succeeded'
			))
)`

func presentationArgs(scope PresentationScope) []any {
	language := scope.Language
	if language == "" {
		language = "de"
	}
	translationLanguage := language
	if scope.TranslationLanguage != "" {
		translationLanguage = scope.TranslationLanguage
	}
	return []any{
		sql.Named("prompt", scope.PromptVersion),
		sql.Named("language", language),
		sql.Named("translation_language", translationLanguage),
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
				'', '', '', '', '', '', '',
				'', '', '', '', '', '',
				'', '', '',
				'', '', '',
				'', '', '',
				'', 0, '', 0, '', '',
				'', 0, '', '', 0
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
			CASE WHEN @language = 'de' THEN
				COALESCE((SELECT r.completed_at FROM presentation_runs r WHERE r.id = ` + latestPresentationRun + `), i.updated_at)
			ELSE COALESCE((SELECT completed_at FROM presentation_translations WHERE presentation_run_id = ` + latestPresentationRun + ` AND language_code=@language AND status='succeeded' ORDER BY completed_at DESC,id DESC LIMIT 1), i.updated_at) END
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
		filterCondition = " AND NOT (" + publicReadyCondition + ")"
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

// ListAdminTranslations loads every requested incident/language pair in one
// query so adding a registered translation does not introduce per-card reads.
func (s *Store) ListAdminTranslations(ctx context.Context, incidentIDs []int64, languages []string, promptVersion string) ([]AdminTranslation, error) {
	if len(incidentIDs) == 0 || len(languages) == 0 {
		return nil, nil
	}
	incidentValues := make([]string, 0, len(incidentIDs))
	languageValues := make([]string, 0, len(languages))
	args := []any{sql.Named("prompt", promptVersion)}
	for index, incidentID := range incidentIDs {
		name := fmt.Sprintf("incident_%d", index)
		incidentValues = append(incidentValues, "(@"+name+")")
		args = append(args, sql.Named(name, incidentID))
	}
	for index, language := range languages {
		name := fmt.Sprintf("translation_language_%d", index)
		languageValues = append(languageValues, "(@"+name+")")
		args = append(args, sql.Named(name, language))
	}
	query := `
		WITH requested_incidents(incident_id) AS (VALUES ` + strings.Join(incidentValues, ",") + `),
		requested_languages(language_code) AS (VALUES ` + strings.Join(languageValues, ",") + `),
		eligible_runs AS (
			SELECT r.*,
				CASE r.pipeline_version WHEN '` + PipelineVersion + `' THEN 0 WHEN '` + PreviousPipelineVersion + `' THEN 1 ELSE 2 END AS pipeline_priority,
				ROW_NUMBER() OVER (PARTITION BY r.incident_id ORDER BY
					CASE r.pipeline_version WHEN '` + PipelineVersion + `' THEN 0 WHEN '` + PreviousPipelineVersion + `' THEN 1 ELSE 2 END,
					r.completed_at DESC, r.id DESC) AS canonical_rank
			FROM presentation_runs r
			JOIN requested_incidents requested ON requested.incident_id = r.incident_id
			JOIN incidents current_incident ON current_incident.id = r.incident_id AND current_incident.content_hash = r.source_hash
			WHERE r.status = 'complete' AND ((@prompt = '` + PipelineVersion + `' AND
				(r.pipeline_version IN ('` + PipelineVersion + `', '` + PreviousPipelineVersion + `') OR r.legacy = 1))
				OR r.pipeline_version LIKE 'legacy/' || @prompt || '/%')
		),
		canonical_runs AS (SELECT * FROM eligible_runs WHERE canonical_rank = 1),
		ranked_translations AS (
			SELECT eligible.incident_id, translation.*,
				ROW_NUMBER() OVER (PARTITION BY eligible.incident_id, translation.language_code ORDER BY
					eligible.pipeline_priority, eligible.completed_at DESC, eligible.id DESC,
					translation.completed_at DESC, translation.id DESC) AS translation_rank
			FROM eligible_runs eligible
			JOIN presentation_translations translation ON translation.presentation_run_id = eligible.id AND translation.status = 'succeeded'
			JOIN requested_languages language ON language.language_code = translation.language_code
		),
		selected_translations AS (SELECT * FROM ranked_translations WHERE translation_rank = 1),
		ranked_attempts AS (
			SELECT canonical.incident_id, attempt.*,
				ROW_NUMBER() OVER (PARTITION BY canonical.incident_id, attempt.language_code ORDER BY attempt.created_at DESC, attempt.id DESC) AS attempt_rank
			FROM canonical_runs canonical
			JOIN presentation_translations attempt ON attempt.presentation_run_id = canonical.id
			JOIN requested_languages language ON language.language_code = attempt.language_code
		),
		latest_attempts AS (SELECT * FROM ranked_attempts WHERE attempt_rank = 1)
		SELECT requested.incident_id, language.language_code,
			COALESCE(selected.title, ''), COALESCE(selected.summary, ''),
			COALESCE(selected.model_identity, ''), COALESCE(selected.prompt_version, ''), COALESCE(selected.completed_at, ''),
			COALESCE(attempt.status, ''), COALESCE(attempt.attempt_count, 0), COALESCE(attempt.next_retry_at, ''), COALESCE(attempt.failure_kind, ''),
			CASE WHEN selected.presentation_run_id IS NOT NULL AND selected.presentation_run_id != canonical.id THEN 1 ELSE 0 END
		FROM requested_incidents requested
		CROSS JOIN requested_languages language
		LEFT JOIN canonical_runs canonical ON canonical.incident_id = requested.incident_id
		LEFT JOIN selected_translations selected ON selected.incident_id = requested.incident_id AND selected.language_code = language.language_code
		LEFT JOIN latest_attempts attempt ON attempt.incident_id = requested.incident_id AND attempt.language_code = language.language_code
		ORDER BY requested.incident_id, language.language_code`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list admin translations: %w", err)
	}
	defer rows.Close()
	translations := make([]AdminTranslation, 0, len(incidentIDs)*len(languages))
	for rows.Next() {
		var translation AdminTranslation
		var generatedAt, nextRetryAt string
		var fallback int
		if err := rows.Scan(
			&translation.IncidentID, &translation.Language, &translation.Title, &translation.Summary,
			&translation.Model, &translation.PromptVersion, &generatedAt,
			&translation.Status, &translation.Attempts, &nextRetryAt, &translation.FailureKind, &fallback,
		); err != nil {
			return nil, fmt.Errorf("scan admin translation: %w", err)
		}
		translation.Fallback = fallback == 1
		if generatedAt != "" {
			parsed, err := time.Parse(time.RFC3339Nano, generatedAt)
			if err != nil {
				return nil, fmt.Errorf("parse admin translation generation time: %w", err)
			}
			translation.GeneratedAt = &parsed
		}
		if nextRetryAt != "" {
			parsed, err := time.Parse(time.RFC3339Nano, nextRetryAt)
			if err != nil {
				return nil, fmt.Errorf("parse admin translation retry time: %w", err)
			}
			translation.NextRetryAt = &parsed
		}
		translations = append(translations, translation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin translations: %w", err)
	}
	return translations, nil
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
	var publishedAt, updatedAt, aiMetadataGeneratedAt, aiGeneratedAt, aiTranslationGeneratedAt, nextRetryAt, translationNextRetryAt string
	var aiLegacy, translationFallback int
	if err := row.Scan(
		&record.ID, &record.SourceDocumentID, &record.HasIncident, &record.Number, &record.Position,
		&record.TitleDE, &record.BodyDE, &record.ContentHash, &record.SourceTitle, &record.SourceURL,
		&record.SourceExternalID, &publishedAt, &updatedAt, &record.FetchStatus, &record.ErrorMessage,
		&record.AITitleDE, &record.AISummaryDE, &record.AITranslatedTitle, &record.AITranslatedSummary,
		&record.AICategory, &record.AIAreaName, &record.AIAreaType,
		&record.AIEventStartDate, &record.AIEventStartTime, &record.AIEventDayPart,
		&record.AIReportKind, &record.AIPublicAssistanceStatus, &record.AIPublicAssistanceTypes,
		&record.AIMetadataModel, &record.AIMetadataPromptVersion, &aiMetadataGeneratedAt,
		&record.AIModel, &record.AIPromptVersion, &aiGeneratedAt,
		&record.AITranslationModel, &record.AITranslationPromptVersion, &aiTranslationGeneratedAt,
		&record.AIPipelineVersion, &aiLegacy,
		&record.ProcessingStatus, &record.ProcessingAttempts, &nextRetryAt, &record.ProcessingFailureKind,
		&record.AITranslationStatus, &record.AITranslationAttempts, &translationNextRetryAt, &record.AITranslationFailureKind, &translationFallback,
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
	if aiMetadataGeneratedAt != "" {
		generatedAt, err := time.Parse(time.RFC3339Nano, aiMetadataGeneratedAt)
		if err != nil {
			return IncidentRecord{}, fmt.Errorf("parse AI metadata generation time: %w", err)
		}
		record.AIMetadataGeneratedAt = &generatedAt
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
	record.AITranslationFallback = translationFallback == 1
	if nextRetryAt != "" {
		next, err := time.Parse(time.RFC3339Nano, nextRetryAt)
		if err != nil {
			return IncidentRecord{}, fmt.Errorf("parse processing retry time: %w", err)
		}
		record.ProcessingNextRetryAt = &next
	}
	if translationNextRetryAt != "" {
		next, err := time.Parse(time.RFC3339Nano, translationNextRetryAt)
		if err != nil {
			return IncidentRecord{}, fmt.Errorf("parse translation retry time: %w", err)
		}
		record.AITranslationNextRetryAt = &next
	}
	record.HasAI = record.AITitleDE != "" && record.AISummaryDE != ""
	return record, nil
}
