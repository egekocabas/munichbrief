package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AdminVerificationFilter selects one operational verifier subset. Successful
// result filters intentionally overlap attention when a newer replacement has
// failed and the previous result remains effective.
type AdminVerificationFilter string

const (
	AdminVerificationsAttention   AdminVerificationFilter = "attention"
	AdminVerificationsUnverified  AdminVerificationFilter = "unverified"
	AdminVerificationsAll         AdminVerificationFilter = "all"
	AdminVerificationsNeverQueued AdminVerificationFilter = "never_queued"
	AdminVerificationsActive      AdminVerificationFilter = "active"
	AdminVerificationsConfirmed   AdminVerificationFilter = "confirmed"
	AdminVerificationsCorrected   AdminVerificationFilter = "corrected"
	AdminVerificationsSkipped     AdminVerificationFilter = "skipped"
)

// AdminVerificationFieldSpec is one canonical-to-corrected value mapping from
// a registered verification contract.
type AdminVerificationFieldSpec struct {
	OriginalKind  string
	CorrectedKind string
}

// AdminVerificationSpec identifies one registered verification scope.
type AdminVerificationSpec struct {
	ProcessorKey string
	ScopeKey     string
	VerdictKind  string
	Fields       []AdminVerificationFieldSpec
}

// AdminVerificationCoverage summarizes current canonical presentations for a
// single verification scope.
type AdminVerificationCoverage struct {
	Current              int
	Verified             int
	Unverified           int
	NeverQueued          int
	Active               int
	Attention            int
	Confirmed            int
	Corrected            int
	Skipped              int
	ReplacementAttention int
}

// AdminVerificationIncident combines the latest valid successful result with
// the newest attempt for one current canonical presentation.
type AdminVerificationIncident struct {
	IncidentID           int64
	PresentationRunID    int64
	CanonicalTitle       string
	PublishedAt          time.Time
	OriginalValues       []string
	EffectiveValues      []string
	IsCorrect            *bool
	SuccessModel         string
	SuccessPromptVersion string
	SuccessCompletedAt   *time.Time
	Status               string
	StatusReason         string
	StatusDetail         string
	Attempts             int
	NextRetryAt          *time.Time
	FailureKind          string
	ErrorMessage         string
	RequestKind          string
	AttemptModel         string
	AttemptPromptVersion string
	AttemptUpdatedAt     *time.Time
}

// AdminVerificationCoverageFor returns current-canonical operational counts
// for one registered verifier scope.
func (s *Store) AdminVerificationCoverageFor(ctx context.Context, sourceMode string, spec AdminVerificationSpec) (AdminVerificationCoverage, error) {
	base, args, err := adminVerificationMatrixSQL(sourceMode, spec)
	if err != nil {
		return AdminVerificationCoverage{}, err
	}
	var coverage AdminVerificationCoverage
	query := base + ` SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN success_id IS NOT NULL THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN success_id IS NULL THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN latest_id IS NULL THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN latest_status IN ('pending','running') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN latest_status IN ('needs_review','failed','skipped') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN success_verdict='true' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN success_verdict='false' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN latest_status='skipped' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN success_id IS NOT NULL AND latest_status IN ('needs_review','failed','skipped') THEN 1 ELSE 0 END),0)
		FROM verification_matrix`
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&coverage.Current, &coverage.Verified, &coverage.Unverified, &coverage.NeverQueued,
		&coverage.Active, &coverage.Attention, &coverage.Confirmed, &coverage.Corrected,
		&coverage.Skipped, &coverage.ReplacementAttention,
	); err != nil {
		return AdminVerificationCoverage{}, fmt.Errorf("read admin verification coverage: %w", err)
	}
	return coverage, nil
}

// ListAdminVerificationIncidents returns one bounded, current-canonical
// verifier review list ordered by source publication time.
func (s *Store) ListAdminVerificationIncidents(ctx context.Context, limit, offset int, sourceMode string, spec AdminVerificationSpec, filter AdminVerificationFilter) ([]AdminVerificationIncident, int, error) {
	if limit < 1 {
		return nil, 0, errors.New("limit must be positive")
	}
	if offset < 0 {
		return nil, 0, errors.New("offset must not be negative")
	}
	condition, err := adminVerificationFilterCondition(filter)
	if err != nil {
		return nil, 0, err
	}
	base, args, err := adminVerificationMatrixSQL(sourceMode, spec)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.db.QueryRowContext(ctx, base+` SELECT COUNT(*) FROM verification_matrix WHERE `+condition, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count admin verification incidents: %w", err)
	}

	var valueColumns strings.Builder
	for index := range spec.Fields {
		originalName := fmt.Sprintf("original_kind_%d", index)
		correctedName := fmt.Sprintf("corrected_kind_%d", index)
		originalValue := `(SELECT value FROM presentation_values WHERE presentation_run_id=matrix.run_id AND kind=@` + originalName + ` LIMIT 1)`
		valueColumns.WriteString(`,COALESCE(` + originalValue + `,''),COALESCE((SELECT value FROM post_processing_values WHERE job_id=matrix.success_id AND kind=@` + correctedName + ` LIMIT 1),` + originalValue + `,'')`)
	}
	query := base + ` SELECT matrix.incident_id,matrix.run_id,
		COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=matrix.run_id AND kind='title_de' LIMIT 1),''),
		d.published_at` + valueColumns.String() + `,matrix.success_verdict,
		COALESCE(success.model_identity,''),COALESCE(success.prompt_version,''),COALESCE(success.completed_at,''),
		COALESCE(latest.status,''),COALESCE(latest.status_reason,''),COALESCE(latest.status_detail,''),COALESCE(latest.attempt_count,0),
		COALESCE(latest.next_retry_at,''),COALESCE(latest.failure_kind,''),COALESCE(latest.error_message,''),COALESCE(latest.request_kind,''),
		COALESCE(latest.model_identity,''),COALESCE(latest.prompt_version,''),COALESCE(latest.updated_at,'')
		FROM verification_matrix matrix
		JOIN incidents i ON i.id=matrix.incident_id JOIN source_documents d ON d.id=i.source_document_id
		LEFT JOIN post_processing_jobs success ON success.id=matrix.success_id
		LEFT JOIN post_processing_jobs latest ON latest.id=matrix.latest_id
		WHERE ` + condition + ` ORDER BY d.published_at DESC,matrix.incident_id DESC LIMIT @limit OFFSET @offset`
	queryArgs := append(append([]any(nil), args...), sql.Named("limit", limit), sql.Named("offset", offset))
	rows, err := s.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list admin verification incidents: %w", err)
	}
	defer rows.Close()
	items := make([]AdminVerificationIncident, 0, min(limit, total))
	for rows.Next() {
		item := AdminVerificationIncident{OriginalValues: make([]string, len(spec.Fields)), EffectiveValues: make([]string, len(spec.Fields))}
		var publishedAt, verdict, successCompletedAt, nextRetryAt, attemptUpdatedAt string
		destinations := []any{&item.IncidentID, &item.PresentationRunID, &item.CanonicalTitle, &publishedAt}
		for index := range spec.Fields {
			destinations = append(destinations, &item.OriginalValues[index], &item.EffectiveValues[index])
		}
		destinations = append(destinations, &verdict, &item.SuccessModel, &item.SuccessPromptVersion, &successCompletedAt,
			&item.Status, &item.StatusReason, &item.StatusDetail, &item.Attempts, &nextRetryAt, &item.FailureKind,
			&item.ErrorMessage, &item.RequestKind, &item.AttemptModel, &item.AttemptPromptVersion, &attemptUpdatedAt)
		if err := rows.Scan(destinations...); err != nil {
			return nil, 0, fmt.Errorf("scan admin verification incident: %w", err)
		}
		if item.PublishedAt, err = time.Parse(time.RFC3339Nano, publishedAt); err != nil {
			return nil, 0, fmt.Errorf("parse admin verification publication time: %w", err)
		}
		switch verdict {
		case "true", "false":
			value := verdict == "true"
			item.IsCorrect = &value
		case "":
		default:
			return nil, 0, fmt.Errorf("invalid stored verification verdict %q", verdict)
		}
		if item.SuccessCompletedAt, err = optionalAdminVerificationTime(successCompletedAt); err != nil {
			return nil, 0, fmt.Errorf("parse verification success time: %w", err)
		}
		if item.NextRetryAt, err = optionalAdminVerificationTime(nextRetryAt); err != nil {
			return nil, 0, fmt.Errorf("parse verification retry time: %w", err)
		}
		if item.AttemptUpdatedAt, err = optionalAdminVerificationTime(attemptUpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("parse verification attempt time: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate admin verification incidents: %w", err)
	}
	return items, total, nil
}

func adminVerificationMatrixSQL(sourceMode string, spec AdminVerificationSpec) (string, []any, error) {
	statusCondition, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(spec.ProcessorKey) == "" || strings.TrimSpace(spec.ScopeKey) == "" || strings.TrimSpace(spec.VerdictKind) == "" || len(spec.Fields) == 0 {
		return "", nil, errors.New("verification processor, scope, verdict, and fields are required")
	}
	args := []any{
		sql.Named("verification_processor", spec.ProcessorKey),
		sql.Named("verification_scope", spec.ScopeKey),
		sql.Named("verdict_kind", spec.VerdictKind),
	}
	var successRequirements strings.Builder
	successRequirements.WriteString(` AND EXISTS (SELECT 1 FROM post_processing_values output WHERE output.job_id=job.id AND output.kind=@verdict_kind AND output.value IN ('true','false'))`)
	seenOriginals := make(map[string]struct{}, len(spec.Fields))
	seenCorrected := make(map[string]struct{}, len(spec.Fields))
	for index, field := range spec.Fields {
		field.OriginalKind = strings.TrimSpace(field.OriginalKind)
		field.CorrectedKind = strings.TrimSpace(field.CorrectedKind)
		if field.OriginalKind == "" || field.CorrectedKind == "" {
			return "", nil, errors.New("verification original and corrected kinds are required")
		}
		if _, duplicate := seenOriginals[field.OriginalKind]; duplicate {
			return "", nil, errors.New("verification original kinds must be unique")
		}
		if _, duplicate := seenCorrected[field.CorrectedKind]; duplicate {
			return "", nil, errors.New("verification corrected kinds must be unique")
		}
		originalName := fmt.Sprintf("original_kind_%d", index)
		correctedName := fmt.Sprintf("corrected_kind_%d", index)
		args = append(args, sql.Named(originalName, field.OriginalKind), sql.Named(correctedName, field.CorrectedKind))
		successRequirements.WriteString(` AND EXISTS (SELECT 1 FROM post_processing_values output WHERE output.job_id=job.id AND output.kind=@` + correctedName + `)`)
		seenOriginals[field.OriginalKind] = struct{}{}
		seenCorrected[field.CorrectedKind] = struct{}{}
	}
	query := `WITH canonical_runs AS (` + adminCurrentCanonicalRunsSQL(statusCondition) + `),
		ranked_successes AS (
			SELECT job.*,ROW_NUMBER() OVER (PARTITION BY job.presentation_run_id ORDER BY job.completed_at DESC,job.id DESC) AS rank
			FROM post_processing_jobs job JOIN canonical_runs canonical ON canonical.run_id=job.presentation_run_id
			WHERE job.processor_key=@verification_processor AND job.scope_key=@verification_scope AND job.status='succeeded'` + successRequirements.String() + `
		),
		selected_successes AS (SELECT * FROM ranked_successes WHERE rank=1),
		ranked_attempts AS (
			SELECT job.*,ROW_NUMBER() OVER (PARTITION BY job.presentation_run_id ORDER BY job.created_at DESC,job.id DESC) AS rank
			FROM post_processing_jobs job JOIN canonical_runs canonical ON canonical.run_id=job.presentation_run_id
			WHERE job.processor_key=@verification_processor AND job.scope_key=@verification_scope
		),
		latest_attempts AS (SELECT * FROM ranked_attempts WHERE rank=1),
		verification_matrix AS (
			SELECT canonical.incident_id,canonical.run_id,success.id AS success_id,latest.id AS latest_id,
				COALESCE((SELECT value FROM post_processing_values WHERE job_id=success.id AND kind=@verdict_kind LIMIT 1),'') AS success_verdict,
				COALESCE(latest.status,'') AS latest_status
			FROM canonical_runs canonical
			LEFT JOIN selected_successes success ON success.presentation_run_id=canonical.run_id
			LEFT JOIN latest_attempts latest ON latest.presentation_run_id=canonical.run_id
		)`
	return query, args, nil
}

func adminVerificationFilterCondition(filter AdminVerificationFilter) (string, error) {
	switch filter {
	case AdminVerificationsAttention:
		return "latest_status IN ('needs_review','failed','skipped')", nil
	case AdminVerificationsUnverified:
		return "success_id IS NULL", nil
	case AdminVerificationsAll:
		return "1=1", nil
	case AdminVerificationsNeverQueued:
		return "latest_id IS NULL", nil
	case AdminVerificationsActive:
		return "latest_status IN ('pending','running')", nil
	case AdminVerificationsConfirmed:
		return "success_verdict='true'", nil
	case AdminVerificationsCorrected:
		return "success_verdict='false'", nil
	case AdminVerificationsSkipped:
		return "latest_status='skipped'", nil
	default:
		return "", errors.New("invalid admin verification filter")
	}
}

func optionalAdminVerificationTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
