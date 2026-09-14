package store

import (
	"context"
	"strings"
	"testing"

	langregistry "github.com/egekocabas/munichbrief/internal/languages"
)

// Keep the original projection as a compatibility oracle while optimizing the
// derived SQL. Shared selection constants retain the current pipeline contract.
func originalReaderProjection() string {
	var languages []string
	for _, l := range langregistry.Registered() {
		languages = append(languages, "SELECT "+sqlStringLiteral(l.Code)+" AS language")
	}
	val := func(kind string) string {
		return "COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=" + latestPresentationRun + " AND kind=" + sqlStringLiteral(kind) + "),'')"
	}
	translation := strings.ReplaceAll(latestCompletePublicTranslationJob, "@language", "l.language")
	translated := func(kind string) string {
		return "COALESCE((SELECT value FROM post_processing_values WHERE job_id=" + translation + " AND kind=" + sqlStringLiteral(kind) + "),'')"
	}
	corrected := func(job, kind, fallback string) string {
		return "COALESCE((SELECT value FROM post_processing_values WHERE job_id=" + job + " AND kind=" + sqlStringLiteral(kind) + ")," + val(fallback) + ")"
	}
	canonical := sqlStringLiteral(langregistry.Canonical(langregistry.Registered()).Code)
	ready := strings.NewReplacer("@canonical_language", canonical, "@language", "l.language").Replace(publicReadyCondition)
	projection := `CREATE VIEW reader_projection AS SELECT *, CASE WHEN event_time<>'' THEN 0 WHEN day_part<>'' THEN 1 ELSE 2 END AS time_group,
 reader_normalize(title||' '||summary||' '||area||' '||number) AS search_text FROM (SELECT i.id AS incident_id,l.language,` + latestPresentationRun + ` AS run_id,
 CASE WHEN l.language=` + canonical + ` THEN ` + val("title_de") + ` ELSE ` + translated("title") + ` END AS title,
 CASE WHEN l.language=` + canonical + ` THEN ` + val("summary_de") + ` ELSE ` + translated("summary") + ` END AS summary,
 ` + val("area_name") + ` AS area,` + corrected(latestCompleteCategoryVerificationJob, "corrected_category", "category") + ` AS category,
 ` + corrected(latestCompletePublicAssistanceVerificationJob, "corrected_public_assistance_status", "public_assistance_status") + ` AS assistance,
 ` + val("event_start_date") + ` AS event_date,` + val("event_start_time") + ` AS event_time,` + val("event_day_part") + ` AS day_part,
 d.published_at,reader_berlin_date(d.published_at) AS published_date,i.position,i.incident_number AS number
 FROM incidents i JOIN source_documents d ON d.id=i.source_document_id CROSS JOIN (` + strings.Join(languages, " UNION ALL ") + `) l WHERE ` + ready + `)`
	return strings.TrimPrefix(projection, "CREATE VIEW reader_projection AS ")
}

func assertReaderProjectionParity(t *testing.T, db *Store) {
	t.Helper()
	current := "SELECT " + readerIndexColumns + " FROM reader_documents"
	original := "SELECT " + readerIndexColumns + " FROM (" + originalReaderProjection() + ")"
	for _, query := range []string{current + " EXCEPT " + original, original + " EXCEPT " + current} {
		rows, err := db.db.QueryContext(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		if rows.Next() {
			rows.Close()
			t.Fatal("optimized reader index differs from original projection")
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
