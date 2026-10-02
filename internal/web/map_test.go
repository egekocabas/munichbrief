package web

import (
	"context"
	"html"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/location"
	"github.com/egekocabas/munichbrief/internal/store"
)

func TestMapCalendarPeriodsAndValidation(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ now, period, from, to string }{
		{"2026-03-30", "week", "2026-03-24", "2026-03-30"},
		{"2026-10-26", "month", "2026-09-27", "2026-10-26"},
		{"2026-03-01", "previous_month", "2026-02-01", "2026-02-28"},
		{"2024-03-31", "previous_month", "2024-02-01", "2024-02-29"},
		{"2026-01-01", "previous_month", "2025-12-01", "2025-12-31"},
	} {
		now, _ := time.ParseInLocation(time.DateOnly, tc.now, berlin)
		_, got, err := parseMapFilters(url.Values{"period": {tc.period}}, now)
		if err != nil || got.From != tc.from || got.To != tc.to {
			t.Fatalf("%+v => %+v, %v", tc, got, err)
		}
	}
	for _, raw := range []string{"period=today", "period=week&period=month", "from=2026-02-30", "from=2026-10-03&to=2026-10-01", "district=Schwabing", "district=outside&district=unassigned", "category=unknown", "area=Schwabing", "q=hello", "neighborhood=Lehel", "view=incident"} {
		q, _ := url.ParseQuery(raw)
		if _, _, err := parseMapFilters(q, time.Now().In(berlin)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	f, r, err := parseMapFilters(url.Values{"period": {"week"}, "from": {"2026-09-01"}}, time.Now().In(berlin))
	if err != nil || f.Period != "custom" || r.From != "2026-09-01" || r.To != "" {
		t.Fatal(f, r, err)
	}
	f, _, err = parseMapFilters(url.Values{"period": {"custom"}}, time.Now().In(berlin))
	if err != nil || f.Period != "all" {
		t.Fatal(f, err)
	}
}

func TestMapPublicCountsLinksTranslationsAndCookies(t *testing.T) {
	s, db := newContactServer(t)
	job := seedV2Presentation(t, db, testPresentation{TitleDE: "Testbericht", SummaryDE: "Zusammenfassung.", TitleEN: "Test report", SummaryEN: "Summary.", AreaName: "Neuperlach"}, time.Now())
	district := location.DistrictGroup("Neuperlach")
	f := store.ReaderFilters{District: district, Category: "other"}
	prefs := httptest.NewRecorder()
	if err := s.writeSearch(prefs, store.ReaderFilters{Area: "Does not exist"}); err != nil {
		t.Fatal(err)
	}
	path := mapURL("en", mapFilters{District: district, Category: "other"})
	page := contactRequest(s, "GET", path, nil, prefs.Result().Cookies()...)
	if page.Code != 200 {
		t.Fatal(page.Code, page.Body.String())
	}
	for _, want := range []string{"Munich, in reports.", "Ramersdorf-Perlach", `class="atlas-district atlas-level-5"`, `aria-current="true"`, `href="` + html.EscapeString(readerActionURL("en", f, "published", 20)) + `"`, `href="` + html.EscapeString(readerActionURL("de", f, "published", 20)) + `"`, "Boundaries simplified"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(page.Body.String(), "Does not exist") {
		t.Fatal("saved reader filter leaked into map")
	}
	for _, cookie := range page.Result().Cookies() {
		if cookie.Name == searchCookieName || cookie.Name == neighborhoodsCookieName {
			t.Fatal("map changed reader preference", cookie.Name)
		}
	}
	for _, lang := range []string{"de", "en"} {
		list, err := db.ListReaderEntries(context.Background(), store.ReaderQuery{Language: lang, SourceMode: "fixture", Filters: f, Limit: 20})
		if err != nil || list.Total != 1 || list.Records[0].ID != job.IncidentID {
			t.Fatal(lang, list, err)
		}
		res := contactRequest(s, "GET", strings.TrimSuffix(readerActionURL(lang, f, "published", 20), "#timeline-heading"), nil, prefs.Result().Cookies()...)
		if res.Code != 200 || !strings.Contains(res.Body.String(), "Ramersdorf-Perlach") {
			t.Fatal(res.Code)
		}
	}
	// French has no accepted translation, but the canonical total stays one.
	stats, err := db.ReaderStats(context.Background(), store.ReaderStatsQuery{Language: "fr", SourceMode: "fixture", Filters: f})
	if err != nil || stats.Total != 1 || stats.Available != 0 {
		t.Fatal(stats, err)
	}
	req := httptest.NewRequest("GET", "https://munichbrief.de"+path, nil)
	req.Header.Set("Accept", "text/markdown")
	md := httptest.NewRecorder()
	s.Handler().ServeHTTP(md, req)
	if md.Code != 200 || !strings.HasPrefix(md.Header().Get("Content-Type"), "text/markdown") || md.Header().Get("X-Robots-Tag") != "noindex,follow" || !strings.Contains(md.Body.String(), "Boundaries simplified") || !strings.Contains(md.Body.String(), "Ramersdorf-Perlach: 1") {
		t.Fatal(md.Code, md.Header(), md.Body.String())
	}
	for _, lang := range s.languages {
		res := contactRequest(s, "GET", "/"+lang.Code+"/map", nil)
		if res.Code != 200 || strings.Contains(res.Body.String(), "[Map") {
			t.Errorf("localized map %s failed %d", lang.Code, res.Code)
		}
	}
	empty := contactRequest(s, "GET", "/en/map?from=2099-01-01", nil)
	if empty.Code != 200 || !strings.Contains(empty.Body.String(), s.localization.Text("en", "MapEmpty")) {
		t.Fatal("empty map missing helpful state", empty.Code)
	}
	for _, raw := range []string{"?from=%zz", "?district=invalid", "?category=other&category=traffic"} {
		if res := contactRequest(s, "GET", "/en/map"+raw, nil); res.Code != 400 {
			t.Errorf("accepted %s: %d", raw, res.Code)
		}
	}
}

func TestMapSitemapAndNavigation(t *testing.T) {
	s, _ := newContactServer(t)
	for _, path := range []string{"/sitemap.xml", "/en", "/en/about", "/en/privacy"} {
		res := contactRequest(s, "GET", path, nil)
		if res.Code != 200 || !strings.Contains(res.Body.String(), "/en/map") {
			t.Errorf("missing map navigation/discovery %s", path)
		}
	}
}

func TestMapMarkdownEscapesTrendAndScopeText(t *testing.T) {
	s, _ := newContactServer(t)
	base := s.base(httptest.NewRequest("GET", "https://munichbrief.de/en/map", nil), "en", "/en/map")
	attack := `<img src=x onerror="alert(1)"> & [label]`
	data := mapPage{basePage: base, PeriodLabel: attack, DistrictLabel: attack, LanguageName: attack, Trend: []mapBar{{Label: attack, Total: 1}}, GermanURL: "/de/search", LanguageURL: "/en/search"}
	w := httptest.NewRecorder()
	s.renderMapMarkdown(w, data)
	body := w.Body.String()
	if strings.Contains(body, "<img") || !strings.Contains(body, "&lt;img") || !strings.Contains(body, "&amp;") || !strings.Contains(body, `\[label\]`) {
		t.Fatal(body)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Header())
	}
	for _, key := range []string{"from", "to"} {
		path := "/en/map?" + url.Values{key: {attack}}.Encode()
		if res := contactRequest(s, "GET", path, nil); res.Code != 400 {
			t.Fatal("accepted malformed date", res.Code)
		}
	}
}
