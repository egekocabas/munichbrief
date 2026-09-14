package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

func TestRememberedTimelineNavigationAndExplicitURLs(t *testing.T) {
	s, _ := newContactServer(t)
	get := func(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://munichbrief.de"+path, nil)
		r.Header.Set("HX-Request", "true")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	selectView := get("/en?view=incident")
	var preference *http.Cookie
	for _, c := range selectView.Result().Cookies() {
		if c.Name == timelineCookieName {
			preference = c
		}
	}
	if preference == nil || !preference.HttpOnly || preference.SameSite != http.SameSiteLaxMode || preference.MaxAge != 30*24*60*60 || preference.Path != "/" {
		t.Fatal("invalid preference cookie", preference)
	}
	if !strings.Contains(selectView.Body.String(), `href="/en?view=incident"`) {
		t.Fatal("home lost currently selected view")
	}
	for _, path := range []string{"/en/about", "/en/licenses", "/tr/about", "/en/privacy"} {
		w := get(path, preference)
		lang := strings.Split(path, "/")[1]
		if w.Code != 200 || !strings.Contains(w.Body.String(), `href="/`+lang+`?view=incident"`) {
			t.Errorf("%s lost home preference", path)
		}
		if w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Header().Get("Vary"), "Cookie") {
			t.Error("personalized navigation cacheable")
		}
	}
	language := &http.Cookie{Name: "munichbrief_language", Value: "en"}
	root := get("/", preference, language)
	if root.Code != 302 || root.Header().Get("Location") != "/en?view=incident" || root.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("root did not restore", root.Code, root.Header())
	}
	// A bookmark with no view parameter explicitly means publication order.
	published := get("/en", preference)
	if published.Code != 200 || !strings.Contains(published.Body.String(), `content="index,follow,max-image-preview:large"`) {
		t.Fatal("cookie overrode canonical publication page")
	}
	for _, c := range published.Result().Cookies() {
		if c.Name == timelineCookieName {
			preference = c
		}
	}
	if got := get("/", preference, language).Header().Get("Location"); got != "/en" {
		t.Fatal("publication choice not remembered", got)
	}
	for _, path := range []string{"/en?view=invalid", "/en?page=999999"} {
		w := get(path, preference)
		for _, c := range w.Result().Cookies() {
			if c.Name == timelineCookieName {
				t.Error("invalid navigation changed preference")
			}
		}
	}
}

func TestTimelinePreferenceExpiryAndSearchReturn(t *testing.T) {
	s, _ := newContactServer(t)
	now := time.Now()
	for _, value := range []string{"", "incident", "v2.incident.9999999999", "v1.incident." + strconv.FormatInt(now.Add(-time.Second).Unix(), 10), "v1.incident." + strconv.FormatInt(now.Add(31*24*time.Hour).Unix(), 10), "v1.incident.invalid", strings.Repeat("x", 65)} {
		r := httptest.NewRequest("GET", "https://munichbrief.de/en/about", nil)
		r.AddCookie(&http.Cookie{Name: timelineCookieName, Value: value})
		if readTimelineView(r) != "published" {
			t.Errorf("restored malformed/expired cookie %q", value)
		}
	}
	state, _ := json.Marshal(savedSearch{Version: 1, Expires: now.Add(searchLifetime).Unix(), Filters: store.ReaderFilters{Text: "bicycle"}})
	r := httptest.NewRequest("GET", "https://munichbrief.de/en/about", nil)
	r.AddCookie(&http.Cookie{Name: timelineCookieName, Value: "v1.incident." + strconv.FormatInt(now.Add(searchLifetime).Unix(), 10)})
	r.AddCookie(&http.Cookie{Name: searchCookieName, Value: base64.RawURLEncoding.EncodeToString(state)})
	if got := s.readerHomeURL(r, "tr"); got != "/tr/search?view=incident" {
		t.Fatal("lost saved search or language", got)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "bicycle") {
		t.Fatal("search terms exposed on information page")
	}
	// Cookie's Secure flag follows the established production setting.
	s.options.SecureCookies = true
	w = httptest.NewRecorder()
	s.writeTimelineView(w, "incident")
	if !w.Result().Cookies()[0].Secure {
		t.Fatal("production preference cookie insecure")
	}
}

func TestArticleReturnFallbackKeepsSavedViewAndSearch(t *testing.T) {
	s, db := newContactServer(t)
	job := seedV2Presentation(t, db, testPresentation{TitleDE: "Fahrrad", SummaryDE: "Ein Fahrrad wurde gefunden.", TitleEN: "Bicycle recovered", SummaryEN: "Synthetic bicycle report."}, time.Now())
	state, _ := json.Marshal(savedSearch{Version: 1, Expires: time.Now().Add(searchLifetime).Unix(), Filters: store.ReaderFilters{Text: "bicycle"}})
	r := httptest.NewRequest("GET", "https://munichbrief.de/en/incidents/"+strconv.FormatInt(job.IncidentID, 10), nil)
	r.AddCookie(&http.Cookie{Name: timelineCookieName, Value: "v1.incident." + strconv.FormatInt(time.Now().Add(searchLifetime).Unix(), 10)})
	r.AddCookie(&http.Cookie{Name: searchCookieName, Value: base64.RawURLEncoding.EncodeToString(state)})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `data-timeline-back="/en/search?view=incident"`) {
		t.Fatal("native Back link lost selection", w.Code)
	}
	if !strings.Contains(w.Body.String(), `rel="canonical" href="`+r.URL.String()+`"`) {
		t.Fatal("article canonical changed")
	}
}
