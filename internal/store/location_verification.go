package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/egekocabas/munichbrief/internal/location"
)

func locationOriginalSQL(run string) string {
	return `json_object('name',COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=` + run + ` AND kind='area_name'),''),'type',COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=` + run + ` AND kind='area_type'),''))`
}
func locationResultEligibility(job string) string {
	return ` AND EXISTS (SELECT 1 FROM post_processing_values assessment JOIN presentation_runs lr ON lr.id=` + job + `.presentation_run_id JOIN incidents li ON li.id=lr.incident_id WHERE assessment.job_id=` + job + `.id AND assessment.kind='location_assessment' AND json_valid(assessment.value) AND json_extract(assessment.value,'$.outcome') IN ('confirmed','corrected') AND json_extract(assessment.value,'$.source.source_hash')=li.content_hash AND json_extract(assessment.value,'$.source.context_hash')=li.context_hash AND json_extract(assessment.value,'$.resolver_version')=` + sqlStringLiteral(location.CatalogVersion) + `)`
}
func effectiveLocationSQL(run, field string) string {
	job := latestCompletePostProcessingJob(run, "location_verification", "'default'", "location_proposed", "location_assessment")
	return `COALESCE((SELECT json_extract(value,'$.` + field + `') FROM post_processing_values WHERE job_id=` + job + ` AND kind='location_proposed'),(SELECT value FROM presentation_values WHERE presentation_run_id=` + run + ` AND kind='area_` + field + `'),'')`
}
func locationInputsTx(ctx context.Context, tx *sql.Tx, run int64) (string, string, error) {
	var source location.Source
	var original string
	err := tx.QueryRowContext(ctx, `SELECT i.content_hash,i.context_hash,i.section_context,i.incident_number,`+locationOriginalSQL("r.id")+` FROM presentation_runs r JOIN incidents i ON i.id=r.incident_id AND i.content_hash=r.source_hash WHERE r.id=?`, run).Scan(&source.SourceHash, &source.ContextHash, &source.SectionContext, &source.ReportNumber, &original)
	if err != nil {
		return "", "", err
	}
	data, err := json.Marshal(source)
	return string(data), original, err
}

// Assessment JSON is protected admin-only data, never part of IncidentRecord.
func (s *Store) latestLocationAssessment(ctx context.Context, runID int64) (*location.Assessment, bool, error) {
	var data, sourceHash, contextHash string
	err := s.db.QueryRowContext(ctx, `SELECT v.value,i.content_hash,i.context_hash FROM post_processing_values v JOIN post_processing_jobs j ON j.id=v.job_id JOIN presentation_runs r ON r.id=j.presentation_run_id JOIN incidents i ON i.id=r.incident_id WHERE j.presentation_run_id=? AND j.processor_key='location_verification' AND j.status='succeeded' AND v.kind='location_assessment' ORDER BY j.completed_at DESC,j.id DESC LIMIT 1`, runID).Scan(&data, &sourceHash, &contextHash)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var a location.Assessment
	err = json.Unmarshal([]byte(data), &a)
	return &a, a.Source.SourceHash != sourceHash || a.Source.ContextHash != contextHash || a.ResolverVersion != location.CatalogVersion, err
}
