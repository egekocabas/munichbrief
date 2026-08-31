package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AdminTranslationFilter selects one operational translation subset.
type AdminTranslationFilter string

const (
	AdminTranslationsAll         AdminTranslationFilter = "all"
	AdminTranslationsPublished   AdminTranslationFilter = "published"
	AdminTranslationsUnpublished AdminTranslationFilter = "unpublished"
	AdminTranslationsNeverQueued AdminTranslationFilter = "never_queued"
	AdminTranslationsActive      AdminTranslationFilter = "active"
	AdminTranslationsAttention   AdminTranslationFilter = "attention"
)

// AdminCanonicalCoverage reports the current source and canonical German
// presentation counts used as translation coverage's denominator.
type AdminCanonicalCoverage struct {
	Total     int
	Published int
}

// AdminLanguageCoverage separates effective reader publication from the
// newest replacement attempt's operational state. Active and Attention may
// overlap Published when a previous successful translation remains effective.
type AdminLanguageCoverage struct {
	Language             string
	Eligible             int
	Published            int
	Unpublished          int
	NeverQueued          int
	Active               int
	Attention            int
	ReplacementAttention int
}

// AdminTranslationIncident is one current canonical presentation paired with
// the effective translation and latest attempt for a selected language.
type AdminTranslationIncident struct {
	IncidentID           int64
	CanonicalTitle       string
	PublishedAt          time.Time
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

// AdminTranslationCoverage returns canonical German coverage plus a row for
// every requested registered translation language.
func (s *Store) AdminTranslationCoverage(ctx context.Context, sourceMode string, languages []string) (AdminCanonicalCoverage, []AdminLanguageCoverage, error) {
	statusCondition, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return AdminCanonicalCoverage{}, nil, err
	}
	languageValues, args, err := adminTranslationLanguageValues(languages)
	if err != nil {
		return AdminCanonicalCoverage{}, nil, err
	}
	var canonical AdminCanonicalCoverage
	canonicalQuery := `WITH canonical_runs AS (` + adminCurrentCanonicalRunsSQL(statusCondition) + `)
		SELECT (SELECT COUNT(*) FROM incidents i JOIN source_documents d ON d.id=i.source_document_id WHERE ` + statusCondition + `), COUNT(*) FROM canonical_runs`
	if err := s.db.QueryRowContext(ctx, canonicalQuery).Scan(&canonical.Total, &canonical.Published); err != nil {
		return AdminCanonicalCoverage{}, nil, fmt.Errorf("read admin canonical coverage: %w", err)
	}
	if len(languages) == 0 {
		return canonical, nil, nil
	}
	query := adminTranslationMatrixSQL(statusCondition, languageValues) + `
		SELECT language.language_code, COUNT(matrix.incident_id),
			COALESCE(SUM(CASE WHEN matrix.success_id IS NOT NULL THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN matrix.incident_id IS NOT NULL AND matrix.success_id IS NULL THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN matrix.incident_id IS NOT NULL AND matrix.latest_id IS NULL THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN matrix.latest_status IN ('pending','running') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN matrix.latest_status IN ('needs_review','failed','skipped') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN matrix.success_id IS NOT NULL AND matrix.latest_status IN ('needs_review','failed','skipped') THEN 1 ELSE 0 END),0)
		FROM requested_languages language LEFT JOIN translation_matrix matrix ON matrix.language_code=language.language_code
		GROUP BY language.language_code,language.language_order ORDER BY language.language_order`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return AdminCanonicalCoverage{}, nil, fmt.Errorf("read admin translation coverage: %w", err)
	}
	defer rows.Close()
	coverage := make([]AdminLanguageCoverage, 0, len(languages))
	for rows.Next() {
		var item AdminLanguageCoverage
		if err := rows.Scan(&item.Language, &item.Eligible, &item.Published, &item.Unpublished, &item.NeverQueued, &item.Active, &item.Attention, &item.ReplacementAttention); err != nil {
			return AdminCanonicalCoverage{}, nil, fmt.Errorf("scan admin translation coverage: %w", err)
		}
		coverage = append(coverage, item)
	}
	if err := rows.Err(); err != nil {
		return AdminCanonicalCoverage{}, nil, fmt.Errorf("iterate admin translation coverage: %w", err)
	}
	return canonical, coverage, nil
}

// ListAdminTranslationIncidents returns a bounded language-specific operations
// list ordered by source publication time.
func (s *Store) ListAdminTranslationIncidents(ctx context.Context, limit, offset int, sourceMode, languageCode string, filter AdminTranslationFilter) ([]AdminTranslationIncident, int, error) {
	if limit < 1 {
		return nil, 0, errors.New("limit must be positive")
	}
	if offset < 0 {
		return nil, 0, errors.New("offset must not be negative")
	}
	condition, err := adminTranslationFilterCondition(filter)
	if err != nil {
		return nil, 0, err
	}
	statusCondition, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return nil, 0, err
	}
	languageValues, args, err := adminTranslationLanguageValues([]string{languageCode})
	if err != nil {
		return nil, 0, err
	}
	base := adminTranslationMatrixSQL(statusCondition, languageValues)
	var total int
	if err := s.db.QueryRowContext(ctx, base+` SELECT COUNT(*) FROM translation_matrix WHERE `+condition, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count admin translation incidents: %w", err)
	}
	query := base + `
		SELECT matrix.incident_id, COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=matrix.run_id AND kind='title_de'), ''),
			d.published_at, matrix.language_code,
			COALESCE((SELECT value FROM post_processing_values WHERE job_id=matrix.success_id AND kind='title'), ''),
			COALESCE((SELECT value FROM post_processing_values WHERE job_id=matrix.success_id AND kind='summary'), ''),
			COALESCE(success.model_identity, ''), COALESCE(success.prompt_version, ''), COALESCE(success.completed_at, ''),
			COALESCE(latest.status, ''), COALESCE(latest.status_reason, ''), COALESCE(latest.status_detail, ''),
			COALESCE(latest.attempt_count, 0), COALESCE(latest.next_retry_at, ''), COALESCE(latest.failure_kind, ''),
			COALESCE(latest.model_identity, ''), COALESCE(latest.prompt_version, ''), COALESCE(latest.updated_at, '')
		FROM translation_matrix matrix
		JOIN incidents i ON i.id=matrix.incident_id JOIN source_documents d ON d.id=i.source_document_id
		LEFT JOIN post_processing_jobs success ON success.id=matrix.success_id
		LEFT JOIN post_processing_jobs latest ON latest.id=matrix.latest_id
		WHERE ` + condition + ` ORDER BY d.published_at DESC,matrix.incident_id DESC LIMIT @limit OFFSET @offset`
	queryArgs := append(append([]any(nil), args...), sql.Named("limit", limit), sql.Named("offset", offset))
	rows, err := s.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list admin translation incidents: %w", err)
	}
	defer rows.Close()
	items := make([]AdminTranslationIncident, 0, min(limit, total))
	for rows.Next() {
		var item AdminTranslationIncident
		var publishedAt, generatedAt, nextRetryAt, attemptUpdatedAt string
		if err := rows.Scan(
			&item.IncidentID, &item.CanonicalTitle, &publishedAt, &item.Language,
			&item.Title, &item.Summary, &item.Model, &item.PromptVersion, &generatedAt,
			&item.Status, &item.StatusReason, &item.StatusDetail, &item.Attempts, &nextRetryAt, &item.FailureKind,
			&item.AttemptModel, &item.AttemptPromptVersion, &attemptUpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan admin translation incident: %w", err)
		}
		if item.PublishedAt, err = time.Parse(time.RFC3339Nano, publishedAt); err != nil {
			return nil, 0, fmt.Errorf("parse admin translation publication time: %w", err)
		}
		if item.GeneratedAt, err = optionalAdminTranslationTime(generatedAt); err != nil {
			return nil, 0, fmt.Errorf("parse admin translation generation time: %w", err)
		}
		if item.NextRetryAt, err = optionalAdminTranslationTime(nextRetryAt); err != nil {
			return nil, 0, fmt.Errorf("parse admin translation retry time: %w", err)
		}
		if item.AttemptUpdatedAt, err = optionalAdminTranslationTime(attemptUpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("parse admin translation attempt time: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate admin translation incidents: %w", err)
	}
	return items, total, nil
}

func adminTranslationLanguageValues(languages []string) (string, []any, error) {
	values := make([]string, 0, len(languages))
	args := make([]any, 0, len(languages))
	seen := make(map[string]struct{}, len(languages))
	for index, code := range languages {
		code = strings.TrimSpace(code)
		if code == "" {
			return "", nil, errors.New("translation language is required")
		}
		if _, duplicate := seen[code]; duplicate {
			return "", nil, errors.New("translation languages must be unique")
		}
		seen[code] = struct{}{}
		name := fmt.Sprintf("language_%d", index)
		values = append(values, fmt.Sprintf("(@%s,%d)", name, index))
		args = append(args, sql.Named(name, code))
	}
	return strings.Join(values, ","), args, nil
}

func adminCurrentCanonicalRunsSQL(statusCondition string) string {
	return `SELECT r.id AS run_id,r.incident_id FROM presentation_runs r
		JOIN incidents i ON i.id=r.incident_id AND i.content_hash=r.source_hash
		JOIN source_documents d ON d.id=i.source_document_id
		WHERE ` + statusCondition + ` AND r.status='complete' AND r.pipeline_version='` + PipelineVersion + `' AND r.legacy=0
		AND r.id=(SELECT candidate.id FROM presentation_runs candidate
			WHERE candidate.incident_id=i.id AND candidate.source_hash=i.content_hash AND candidate.status='complete'
			AND candidate.pipeline_version='` + PipelineVersion + `' AND candidate.legacy=0
			ORDER BY candidate.completed_at DESC,candidate.id DESC LIMIT 1)`
}

func adminTranslationMatrixSQL(statusCondition, languageValues string) string {
	return `WITH requested_languages(language_code,language_order) AS (VALUES ` + languageValues + `),
		canonical_runs AS (` + adminCurrentCanonicalRunsSQL(statusCondition) + `),
		ranked_successes AS (
			SELECT job.*,ROW_NUMBER() OVER (PARTITION BY job.presentation_run_id,job.scope_key ORDER BY job.completed_at DESC,job.id DESC) AS rank
			FROM post_processing_jobs job JOIN canonical_runs canonical ON canonical.run_id=job.presentation_run_id
			JOIN requested_languages language ON language.language_code=job.scope_key
			WHERE job.processor_key='translation' AND job.status='succeeded'
			AND EXISTS (SELECT 1 FROM post_processing_values value WHERE value.job_id=job.id AND value.kind='title')
			AND EXISTS (SELECT 1 FROM post_processing_values value WHERE value.job_id=job.id AND value.kind='summary')
		),
		selected_successes AS (SELECT * FROM ranked_successes WHERE rank=1),
		ranked_attempts AS (
			SELECT job.*,ROW_NUMBER() OVER (PARTITION BY job.presentation_run_id,job.scope_key ORDER BY job.created_at DESC,job.id DESC) AS rank
			FROM post_processing_jobs job JOIN canonical_runs canonical ON canonical.run_id=job.presentation_run_id
			JOIN requested_languages language ON language.language_code=job.scope_key
			WHERE job.processor_key='translation'
		),
		latest_attempts AS (SELECT * FROM ranked_attempts WHERE rank=1),
		translation_matrix AS (
			SELECT canonical.incident_id,canonical.run_id,language.language_code,language.language_order,
				success.id AS success_id,latest.id AS latest_id,COALESCE(latest.status,'') AS latest_status
			FROM canonical_runs canonical CROSS JOIN requested_languages language
			LEFT JOIN selected_successes success ON success.presentation_run_id=canonical.run_id AND success.scope_key=language.language_code
			LEFT JOIN latest_attempts latest ON latest.presentation_run_id=canonical.run_id AND latest.scope_key=language.language_code
		)`
}

func adminTranslationFilterCondition(filter AdminTranslationFilter) (string, error) {
	switch filter {
	case AdminTranslationsAll:
		return "1=1", nil
	case AdminTranslationsPublished:
		return "success_id IS NOT NULL", nil
	case AdminTranslationsUnpublished:
		return "success_id IS NULL", nil
	case AdminTranslationsNeverQueued:
		return "latest_id IS NULL", nil
	case AdminTranslationsActive:
		return "latest_status IN ('pending','running')", nil
	case AdminTranslationsAttention:
		return "latest_status IN ('needs_review','failed','skipped')", nil
	default:
		return "", errors.New("invalid admin translation filter")
	}
}

func optionalAdminTranslationTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
