package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	langregistry "github.com/egekocabas/munichbrief/internal/languages"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"modernc.org/sqlite"
)

// ReaderFilters contains the last applied search, never an executable query.
type ReaderFilters struct {
	Text       string `json:"q,omitempty"`
	Area       string `json:"area,omitempty"`
	Category   string `json:"category,omitempty"`
	Number     string `json:"number,omitempty"`
	Assistance string `json:"assistance,omitempty"`
	DateField  string `json:"date_field,omitempty"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
}

func (f ReaderFilters) Active() bool {
	return f.Text != "" || f.Area != "" || f.Category != "" || f.Number != "" || f.Assistance != "" || f.From != "" || f.To != ""
}
func (f ReaderFilters) Validate() error {
	for _, v := range []string{f.Text, f.Area, f.Number} {
		if !utf8.ValidString(v) || utf8.RuneCountInString(v) > 200 || strings.ContainsRune(v, 0) {
			return errors.New("invalid search text")
		}
	}
	if f.DateField != "" && f.DateField != "published" && f.DateField != "incident" {
		return errors.New("invalid date field")
	}
	if f.Assistance != "" && f.Assistance != "yes" && f.Assistance != "no" {
		return errors.New("invalid assistance")
	}
	switch f.Category {
	case "", "violence", "theft_burglary", "robbery_extortion", "sexual_offense", "fraud_cyber", "drugs", "traffic", "fire_hazard", "property_damage", "missing_wanted", "police_operation", "other":
	default:
		return errors.New("invalid category")
	}
	for _, v := range []string{f.From, f.To} {
		if v != "" {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return errors.New("invalid date")
			}
		}
	}
	if f.From != "" && f.To != "" && f.From > f.To {
		return errors.New("reversed date range")
	}
	return nil
}

type ReaderQuery struct {
	Language, SourceMode, View string
	Limit, Offset              int
	Filters                    ReaderFilters
}
type ReaderResult struct {
	Records []IncidentRecord
	Total   int
}

// NFC and Unicode folding preserve marks that carry meaning in Hindi and other
// scripts. Treat Turkish dotted/dotless I alike for discoverability.
func normalizeReaderText(s string) string {
	s = strings.NewReplacer("İ", "i", "ı", "i").Replace(s)
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(s)))
}
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("reader_normalize", 1, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		s, _ := a[0].(string)
		return normalizeReaderText(s), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("reader_berlin_date", 1, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		s, _ := a[0].(string)
		t, e := time.Parse(time.RFC3339Nano, s)
		if e != nil {
			return "", nil
		}
		loc, e := time.LoadLocation("Europe/Berlin")
		if e != nil {
			return nil, e
		}
		return t.In(loc).Format("2006-01-02"), nil
	})
}

// Bump the contract comment when registered SQL function semantics change.
// The complete SQL definitions also capture pipeline/language/selection changes.
// v3 rebuilds normalized text after the x/text v0.42.0 normalization fixes.
const readerIndexContract = "reader-index-v3"
const readerIndexColumns = "incident_id,language,run_id,title,summary,area,category,assistance,event_date,event_time,day_part,published_at,published_date,position,number,time_group,search_text"

type readerIndexDefinition struct{ name, kind, sql string }

func readerIndexDefinitions() []readerIndexDefinition {
	var languages []string
	for _, l := range langregistry.Registered() {
		languages = append(languages, "SELECT "+sqlStringLiteral(l.Code)+" AS language")
	}
	val := func(kind string) string {
		return "COALESCE((SELECT value FROM presentation_values WHERE presentation_run_id=pr.id AND kind=" + sqlStringLiteral(kind) + "),'')"
	}
	translated := func(kind string) string {
		return "COALESCE((SELECT value FROM post_processing_values WHERE job_id=translation_job.id AND kind=" + sqlStringLiteral(kind) + "),'')"
	}
	corrected := func(job, kind, fallback string) string {
		return "COALESCE((SELECT value FROM post_processing_values WHERE job_id=" + job + ".id AND kind=" + sqlStringLiteral(kind) + ")," + val(fallback) + ")"
	}
	canonical := sqlStringLiteral(langregistry.Canonical(langregistry.Registered()).Code)
	projection := `CREATE VIEW reader_projection AS /* ` + readerIndexContract + ` */ SELECT *, CASE WHEN event_time<>'' THEN 0 WHEN day_part<>'' THEN 1 ELSE 2 END AS time_group,
 reader_normalize(title||' '||summary||' '||area||' '||number) AS search_text FROM (SELECT i.id AS incident_id,l.language,pr.id AS run_id,
 CASE WHEN l.language=` + canonical + ` THEN ` + val("title_de") + ` ELSE ` + translated("title") + ` END AS title,
 CASE WHEN l.language=` + canonical + ` THEN ` + val("summary_de") + ` ELSE ` + translated("summary") + ` END AS summary,
 ` + val("area_name") + ` AS area,` + corrected("category_job", "corrected_category", "category") + ` AS category,
 ` + corrected("assistance_job", "corrected_public_assistance_status", "public_assistance_status") + ` AS assistance,
 ` + val("event_start_date") + ` AS event_date,` + val("event_start_time") + ` AS event_time,` + val("event_day_part") + ` AS day_part,
 d.published_at,reader_berlin_date(d.published_at) AS published_date,i.position,i.incident_number AS number
 FROM incidents i JOIN source_documents d ON d.id=i.source_document_id
 JOIN presentation_runs pr ON pr.id=` + latestPresentationRun + `
 LEFT JOIN post_processing_jobs category_job ON category_job.id=` + latestCompletePostProcessingJob("pr.id", "category_verification", "'default'", "is_correct", "corrected_category") + `
 LEFT JOIN post_processing_jobs assistance_job ON assistance_job.id=` + latestCompletePostProcessingJob("pr.id", "public_assistance_verification", "'default'", "is_correct", "corrected_public_assistance_status", "corrected_public_assistance_types") + `
 CROSS JOIN (` + strings.Join(languages, " UNION ALL ") + `) l
 LEFT JOIN post_processing_jobs translation_job ON translation_job.id=` + latestCompletePostProcessingJob("pr.id", "translation", "l.language", "title", "summary") + `
 WHERE l.language=` + canonical + ` OR translation_job.id IS NOT NULL)`
	definitions := []readerIndexDefinition{{name: "reader_projection", kind: "VIEW", sql: projection}}
	refresh := func(ids string) string {
		return "DELETE FROM reader_documents WHERE incident_id IN (" + ids + "); INSERT INTO reader_documents(" + readerIndexColumns + ") SELECT " + readerIndexColumns + " FROM reader_projection WHERE incident_id IN (" + ids + ");"
	}
	for _, spec := range []struct{ table, ids string }{
		{"incidents", "SELECT %s.id"},
		{"source_documents", "SELECT id FROM incidents WHERE source_document_id=%s.id"},
		{"presentation_runs", "SELECT %s.incident_id"},
		{"presentation_values", "SELECT incident_id FROM presentation_runs WHERE id=%s.presentation_run_id"},
		{"post_processing_jobs", "SELECT incident_id FROM presentation_runs WHERE id=%s.presentation_run_id"},
		{"post_processing_values", "SELECT r.incident_id FROM presentation_runs r JOIN post_processing_jobs j ON j.presentation_run_id=r.id WHERE j.id=%s.job_id"},
	} {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			name := "reader_refresh_" + spec.table + "_" + strings.ToLower(event)
			ref := "new"
			if event == "DELETE" {
				ref = "old"
			}
			ids := fmt.Sprintf(spec.ids, ref)
			if event == "UPDATE" {
				ids += " UNION " + fmt.Sprintf(spec.ids, "old")
			}
			when := ""
			if spec.table == "presentation_values" {
				when = " WHEN EXISTS(SELECT 1 FROM presentation_runs WHERE id=" + ref + ".presentation_run_id AND status='complete')"
				if event == "UPDATE" {
					when += " OR EXISTS(SELECT 1 FROM presentation_runs WHERE id=old.presentation_run_id AND status='complete')"
				}
			}
			if spec.table == "post_processing_values" {
				when = " WHEN EXISTS(SELECT 1 FROM post_processing_jobs WHERE id=" + ref + ".job_id AND status='succeeded')"
				if event == "UPDATE" {
					when += " OR EXISTS(SELECT 1 FROM post_processing_jobs WHERE id=old.job_id AND status='succeeded')"
				}
			}
			if spec.table == "presentation_runs" || spec.table == "post_processing_jobs" {
				state := "complete"
				if spec.table == "post_processing_jobs" {
					state = "succeeded"
				}
				when = " WHEN " + ref + ".status='" + state + "'"
				if event == "UPDATE" {
					when += " OR old.status='" + state + "'"
				}
			}
			if spec.table == "source_documents" && event == "UPDATE" {
				when = " WHEN old.published_at<>new.published_at"
			}
			triggerEvent := event
			if event == "UPDATE" {
				switch spec.table {
				case "source_documents":
					triggerEvent += " OF published_at"
				case "incidents":
					triggerEvent += " OF content_hash,source_document_id,position,incident_number"
				case "presentation_runs":
					triggerEvent += " OF status,completed_at,source_hash,pipeline_version,legacy,incident_id"
				case "post_processing_jobs":
					triggerEvent += " OF status,completed_at,presentation_run_id,processor_key,scope_key"
				}
			}
			definitions = append(definitions, readerIndexDefinition{name: name, kind: "TRIGGER", sql: "CREATE TRIGGER " + name + " AFTER " + triggerEvent + " ON " + spec.table + when + " BEGIN " + refresh(ids) + " END"})
		}
	}
	return definitions
}

// installReaderIndex keeps the derived view and its transactional triggers in
// sync with the canonical selection rules and language registry. No AI runs.
func (s *Store) installReaderIndex(ctx context.Context) error {
	definitions := readerIndexDefinitions()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Compare actual schema, not just a version marker: missing or modified
	// derived objects must be repaired too. No mutable database is shared by tests.
	rows, err := tx.QueryContext(ctx, "SELECT name,sql FROM sqlite_schema WHERE (type='view' AND name='reader_projection') OR (type='trigger' AND name GLOB 'reader_refresh_*')")
	if err != nil {
		return err
	}
	actual := map[string]string{}
	for rows.Next() {
		var name, definition string
		if err = rows.Scan(&name, &definition); err != nil {
			rows.Close()
			return err
		}
		actual[name] = definition
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	unchanged := len(actual) == len(definitions)
	for _, definition := range definitions {
		unchanged = unchanged && actual[definition.name] == definition.sql
	}
	if unchanged {
		return tx.Commit()
	}
	for name := range actual {
		if name == "reader_projection" {
			continue
		}
		if _, err = tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS "`+strings.ReplaceAll(name, `"`, `""`)+`"`); err != nil {
			return err
		}
	}
	for _, definition := range definitions {
		if _, err = tx.ExecContext(ctx, "DROP "+definition.kind+" IF EXISTS "+definition.name); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, definition.sql); err != nil {
			return fmt.Errorf("install %s: %w", definition.name, err)
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM reader_documents; INSERT INTO reader_documents("+readerIndexColumns+") SELECT "+readerIndexColumns+" FROM reader_projection"); err != nil {
		return fmt.Errorf("backfill reader: %w", err)
	}
	return tx.Commit()
}

func (s *Store) ListReaderEntries(ctx context.Context, q ReaderQuery) (ReaderResult, error) {
	var out ReaderResult
	if q.Limit < 1 || q.Limit > 100 || q.Offset < 0 {
		return out, errors.New("invalid reader page")
	}
	if q.View != "" && q.View != "published" && q.View != "incident" {
		return out, errors.New("invalid reader view")
	}
	if err := q.Filters.Validate(); err != nil {
		return out, err
	}
	status, err := sourceStatusCondition(q.SourceMode)
	if err != nil {
		return out, err
	}
	scope := PresentationScope{Language: q.Language, TranslationLanguage: q.Language, PublicOnly: true}
	args := presentationArgs(scope)
	where := ` rd.language=@language AND ` + status + ` AND rd.run_id=` + latestPresentationRun + ` AND ` + publicReadyCondition
	add := func(column, key, value string) {
		if value != "" {
			where += " AND " + column + "=@" + key
			args = append(args, sql.Named(key, value))
		}
	}
	add("rd.area", "area", q.Filters.Area)
	add("rd.category", "category", q.Filters.Category)
	add("rd.number", "number", q.Filters.Number)
	if q.Filters.Assistance == "yes" {
		where += " AND rd.assistance='requested'"
	} else if q.Filters.Assistance == "no" {
		where += " AND rd.assistance='not_requested'"
	}
	date := "rd.published_date"
	if q.Filters.DateField == "incident" {
		date = "rd.event_date"
	}
	if q.Filters.From != "" {
		where += " AND " + date + ">=@from_date"
		args = append(args, sql.Named("from_date", q.Filters.From))
	}
	if q.Filters.To != "" {
		where += " AND " + date + "<>'' AND " + date + "<=@to_date"
		args = append(args, sql.Named("to_date", q.Filters.To))
	}
	var terms []string
	for n, word := range strings.Fields(normalizeReaderText(q.Filters.Text)) {
		if utf8.RuneCountInString(word) >= 3 {
			terms = append(terms, `"`+strings.ReplaceAll(word, `"`, `""`)+`"`)
		} else {
			key := fmt.Sprintf("short%d", n)
			where += " AND instr(rd.search_text,@" + key + ")>0"
			args = append(args, sql.Named(key, word))
		}
	}
	if len(terms) > 0 {
		where += " AND rd.id IN (SELECT rowid FROM reader_fts WHERE reader_fts MATCH @terms)"
		args = append(args, sql.Named("terms", strings.Join(terms, " AND ")))
	}
	from := ` FROM reader_documents rd JOIN incidents i ON i.id=rd.incident_id JOIN source_documents d ON d.id=i.source_document_id WHERE ` + where
	// Count and page share a snapshot. The public safety predicate is retained in
	// addition to trigger maintenance, so stale index rows can never expose text.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*)"+from, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	order := "rd.published_at DESC,rd.position,rd.incident_id"
	if q.View == "incident" {
		order = "rd.event_date DESC,rd.time_group,rd.event_time DESC,rd.published_at DESC,rd.incident_id"
	}
	args = append(args, sql.Named("limit", q.Limit), sql.Named("offset", q.Offset))
	rows, err := tx.QueryContext(ctx, `SELECT i.id,d.id,1,i.incident_number,i.position,i.title_de,COALESCE(i.body_de,''),i.content_hash,d.title,d.source_url,d.external_id,d.published_at,i.updated_at,d.fetch_status,COALESCE(d.error_message,''),`+scopedAIColumns+from+" ORDER BY "+order+" LIMIT @limit OFFSET @offset", args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		r, e := scanPresentationIncident(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Records = append(out.Records, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Store) ReaderAreas(ctx context.Context, language, sourceMode string) ([]string, error) {
	status, err := sourceStatusCondition(sourceMode)
	if err != nil {
		return nil, err
	}
	args := presentationArgs(PresentationScope{Language: language, TranslationLanguage: language, PublicOnly: true})
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT rd.area FROM reader_documents rd JOIN incidents i ON i.id=rd.incident_id JOIN source_documents d ON d.id=i.source_document_id WHERE rd.language=@language AND rd.area<>'' AND `+status+` AND rd.run_id=`+latestPresentationRun+` AND `+publicReadyCondition+` ORDER BY rd.area`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var area string
		if err := rows.Scan(&area); err != nil {
			return nil, err
		}
		out = append(out, area)
	}
	return out, rows.Err()
}
