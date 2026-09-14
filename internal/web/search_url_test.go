package web

import (
	"context"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

func searchBrowser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 10 * time.Second}
}
func searchVisit(t *testing.T, client *http.Client, target string) (*http.Response, string) {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, string(body)
}
func searchPost(t *testing.T, client *http.Client, target string, values url.Values) (*http.Response, string) {
	t.Helper()
	r, err := http.NewRequest("POST", target, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := url.Parse(target)
	r.Header.Set("Origin", origin.Scheme+"://"+origin.Host)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, string(body)
}

func TestSharedSearchFreshSessionsAndNavigation(t *testing.T) {
	db := fixtureStore(t)
	for i := range 22 {
		title := "Bicycle recovered " + strconv.Itoa(i)
		seedV2Presentation(t, db, testPresentation{TitleDE: "Fahrrad gefunden", SummaryDE: "Synthetischer Bericht.", TitleEN: title, SummaryEN: "Synthetic bicycle report."}, time.Now())
	}
	seedV2Presentation(t, db, testPresentation{TitleDE: "Zug", SummaryDE: "Synthetischer Bericht.", TitleEN: "Train report", SummaryEN: "Synthetic train report."}, time.Now())
	s, err := NewWithOptions(db, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{SourceMode: "fixture", PresentationMode: "public", PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	browser := searchBrowser(t)
	// The native GET form can send empty/default controls; the final URL omits them.
	response, body := searchVisit(t, browser, server.URL+"/en/search?q=bicycle&area=&category=&number=&assistance=&date_field=published&from=&to=&page_size=10&view=incident")
	canonical := "/en/search?page_size=10&q=bicycle&view=incident"
	if response.StatusCode != 200 || response.Request.URL.RequestURI() != canonical || !strings.Contains(body, "Reports 1–10 of 22") {
		t.Fatalf("apply: %d %s", response.StatusCode, response.Request.URL)
	}
	response, body = searchVisit(t, browser, server.URL+"/en/search?page=2&page_size=10&q=bicycle&view=incident")
	shared := response.Request.URL.String()
	for _, want := range []string{"Reports 11–20 of 22", `hx-history="false"`, `name="robots" content="noindex,follow"`, `name="q" value="bicycle"`, `data-timeline-url="/en/search?page=2&amp;page_size=10&amp;q=bicycle&amp;view=incident"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s", want)
		}
	}
	// Paste the exact address into a new browser with an empty cookie jar.
	fresh := searchBrowser(t)
	freshResponse, freshBody := searchVisit(t, fresh, shared)
	if freshResponse.StatusCode != 200 || freshResponse.Request.URL.String() != shared || !strings.Contains(freshBody, "Reports 11–20 of 22") {
		t.Fatal("fresh session did not reproduce the shared page")
	}
	firstIDs := incidentLinks(body)
	if got := incidentLinks(freshBody); strings.Join(got, ",") != strings.Join(firstIDs, ",") || len(got) == 0 {
		t.Fatal("shared search returned different incidents")
	}
	// A different saved search must not add hidden criteria to a pasted link.
	searchVisit(t, fresh, server.URL+"/en/search?q=train&category=other")
	_, freshBody = searchVisit(t, fresh, shared)
	if strings.Contains(freshBody, `name="remove" value="category"`) || !strings.Contains(freshBody, "Reports 11–20 of 22") {
		t.Fatal("cookie contaminated explicit URL")
	}
	home, _ := searchVisit(t, fresh, server.URL+"/")
	if home.Request.URL.Path != "/en/search" || home.Request.URL.Query().Get("q") != "bicycle" || home.Request.URL.Query().Get("view") != "incident" || home.Request.URL.Query().Has("page") {
		t.Fatal("Home did not restore pasted search", home.Request.URL)
	}
	// Language, view and size navigation carry filters and reset pagination.
	for _, target := range []string{"/de/search?page_size=10&q=bicycle&view=incident", "/en/search?page_size=10&q=bicycle", "/en/search?page_size=30&q=bicycle&view=incident"} {
		w, b := searchVisit(t, fresh, server.URL+target)
		if w.StatusCode != 200 || w.Request.URL.Query().Get("q") != "bicycle" || w.Request.URL.Query().Has("page") || !strings.Contains(b, `name="q" value="bicycle"`) {
			t.Fatal("navigation lost filters", w.Request.URL)
		}
	}
	// Removing a filter in an older tab uses that tab's URL, not the latest cookie.
	searchVisit(t, fresh, server.URL+"/en/search?q=train")
	removed, _ := searchPost(t, fresh, server.URL+"/en/search?category=other&q=bicycle", url.Values{"remove": {"q"}})
	if removed.Request.URL.RequestURI() != "/en/search?category=other" {
		t.Fatal("stale tab removal used another search", removed.Request.URL)
	}
	cleared, _ := searchPost(t, fresh, server.URL+"/en/search/clear", nil)
	if cleared.Request.URL.RequestURI() != "/en" {
		t.Fatal("clear did not return home", cleared.Request.URL)
	}
	restored, _ := searchVisit(t, fresh, server.URL+"/en")
	if restored.Request.URL.RequestURI() != "/en" {
		t.Fatal("cleared search restored itself")
	}
	_, count, err := db.ListPresentationEntries(context.Background(), 50, 0, "fixture", store.PresentationScope{Language: "en", PublicOnly: true})
	if err != nil || count != 23 {
		t.Fatal("search changed reports", count, err)
	}
}

func incidentLinks(body string) []string {
	var links []string
	// Titles and the Read links repeat each incident; equality still detects page drift.
	for rest := body; ; {
		_, tail, ok := strings.Cut(rest, `href="/en/incidents/`)
		if !ok {
			break
		}
		id, tail, _ := strings.Cut(tail, `"`)
		links = append(links, id)
		rest = tail
	}
	return links
}

func TestSharedSearchURLBoundsAndAllFilters(t *testing.T) {
	all := store.ReaderFilters{Text: "İstanbul 自行车 & + # / ?", Area: "Altstadt – Lehel", Category: "traffic", Number: "1201", Assistance: "yes", DateField: "incident", From: "2026-09-01", To: "2026-09-14"}
	target := listingURL("tr", all, "incident", 30, 4)
	r := httptest.NewRequest("GET", target, nil)
	got, explicit, err := readerURLFilters(r)
	if err != nil || !explicit || got != all {
		t.Fatalf("all filters lost: %+v %v", got, err)
	}
	if strings.Contains(target, "#") || strings.Contains(target, "İstanbul") {
		t.Fatal("URL values not escaped")
	}
	s, _ := newContactServer(t)
	for _, target := range []string{"/en/search?q=a&q=b", "/en/search?page=1&page=2", "/en/search?view=incident&view=published", "/en/search?q=%ZZ", "/en/search?q=a;b", "/en/search?category=invalid", "/en/search?from=2026-02-30", "/en/search?from=2026-09-14&to=2026-09-01", "/en/search?q=" + url.QueryEscape(strings.Repeat("界", 201)), "/en/search?q=%00"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "https://munichbrief.de"+target, nil))
		if w.Code != 400 {
			t.Errorf("invalid query accepted: %s status=%d", target, w.Code)
		}
		for _, c := range w.Result().Cookies() {
			if c.Name == searchCookieName || c.Name == timelineCookieName {
				t.Error("invalid URL changed preferences")
			}
		}
	}
	long := "/en/search?q=" + strings.Repeat("a", maxReaderQueryBytes)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "https://munichbrief.de"+long, nil))
	if w.Code != http.StatusRequestURITooLong {
		t.Fatal("long URL not bounded", w.Code)
	}
	// The maximum allowed non-Latin text fields still fit a portable URL and cookie.
	largest := store.ReaderFilters{Text: strings.Repeat("𐐀", 200), Area: strings.Repeat("𐐀", 200), Number: strings.Repeat("𐐀", 200), Category: "other", Assistance: "no", From: "2026-01-01", To: "2026-12-31", DateField: "incident"}
	path := listingURL("zh", largest, "incident", 50, 1)
	if len(path) > maxReaderQueryBytes {
		t.Fatal("valid fields produce an oversized URL", len(path))
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "https://munichbrief.de"+path, nil))
	if w.Code != 200 {
		t.Fatal("valid long Unicode search failed", w.Code)
	}
	t.Logf("all fields with 600 supplementary Unicode characters: %d URL bytes", len(path))
	punctuation := store.ReaderFilters{Text: strings.Repeat("&", 200), Area: strings.Repeat("<", 200), Number: strings.Repeat(">", 200)}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "https://munichbrief.de"+listingURL("en", punctuation, "published", 20, 1), nil))
	if w.Code != 200 {
		t.Fatal("valid punctuation exceeded the preference cookie", w.Code)
	}
	escaped := listingURL("en", store.ReaderFilters{Text: `<script>alert("x")</script>`}, "published", 20, 1)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "https://munichbrief.de"+escaped, nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), `<script>alert("x")</script>`) || !strings.Contains(w.Body.String(), html.EscapeString(`<script>alert("x")</script>`)) {
		t.Fatal("unsafe search rendering")
	}
}

func TestSharedSearchPreferenceExpiryAndEmptyURL(t *testing.T) {
	s, _ := newContactServer(t)
	// A shared URL carries the full filter state and wins over unrelated cookies.
	saved := httptest.NewRecorder()
	if err := s.writeSearch(saved, store.ReaderFilters{Text: "old", Area: "Haar"}); err != nil {
		t.Fatal(err)
	}
	cookie := saved.Result().Cookies()[0]
	run := func(target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://munichbrief.de"+target, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	w := run("/en/search?q=new")
	if w.Code != 200 || w.Header().Get("X-Robots-Tag") != "noindex,follow" {
		t.Fatal(w.Code, w.Header())
	}
	var updated *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == searchCookieName {
			updated = c
		}
	}
	if updated == nil {
		t.Fatal("pasted URL was not remembered")
	}
	r := httptest.NewRequest("GET", "/en", nil)
	r.AddCookie(updated)
	if f := readSearch(r); f.Text != "new" || f.Area != "" {
		t.Fatal("saved filters were merged", f)
	}
	cookie = updated
	w = run("/en/search?q=new")
	for _, c := range w.Result().Cookies() {
		if c.Name == searchCookieName {
			t.Fatal("unchanged search renewed its expiry")
		}
	}
	w = run("/en/search?page=999&q=different")
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == searchCookieName || c.Name == timelineCookieName {
			t.Fatal("invalid page changed preferences")
		}
	}
	// Empty search is explicit, rather than a cookie-dependent hidden search.
	w = run("/en/search")
	if w.Code != 302 || w.Header().Get("Location") != "/en" {
		t.Fatal("empty search did not clear", w.Code, w.Header())
	}
	deleted := false
	for _, c := range w.Result().Cookies() {
		if c.Name == searchCookieName && c.MaxAge == -1 {
			deleted = true
		}
	}
	if !deleted {
		t.Fatal("empty search left saved filters behind")
	}
}
