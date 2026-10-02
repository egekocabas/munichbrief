package web

import (
	"context"
	_ "embed"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/munichmap"
	"github.com/egekocabas/munichbrief/internal/store"
)

//go:embed templates/map.html
var mapTemplate string

type mapStore interface {
	ReaderStats(context.Context, store.ReaderStatsQuery) (store.ReaderStatsResult, error)
}
type mapFilters struct{ Period, From, To, Category, District string }
type mapDistrict struct {
	InScope    bool
	CountClass string
	munichmap.District
	Total, Available, Level int
	URL                     string
	Selected                bool
}
type mapBar struct {
	Label, URL              string
	Total, Available, Width int
}
type mapPeriod struct {
	Label, URL string
	Selected   bool
}
type mapPage struct {
	basePage
	Filters                                                                               mapFilters
	Stats                                                                                 store.ReaderStatsResult
	Districts                                                                             []mapDistrict
	Ranking, Categories, Trend                                                            []mapBar
	Periods                                                                               []mapPeriod
	Choices                                                                               []readerChoice
	DistrictChoices                                                                       []readerChoice
	Max, CategoryMax, TrendMax                                                            int
	Earliest, PeriodLabel, DistrictLabel, CategoryLabel, LanguageName, TrendFrom, TrendTo string
	GermanURL, LanguageURL, ClearURL, AllDistrictsURL, OutsideURL, UnassignedURL          string
	ViewBox, SourceURL, LicenseURL, Attribution                                           string
}

// Map state is entirely in the URL. Reader search and neighborhood cookies never
// narrow these statistics. Relative periods resolve once, in the Munich calendar.
func parseMapFilters(values url.Values, now time.Time) (mapFilters, store.ReaderFilters, error) {
	f := mapFilters{Period: values.Get("period"), From: values.Get("from"), To: values.Get("to"), Category: values.Get("category"), District: values.Get("district")}
	for key, v := range values {
		switch key {
		case "period", "from", "to", "category", "district":
		default:
			return f, store.ReaderFilters{}, fmt.Errorf("unknown map filter")
		}
		if len(v) != 1 {
			return f, store.ReaderFilters{}, fmt.Errorf("repeated map filter")
		}
	}
	if f.Period == "" {
		f.Period = "all"
	}
	switch f.Period {
	case "all", "week", "month", "previous_month", "custom":
	default:
		return f, store.ReaderFilters{}, fmt.Errorf("invalid map period")
	}
	r := store.ReaderFilters{Category: f.Category, District: f.District, From: f.From, To: f.To}
	// Validate even date inputs that a preset would replace.
	if err := r.Validate(); err != nil {
		return f, r, err
	}
	if f.From != "" || f.To != "" {
		f.Period = "custom"
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch f.Period {
	case "week":
		r.From, r.To = today.AddDate(0, 0, -6).Format(time.DateOnly), today.Format(time.DateOnly)
	case "month":
		r.From, r.To = today.AddDate(0, 0, -29).Format(time.DateOnly), today.Format(time.DateOnly)
	case "previous_month":
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		r.From, r.To = first.AddDate(0, -1, 0).Format(time.DateOnly), first.AddDate(0, 0, -1).Format(time.DateOnly)
	case "custom":
		if r.From == "" && r.To == "" {
			f.Period = "all"
		}
	}
	return f, r, r.Validate()
}
func mapURL(language string, f mapFilters) string {
	q := url.Values{}
	if f.Period != "" && f.Period != "all" {
		q.Set("period", f.Period)
	}
	for k, v := range map[string]string{"from": f.From, "to": f.To, "category": f.Category, "district": f.District} {
		if v != "" {
			q.Set(k, v)
		}
	}
	path := "/" + language + "/map"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return path
}
func (s *Server) mapPage(w http.ResponseWriter, r *http.Request) {
	lang, ok := s.routeLanguage(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if len(r.URL.RawQuery) > maxReaderQueryBytes {
		http.Error(w, s.localization.Text(lang, "SearchInvalid"), http.StatusRequestURITooLong)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, s.localization.Text(lang, "SearchInvalid"), 400)
		return
	}
	filters, reader, err := parseMapFilters(query, time.Now().In(s.location))
	if err != nil {
		http.Error(w, s.localization.Text(lang, "SearchInvalid"), 400)
		return
	}
	db, ok := s.store.(mapStore)
	if !ok {
		s.internalError(w, r, "map store", fmt.Errorf("statistics unavailable"))
		return
	}
	stats, err := db.ReaderStats(r.Context(), store.ReaderStatsQuery{Language: lang, SourceMode: s.options.SourceMode, Filters: reader})
	if err != nil {
		s.internalError(w, r, "map statistics", err)
		return
	}
	base := s.base(r, lang, mapURL(lang, filters))
	base.SocialTitle = s.localization.Text(lang, "MapTitle") + " · MunichBrief"
	base.Description = s.localization.Text(lang, "MapIntro")
	base.ShowAIDisclosure = false
	if stats.Total > 0 {
		base.setAIMetadata("true", nil)
	}
	if len(query) > 0 {
		base.Robots = "noindex,follow"
	}
	base.StructuredData = structuredPageData(base, "CollectionPage")
	definition, _ := s.languageByCode(lang)
	data := mapPage{basePage: base, Filters: filters, Stats: stats, LanguageName: definition.DisplayName, ClearURL: "/" + lang + "/map", ViewBox: munichmap.ViewBox, SourceURL: munichmap.SourceURL, LicenseURL: munichmap.LicenseURL, Attribution: munichmap.Attribution}
	if filters.Category != "" {
		data.CategoryLabel = s.metadataCodeLabel(lang, "Category", filters.Category)
	}
	data.GermanURL = readerActionURL(s.canonicalLanguage().Code, reader, "published", s.options.PageSize)
	data.LanguageURL = readerActionURL(lang, reader, "published", s.options.PageSize)
	if date, e := time.Parse(time.DateOnly, stats.EarliestPublishedDate); e == nil {
		data.Earliest = s.formatIncidentDate(lang, date)
	}
	for _, p := range []struct{ value, key string }{{"all", "MapAllTime"}, {"week", "QuickWeek"}, {"month", "MapMonth"}, {"previous_month", "MapPreviousMonth"}} {
		copy := filters
		copy.Period = p.value
		copy.From = ""
		copy.To = ""
		label := s.localization.Text(lang, p.key)
		data.Periods = append(data.Periods, mapPeriod{label, mapURL(lang, copy), filters.Period == p.value})
		if filters.Period == p.value {
			data.PeriodLabel = label
		}
	}
	if filters.Period == "custom" {
		var bounds []string
		for _, bound := range []struct{ label, value string }{{"DateFrom", filters.From}, {"DateTo", filters.To}} {
			if date, err := time.Parse(time.DateOnly, bound.value); err == nil {
				bounds = append(bounds, s.localization.Text(lang, bound.label)+": "+s.formatIncidentDate(lang, date))
			}
		}
		data.PeriodLabel = strings.Join(bounds, " · ")
	}
	counts := map[string]store.DistrictCount{}
	for _, d := range stats.Districts {
		counts[d.ID] = d
		if d.Total > data.Max {
			data.Max = d.Total
		}
	}
	data.DistrictLabel = s.localization.Text(lang, "MapAllDistricts")
	for _, shape := range munichmap.Districts() {
		count := counts[shape.ID]
		next := filters
		next.District = shape.ID
		if shape.ID == filters.District {
			next.District = ""
			data.DistrictLabel = shape.Name
		}
		d := mapDistrict{District: shape, Total: count.Total, Available: count.Available, URL: mapURL(lang, next) + "#map-results", Selected: shape.ID == filters.District, InScope: filters.District == "" || filters.District == shape.ID}
		switch {
		case count.Total >= 10000:
			d.CountClass = "atlas-count-dense"
		case count.Total >= 1000:
			d.CountClass = "atlas-count-many"
		case count.Total >= 100:
			d.CountClass = "atlas-count-three"
		}
		if data.Max > 0 && count.Total > 0 {
			d.Level = (count.Total*5 + data.Max - 1) / data.Max
		}
		data.Districts = append(data.Districts, d)
		data.DistrictChoices = append(data.DistrictChoices, readerChoice{Value: shape.ID, Label: shape.Name})
		if count.Total > 0 {
			data.Ranking = append(data.Ranking, mapBar{Label: shape.Name, URL: mapURL(lang, next) + "#map-results", Total: count.Total, Available: count.Available, Width: barWidth(count.Total, data.Max)})
		}
	}
	for _, v := range []struct{ value, key string }{{"outside", "MapOutside"}, {"unassigned", "MapUnassigned"}} {
		data.DistrictChoices = append(data.DistrictChoices, readerChoice{Value: v.value, Label: s.localization.Text(lang, v.key)})
		if filters.District == v.value {
			data.DistrictLabel = s.localization.Text(lang, v.key)
		}
	}
	next := filters
	next.District = ""
	data.AllDistrictsURL = mapURL(lang, next) + "#map-results"
	next.District = "outside"
	data.OutsideURL = mapURL(lang, next) + "#map-results"
	next.District = "unassigned"
	data.UnassignedURL = mapURL(lang, next) + "#map-results"
	sort.SliceStable(data.Ranking, func(i, j int) bool { return data.Ranking[i].Total > data.Ranking[j].Total })
	if len(data.Ranking) > 5 {
		data.Ranking = data.Ranking[:5]
	}
	for _, code := range []string{"traffic", "theft_burglary", "robbery_extortion", "violence", "sexual_offense", "fraud_cyber", "drugs", "fire_hazard", "property_damage", "missing_wanted", "police_operation", "other"} {
		data.Choices = append(data.Choices, readerChoice{Value: code, Label: s.metadataCodeLabel(lang, "Category", code)})
	}
	catMax := 0
	for _, c := range stats.Categories {
		if c.Total > catMax {
			catMax = c.Total
		}
	}
	data.CategoryMax = catMax
	for _, c := range stats.Categories {
		next := filters
		next.Category = c.Category
		data.Categories = append(data.Categories, mapBar{Label: s.metadataCodeLabel(lang, "Category", c.Category), URL: mapURL(lang, next) + "#map-results", Total: c.Total, Available: c.Available, Width: barWidth(c.Total, catMax)})
	}
	for _, p := range stats.Trend {
		if p.Total > data.TrendMax {
			data.TrendMax = p.Total
		}
	}
	for _, p := range stats.Trend {
		next := filters
		next.Period = "custom"
		next.From = p.From
		next.To = p.To
		data.Trend = append(data.Trend, mapBar{Label: p.From + " – " + p.To, URL: mapURL(lang, next) + "#map-results", Total: p.Total, Available: p.Available, Width: barWidth(p.Total, data.TrendMax)})
	}
	if len(stats.Trend) > 0 {
		data.TrendFrom = stats.Trend[0].From
		data.TrendTo = stats.Trend[len(stats.Trend)-1].To
	}
	s.setLanguagePreference(w, lang)
	if wantsMarkdown(r.Header.Get("Accept")) {
		s.renderMapMarkdown(w, data)
		return
	}
	s.prepareHTML(w, r, base)
	if err := s.mapTemplate.ExecuteTemplate(w, "layout", data); err != nil {
		s.logger.ErrorContext(r.Context(), "render map", "error", err)
	}
}
func barWidth(value, max int) int {
	if max == 0 || value == 0 {
		return 0
	}
	return (value*100 + max - 1) / max
}
func (s *Server) renderMapMarkdown(w http.ResponseWriter, data mapPage) {
	s.prepareMarkdown(w, data.basePage)
	var b strings.Builder
	writeMarkdownFrontMatter(&b, s.localization.Text(data.Lang, "MapTitle"), data.basePage)
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", mapMarkdownText(s.localization.Text(data.Lang, "MapTitle")), mapMarkdownText(data.Description))
	for _, key := range []string{"MapBasis", "MapCaution", "MapLanguageNote", "MapLocationNote"} {
		fmt.Fprintf(&b, "%s\n\n", mapMarkdownText(s.localization.Text(data.Lang, key)))
	}
	fmt.Fprintf(&b, "%s: %s\n\n%s · %s\n\n%s: %d\n\n%s (%s): %d\n\n", mapMarkdownText(s.localization.Text(data.Lang, "MapEarliest")), mapMarkdownText(data.Earliest), mapMarkdownText(data.PeriodLabel), mapMarkdownText(strings.Trim(strings.Join([]string{data.DistrictLabel, data.CategoryLabel}, " · "), " ·")), mapMarkdownText(s.localization.Text(data.Lang, "MapTotal")), data.Stats.Total, mapMarkdownText(s.localization.Text(data.Lang, "MapAvailable")), mapMarkdownText(data.LanguageName), data.Stats.Available)
	fmt.Fprintf(&b, "## %s\n\n", mapMarkdownText(s.localization.Text(data.Lang, "MapDistricts")))
	for _, d := range data.Districts {
		if d.InScope {
			fmt.Fprintf(&b, "- %s: %d\n", mapMarkdownText(d.Name), d.Total)
		}
	}
	if data.Filters.District == "" || data.Filters.District == "outside" {
		fmt.Fprintf(&b, "- %s: %d\n", mapMarkdownText(s.localization.Text(data.Lang, "MapOutside")), data.Stats.Outside)
	}
	if data.Filters.District == "" || data.Filters.District == "unassigned" {
		fmt.Fprintf(&b, "- %s: %d\n", mapMarkdownText(s.localization.Text(data.Lang, "MapUnassigned")), data.Stats.Unassigned)
	}
	fmt.Fprintf(&b, "\n## %s\n\n", mapMarkdownText(s.localization.Text(data.Lang, "MapCategories")))
	for _, c := range data.Categories {
		fmt.Fprintf(&b, "- %s: %d\n", mapMarkdownText(c.Label), c.Total)
	}
	fmt.Fprintf(&b, "\n## %s\n\n", mapMarkdownText(s.localization.Text(data.Lang, "MapTrend")))
	for _, period := range data.Trend {
		fmt.Fprintf(&b, "- %s: %d\n", mapMarkdownText(period.Label), period.Total)
	}
	fmt.Fprintf(&b, "\n[%s](<%s>) · [%s](<%s>)\n\n%s · [dl-de/by-2-0](%s) · [Source](%s)\n", mapMarkdownText(s.localization.Text(data.Lang, "MapReadGerman")), html.EscapeString(markdownURL(data.GermanURL)), mapMarkdownText(data.LanguageName), html.EscapeString(markdownURL(data.LanguageURL)), mapMarkdownText(data.Attribution+" · "+s.localization.Text(data.Lang, "MapBoundaryNote")), data.LicenseURL, data.SourceURL)
	fmt.Fprint(w, b.String())
}

// Keep Markdown text safe even if rendered by an HTML-aware Markdown client.
func mapMarkdownText(value string) string { return markdownText(html.EscapeString(value)) }
