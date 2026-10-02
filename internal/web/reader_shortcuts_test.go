package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

func TestShortcutsPreserveAdvancedFiltersAndExplicitEmptyState(t *testing.T) {
	s, _ := newContactServer(t)
	f := store.ReaderFilters{Text: "bicycle", Areas: []string{"Maxvorstadt", "Schwabing"}, Category: "traffic", Number: "1201", Assistance: "no", DateField: "incident", From: "2026-09-01", To: "2026-09-15"}
	data := timelinePage{Page: 3, TotalPages: 5}
	s.decorateReader(&data, "en", true, "incident", 30, f)
	for _, link := range []string{data.TodayURL, data.WeekURL, data.AssistanceURL, data.AllDatesURL} {
		u, _ := url.Parse(link)
		got, explicit, err := readerURLFilters(httptest.NewRequest("GET", link, nil))
		if err != nil || !explicit || got.Text != f.Text || got.Category != f.Category || got.Number != f.Number || !reflect.DeepEqual(got.Areas, f.Areas) || u.Query().Get("view") != "incident" || u.Query().Get("page_size") != "30" || u.Query().Has("page") {
			t.Fatalf("shortcut lost state: %s / %+v / %v", link, got, err)
		}
		if link == data.AssistanceURL {
			if got.Assistance != "yes" || got.From != f.From || got.DateField != "incident" {
				t.Fatal(got)
			}
		} else if got.From != "" || got.To != "" || got.DateField != "" || got.Assistance != "no" {
			t.Fatal(got)
		}
	}
	empty := readerActionURL("en", store.ReaderFilters{}, "incident", 30)
	_, explicit, err := readerURLFilters(httptest.NewRequest("GET", empty, nil))
	if err != nil || !explicit || !strings.HasPrefix(empty, "/en/search?") {
		t.Fatal("empty shortcut can restore another tab's cookie", empty)
	}
	// Native advanced inputs can override the matching shortcut without losing
	// other criteria. An empty area keeps the selected neighborhood union.
	values := readerFilterValues(f)
	values.Set("period", "today")
	values.Set("area", "Sendling")
	got, err := readerFiltersFromValues(values)
	if err != nil || got.Area != "Sendling" || len(got.Areas) != 0 || got.Period != "" || got.From != f.From || got.Text != f.Text {
		t.Fatal(got, err)
	}
	for _, raw := range []string{"period=invalid", "period=today&period=week", "neighborhood=%00", "neighborhood=", "period=today&date_field=invalid", strings.Repeat("neighborhood=A&", 11)} {
		_, _, err := readerURLFilters(httptest.NewRequest("GET", "/en/search?"+raw, nil))
		if err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestNeighborhoodPreferencesAndNativeAdvancedSearch(t *testing.T) {
	s, _ := newContactServer(t)
	post := func(path string, values url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		return contactRequest(s, "POST", path, values, cookies...)
	}
	saved := post("/en/search?category=traffic&page_size=30&q=bicycle&view=incident", url.Values{"neighborhoods_action": {"save"}, "saved_neighborhood": {"Schwabing", "Maxvorstadt"}})
	if saved.Code != 303 {
		t.Fatal(saved.Code, saved.Body.String())
	}
	location := saved.Header().Get("Location")
	if !strings.Contains(location, "neighborhood=Maxvorstadt&neighborhood=Schwabing") || !strings.Contains(location, "category=traffic") || !strings.Contains(location, "q=bicycle") || !strings.Contains(location, "page_size=30") {
		t.Fatal(location)
	}
	var preferences, search *http.Cookie
	for _, c := range saved.Result().Cookies() {
		if c.Name == neighborhoodsCookieName {
			preferences = c
		}
		if c.Name == searchCookieName {
			search = c
		}
	}
	if preferences == nil || !preferences.HttpOnly || !preferences.Secure || preferences.MaxAge != int(searchLifetime.Seconds()) {
		t.Fatal(preferences)
	}
	r := httptest.NewRequest("GET", "/en", nil)
	r.AddCookie(preferences)
	if got := readReaderPreferences(r, neighborhoodsCookieName).Areas; !reflect.DeepEqual(got, []string{"Maxvorstadt", "Schwabing"}) {
		t.Fatal(got)
	}
	// Shared URLs carry all neighborhoods, independent of the recipient's cookies.
	shared := contactRequest(s, "GET", strings.TrimSuffix(location, "#timeline-heading"), nil)
	if shared.Code != 200 {
		t.Fatal(shared.Code, shared.Header())
	}
	for _, name := range []string{"q", "area", "category", "number", "assistance", "date_field", "from", "to"} {
		if !strings.Contains(shared.Body.String(), `name="`+name+`"`) {
			t.Errorf("lost advanced control %s", name)
		}
	}
	if strings.Count(shared.Body.String(), `name="neighborhood" value="Maxvorstadt"`) < 2 {
		t.Fatal("advanced form and pagination must preserve neighborhoods")
	}
	disabled := post(strings.TrimSuffix(location, "#timeline-heading"), url.Values{"neighborhoods_action": {"disable"}}, preferences, search)
	if disabled.Code != 303 || strings.Contains(disabled.Header().Get("Location"), "neighborhood=") || !strings.Contains(disabled.Header().Get("Location"), "q=bicycle") {
		t.Fatal(disabled.Code, disabled.Header())
	}
	for _, c := range disabled.Result().Cookies() {
		if c.Name == neighborhoodsCookieName {
			t.Fatal("toggle deleted the saved selection")
		}
	}
	enabled := post("/en/search?q=train", url.Values{"neighborhoods_action": {"enable"}}, preferences, search)
	if enabled.Code != 303 || !strings.Contains(enabled.Header().Get("Location"), "q=train") || strings.Contains(enabled.Header().Get("Location"), "category=") {
		t.Fatal("older-tab URL lost to cookie", enabled.Header())
	}
	cleared := post("/en/search", url.Values{"neighborhoods_action": {"save"}}, preferences)
	deleted := false
	for _, c := range cleared.Result().Cookies() {
		if c.Name == neighborhoodsCookieName && c.MaxAge == -1 {
			deleted = true
		}
	}
	if cleared.Code != 303 || !deleted {
		t.Fatal("empty selection not deleted", cleared.Header())
	}
	expired, _ := json.Marshal(savedSearch{Version: 1, Expires: time.Now().Add(-time.Hour).Unix(), Filters: store.ReaderFilters{Areas: []string{"Schwabing"}}})
	r = httptest.NewRequest("GET", "/en", nil)
	r.AddCookie(&http.Cookie{Name: neighborhoodsCookieName, Value: base64.RawURLEncoding.EncodeToString(expired)})
	if len(readReaderPreferences(r, neighborhoodsCookieName).Areas) != 0 {
		t.Fatal("expired neighborhoods restored")
	}
	r = httptest.NewRequest("POST", "https://munichbrief.de/en/search", strings.NewReader("neighborhoods_action=save&saved_neighborhood=Schwabing"))
	r.Header.Set("Origin", "https://evil.invalid")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin preference write", w.Code)
	}
}

func TestIncidentCorrectionContextSurvivesValidationAndLanguageSwitch(t *testing.T) {
	s, db := newContactServer(t)
	job := seedV2Presentation(t, db, testPresentation{TitleDE: "Synthetische Meldung", SummaryDE: "Erfundener Bericht.", TitleEN: "Synthetic <b>report</b>", SummaryEN: "An invented report."}, time.Now())
	id := formatID(job.IncidentID)
	get := contactRequest(s, "GET", "/en/contact?incident="+id, nil)
	if get.Code != 200 || !strings.Contains(get.Body.String(), `value="correction" selected`) || !strings.Contains(get.Body.String(), html.EscapeString("Synthetic <b>report</b>")) || !strings.Contains(get.Body.String(), "report_language=en") || get.Header().Get("X-Robots-Tag") != "noindex,follow" {
		t.Fatal("missing safe correction context", get.Code)
	}
	cookie, token := findContactForm(t, get)
	f := url.Values{"token": {token}, "incident": {id}, "report_language": {"en"}, "topic": {"correction"}, "email": {"invalid"}, "message": {"The location needs checking."}}
	invalid := contactRequest(s, "POST", "/de/contact", f, cookie)
	if invalid.Code != 400 || !strings.Contains(invalid.Body.String(), `name="incident" value="`+id+`"`) || !strings.Contains(invalid.Body.String(), "The location needs checking.") || !strings.Contains(invalid.Body.String(), html.EscapeString("Synthetic <b>report</b>")) {
		t.Fatal("retry lost report or message", invalid.Code)
	}
	f.Set("email", "reader@example.org")
	for range 2 {
		w := contactRequest(s, "POST", "/de/contact", f, cookie)
		if w.Code != 303 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	messages, total, err := db.ListContacts(context.Background(), "", 1)
	if err != nil || total != 1 || !strings.Contains(messages[0].Message, "https://munichbrief.de/en/incidents/"+id+"\nLanguage: en") || !strings.HasSuffix(messages[0].Message, f.Get("message")) || messages[0].Language != "de" {
		t.Fatal("missing or duplicated context", messages, total, err)
	}
	for _, suffix := range []string{"-1", "999999999", "abc", id + "&report_language=xx", id + "&report_language=tr", id + "&incident=" + id} {
		w := contactRequest(s, "GET", "/en/contact?incident="+suffix, nil)
		if w.Code != 404 && w.Code != 400 {
			t.Errorf("invalid/private reference %s = %d", suffix, w.Code)
		}
	}
	// The raw fixture exists but has no accepted public presentation.
	private, _, err := db.ListAdminIncidents(context.Background(), 1, 0, "fixture", store.PresentationScope{Language: "de"}, store.AdminIncidentsUnprocessed)
	if err != nil || len(private) != 1 {
		t.Fatal(err)
	}
	w := contactRequest(s, "GET", "/en/contact?incident="+formatID(private[0].ID), nil)
	if w.Code != 404 || strings.Contains(w.Body.String(), private[0].BodyDE) {
		t.Fatal("unpublished report exposed")
	}
}
