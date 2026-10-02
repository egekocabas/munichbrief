package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/location"
	"github.com/egekocabas/munichbrief/internal/store"
)

const searchCookieName = "munichbrief_search"
const neighborhoodsCookieName = "munichbrief_neighborhoods"
const searchLifetime = 30 * 24 * time.Hour

const timelineCookieName = "munichbrief_timeline"

// A saved default never overrides an explicit listing URL (including the
// publication view's canonical URL without a view parameter).
func readTimelineView(r *http.Request) string {
	c, err := r.Cookie(timelineCookieName)
	if err != nil || len(c.Value) > 64 {
		return "published"
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 || parts[0] != "v1" || parts[1] != "incident" {
		return "published"
	}
	expires, err := strconv.ParseInt(parts[2], 10, 64)
	now := time.Now()
	if err != nil || expires <= now.Unix() || expires > now.Add(searchLifetime+time.Minute).Unix() {
		return "published"
	}
	return "incident"
}

func (s *Server) writeTimelineView(w http.ResponseWriter, view string) {
	expires := time.Now().Add(searchLifetime)
	http.SetCookie(w, &http.Cookie{Name: timelineCookieName, Value: "v1." + view + "." + strconv.FormatInt(expires.Unix(), 10), Path: "/", MaxAge: int(searchLifetime.Seconds()), Expires: expires, HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteLaxMode})
}

func (s *Server) readerHomeURL(r *http.Request, language string) string {
	return listingURL(language, readSearch(r), readTimelineView(r), s.options.PageSize, 1)
}

type savedSearch struct {
	Version int                 `json:"v"`
	Expires int64               `json:"expires"`
	Filters store.ReaderFilters `json:"filters"`
}
type pageLink struct {
	Number       int
	URL          string
	Current, Gap bool
}
type readerChoice struct{ Value, Label string }
type activeFilter struct{ Key, Label, Value string }
type readerField struct{ Key, Value string }

const maxReaderQueryBytes = 8192

var readerFilterKeys = []string{"q", "area", "neighborhood", "district", "period", "category", "number", "assistance", "date_field", "from", "to"}

// URL filters replace the complete saved search, never merge with it.
func readerURLFilters(r *http.Request) (store.ReaderFilters, bool, error) {
	if len(r.URL.RawQuery) > maxReaderQueryBytes {
		return store.ReaderFilters{}, false, fmt.Errorf("search URL too long")
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return store.ReaderFilters{}, false, err
	}
	explicit := strings.HasSuffix(r.URL.Path, "/search")
	for _, key := range append([]string{"page", "page_size", "view"}, readerFilterKeys...) {
		if key != "neighborhood" && len(values[key]) > 1 {
			return store.ReaderFilters{}, false, fmt.Errorf("repeated query parameter")
		}
	}
	for _, key := range readerFilterKeys {
		if values.Has(key) {
			explicit = true
		}
	}
	f, err := readerFiltersFromValues(values)
	return f, explicit, err
}

func readerFiltersFromValues(values url.Values) (store.ReaderFilters, error) {
	f := store.ReaderFilters{Text: strings.TrimSpace(values.Get("q")), Area: strings.TrimSpace(values.Get("area")), District: values.Get("district"), Period: values.Get("period"), Category: values.Get("category"), Number: strings.TrimSpace(values.Get("number")), Assistance: values.Get("assistance"), DateField: values.Get("date_field"), From: values.Get("from"), To: values.Get("to")}
	// Validate the complete input before normalizing controls overridden by the
	// advanced form. Only neighborhoods are allowed to repeat.
	for _, key := range readerFilterKeys {
		if key != "neighborhood" && len(values[key]) > 1 {
			return f, fmt.Errorf("repeated search control")
		}
	}
	if len(values["neighborhood"]) > 10 {
		return f, fmt.Errorf("too many neighborhoods")
	}
	if f.DateField != "" && f.DateField != "published" && f.DateField != "incident" {
		return f, fmt.Errorf("invalid date field")
	}
	for _, area := range values["neighborhood"] {
		f.Areas = append(f.Areas, strings.TrimSpace(area))
	}
	if err := (store.ReaderFilters{Areas: f.Areas, Period: f.Period}).Validate(); err != nil {
		return f, err
	}
	if err := (store.ReaderFilters{District: f.District}).Validate(); err != nil {
		return f, err
	}
	slices.Sort(f.Areas)
	f.Areas = slices.Compact(f.Areas)
	// An explicit area or custom date range in advanced search replaces the
	// corresponding shortcut; all other advanced criteria remain untouched.
	if f.Area != "" {
		f.Areas = nil
	}
	if f.Area != "" || len(f.Areas) > 0 {
		f.District = ""
	}
	if f.From != "" || f.To != "" {
		f.Period = ""
	} else if f.Period != "" {
		f.DateField = ""
	}
	if err := f.Validate(); err != nil {
		return store.ReaderFilters{}, err
	}
	// These values have no effect on the result, so omit them from shared URLs.
	if f.DateField == "published" || (f.From == "" && f.To == "") {
		f.DateField = ""
	}
	return f, nil
}

func readerFilterValues(f store.ReaderFilters) url.Values {
	values := url.Values{}
	for key, value := range map[string]string{"q": f.Text, "area": f.Area, "district": f.District, "period": f.Period, "category": f.Category, "number": f.Number, "assistance": f.Assistance, "date_field": f.DateField, "from": f.From, "to": f.To} {
		if value != "" && !(key == "date_field" && (value == "published" || (f.From == "" && f.To == ""))) {
			values.Set(key, value)
		}
	}
	for _, area := range f.Areas {
		values.Add("neighborhood", area)
	}
	return values
}

func readSearch(r *http.Request) store.ReaderFilters {
	return readReaderPreferences(r, searchCookieName)
}

func readReaderPreferences(r *http.Request, name string) store.ReaderFilters {
	c, err := r.Cookie(name)
	if err != nil || len(c.Value) > 3800 {
		return store.ReaderFilters{}
	}
	data, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return store.ReaderFilters{}
	}
	var state savedSearch
	if json.Unmarshal(data, &state) != nil || state.Version != 1 || state.Expires <= time.Now().Unix() || state.Expires > time.Now().Add(searchLifetime+time.Minute).Unix() || state.Filters.Validate() != nil {
		return store.ReaderFilters{}
	}
	filters, _ := readerFiltersFromValues(readerFilterValues(state.Filters))
	return filters
}
func (s *Server) writeSearch(w http.ResponseWriter, f store.ReaderFilters) error {
	return s.writeReaderPreferences(w, searchCookieName, f)
}

func (s *Server) writeReaderPreferences(w http.ResponseWriter, name string, f store.ReaderFilters) error {
	expires := time.Now().Add(searchLifetime)
	var data strings.Builder
	encoder := json.NewEncoder(&data)
	// This JSON is base64-encoded cookie data, never HTML. Avoid expanding
	// ordinary search punctuation into six-byte HTML escape sequences.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(savedSearch{Version: 1, Expires: expires.Unix(), Filters: f}); err != nil {
		return err
	}
	value := base64.RawURLEncoding.EncodeToString([]byte(data.String()))
	if len(value) > 3800 {
		return fmt.Errorf("search preferences too large")
	}
	cookie := &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(searchLifetime.Seconds()), Expires: expires, HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteLaxMode}
	if !f.Active() {
		cookie.Value = ""
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(1, 0)
	}
	http.SetCookie(w, cookie)
	return nil
}
func listingURL(language string, filters store.ReaderFilters, view string, size, page int) string {
	path := "/" + language
	if filters.Active() {
		path += "/search"
	}
	q := readerFilterValues(filters)
	if view == "incident" {
		q.Set("view", view)
	}
	if size != 20 {
		q.Set("page_size", strconv.Itoa(size))
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return path
}
func readerOptions(r *http.Request, defaultSize int) (string, int, error) {
	view := r.URL.Query().Get("view")
	if view == "" {
		view = "published"
	}
	if view != "published" && view != "incident" {
		return "", 0, fmt.Errorf("invalid view")
	}
	size := defaultSize
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || (n != 10 && n != 20 && n != 30 && n != 50) {
			return "", 0, fmt.Errorf("invalid page size")
		}
		size = n
	}
	return view, size, nil
}
func paginationLinks(language string, filters store.ReaderFilters, view string, size, page, total int) []pageLink {
	var out []pageLink
	for n := 1; n <= total; n++ {
		show := n == 1 || n == total || (n >= page-2 && n <= page+2)
		if !show {
			if len(out) > 0 && !out[len(out)-1].Gap {
				out = append(out, pageLink{Gap: true})
			}
			continue
		}
		out = append(out, pageLink{Number: n, URL: listingURL(language, filters, view, size, n) + "#timeline-heading", Current: n == page})
	}
	// A one-page gap is clearer as a number than an ellipsis.
	for n := 1; n < len(out)-1; n++ {
		if out[n].Gap && out[n+1].Number-out[n-1].Number == 2 {
			p := out[n-1].Number + 1
			out[n] = pageLink{Number: p, URL: listingURL(language, filters, view, size, p) + "#timeline-heading"}
		}
	}
	return out
}
func (s *Server) submitSearch(w http.ResponseWriter, r *http.Request) {
	language, _ := s.routeLanguage(r)
	if !validReaderMutation(r) || !isFormPost(r) {
		http.Error(w, s.localization.Text(language, "SearchInvalid"), http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		http.Error(w, s.localization.Text(language, "SearchInvalid"), http.StatusBadRequest)
		return
	}
	f, err := readerFiltersFromValues(r.PostForm)
	if err != nil {
		http.Error(w, s.localization.Text(language, "SearchInvalid"), http.StatusBadRequest)
		return
	}
	urlFilters, explicit, err := readerURLFilters(r)
	if err != nil {
		http.Error(w, s.localization.Text(language, "SearchInvalid"), http.StatusBadRequest)
		return
	}
	var savedAreas *store.ReaderFilters
	anchor := "#timeline-heading"
	if action := r.PostForm.Get("neighborhoods_action"); action != "" {
		f = urlFilters
		if !explicit {
			f = readSearch(r)
		}
		switch action {
		case "save":
			selection, e := readerFiltersFromValues(url.Values{"neighborhood": r.PostForm["saved_neighborhood"]})
			if e != nil {
				http.Error(w, s.localization.Text(language, "NeighborhoodsInvalid"), http.StatusBadRequest)
				return
			}
			savedAreas = &selection
			f.Areas = selection.Areas
			if len(f.Areas) > 0 {
				f.Area = ""
				f.District = ""
			}
		case "enable":
			selection := readReaderPreferences(r, neighborhoodsCookieName).Areas
			if len(selection) == 0 {
				// A tab can outlive its preference cookie. Keep its current search
				// and return to the chooser instead of failing or clearing areas.
				anchor = "#reader-neighborhoods"
				break
			}
			f.Areas = selection
			f.Area = ""
			f.District = ""
		case "disable":
			f.Areas = nil
		default:
			http.Error(w, s.localization.Text(language, "SearchInvalid"), http.StatusBadRequest)
			return
		}
	}
	if strings.HasSuffix(r.URL.Path, "/clear") {
		f = store.ReaderFilters{}
	}
	if remove := r.PostForm.Get("remove"); remove != "" {
		f = urlFilters
		if !explicit {
			f = readSearch(r)
		}
		switch remove {
		case "q":
			f.Text = ""
		case "area":
			f.Area = ""
		case "neighborhoods":
			f.Areas = nil
		case "district":
			f.District = ""
		case "category":
			f.Category = ""
		case "number":
			f.Number = ""
		case "assistance":
			f.Assistance = ""
		case "dates":
			f.Period = ""
			f.From = ""
			f.To = ""
			f.DateField = ""
		default:
			http.Error(w, s.localization.Text(language, "SearchInvalid"), http.StatusBadRequest)
			return
		}
	}
	if err := f.Validate(); err != nil {
		http.Error(w, s.localization.Text(language, "SearchInvalid"), http.StatusBadRequest)
		return
	}
	view, size, err := readerOptions(r, s.options.PageSize)
	if err != nil {
		http.Error(w, s.localization.Text(language, "SearchInvalid"), http.StatusBadRequest)
		return
	}
	if err := s.writeSearch(w, f); err != nil {
		http.Error(w, s.localization.Text(language, "SearchInvalid"), http.StatusBadRequest)
		return
	}
	if savedAreas != nil {
		if err := s.writeReaderPreferences(w, neighborhoodsCookieName, *savedAreas); err != nil {
			http.Error(w, s.localization.Text(language, "NeighborhoodsInvalid"), http.StatusBadRequest)
			return
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	http.Redirect(w, r, listingURL(language, f, view, size, 1)+anchor, http.StatusSeeOther)
}
func validReaderMutation(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "same-site"
	}
	if origin == "null" {
		return r.Header.Get("Sec-Fetch-Site") == "same-origin"
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// Compare authority including port; sibling subdomains are not same-origin.
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return u.Scheme == scheme && strings.EqualFold(u.Host, r.Host) && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}
func (s *Server) groupByIncident(incidents []store.IncidentRecord, language string) []dayGroup {
	var groups []dayGroup
	for _, incident := range incidents {
		date := incident.AIEventStartDate
		label := s.localization.Text(language, "DateNotReported")
		if t, err := time.Parse("2006-01-02", date); err == nil {
			label = formatDayFor(s.languages, language, t)
		} else {
			date = "unknown"
		}
		kind := "TimeNotReported"
		if incident.AIEventStartTime != "" {
			kind = "ClockTimeReported"
		} else if incident.AIEventDayPart != "" {
			kind = "DayPartReported"
		}
		id := date + "-" + kind
		if len(groups) == 0 || groups[len(groups)-1].ID != id {
			groups = append(groups, dayGroup{ID: id, Label: label, TimeLabel: s.localization.Text(language, kind)})
		}
		groups[len(groups)-1].Incidents = append(groups[len(groups)-1].Incidents, s.incidentForLanguage(incident, language))
	}
	return groups
}
func (s *Server) decorateReader(data *timelinePage, language string, search bool, view string, size int, f store.ReaderFilters) {
	data.HomeURL = listingURL(language, f, view, size, 1)
	data.View = view
	data.PageSize = size
	data.Search = search
	data.Filters = f
	data.ListURL = listingURL(language, f, view, size, data.Page)
	data.FormURL = listingURL(language, f, view, size, 1)
	if !f.Active() {
		data.FormURL = strings.TrimSuffix(readerActionURL(language, f, view, size), "#timeline-heading")
	}
	filterValues := readerFilterValues(f)
	for _, key := range readerFilterKeys {
		for _, value := range filterValues[key] {
			data.FilterFields = append(data.FilterFields, readerField{key, value})
		}
	}
	data.ClearURL = "/" + language + "/search/clear"
	if u, e := url.Parse(listingURL(language, store.ReaderFilters{}, view, size, 1)); e == nil && u.RawQuery != "" {
		data.ClearURL += "?" + u.RawQuery
	}
	data.PublishedURL = listingURL(language, f, "published", size, 1) + "#timeline-heading"
	data.IncidentViewURL = listingURL(language, f, "incident", size, 1) + "#timeline-heading"
	data.PreviousURL = listingURL(language, f, view, size, data.Page-1) + "#timeline-heading"
	data.NextURL = listingURL(language, f, view, size, data.Page+1) + "#timeline-heading"
	data.Pages = paginationLinks(language, f, view, size, data.Page, data.TotalPages)
	data.PageSizes = []int{10, 20, 30, 50}
	data.First = 0
	data.Last = 0
	if data.Total > 0 {
		data.First = (data.Page-1)*size + 1
		data.Last = data.First + data.Shown - 1
	}
	for _, code := range []string{"traffic", "theft_burglary", "robbery_extortion", "violence", "sexual_offense", "fraud_cyber", "drugs", "fire_hazard", "property_damage", "missing_wanted", "police_operation", "other"} {
		data.Categories = append(data.Categories, readerChoice{Value: code, Label: s.metadataCodeLabel(language, "Category", code)})
	}
	add := func(key, label, value string) {
		if value != "" {
			data.ActiveFilters = append(data.ActiveFilters, activeFilter{Key: key, Label: s.localization.Text(language, label), Value: value})
		}
	}
	add("q", "SearchText", f.Text)
	add("area", "Area", f.Area)
	if f.District != "" {
		name := ""
		switch f.District {
		case location.DistrictOutside:
			name = s.localization.Text(language, "MapOutside")
		case location.DistrictUnassigned:
			name = s.localization.Text(language, "MapUnassigned")
		default:
			if district, ok := location.District(f.District); ok {
				name = district.Name
			}
		}
		add("district", "MapDistrict", name)
	}
	add("neighborhoods", "Area", strings.Join(f.Areas, ", "))
	add("category", "Category", s.metadataCodeLabel(language, "Category", f.Category))
	add("number", "Report", f.Number)
	if f.Assistance != "" {
		label := "AssistanceYes"
		if f.Assistance == "no" {
			label = "AssistanceNo"
		}
		add("assistance", "PublicAssistance", s.localization.Text(language, label))
	}
	if f.Period != "" {
		label := "QuickToday"
		if f.Period == "week" {
			label = "QuickWeek"
		}
		add("dates", "PublishedDate", s.localization.Text(language, label))
	} else if f.From != "" || f.To != "" {
		label := "PublishedDate"
		if f.DateField == "incident" {
			label = "IncidentDate"
		}
		add("dates", label, f.From+" – "+f.To)
	}
	for n := range data.LanguageSwitches {
		data.LanguageSwitches[n].URL = listingURL(data.LanguageSwitches[n].Code, f, view, size, 1)
	}
	quick := func(next store.ReaderFilters) string {
		return readerActionURL(language, next, view, size)
	}
	next := f
	next.From, next.To, next.DateField, next.Period = "", "", "", ""
	data.AllDatesURL = quick(next)
	next.Period = "today"
	data.TodayURL = quick(next)
	next.Period = "week"
	data.WeekURL = quick(next)
	next = f
	if next.Assistance == "yes" {
		next.Assistance = ""
	} else {
		next.Assistance = "yes"
	}
	data.AssistanceURL = quick(next)
}

// An empty /search URL explicitly clears this filter state instead of restoring
// another tab's remembered search when following a shortcut.
func readerActionURL(language string, f store.ReaderFilters, view string, size int) string {
	path := listingURL(language, f, view, size, 1)
	if !f.Active() {
		path = strings.Replace(path, "/"+language, "/"+language+"/search", 1)
	}
	return path + "#timeline-heading"
}

func (s *Server) decorateNeighborhoods(data *timelinePage, r *http.Request) {
	saved := readReaderPreferences(r, neighborhoodsCookieName).Areas
	data.NeighborhoodsSaved = len(saved) > 0
	data.NeighborhoodsActive = len(saved) > 0 && slices.Equal(saved, data.Filters.Areas)
	// Keep a saved area editable even when it currently has no reports in this
	// language. Names are always escaped; queries match them literally.
	areas := append(slices.Clone(data.Areas), saved...)
	areas = append(areas, data.Filters.Areas...)
	slices.Sort(areas)
	for _, area := range slices.Compact(areas) {
		data.Neighborhoods = append(data.Neighborhoods, neighborhoodChoice{Name: area, Selected: slices.Contains(saved, area)})
	}
	for g := range data.Groups {
		for n := range data.Groups[g].Incidents {
			incident := &data.Groups[g].Incidents[n]
			f := data.Filters
			f.Area, f.Areas, f.District = incident.AreaName, nil, ""
			incident.AreaURL = readerActionURL(data.Lang, f, data.View, data.PageSize)
			f = data.Filters
			f.Category = incident.Record.AICategory
			incident.CategoryURL = readerActionURL(data.Lang, f, data.View, data.PageSize)
		}
	}
}

// Only remove explicit report-number decoration, never arbitrary trailing
// digits (years, road numbers and ages can be part of a meaningful headline).
func readerTitle(title, number string) string {
	if number == "" {
		return title
	}
	title = strings.TrimSuffix(title, " · "+number)
	for _, prefix := range []string{number + ". ", "#" + number + ": "} {
		title = strings.TrimPrefix(title, prefix)
	}
	return title
}
func (s *Server) contact(w http.ResponseWriter, r *http.Request) {
	data := contactPage{ReportID: r.URL.Query().Get("incident"), ReportLanguage: r.URL.Query().Get("report_language")}
	if len(r.URL.RawQuery) > maxReaderQueryBytes || len(r.URL.Query()["incident"]) > 1 || len(r.URL.Query()["report_language"]) > 1 {
		http.Error(w, "invalid report reference", http.StatusBadRequest)
		return
	}
	if err := s.contactReport(r, &data); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
		} else {
			s.internalError(w, r, "get correction report", err)
		}
		return
	}
	if data.ReportID != "" {
		data.Topic = "correction"
	}
	received := false
	if c, err := r.Cookie("munichbrief_contact_received"); err == nil {
		received = s.validContactToken(c.Value, time.Now())
		http.SetCookie(w, &http.Cookie{Name: "munichbrief_contact_received", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteLaxMode})
	}
	data.Received = received
	s.renderContact(w, r, data, http.StatusOK)
}
