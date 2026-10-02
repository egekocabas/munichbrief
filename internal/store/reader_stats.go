package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	langregistry "github.com/egekocabas/munichbrief/internal/languages"
	"github.com/egekocabas/munichbrief/internal/location"
)

type ReaderStatsQuery struct {
	// Language controls translation coverage. The count basis is always the
	// current public canonical archive, independent of the interface language.
	Language, SourceMode string
	Filters              ReaderFilters
}

type DistrictCount struct {
	ID, Name         string
	Total, Available int
}

type CategoryCount struct {
	Category         string
	Total, Available int
}

type PublicationCount struct {
	From, To         string
	Total, Available int
}

type ReaderStatsResult struct {
	// EarliestPublishedDate is archive-wide, never the collection start date.
	EarliestPublishedDate                         string
	Total, Available, Mapped, Outside, Unassigned int
	Districts                                     []DistrictCount
	Categories                                    []CategoryCount
	Trend                                         []PublicationCount
	// TrendWeeks is the width of each Monday-aligned bin. It expands only when
	// necessary to keep a long-running archive within 104 bins.
	TrendWeeks int
}

// ReaderStats counts public reports, not police events, original press releases,
// translations, or verification runs. A report contributes to exactly one area
// group and one category. Filtering and every aggregate share a read snapshot.
func (s *Store) ReaderStats(ctx context.Context, q ReaderStatsQuery) (ReaderStatsResult, error) {
	var out ReaderStatsResult
	canonical := langregistry.Canonical(langregistry.Registered()).Code
	if q.Language == "" {
		q.Language = canonical
	}
	knownLanguage := false
	for _, language := range langregistry.Registered() {
		knownLanguage = knownLanguage || q.Language == language.Code
	}
	if !knownLanguage {
		return out, errors.New("invalid statistics language")
	}
	// Text depends on the reading language; including it would break equality
	// between canonical counts and translated report links. This page supports
	// only shared metadata and publication dates.
	if q.Filters.Text != "" || q.Filters.Area != "" || len(q.Filters.Areas) > 0 || q.Filters.Number != "" || q.Filters.Assistance != "" || q.Filters.DateField == "incident" {
		return out, errors.New("unsupported statistics filter")
	}
	if err := q.Filters.Validate(); err != nil {
		return out, err
	}
	if q.Filters.Period != "" {
		var err error
		q.Filters.From, q.Filters.To, err = publicationPeriod(q.Filters.Period, time.Now())
		if err != nil {
			return out, err
		}
		q.Filters.Period = ""
	}
	from, args, err := readerSelection(ReaderQuery{Language: canonical, SourceMode: q.SourceMode, Filters: q.Filters})
	if err != nil {
		return out, err
	}
	archiveFrom, archiveArgs, err := readerSelection(ReaderQuery{Language: canonical, SourceMode: q.SourceMode})
	if err != nil {
		return out, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MIN(NULLIF(rd.published_date,'')),'')"+archiveFrom, archiveArgs...).Scan(&out.EarliestPublishedDate); err != nil {
		return out, err
	}
	// Reapply the publication predicate, including accepted translation outputs,
	// instead of treating a derived index row alone as proof of public readiness.
	coverage := strings.ReplaceAll(publicReadyCondition, "@language", "@coverage_language")
	coverage = "(" + coverage + ` AND EXISTS (SELECT 1 FROM reader_documents translated WHERE translated.incident_id=rd.incident_id AND translated.run_id=rd.run_id AND translated.language=@coverage_language))`
	args = append(args, sql.Named("coverage_language", q.Language))
	rows, err := tx.QueryContext(ctx, `SELECT reader_district(rd.area),rd.category,rd.published_date,COUNT(*),SUM(CASE WHEN `+coverage+` THEN 1 ELSE 0 END)`+from+` GROUP BY reader_district(rd.area),rd.category,rd.published_date`, args...)
	if err != nil {
		return out, err
	}
	districts := make(map[string]*DistrictCount)
	for _, district := range location.Districts() {
		districts[district.ID] = &DistrictCount{ID: district.ID, Name: district.Name}
	}
	categories := make(map[string]*CategoryCount)
	days := make(map[string]PublicationCount)
	for rows.Next() {
		var group, category, date string
		var total, available int
		if err = rows.Scan(&group, &category, &date, &total, &available); err != nil {
			rows.Close()
			return out, err
		}
		out.Total += total
		out.Available += available
		switch group {
		case location.DistrictOutside:
			out.Outside += total
		case location.DistrictUnassigned:
			out.Unassigned += total
		default:
			district := districts[group]
			if district == nil {
				rows.Close()
				return out, fmt.Errorf("unknown district group %q", group)
			}
			out.Mapped += total
			district.Total += total
			district.Available += available
		}
		if categories[category] == nil {
			categories[category] = &CategoryCount{Category: category}
		}
		categories[category].Total += total
		categories[category].Available += available
		day := days[date]
		day.Total += total
		day.Available += available
		days[date] = day
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, district := range location.Districts() {
		out.Districts = append(out.Districts, *districts[district.ID])
	}
	for _, category := range categories {
		out.Categories = append(out.Categories, *category)
	}
	sort.Slice(out.Categories, func(i, j int) bool {
		if out.Categories[i].Total != out.Categories[j].Total {
			return out.Categories[i].Total > out.Categories[j].Total
		}
		return out.Categories[i].Category < out.Categories[j].Category
	})
	out.Trend, out.TrendWeeks, err = publicationTrend(days)
	if err != nil {
		return out, err
	}
	for index := range out.Trend {
		if q.Filters.From != "" && out.Trend[index].From < q.Filters.From {
			out.Trend[index].From = q.Filters.From
		}
		if q.Filters.To != "" && out.Trend[index].To > q.Filters.To {
			out.Trend[index].To = q.Filters.To
		}
	}
	return out, tx.Commit()
}

func publicationTrend(days map[string]PublicationCount) ([]PublicationCount, int, error) {
	if len(days) == 0 {
		return nil, 1, nil
	}
	var first, last time.Time
	parsed := make(map[string]time.Time, len(days))
	for date := range days {
		value, err := time.Parse(time.DateOnly, date)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid public publication date: %w", err)
		}
		parsed[date] = value
		if first.IsZero() || value.Before(first) {
			first = value
		}
		if last.IsZero() || value.After(last) {
			last = value
		}
	}
	first = first.AddDate(0, 0, -(int(first.Weekday())+6)%7)
	// Unix seconds avoid time.Duration's 292-year overflow for extreme dates.
	weeks := int((last.Unix()-first.Unix())/(7*24*60*60)) + 1
	width := (weeks + 103) / 104
	bins := make([]PublicationCount, (weeks+width-1)/width)
	for index := range bins {
		start := first.AddDate(0, 0, index*width*7)
		bins[index] = PublicationCount{From: start.Format(time.DateOnly), To: start.AddDate(0, 0, width*7-1).Format(time.DateOnly)}
	}
	for date, count := range days {
		index := int((parsed[date].Unix()-first.Unix())/(7*24*60*60)) / width
		bins[index].Total += count.Total
		bins[index].Available += count.Available
	}
	return bins, width, nil
}
