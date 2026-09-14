package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

func TestReaderSavedSearchRoutesAndSEO(t *testing.T) {
	db := fixtureStore(t)
	seedV2Presentation(t, db, testPresentation{TitleDE: "Fahrrad gefunden", SummaryDE: "Ein Fahrrad wurde gefunden.", TitleEN: "Bicycle recovered · 1201", SummaryEN: "Police recovered a bicycle."}, time.Now())
	server, err := NewWithOptions(db, slog.Default(), Options{SourceMode: "fixture", PresentationMode: "public", PageSize: 20, SecureCookies: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	apply := func(path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://example.com"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "https://example.com")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	response := apply("/en/search", "q=bicycle&date_field=published", nil)
	if response.Code != 303 || response.Header().Get("Location") != "/en/search?q=bicycle#timeline-heading" {
		t.Fatalf("apply %d/%s", response.Code, response.Header().Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range response.Result().Cookies() {
		if c.Name == searchCookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 30*24*60*60 {
		t.Fatalf("cookie=%#v", cookie)
	}
	get := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://example.com"+path, nil)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := get("/en", cookie); w.Code != 302 || w.Header().Get("Location") != "/en/search?q=bicycle" {
		t.Fatalf("restore=%d/%s", w.Code, w.Header().Get("Location"))
	}
	w := get("/en/search?q=bicycle", cookie)
	for _, want := range []string{`name="robots" content="noindex,follow"`, `hx-history="false"`, "Bicycle recovered", "Clear all", `value="bicycle"`, "Page 1 of 1"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("search missing %s", want)
		}
	}
	if w.Code != 200 || w.Header().Get("X-Robots-Tag") != "noindex,follow" {
		t.Errorf("search response %d/%s", w.Code, w.Header().Get("X-Robots-Tag"))
	}
	for _, path := range []string{"/en?view=invalid", "/en?page_size=99", "/en?page=999999"} {
		if w := get(path, nil); w.Code != 400 && w.Code != 404 {
			t.Errorf("invalid %s = %d", path, w.Code)
		}
	}
	if w := get("/en?page_size=10&view=incident", nil); !strings.Contains(w.Body.String(), `content="noindex,follow"`) {
		t.Error("alternate view indexable")
	}
	if w := get("/en", nil); !strings.Contains(w.Body.String(), `content="index,follow,max-image-preview:large"`) {
		t.Error("default view noindex")
	}
	if w := get("/en/contact", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "contact@munichbrief.de") {
		t.Error("contact unavailable")
	}
	for _, path := range []string{"/en/privacy", "/en/impressum"} {
		if w := get(path, nil); w.Code != 200 {
			t.Errorf("legal page unavailable at %s", path)
		}
	}
	clear := apply("/en/search/clear", "", cookie)
	if clear.Code != 303 || clear.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("clear=%d", clear.Code)
	}
	removed := apply("/en/search?q=bicycle", "remove=q", cookie)
	if removed.Code != 303 || removed.Header().Get("Location") != "/en#timeline-heading" {
		t.Fatal("remove did not clear last filter")
	}
	for _, body := range []string{"from=2026-02-30", "from=2026-09-12&to=2026-09-01", "q=" + url.QueryEscape(strings.Repeat("界", 201))} {
		if w := apply("/en/search", body, nil); w.Code != 400 {
			t.Errorf("invalid filter=%d", w.Code)
		}
	}
	// An expired preference cannot resurrect a previous search.
	state, _ := json.Marshal(savedSearch{Version: 1, Expires: time.Now().Add(-time.Hour).Unix(), Filters: store.ReaderFilters{Text: "bicycle"}})
	expired := &http.Cookie{Name: searchCookieName, Value: base64.RawURLEncoding.EncodeToString(state)}
	if w := get("/en", expired); w.Code != 200 {
		t.Error("expired search restored")
	}
	records, _, e := db.ListPresentationEntries(context.Background(), 20, 0, "fixture", store.PresentationScope{Language: "en", PublicOnly: true})
	if e != nil || len(records) != 1 {
		t.Fatalf("seed %d/%v", len(records), e)
	}
}
func TestReaderRejectsCrossOriginSearch(t *testing.T) {
	for _, origin := range []string{"https://evil.invalid", "https://example.com:444", "http://example.com", "null"} {
		r := httptest.NewRequest("POST", "https://example.com/en/search", nil)
		r.Header.Set("Origin", origin)
		if validReaderMutation(r) {
			t.Errorf("accepted %s", origin)
		}
	}
}
func TestReaderPaginationAndTitles(t *testing.T) {
	pages := paginationLinks("en", store.ReaderFilters{}, "published", 20, 19, 40)
	var got []int
	for _, p := range pages {
		if !p.Gap {
			got = append(got, p.Number)
		}
	}
	if len(got) != 7 || got[0] != 1 || got[1] != 17 || got[3] != 19 || got[6] != 40 {
		t.Fatalf("pages=%v", got)
	}
	for title, want := range map[string]string{"Recovered bike · 1201": "Recovered bike", "1201. Recovered bike": "Recovered bike", "Crash on A 1201": "Crash on A 1201", "Events in 2026": "Events in 2026"} {
		if got := readerTitle(title, "1201"); got != want {
			t.Errorf("%q=>%q", title, got)
		}
	}
}

func TestReaderPreferenceValidationAndNavigation(t *testing.T) {
	for _, state := range []savedSearch{
		{Version: 2, Expires: time.Now().Add(time.Hour).Unix(), Filters: store.ReaderFilters{Text: "bike"}},
		{Version: 1, Expires: time.Now().Add(32 * 24 * time.Hour).Unix(), Filters: store.ReaderFilters{Text: "bike"}},
		{Version: 1, Expires: time.Now().Add(time.Hour).Unix(), Filters: store.ReaderFilters{Category: "invalid"}},
	} {
		data, _ := json.Marshal(state)
		r := httptest.NewRequest("GET", "/en", nil)
		r.AddCookie(&http.Cookie{Name: searchCookieName, Value: base64.RawURLEncoding.EncodeToString(data)})
		if readSearch(r).Active() {
			t.Errorf("restored invalid state: %#v", state)
		}
	}
	for _, value := range []string{"not-base64!", strings.Repeat("a", 3801)} {
		r := httptest.NewRequest("GET", "/en", nil)
		r.AddCookie(&http.Cookie{Name: searchCookieName, Value: value})
		if readSearch(r).Active() {
			t.Error("restored malformed cookie")
		}
	}
	for _, size := range []int{10, 20, 30, 50} {
		path := listingURL("tr", store.ReaderFilters{Text: "bike"}, "incident", size, 3)
		r := httptest.NewRequest("GET", path, nil)
		view, actual, err := readerOptions(r, 20)
		if err != nil || view != "incident" || actual != size || r.URL.Query().Get("page") != "3" {
			t.Fatalf("navigation round trip: %s / %s / %d / %v", path, view, actual, err)
		}
		reset := listingURL("en", store.ReaderFilters{Text: "bike"}, view, size, 1)
		if strings.Contains(reset, "page=") {
			t.Errorf("reset retained page: %s", reset)
		}
	}
	for _, pair := range [][2]int{{1, 1}, {1, 9}, {5, 9}, {9, 9}} {
		pages := paginationLinks("en", store.ReaderFilters{}, "published", 20, pair[0], pair[1])
		if pages[0].Number != 1 || pages[len(pages)-1].Number != pair[1] {
			t.Errorf("missing edge page: %#v", pages)
		}
		if pair[0] == 5 && len(pages) != 9 {
			t.Errorf("single-page gaps were not expanded: %#v", pages)
		}
	}
}
