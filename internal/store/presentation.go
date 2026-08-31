package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	langregistry "github.com/egekocabas/munichbrief/internal/languages"
)

// PresentationScope pins queries to the expected prompt/model provenance and
// determines whether only complete public output may be returned.
type PresentationScope struct {
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
	IncidentID           int64
	Language             string
	Title                string
	Summary              string
	Model                string
	PromptVersion        string
	GeneratedAt          *time.Time
	Status               string
	StatusReason         string
	StatusDetail         string
	Attempts             int
	NextRetryAt          *time.Time
	FailureKind          string
	AttemptModel         string
	AttemptPromptVersion string
	AttemptUpdatedAt     *time.Time
}

// AdminCategoryVerification combines the latest durable attempt with the latest
// successful effective result for one current canonical presentation.
type AdminCategoryVerification struct {
	IncidentID        int64
	PresentationRunID int64
	OriginalCategory  string
	EffectiveCategory string
	IsCorrect         *bool
	Model             string
	PromptVersion     string
	GeneratedAt       *time.Time
	Status            string
	StatusReason      string
	StatusDetail      string
	Attempts          int
	NextRetryAt       *time.Time
	FailureKind       string
}

// AdminPublicAssistanceVerification combines the latest durable attempt with
// the latest successful effective result for one current canonical presentation.
type AdminPublicAssistanceVerification struct {
	IncidentID        int64
	PresentationRunID int64
	OriginalStatus    string
	OriginalTypes     string
	EffectiveStatus   string
	EffectiveTypes    string
	IsCorrect         *bool
	Model             string
	PromptVersion     string
	GeneratedAt       *time.Time
	Status            string
	StatusReason      string
	StatusDetail      string
	Attempts          int
	NextRetryAt       *time.Time
	FailureKind       string
}

type adminVerificationValuePair struct {
	OriginalKind  string
	CorrectedKind string
}

type adminVerificationSelection struct {
	IncidentID        int64
	PresentationRunID int64
	OriginalValues    []string
	EffectiveValues   []string
	IsCorrect         *bool
	Model             string
	PromptVersion     string
	GeneratedAt       *time.Time
	Status            string
	StatusReason      string
	StatusDetail      string
	Attempts          int
	NextRetryAt       *time.Time
	FailureKind       string
}

const (
	AdminIncidentsAll         AdminIncidentFilter = "all"
	AdminIncidentsUnprocessed AdminIncidentFilter = "unprocessed"
)

const latestCanonicalPresentationRun = `(SELECT r.id
	FROM presentation_runs r
	WHERE r.incident_id = i.id AND r.source_hash = i.content_hash AND r.status = 'complete'
		AND r.pipeline_version = '` + PipelineVersion + `' AND r.legacy = 0
	ORDER BY r.completed_at DESC, r.id DESC
	LIMIT 1)`

const latestPresentationRun = latestCanonicalPresentationRun

func latestCompletePostProcessingJob(runExpression, processorKey, scopeExpression string, outputKinds ...string) string {
	query := `(SELECT job.id FROM post_processing_jobs job WHERE job.presentation_run_id = ` + runExpression +
		` AND job.processor_key=` + sqlStringLiteral(processorKey) + ` AND job.scope_key=` + scopeExpression + ` AND job.status='succeeded'`
	for _, kind := range outputKinds {
		query += ` AND EXISTS (SELECT 1 FROM post_processing_values output WHERE output.job_id=job.id AND output.kind=` + sqlStringLiteral(kind) + `)`
	}
	return query + ` ORDER BY job.completed_at DESC,job.id DESC LIMIT 1)`
}

func sqlStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

var (
	latestCompletePublicAssistanceVerificationJob = latestCompletePostProcessingJob(latestPresentationRun, "public_assistance_verification", "'default'", "is_correct", "corrected_public_assistance_status", "corrected_public_assistance_types")
	latestCompleteCategoryVerificationJob         = latestCompletePostProcessingJob(latestPresentationRun, "category_verification", "'default'", "is_correct", "corrected_category")
	latestCompleteTranslationJob                  = latestCompletePostProcessingJob(latestPresentationRun, "translation", "@translation_language", "title", "summary")
	latestCompletePublicTranslationJob            = latestCompletePostProcessingJob(latestPresentationRun, "translation", "@language", "title", "summary")
)

var scopedAIColumns = `
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'title_de' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_de' LIMIT 1), ''),
				COALESCE((SELECT value FROM post_processing_values WHERE job_id = ` + latestCompleteTranslationJob + ` AND kind='title' LIMIT 1), ''),
				COALESCE((SELECT value FROM post_processing_values WHERE job_id = ` + latestCompleteTranslationJob + ` AND kind='summary' LIMIT 1), ''),
				COALESCE((SELECT value FROM post_processing_values WHERE job_id = ` + latestCompleteCategoryVerificationJob + ` AND kind='corrected_category' LIMIT 1),
					(SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'category' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'category' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'area_name' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'area_type' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'event_start_date' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'event_start_time' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'event_day_part' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'report_kind' LIMIT 1), ''),
				COALESCE((SELECT value FROM post_processing_values WHERE job_id = ` + latestCompletePublicAssistanceVerificationJob + ` AND kind='corrected_public_assistance_status' LIMIT 1),
					(SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'public_assistance_status' LIMIT 1), ''),
				COALESCE((SELECT value FROM post_processing_values WHERE job_id = ` + latestCompletePublicAssistanceVerificationJob + ` AND kind='corrected_public_assistance_types' LIMIT 1),
					(SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'public_assistance_types' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'public_assistance_status' LIMIT 1), ''),
				COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'public_assistance_types' LIMIT 1), ''),
				COALESCE((SELECT model_identity FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'category' LIMIT 1), ''),
				COALESCE((SELECT prompt_version FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'category' LIMIT 1), ''),
				COALESCE((SELECT generated_at FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'category' LIMIT 1), ''),
				COALESCE((SELECT model_identity FROM post_processing_jobs WHERE id = ` + latestCompletePublicAssistanceVerificationJob + `), ''),
				COALESCE((SELECT prompt_version FROM post_processing_jobs WHERE id = ` + latestCompletePublicAssistanceVerificationJob + `), ''),
				COALESCE((SELECT completed_at FROM post_processing_jobs WHERE id = ` + latestCompletePublicAssistanceVerificationJob + `), ''),
				COALESCE((SELECT model_identity FROM post_processing_jobs WHERE id = ` + latestCompleteCategoryVerificationJob + `), ''),
				COALESCE((SELECT prompt_version FROM post_processing_jobs WHERE id = ` + latestCompleteCategoryVerificationJob + `), ''),
				COALESCE((SELECT completed_at FROM post_processing_jobs WHERE id = ` + latestCompleteCategoryVerificationJob + `), ''),
				COALESCE((SELECT model_identity FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_de' LIMIT 1), ''),
				COALESCE((SELECT prompt_version FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_de' LIMIT 1), ''),
				COALESCE((SELECT generated_at FROM presentation_values WHERE presentation_run_id = ` + latestPresentationRun + ` AND kind = 'summary_de' LIMIT 1), ''),
				COALESCE((SELECT model_identity FROM post_processing_jobs WHERE id = ` + latestCompleteTranslationJob + `), ''),
				COALESCE((SELECT prompt_version FROM post_processing_jobs WHERE id = ` + latestCompleteTranslationJob + `), ''),
				COALESCE((SELECT completed_at FROM post_processing_jobs WHERE id = ` + latestCompleteTranslationJob + `), ''),
				COALESCE((SELECT pipeline_version FROM presentation_runs WHERE id = ` + latestPresentationRun + `), ''),
				COALESCE((SELECT j.status FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id = j.cycle_item_id WHERE ci.incident_id = i.id AND ci.source_hash = i.content_hash ORDER BY j.updated_at DESC, j.id DESC LIMIT 1), ''),
				COALESCE((SELECT j.attempt_count FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id = j.cycle_item_id WHERE ci.incident_id = i.id AND ci.source_hash = i.content_hash ORDER BY j.updated_at DESC, j.id DESC LIMIT 1), 0),
				COALESCE((SELECT j.next_retry_at FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id = j.cycle_item_id WHERE ci.incident_id = i.id AND ci.source_hash = i.content_hash ORDER BY j.updated_at DESC, j.id DESC LIMIT 1), ''),
				COALESCE((SELECT j.failure_kind FROM processing_step_jobs j JOIN processing_cycle_items ci ON ci.id = j.cycle_item_id WHERE ci.incident_id = i.id AND ci.source_hash = i.content_hash ORDER BY j.updated_at DESC, j.id DESC LIMIT 1), ''),
				COALESCE((SELECT status FROM post_processing_jobs WHERE presentation_run_id = ` + latestCanonicalPresentationRun + ` AND processor_key='translation' AND scope_key=@translation_language ORDER BY created_at DESC,id DESC LIMIT 1), ''),
				COALESCE((SELECT attempt_count FROM post_processing_jobs WHERE presentation_run_id = ` + latestCanonicalPresentationRun + ` AND processor_key='translation' AND scope_key=@translation_language ORDER BY created_at DESC,id DESC LIMIT 1), 0),
				COALESCE((SELECT next_retry_at FROM post_processing_jobs WHERE presentation_run_id = ` + latestCanonicalPresentationRun + ` AND processor_key='translation' AND scope_key=@translation_language ORDER BY created_at DESC,id DESC LIMIT 1), ''),
				COALESCE((SELECT failure_kind FROM post_processing_jobs WHERE presentation_run_id = ` + latestCanonicalPresentationRun + ` AND processor_key='translation' AND scope_key=@translation_language ORDER BY created_at DESC,id DESC LIMIT 1), '')`

var publicReadyCondition = `EXISTS (
		SELECT 1 FROM presentation_runs r
		WHERE r.id = ` + latestCanonicalPresentationRun + `
			AND (@language = @canonical_language OR ` + latestCompletePublicTranslationJob + ` IS NOT NULL)
)`

func presentationArgs(scope PresentationScope) []any {
	language := scope.Language
	if language == "" {
		language = langregistry.Canonical(langregistry.Registered()).Code
	}
	translationLanguage := language
	if scope.TranslationLanguage != "" {
		translationLanguage = scope.TranslationLanguage
	}
	return []any{
		sql.Named("language", language),
		sql.Named("translation_language", translationLanguage),
		sql.Named("canonical_language", langregistry.Canonical(langregistry.Registered()).Code),
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
				'', '', '', '', '', '', '', '',
				'', '', '', '', '', '',
				'', '',
				'', '', '',
				'', '', '',
				'', '', '',
				'', '', '',
				'', '', '',
				'', '', 0, '', '',
				'', 0, '', ''
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
			CASE WHEN @language = @canonical_language THEN
				COALESCE(NULLIF(MAX(
					COALESCE((SELECT r.completed_at FROM presentation_runs r WHERE r.id = ` + latestPresentationRun + `),''),
					COALESCE((SELECT completed_at FROM post_processing_jobs WHERE id = ` + latestCompletePublicAssistanceVerificationJob + `),''),
					COALESCE((SELECT completed_at FROM post_processing_jobs WHERE id = ` + latestCompleteCategoryVerificationJob + `),'')
				),''), i.updated_at)
			ELSE COALESCE(NULLIF(MAX(
				COALESCE((SELECT completed_at FROM post_processing_jobs WHERE id = ` + latestCompletePublicTranslationJob + `),''),
				COALESCE((SELECT completed_at FROM post_processing_jobs WHERE id = ` + latestCompletePublicAssistanceVerificationJob + `),''),
				COALESCE((SELECT completed_at FROM post_processing_jobs WHERE id = ` + latestCompleteCategoryVerificationJob + `),'')
			),''), i.updated_at) END
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
func (s *Store) ListAdminTranslations(ctx context.Context, incidentIDs []int64, languages []string) ([]AdminTranslation, error) {
	if len(incidentIDs) == 0 || len(languages) == 0 {
		return nil, nil
	}
	incidentValues := make([]string, 0, len(incidentIDs))
	languageValues := make([]string, 0, len(languages))
	args := []any{}
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
			SELECT r.*,ROW_NUMBER() OVER (PARTITION BY r.incident_id ORDER BY r.completed_at DESC,r.id DESC) AS canonical_rank
			FROM presentation_runs r
			JOIN requested_incidents requested ON requested.incident_id = r.incident_id
			JOIN incidents current_incident ON current_incident.id = r.incident_id AND current_incident.content_hash = r.source_hash
			WHERE r.status='complete' AND r.pipeline_version='` + PipelineVersion + `' AND r.legacy=0
		),
		canonical_runs AS (SELECT * FROM eligible_runs WHERE canonical_rank = 1),
		ranked_translations AS (
			SELECT canonical.incident_id,translation.*,
				ROW_NUMBER() OVER (PARTITION BY canonical.incident_id,translation.scope_key ORDER BY translation.completed_at DESC,translation.id DESC) AS translation_rank
			FROM canonical_runs canonical
			JOIN post_processing_jobs translation ON translation.presentation_run_id=canonical.id AND translation.processor_key='translation' AND translation.status='succeeded'
			JOIN requested_languages language ON language.language_code=translation.scope_key
			WHERE EXISTS (SELECT 1 FROM post_processing_values output WHERE output.job_id=translation.id AND output.kind='title' AND trim(output.value)<>'')
				AND EXISTS (SELECT 1 FROM post_processing_values output WHERE output.job_id=translation.id AND output.kind='summary' AND trim(output.value)<>'')
		),
		selected_translations AS (SELECT * FROM ranked_translations WHERE translation_rank = 1),
		ranked_attempts AS (
			SELECT canonical.incident_id,attempt.*,
				ROW_NUMBER() OVER (PARTITION BY canonical.incident_id,attempt.scope_key ORDER BY attempt.created_at DESC,attempt.id DESC) AS attempt_rank
			FROM canonical_runs canonical
			JOIN post_processing_jobs attempt ON attempt.presentation_run_id=canonical.id AND attempt.processor_key='translation'
			JOIN requested_languages language ON language.language_code=attempt.scope_key
		),
		latest_attempts AS (SELECT * FROM ranked_attempts WHERE attempt_rank = 1)
		SELECT requested.incident_id, language.language_code,
			COALESCE((SELECT value FROM post_processing_values WHERE job_id=selected.id AND kind='title'),''),
			COALESCE((SELECT value FROM post_processing_values WHERE job_id=selected.id AND kind='summary'),''),
			COALESCE(selected.model_identity, ''), COALESCE(selected.prompt_version, ''), COALESCE(selected.completed_at, ''),
			COALESCE(attempt.status, ''), COALESCE(attempt.status_reason, ''), COALESCE(attempt.status_detail, ''),
			COALESCE(attempt.attempt_count, 0), COALESCE(attempt.next_retry_at, ''), COALESCE(attempt.failure_kind, ''),
			COALESCE(attempt.model_identity, ''), COALESCE(attempt.prompt_version, ''), COALESCE(attempt.updated_at, '')
		FROM requested_incidents requested
		CROSS JOIN requested_languages language
		LEFT JOIN canonical_runs canonical ON canonical.incident_id = requested.incident_id
		LEFT JOIN selected_translations selected ON selected.incident_id=requested.incident_id AND selected.scope_key=language.language_code
		LEFT JOIN latest_attempts attempt ON attempt.incident_id=requested.incident_id AND attempt.scope_key=language.language_code
		ORDER BY requested.incident_id, language.language_code`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list admin translations: %w", err)
	}
	defer rows.Close()
	translations := make([]AdminTranslation, 0, len(incidentIDs)*len(languages))
	for rows.Next() {
		var translation AdminTranslation
		var generatedAt, nextRetryAt, attemptUpdatedAt string
		if err := rows.Scan(
			&translation.IncidentID, &translation.Language, &translation.Title, &translation.Summary,
			&translation.Model, &translation.PromptVersion, &generatedAt,
			&translation.Status, &translation.StatusReason, &translation.StatusDetail,
			&translation.Attempts, &nextRetryAt, &translation.FailureKind,
			&translation.AttemptModel, &translation.AttemptPromptVersion, &attemptUpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan admin translation: %w", err)
		}
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
		if attemptUpdatedAt != "" {
			parsed, err := time.Parse(time.RFC3339Nano, attemptUpdatedAt)
			if err != nil {
				return nil, fmt.Errorf("parse admin translation attempt update time: %w", err)
			}
			translation.AttemptUpdatedAt = &parsed
		}
		translations = append(translations, translation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin translations: %w", err)
	}
	return translations, nil
}

// ListAdminCategoryVerifications loads verifier state for the newest current
// canonical presentation of each requested incident in one bounded query.
func (s *Store) ListAdminCategoryVerifications(ctx context.Context, incidentIDs []int64) ([]AdminCategoryVerification, error) {
	selected, err := s.listAdminVerificationSelections(ctx, incidentIDs, "category_verification", "default", "is_correct", []adminVerificationValuePair{{OriginalKind: "category", CorrectedKind: "corrected_category"}})
	if err != nil {
		return nil, fmt.Errorf("list admin category verifications: %w", err)
	}
	results := make([]AdminCategoryVerification, 0, len(selected))
	for _, item := range selected {
		results = append(results, AdminCategoryVerification{
			IncidentID: item.IncidentID, PresentationRunID: item.PresentationRunID,
			OriginalCategory: item.OriginalValues[0], EffectiveCategory: item.EffectiveValues[0], IsCorrect: item.IsCorrect,
			Model: item.Model, PromptVersion: item.PromptVersion, GeneratedAt: item.GeneratedAt,
			Status: item.Status, StatusReason: item.StatusReason, StatusDetail: item.StatusDetail,
			Attempts: item.Attempts, NextRetryAt: item.NextRetryAt, FailureKind: item.FailureKind,
		})
	}
	return results, nil
}

// ListAdminPublicAssistanceVerifications loads verifier state for the newest
// current canonical presentation of each requested incident in one bounded query.
func (s *Store) ListAdminPublicAssistanceVerifications(ctx context.Context, incidentIDs []int64) ([]AdminPublicAssistanceVerification, error) {
	selected, err := s.listAdminVerificationSelections(ctx, incidentIDs, "public_assistance_verification", "default", "is_correct", []adminVerificationValuePair{
		{OriginalKind: "public_assistance_status", CorrectedKind: "corrected_public_assistance_status"},
		{OriginalKind: "public_assistance_types", CorrectedKind: "corrected_public_assistance_types"},
	})
	if err != nil {
		return nil, fmt.Errorf("list admin public assistance verifications: %w", err)
	}
	results := make([]AdminPublicAssistanceVerification, 0, len(selected))
	for _, item := range selected {
		results = append(results, AdminPublicAssistanceVerification{
			IncidentID: item.IncidentID, PresentationRunID: item.PresentationRunID,
			OriginalStatus: item.OriginalValues[0], OriginalTypes: item.OriginalValues[1],
			EffectiveStatus: item.EffectiveValues[0], EffectiveTypes: item.EffectiveValues[1], IsCorrect: item.IsCorrect,
			Model: item.Model, PromptVersion: item.PromptVersion, GeneratedAt: item.GeneratedAt,
			Status: item.Status, StatusReason: item.StatusReason, StatusDetail: item.StatusDetail,
			Attempts: item.Attempts, NextRetryAt: item.NextRetryAt, FailureKind: item.FailureKind,
		})
	}
	return results, nil
}

// listAdminVerificationSelections is the shared persistence path for
// correction-style post-processors. A new verifier supplies only its immutable
// input-to-corrected-output pairs; latest-success fallback and latest-attempt
// state stay consistent across admin views.
func (s *Store) listAdminVerificationSelections(ctx context.Context, incidentIDs []int64, processorKey, scopeKey, verdictKind string, valuePairs []adminVerificationValuePair) ([]adminVerificationSelection, error) {
	if len(incidentIDs) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(processorKey) == "" || strings.TrimSpace(scopeKey) == "" || strings.TrimSpace(verdictKind) == "" || len(valuePairs) == 0 {
		return nil, errors.New("verification processor, scope, verdict, and value pairs are required")
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(incidentIDs)), ",")
	args := make([]any, 0, len(incidentIDs)+3+len(valuePairs)*2)
	for _, id := range incidentIDs {
		args = append(args, id)
	}
	args = append(args, sql.Named("verification_processor", processorKey), sql.Named("verification_scope", scopeKey), sql.Named("verdict_kind", verdictKind))
	var valueColumns strings.Builder
	var successRequirements strings.Builder
	successRequirements.WriteString(` AND EXISTS (SELECT 1 FROM post_processing_values output WHERE output.job_id=job.id AND output.kind=@verdict_kind)`)
	for index, pair := range valuePairs {
		if strings.TrimSpace(pair.OriginalKind) == "" || strings.TrimSpace(pair.CorrectedKind) == "" {
			return nil, errors.New("verification original and corrected value kinds are required")
		}
		originalParameter := fmt.Sprintf("original_kind_%d", index)
		correctedParameter := fmt.Sprintf("corrected_kind_%d", index)
		originalValue := `(SELECT value FROM presentation_values WHERE presentation_run_id=selected.run_id AND kind=@` + originalParameter + ` LIMIT 1)`
		valueColumns.WriteString(`,
		COALESCE(` + originalValue + `,''),
		COALESCE((SELECT value FROM post_processing_values WHERE job_id=successful.id AND kind=@` + correctedParameter + ` LIMIT 1),` + originalValue + `,'')`)
		successRequirements.WriteString(` AND EXISTS (SELECT 1 FROM post_processing_values output WHERE output.job_id=job.id AND output.kind=@` + correctedParameter + `)`)
		args = append(args, sql.Named(originalParameter, pair.OriginalKind), sql.Named(correctedParameter, pair.CorrectedKind))
	}
	query := `WITH requested(incident_id) AS (SELECT id FROM incidents WHERE id IN (` + placeholders + `)),
		selected_runs AS (
			SELECT requested.incident_id, (SELECT r.id FROM presentation_runs r JOIN incidents i ON i.id=r.incident_id
				WHERE r.incident_id=requested.incident_id AND r.source_hash=i.content_hash AND r.status='complete'
				AND r.pipeline_version='` + PipelineVersion + `' AND r.legacy=0
				ORDER BY r.completed_at DESC,r.id DESC LIMIT 1) AS run_id
			FROM requested
		)
	SELECT selected.incident_id,COALESCE(selected.run_id,0)` + valueColumns.String() + `,
		COALESCE((SELECT value FROM post_processing_values WHERE job_id=successful.id AND kind=@verdict_kind LIMIT 1),''),
		COALESCE(successful.model_identity,attempt.model_identity,''),
		COALESCE(successful.prompt_version,attempt.prompt_version,''),
		COALESCE(successful.completed_at,''),COALESCE(attempt.status,''),
		COALESCE(attempt.status_reason,''),COALESCE(attempt.status_detail,''),COALESCE(attempt.attempt_count,0),
		COALESCE(attempt.next_retry_at,''),COALESCE(attempt.failure_kind,'')
	FROM selected_runs selected
	LEFT JOIN post_processing_jobs successful ON successful.id=(
		SELECT job.id FROM post_processing_jobs job WHERE job.presentation_run_id=selected.run_id
			AND job.processor_key=@verification_processor AND job.scope_key=@verification_scope AND job.status='succeeded'` + successRequirements.String() + `
		ORDER BY job.completed_at DESC,job.id DESC LIMIT 1)
	LEFT JOIN post_processing_jobs attempt ON attempt.id=(
		SELECT job.id FROM post_processing_jobs job WHERE job.presentation_run_id=selected.run_id
			AND job.processor_key=@verification_processor AND job.scope_key=@verification_scope
		ORDER BY job.created_at DESC,job.id DESC LIMIT 1)
	ORDER BY selected.incident_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]adminVerificationSelection, 0, len(incidentIDs))
	for rows.Next() {
		result := adminVerificationSelection{OriginalValues: make([]string, len(valuePairs)), EffectiveValues: make([]string, len(valuePairs))}
		var correct, generatedAt, nextRetryAt string
		destinations := []any{&result.IncidentID, &result.PresentationRunID}
		for index := range valuePairs {
			destinations = append(destinations, &result.OriginalValues[index], &result.EffectiveValues[index])
		}
		destinations = append(destinations, &correct, &result.Model, &result.PromptVersion, &generatedAt,
			&result.Status, &result.StatusReason, &result.StatusDetail, &result.Attempts, &nextRetryAt, &result.FailureKind)
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		switch correct {
		case "true", "false":
			value := correct == "true"
			result.IsCorrect = &value
		case "":
		default:
			return nil, fmt.Errorf("invalid stored verification verdict %q", correct)
		}
		if generatedAt != "" {
			value, err := time.Parse(time.RFC3339Nano, generatedAt)
			if err != nil {
				return nil, fmt.Errorf("parse verification generation time: %w", err)
			}
			result.GeneratedAt = &value
		}
		if nextRetryAt != "" {
			value, err := time.Parse(time.RFC3339Nano, nextRetryAt)
			if err != nil {
				return nil, fmt.Errorf("parse verification retry time: %w", err)
			}
			result.NextRetryAt = &value
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
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
	var publishedAt, updatedAt, aiMetadataGeneratedAt, aiPublicAssistanceVerificationGeneratedAt, aiCategoryVerificationGeneratedAt, aiGeneratedAt, aiTranslationGeneratedAt, nextRetryAt, translationNextRetryAt string
	if err := row.Scan(
		&record.ID, &record.SourceDocumentID, &record.HasIncident, &record.Number, &record.Position,
		&record.TitleDE, &record.BodyDE, &record.ContentHash, &record.SourceTitle, &record.SourceURL,
		&record.SourceExternalID, &publishedAt, &updatedAt, &record.FetchStatus, &record.ErrorMessage,
		&record.AITitleDE, &record.AISummaryDE, &record.AITranslatedTitle, &record.AITranslatedSummary,
		&record.AICategory, &record.AIOriginalCategory, &record.AIAreaName, &record.AIAreaType,
		&record.AIEventStartDate, &record.AIEventStartTime, &record.AIEventDayPart,
		&record.AIReportKind, &record.AIPublicAssistanceStatus, &record.AIPublicAssistanceTypes,
		&record.AIOriginalPublicAssistanceStatus, &record.AIOriginalPublicAssistanceTypes,
		&record.AIMetadataModel, &record.AIMetadataPromptVersion, &aiMetadataGeneratedAt,
		&record.AIPublicAssistanceVerificationModel, &record.AIPublicAssistanceVerificationPromptVersion, &aiPublicAssistanceVerificationGeneratedAt,
		&record.AICategoryVerificationModel, &record.AICategoryVerificationPromptVersion, &aiCategoryVerificationGeneratedAt,
		&record.AIModel, &record.AIPromptVersion, &aiGeneratedAt,
		&record.AITranslationModel, &record.AITranslationPromptVersion, &aiTranslationGeneratedAt,
		&record.AIPipelineVersion,
		&record.ProcessingStatus, &record.ProcessingAttempts, &nextRetryAt, &record.ProcessingFailureKind,
		&record.AITranslationStatus, &record.AITranslationAttempts, &translationNextRetryAt, &record.AITranslationFailureKind,
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
	if aiPublicAssistanceVerificationGeneratedAt != "" {
		generatedAt, err := time.Parse(time.RFC3339Nano, aiPublicAssistanceVerificationGeneratedAt)
		if err != nil {
			return IncidentRecord{}, fmt.Errorf("parse AI public assistance verification generation time: %w", err)
		}
		record.AIPublicAssistanceVerificationGeneratedAt = &generatedAt
	}
	if aiCategoryVerificationGeneratedAt != "" {
		generatedAt, err := time.Parse(time.RFC3339Nano, aiCategoryVerificationGeneratedAt)
		if err != nil {
			return IncidentRecord{}, fmt.Errorf("parse AI category verification generation time: %w", err)
		}
		record.AICategoryVerificationGeneratedAt = &generatedAt
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
