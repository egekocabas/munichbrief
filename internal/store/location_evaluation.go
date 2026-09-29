package store

import (
	"context"
	"encoding/json"

	"github.com/egekocabas/munichbrief/internal/location"
)

type LocationEvaluationInput struct {
	IncidentID int64
	Values     map[string]string
}

// LocationEvaluationInputs reads current canonical presentations. The caller
// must use an isolated snapshot; this does not queue or apply any correction.
func (s *Store) LocationEvaluationInputs(ctx context.Context) ([]LocationEvaluationInput, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.title_de,COALESCE(i.body_de,''),i.content_hash,i.context_hash,i.section_context,i.incident_number,`+locationOriginalSQL("pr.id")+`,COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=pr.id AND kind='title_de'),''),COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=pr.id AND kind='summary_de'),'') FROM incidents i JOIN presentation_runs pr ON pr.id=`+latestPresentationRun+` ORDER BY i.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []LocationEvaluationInput
	for rows.Next() {
		var r LocationEvaluationInput
		var title, body, original, pTitle, pSummary string
		var source location.Source
		if err := rows.Scan(&r.IncidentID, &title, &body, &source.SourceHash, &source.ContextHash, &source.SectionContext, &source.ReportNumber, &original, &pTitle, &pSummary); err != nil {
			return nil, err
		}
		data, err := json.Marshal(source)
		if err != nil {
			return nil, err
		}
		r.Values = map[string]string{"original_title": title, "incident_body": body, "location_source": string(data), "location_original": original, "title_de": pTitle, "summary_de": pSummary}
		result = append(result, r)
	}
	return result, rows.Err()
}
