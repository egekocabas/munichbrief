package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

const searchCookieName = "munichbrief_search"
const searchLifetime = 30 * 24 * time.Hour

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

func readSearch(r *http.Request) store.ReaderFilters {
	c, err := r.Cookie(searchCookieName)
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
	return state.Filters
}
func (s *Server) writeSearch(w http.ResponseWriter, f store.ReaderFilters) error {
	expires := time.Now().Add(searchLifetime)
	data, err := json.Marshal(savedSearch{Version: 1, Expires: expires.Unix(), Filters: f})
	if err != nil {
		return err
	}
	value := base64.RawURLEncoding.EncodeToString(data)
	if len(value) > 3800 {
		return fmt.Errorf("search preferences too large")
	}
	cookie := &http.Cookie{Name: searchCookieName, Value: value, Path: "/", MaxAge: int(searchLifetime.Seconds()), Expires: expires, HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteLaxMode}
	if !f.Active() {
		cookie.Value = ""
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(1, 0)
	}
	http.SetCookie(w, cookie)
	return nil
}
func listingURL(language string, search bool, view string, size, page int) string {
	path := "/" + language
	if search {
		path += "/search"
	}
	q := url.Values{}
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
func paginationLinks(language string, search bool, view string, size, page, total int) []pageLink {
	var out []pageLink
	for n := 1; n <= total; n++ {
		show := n == 1 || n == total || (n >= page-2 && n <= page+2)
		if !show {
			if len(out) > 0 && !out[len(out)-1].Gap {
				out = append(out, pageLink{Gap: true})
			}
			continue
		}
		out = append(out, pageLink{Number: n, URL: listingURL(language, search, view, size, n) + "#timeline-heading", Current: n == page})
	}
	// A one-page gap is clearer as a number than an ellipsis.
	for n := 1; n < len(out)-1; n++ {
		if out[n].Gap && out[n+1].Number-out[n-1].Number == 2 {
			p := out[n-1].Number + 1
			out[n] = pageLink{Number: p, URL: listingURL(language, search, view, size, p) + "#timeline-heading"}
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
	f := store.ReaderFilters{Text: strings.TrimSpace(r.PostForm.Get("q")), Area: strings.TrimSpace(r.PostForm.Get("area")), Category: r.PostForm.Get("category"), Number: strings.TrimSpace(r.PostForm.Get("number")), Assistance: r.PostForm.Get("assistance"), DateField: r.PostForm.Get("date_field"), From: r.PostForm.Get("from"), To: r.PostForm.Get("to")}
	if strings.HasSuffix(r.URL.Path, "/clear") {
		f = store.ReaderFilters{}
	}
	if remove := r.PostForm.Get("remove"); remove != "" {
		f = readSearch(r)
		switch remove {
		case "q":
			f.Text = ""
		case "area":
			f.Area = ""
		case "category":
			f.Category = ""
		case "number":
			f.Number = ""
		case "assistance":
			f.Assistance = ""
		case "dates":
			f.From = ""
			f.To = ""
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
	w.Header().Set("Cache-Control", "private, no-store")
	http.Redirect(w, r, listingURL(language, f.Active(), view, size, 1)+"#timeline-heading", http.StatusSeeOther)
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
	data.View = view
	data.PageSize = size
	data.Search = search
	data.Filters = f
	data.ListURL = listingURL(language, search, view, size, data.Page)
	data.FormURL = listingURL(language, true, view, size, 1)
	data.ClearURL = "/" + language + "/search/clear"
	if u, e := url.Parse(data.FormURL); e == nil && u.RawQuery != "" {
		data.ClearURL += "?" + u.RawQuery
	}
	data.PublishedURL = listingURL(language, search, "published", size, 1) + "#timeline-heading"
	data.IncidentViewURL = listingURL(language, search, "incident", size, 1) + "#timeline-heading"
	data.PreviousURL = listingURL(language, search, view, size, data.Page-1) + "#timeline-heading"
	data.NextURL = listingURL(language, search, view, size, data.Page+1) + "#timeline-heading"
	data.Pages = paginationLinks(language, search, view, size, data.Page, data.TotalPages)
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
	add("category", "Category", s.metadataCodeLabel(language, "Category", f.Category))
	add("number", "Report", f.Number)
	if f.Assistance != "" {
		label := "AssistanceYes"
		if f.Assistance == "no" {
			label = "AssistanceNo"
		}
		add("assistance", "PublicAssistance", s.localization.Text(language, label))
	}
	if f.From != "" || f.To != "" {
		label := "PublishedDate"
		if f.DateField == "incident" {
			label = "IncidentDate"
		}
		add("dates", label, f.From+" – "+f.To)
	}
	for n := range data.LanguageSwitches {
		data.LanguageSwitches[n].URL = listingURL(data.LanguageSwitches[n].Code, search, view, size, 1)
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
	received := false
	if c, err := r.Cookie("munichbrief_contact_received"); err == nil {
		received = s.validContactToken(c.Value, time.Now())
		http.SetCookie(w, &http.Cookie{Name: "munichbrief_contact_received", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteLaxMode})
	}
	s.renderContact(w, r, contactPage{Received: received}, http.StatusOK)
}
