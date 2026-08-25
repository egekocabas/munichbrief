package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/source"
	"github.com/egekocabas/munichbrief/internal/store"
)

func TestTimelineAndDetailRenderFixtureData(t *testing.T) {
	database := fixtureStore(t)
	server := testServer(t, database)
	handler := server.Handler()

	timeline := httptest.NewRecorder()
	handler.ServeHTTP(timeline, englishRequest(http.MethodGet, "/en", nil))
	if timeline.Code != http.StatusOK {
		t.Fatalf("timeline status = %d, want 200", timeline.Code)
	}
	for _, expected := range []string{
		"Synthetic reports",
		"Fahrradunfall; eine Person leicht verletzt",
		"Größerer Polizeieinsatz",
		"Fixture data · Review mode",
		"Not yet summarized or translated",
		"28 reports",
		"Page 1 of 2",
		"/en?page=2",
	} {
		if !strings.Contains(timeline.Body.String(), expected) {
			t.Errorf("timeline body does not contain %q", expected)
		}
	}

	olderTimeline := httptest.NewRecorder()
	handler.ServeHTTP(olderTimeline, englishRequest(http.MethodGet, "/en?page=2", nil))
	if olderTimeline.Code != http.StatusOK {
		t.Fatalf("older timeline status = %d, want 200", olderTimeline.Code)
	}
	for _, expected := range []string{"Page 2 of 2", "Beschädigte Eingangstür", "← Newer"} {
		if !strings.Contains(olderTimeline.Body.String(), expected) {
			t.Errorf("older timeline body does not contain %q", expected)
		}
	}

	records, _, err := database.ListIncidents(context.Background(), 20, 0)
	if err != nil {
		t.Fatalf("ListIncidents() error = %v", err)
	}
	detail := httptest.NewRecorder()
	handler.ServeHTTP(detail, englishRequest(http.MethodGet, "/en/incidents/"+formatID(records[0].ID), nil))
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", detail.Code)
	}
	if !strings.Contains(detail.Body.String(), records[0].TitleDE) {
		t.Errorf("detail body does not contain incident title %q", records[0].TitleDE)
	}
}

func TestTemplatesEscapeIncidentContent(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "munichbrief.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { database.Close() })

	documents := []domain.SourceDocument{{
		ExternalID:      "hostile-fixture",
		SourceURL:       "https://fixture.invalid/hostile",
		Title:           "Hostile fixture",
		PublishedAt:     time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC),
		FeedFingerprint: "feed-hash",
		SourceHash:      "source-hash",
		Incidents: []domain.Incident{{
			Number:      "1",
			Position:    0,
			TitleDE:     "<script>alert('title')</script>",
			BodyDE:      "<img src=x onerror=alert('body')>",
			ContentHash: "incident-hash",
		}},
	}}
	if err := database.UpsertDocuments(ctx, documents, time.Now()); err != nil {
		t.Fatalf("UpsertDocuments() error = %v", err)
	}
	job, found, err := database.QueueAndClaimProcessingJob(ctx, processing.Operation("qwen3.5:4b"), time.Now())
	if err != nil || !found {
		t.Fatalf("QueueAndClaimProcessingJob() = found:%t err:%v", found, err)
	}
	if err := database.CompleteProcessingJob(ctx, job, store.AIPresentation{
		TitleDE:       "<script>alert('title')</script>",
		SummaryDE:     "<img src=x onerror=alert('body')>",
		TitleEN:       "<script>alert('english-title')</script>",
		SummaryEN:     "<img src=x onerror=alert('english-body')>",
		PrivacyStatus: "safe",
	}, "qwen3.5:4b", processing.PromptVersion, time.Now()); err != nil {
		t.Fatalf("CompleteProcessingJob() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	testServer(t, database).Handler().ServeHTTP(recorder, englishRequest(http.MethodGet, "/en", nil))
	body := recorder.Body.String()
	if strings.Contains(body, "<script>") || strings.Contains(body, "<img src=x") {
		t.Fatalf("timeline rendered unescaped hostile markup: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, "&lt;img") {
		t.Error("timeline did not render escaped hostile content")
	}
	detail := httptest.NewRecorder()
	testServer(t, database).Handler().ServeHTTP(detail, englishRequest(http.MethodGet, "/en/incidents/"+formatID(job.IncidentID), nil))
	if strings.Contains(detail.Body.String(), "<script>") || strings.Contains(detail.Body.String(), "<img src=x") {
		t.Fatalf("detail rendered unescaped hostile AI markup: %s", detail.Body.String())
	}
}

func TestTimelineAndDetailRenderAIContentWithProvenance(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	generatedAt := time.Date(2026, time.August, 23, 9, 0, 0, 0, time.UTC)
	operation := "incident-presentation/" + processing.PromptVersion + "/qwen3.5:4b"
	job, found, err := database.QueueAndClaimProcessingJob(ctx, operation, generatedAt)
	if err != nil || !found {
		t.Fatalf("QueueAndClaimProcessingJob() = found:%t err:%v", found, err)
	}
	presentation := store.AIPresentation{
		TitleDE:       "Kurzer deutscher Titel",
		SummaryDE:     "Eine sachliche deutsche Zusammenfassung.",
		TitleEN:       "Short English title",
		SummaryEN:     "A factual English summary.",
		PrivacyStatus: "safe",
	}
	if err := database.CompleteProcessingJob(ctx, job, presentation, "qwen3.5:4b", processing.PromptVersion, generatedAt); err != nil {
		t.Fatalf("CompleteProcessingJob() error = %v", err)
	}

	handler := testServer(t, database).Handler()
	timeline := httptest.NewRecorder()
	handler.ServeHTTP(timeline, englishRequest(http.MethodGet, "/en", nil))
	for _, expected := range []string{"AI-generated summary", presentation.TitleEN, presentation.SummaryEN} {
		if !strings.Contains(timeline.Body.String(), expected) {
			t.Errorf("timeline body does not contain %q", expected)
		}
	}

	detail := httptest.NewRecorder()
	handler.ServeHTTP(detail, englishRequest(http.MethodGet, "/en/incidents/"+formatID(job.IncidentID), nil))
	for _, expected := range []string{
		"AI-generated summary", presentation.TitleEN, presentation.SummaryEN,
		"Original German text", "Visible only for quality review",
	} {
		if !strings.Contains(detail.Body.String(), expected) {
			t.Errorf("detail body does not contain %q", expected)
		}
	}
}

func TestLiveTimelineFallbackAndIncidentAttribution(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "munichbrief.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Date(2026, time.August, 22, 10, 0, 0, 0, time.UTC)
	documents := []domain.SourceDocument{
		{
			ExternalID:      "107500",
			SourceURL:       "https://polizei.bayern.de/aktuelles/pressemitteilungen/107500/index.html",
			Title:           "Live parsed report",
			PublishedAt:     now,
			FeedFingerprint: "live-parsed",
		},
		{
			ExternalID:      "107501",
			SourceURL:       "https://polizei.bayern.de/aktuelles/pressemitteilungen/107501/index.html",
			Title:           "Live metadata fallback",
			PublishedAt:     now.Add(-time.Hour),
			FeedFingerprint: "live-fallback",
		},
	}
	if err := database.UpsertSourceMetadata(ctx, documents, now); err != nil {
		t.Fatalf("UpsertSourceMetadata() error = %v", err)
	}
	records, err := database.ListDocumentsForFetch(ctx, now.Add(-24*time.Hour), now.Add(24*time.Hour), now, false)
	if err != nil {
		t.Fatalf("ListDocumentsForFetch() error = %v", err)
	}
	for _, record := range records {
		switch record.ExternalID {
		case "107500":
			if err := database.ReplaceDocumentIncidents(ctx, record.ID, "source-hash", []domain.Incident{{
				Number: "1300", Position: 0, TitleDE: "Live incident", BodyDE: "Live body", ContentHash: "content-hash",
			}}, now); err != nil {
				t.Fatalf("ReplaceDocumentIncidents() error = %v", err)
			}
		case "107501":
			if err := database.MarkDocumentFetchFailed(ctx, record.ID, now, context.DeadlineExceeded); err != nil {
				t.Fatalf("MarkDocumentFetchFailed() error = %v", err)
			}
		}
	}

	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	server, err := New(database, logger, 20, "live")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	timeline := httptest.NewRecorder()
	server.Handler().ServeHTTP(timeline, englishRequest(http.MethodGet, "/en", nil))
	for _, expected := range []string{"Live source", "Live incident", "Live metadata fallback", "Open official source"} {
		if !strings.Contains(timeline.Body.String(), expected) {
			t.Errorf("live timeline does not contain %q", expected)
		}
	}

	incidents, _, err := database.ListTimelineEntries(ctx, 20, 0, "live")
	if err != nil {
		t.Fatalf("ListTimelineEntries() error = %v", err)
	}
	var incidentID int64
	for _, incident := range incidents {
		if incident.HasIncident {
			incidentID = incident.ID
		}
	}
	detail := httptest.NewRecorder()
	server.Handler().ServeHTTP(detail, englishRequest(http.MethodGet, "/en/incidents/"+formatID(incidentID), nil))
	for _, expected := range []string{"Last processed", "Bavarian Police release remains authoritative", documents[0].SourceURL} {
		if !strings.Contains(detail.Body.String(), expected) {
			t.Errorf("live detail does not contain %q", expected)
		}
	}
}

func TestUnknownIncidentReturnsNotFound(t *testing.T) {
	recorder := httptest.NewRecorder()
	testServer(t, fixtureStore(t)).Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/en/incidents/9999", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestAboutHealthReadinessAndRequestHeaders(t *testing.T) {
	database := fixtureStore(t)
	handler := testServer(t, database).Handler()

	about := httptest.NewRecorder()
	request := englishRequest(http.MethodGet, "/en/about", nil)
	request.Header.Set("X-Request-ID", "test-request")
	handler.ServeHTTP(about, request)
	if about.Code != http.StatusOK || !strings.Contains(about.Body.String(), "What MunichBrief does") {
		t.Fatalf("about response = %d/%q", about.Code, about.Body.String())
	}
	if about.Header().Get("X-Request-ID") != "test-request" || about.Header().Get("X-Correlation-ID") != "test-request" {
		t.Errorf("request headers = %q/%q", about.Header().Get("X-Request-ID"), about.Header().Get("X-Correlation-ID"))
	}
	if about.Header().Get("Content-Security-Policy") == "" {
		t.Error("about response has no Content-Security-Policy")
	}
	for _, expected := range []string{`script-src 'self'`, `style-src 'self'`} {
		if !strings.Contains(about.Header().Get("Content-Security-Policy"), expected) {
			t.Errorf("content security policy does not contain %q", expected)
		}
	}
	for _, expected := range []string{`src="/static/htmx.min.js"`, `hx-boost="true"`, `"allowEval":false`} {
		if !strings.Contains(about.Body.String(), expected) {
			t.Errorf("about response does not contain %q", expected)
		}
	}

	for _, path := range []string{"/healthz", "/readyz"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", path, recorder.Code)
		}
	}

	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metrics.Code != http.StatusNotFound {
		t.Errorf("reader /metrics status = %d, want 404", metrics.Code)
	}

	for _, asset := range []struct {
		path        string
		contentType string
		body        string
	}{
		{path: "/static/app.css", contentType: "text/css; charset=utf-8", body: "--color-civic"},
		{path: "/static/htmx.min.js", contentType: "text/javascript; charset=utf-8", body: "htmx"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, asset.path, nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != asset.contentType || !strings.Contains(response.Header().Get("Cache-Control"), "public") || !strings.Contains(response.Body.String(), asset.body) {
			t.Errorf("asset %s response = %d/%q/%q", asset.path, response.Code, response.Header().Get("Content-Type"), response.Header().Get("Cache-Control"))
		}
	}
}

func TestAdminIsDisabledByDefault(t *testing.T) {
	handler := testServer(t, fixtureStore(t)).Handler()
	for _, test := range []struct {
		method, path string
	}{
		{method: http.MethodGet, path: "/admin"},
		{method: http.MethodPost, path: "/api/admin/ai/retry-all"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404", test.method, test.path, response.Code)
		}
	}
}

func TestAdminRendersStatsAndQueuesRetries(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	operation := processing.Operation("qwen3.5:4b")
	failedIDs := make([]int64, 0, 2)
	for range 2 {
		job, found, err := database.QueueAndClaimProcessingJob(ctx, operation, time.Now())
		if err != nil || !found {
			t.Fatalf("claim admin fixture job = %t/%v", found, err)
		}
		if err := database.FailProcessingJob(ctx, job, "needs_review", "privacy", nil, time.Now(), context.Canceled); err != nil {
			t.Fatal(err)
		}
		failedIDs = append(failedIDs, job.IncidentID)
	}
	server := adminTestServer(t, database, nil)
	handler := server.Handler()
	initialStats, err := database.ProcessingQueueStats(ctx, operation, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if page.Code != http.StatusOK || page.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("admin page = %d/%q", page.Code, page.Header().Get("Cache-Control"))
	}
	for _, expected := range []string{"AI processing", "Needs review", ">2<", "/api/admin/ai/retry", "/api/admin/ai/retry-all"} {
		if !strings.Contains(page.Body.String(), expected) {
			t.Errorf("admin page does not contain %q", expected)
		}
	}

	for _, attack := range []struct {
		name, fetchSite, origin string
	}{
		{name: "fetch metadata", fetchSite: "cross-site", origin: ""},
		{name: "origin", fetchSite: "same-site", origin: "https://attacker.example"},
	} {
		crossSite := formRequest(http.MethodPost, "/api/admin/ai/retry", "incident_id="+formatID(failedIDs[0]))
		crossSite.Header.Set("Sec-Fetch-Site", attack.fetchSite)
		crossSite.Header.Set("Origin", attack.origin)
		crossSiteResponse := httptest.NewRecorder()
		handler.ServeHTTP(crossSiteResponse, crossSite)
		if crossSiteResponse.Code != http.StatusForbidden {
			t.Errorf("%s cross-site retry status = %d, want 403", attack.name, crossSiteResponse.Code)
		}
	}

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, formRequest(http.MethodPost, "/api/admin/ai/retry", "incident_id=zero"))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid retry status = %d, want 400", invalid.Code)
	}

	retryOne := httptest.NewRecorder()
	handler.ServeHTTP(retryOne, formRequest(http.MethodPost, "/api/admin/ai/retry", "incident_id="+formatID(failedIDs[0])))
	if retryOne.Code != http.StatusSeeOther || retryOne.Header().Get("Location") != "/admin?retried=1" {
		t.Fatalf("retry one = %d/%q", retryOne.Code, retryOne.Header().Get("Location"))
	}
	retryAll := httptest.NewRecorder()
	handler.ServeHTTP(retryAll, formRequest(http.MethodPost, "/api/admin/ai/retry-all", ""))
	if retryAll.Code != http.StatusSeeOther || retryAll.Header().Get("Location") != "/admin?retried=1" {
		t.Fatalf("retry all = %d/%q", retryAll.Code, retryAll.Header().Get("Location"))
	}
	stats, err := database.ProcessingQueueStats(ctx, operation, time.Now())
	if err != nil || stats.NeedsReview != 0 || stats.Queued != initialStats.Queued+2 {
		t.Fatalf("retried stats = %#v/%v", stats, err)
	}
}

func TestAdminReportsStoreErrors(t *testing.T) {
	database := fixtureStore(t)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	statsServer, err := NewWithOptions(failingAdminStore{Store: database, statsError: errors.New("stats unavailable")}, logger, Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review", ModelIdentity: "qwen3.5:4b",
		PromptVersion: processing.PromptVersion, AdminEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	statsResponse := httptest.NewRecorder()
	statsServer.Handler().ServeHTTP(statsResponse, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if statsResponse.Code != http.StatusInternalServerError {
		t.Fatalf("admin stats error status = %d, want 500", statsResponse.Code)
	}

	retryServer, err := NewWithOptions(failingAdminStore{Store: database, retryError: errors.New("retry unavailable")}, logger, Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review", ModelIdentity: "qwen3.5:4b",
		PromptVersion: processing.PromptVersion, AdminEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	retryResponse := httptest.NewRecorder()
	retryServer.Handler().ServeHTTP(retryResponse, formRequest(http.MethodPost, "/api/admin/ai/retry-all", ""))
	if retryResponse.Code != http.StatusInternalServerError {
		t.Fatalf("admin retry error status = %d, want 500", retryResponse.Code)
	}
}

func TestPublicHostUsesFailClosedPresentationAndRejectsAdmin(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	job, found, err := database.QueueAndClaimProcessingJob(ctx, processing.Operation("qwen3.5:4b"), time.Now())
	if err != nil || !found {
		t.Fatalf("claim public fixture job = %t/%v", found, err)
	}
	presentation := store.AIPresentation{
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
		TitleEN: "Safe title", SummaryEN: "Safe summary.", PrivacyStatus: "safe",
	}
	if err := database.CompleteProcessingJob(ctx, job, presentation, "qwen3.5:4b", processing.PromptVersion, time.Now()); err != nil {
		t.Fatal(err)
	}
	publicHosts := []string{"munichbrief.egekocabas.com", "munichbrief.de"}
	server := adminTestServer(t, database, publicHosts)
	handler := server.Handler()
	path := "/en/incidents/" + formatID(job.IncidentID)

	lan := httptest.NewRecorder()
	lanRequest := englishRequest(http.MethodGet, path, nil)
	lanRequest.Host = "munichbrief.home.egekocabas.com"
	handler.ServeHTTP(lan, lanRequest)
	if lan.Code != http.StatusOK || !strings.Contains(lan.Body.String(), "Original German text") {
		t.Fatalf("LAN detail did not retain review mode: %d", lan.Code)
	}

	for _, publicHost := range publicHosts {
		t.Run(publicHost, func(t *testing.T) {
			public := httptest.NewRecorder()
			publicRequest := englishRequest(http.MethodGet, path, nil)
			publicRequest.Host = publicHost
			handler.ServeHTTP(public, publicRequest)
			if public.Code != http.StatusOK || !strings.Contains(public.Body.String(), presentation.SummaryEN) {
				t.Fatalf("public detail = %d/%q", public.Code, public.Body.String())
			}
			for _, forbidden := range []string{"Original German text", "Review mode", job.BodyDE} {
				if strings.Contains(public.Body.String(), forbidden) {
					t.Errorf("public detail exposed %q", forbidden)
				}
			}

			for _, path := range []string{"/admin", "/api/admin/ai/retry-all", "/private"} {
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodGet, path, nil)
				request.Host = publicHost
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusNotFound {
					t.Errorf("public %s status = %d, want 404", path, response.Code)
				}
			}
		})
	}
}

func TestReadinessFailsOnlyWhenDatabaseIsUnavailable(t *testing.T) {
	database := fixtureStore(t)
	handler := testServer(t, database).Handler()
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want 503", recorder.Code)
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", health.Code)
	}
}

func TestLocalizedRoutesAndLanguagePreference(t *testing.T) {
	database := fixtureStore(t)
	handler := testServer(t, database).Handler()

	browserRequest := httptest.NewRequest(http.MethodGet, "/?page=2", nil)
	browserRequest.Header.Set("Accept-Language", "en-GB;q=0.9,de;q=0.8")
	browserResponse := httptest.NewRecorder()
	handler.ServeHTTP(browserResponse, browserRequest)
	if browserResponse.Code != http.StatusFound || browserResponse.Header().Get("Location") != "/en?page=2" {
		t.Fatalf("browser redirect = %d %q", browserResponse.Code, browserResponse.Header().Get("Location"))
	}

	cookieRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	cookieRequest.Header.Set("Accept-Language", "en")
	cookieRequest.AddCookie(&http.Cookie{Name: "munichbrief_language", Value: "de"})
	cookieResponse := httptest.NewRecorder()
	handler.ServeHTTP(cookieResponse, cookieRequest)
	if cookieResponse.Header().Get("Location") != "/de" {
		t.Fatalf("cookie redirect = %q", cookieResponse.Header().Get("Location"))
	}

	defaultResponse := httptest.NewRecorder()
	handler.ServeHTTP(defaultResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	if defaultResponse.Header().Get("Location") != "/de" {
		t.Fatalf("default redirect = %q", defaultResponse.Header().Get("Location"))
	}

	english := httptest.NewRecorder()
	handler.ServeHTTP(english, httptest.NewRequest(http.MethodGet, "/en/about?page=2", nil))
	if english.Code != http.StatusOK || english.Header().Get("Content-Language") != "en" || !strings.Contains(english.Body.String(), "What MunichBrief does") {
		t.Fatalf("English page = %d/%q", english.Code, english.Header().Get("Content-Language"))
	}
	if !strings.Contains(english.Body.String(), `href="/de/about?page=2"`) || !strings.Contains(english.Body.String(), `hreflang="de"`) {
		t.Fatal("English page does not preserve path and query in its language switch")
	}
	cookies := english.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "munichbrief_language" || cookies[0].Value != "en" || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].MaxAge != 365*24*60*60 || cookies[0].Expires.Before(time.Now().Add(364*24*time.Hour)) {
		t.Fatalf("language cookie = %#v", cookies)
	}

	german := httptest.NewRecorder()
	germanRequest := httptest.NewRequest(http.MethodGet, "/de", nil)
	germanRequest.Header.Set("Accept-Language", "en")
	handler.ServeHTTP(german, germanRequest)
	if german.Header().Get("Content-Language") != "de" || !strings.Contains(german.Body.String(), "Aktuelle Meldungen aus München") {
		t.Fatal("localized path did not override the browser language")
	}

	legacy := httptest.NewRecorder()
	legacyRequest := httptest.NewRequest(http.MethodGet, "/about?from=legacy", nil)
	legacyRequest.Header.Set("Accept-Language", "en")
	handler.ServeHTTP(legacy, legacyRequest)
	if legacy.Code != http.StatusFound || legacy.Header().Get("Location") != "/en/about?from=legacy" {
		t.Fatalf("legacy redirect = %d %q", legacy.Code, legacy.Header().Get("Location"))
	}
	records, _, err := database.ListIncidents(context.Background(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	legacyIncident := httptest.NewRecorder()
	legacyIncidentRequest := httptest.NewRequest(http.MethodGet, "/incidents/"+formatID(records[0].ID)+"?from=legacy", nil)
	legacyIncidentRequest.Header.Set("Accept-Language", "en")
	handler.ServeHTTP(legacyIncident, legacyIncidentRequest)
	wantedLocation := "/en/incidents/" + formatID(records[0].ID) + "?from=legacy"
	if legacyIncident.Code != http.StatusFound || legacyIncident.Header().Get("Location") != wantedLocation {
		t.Fatalf("legacy incident redirect = %d %q", legacyIncident.Code, legacyIncident.Header().Get("Location"))
	}

	for _, path := range []string{"/fr", "/fr/about"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, response.Code)
		}
	}
	removedForm := httptest.NewRecorder()
	handler.ServeHTTP(removedForm, httptest.NewRequest(http.MethodPost, "/language", nil))
	if removedForm.Code != http.StatusMethodNotAllowed {
		t.Fatalf("removed language form status = %d, want 405", removedForm.Code)
	}

	secureServer, err := NewWithOptions(fixtureStore(t), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review", ModelIdentity: "qwen3.5:4b",
		PromptVersion: processing.PromptVersion, SecureCookies: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	secureResponse := httptest.NewRecorder()
	secureServer.Handler().ServeHTTP(secureResponse, httptest.NewRequest(http.MethodGet, "/en", nil))
	secureCookies := secureResponse.Result().Cookies()
	if len(secureCookies) != 1 || !secureCookies[0].Secure {
		t.Fatalf("secure language cookie = %#v", secureCookies)
	}
}

func TestLocalizedProcessingStateLabels(t *testing.T) {
	server := testServer(t, fixtureStore(t))
	for _, test := range []struct {
		record   store.IncidentRecord
		expected string
	}{
		{record: store.IncidentRecord{}, expected: "Not yet summarized or translated"},
		{record: store.IncidentRecord{ProcessingStatus: "pending"}, expected: "AI summary and translation queued"},
		{record: store.IncidentRecord{ProcessingStatus: "running", ProcessingAttempts: 1}, expected: "AI summary and translation in progress"},
		{record: store.IncidentRecord{ProcessingStatus: "pending", ProcessingAttempts: 1}, expected: "AI temporarily unavailable; retry scheduled"},
		{record: store.IncidentRecord{ProcessingStatus: "needs_review", ProcessingAttempts: 3}, expected: "AI output requires review"},
		{record: store.IncidentRecord{ProcessingStatus: "failed"}, expected: "AI output requires review"},
	} {
		view := server.incidentForLanguage(test.record, "en")
		if view.ProcessingLabel != test.expected {
			t.Errorf("state %q label = %q, want %q", test.record.ProcessingState(), view.ProcessingLabel, test.expected)
		}
	}
}

func TestTranslationCatalogsAreCompleteAndPluralized(t *testing.T) {
	translations, err := newLocalization()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		language string
		count    int
		expected string
	}{
		{language: "de", count: 1, expected: "1 Meldung"},
		{language: "de", count: 2, expected: "2 Meldungen"},
		{language: "en", count: 1, expected: "1 report"},
		{language: "en", count: 2, expected: "2 reports"},
	} {
		if actual := translations.Count(test.language, "Reports", test.count); actual != test.expected {
			t.Errorf("Count(%q, %d) = %q, want %q", test.language, test.count, actual, test.expected)
		}
	}

	broken := fstest.MapFS{
		"de.toml": {Data: []byte("[OnlyGerman]\nother = 'Deutsch'\n")},
		"en.toml": {Data: []byte("[OnlyEnglish]\nother = 'English'\n")},
	}
	if err := validateCatalogParity(broken, "de.toml", "en.toml"); err == nil {
		t.Fatal("mismatched translation catalogs were accepted")
	}
}

func TestPublicModeHidesUnprocessedStaleAndOriginalContent(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	records, _, err := database.ListIncidents(ctx, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	publicServer, err := NewWithOptions(database, logger, Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "public",
		ModelIdentity: "qwen3.5:4b", PromptVersion: processing.PromptVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	empty := httptest.NewRecorder()
	publicServer.Handler().ServeHTTP(empty, englishRequest(http.MethodGet, "/en", nil))
	if strings.Contains(empty.Body.String(), records[0].TitleDE) {
		t.Fatal("public timeline exposed an unprocessed original")
	}
	notFound := httptest.NewRecorder()
	publicServer.Handler().ServeHTTP(notFound, englishRequest(http.MethodGet, "/en/incidents/"+formatID(records[0].ID), nil))
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("unprocessed public detail status = %d, want 404", notFound.Code)
	}

	job, found, err := database.QueueAndClaimProcessingJob(ctx, processing.Operation("qwen3.5:4b"), time.Now())
	if err != nil || !found {
		t.Fatalf("claim current job = %t/%v", found, err)
	}
	presentation := store.AIPresentation{TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.", TitleEN: "Safe title", SummaryEN: "Safe summary.", PrivacyStatus: "safe"}
	if err := database.CompleteProcessingJob(ctx, job, presentation, "qwen3.5:4b", processing.PromptVersion, time.Now()); err != nil {
		t.Fatal(err)
	}
	processedRecord, err := database.GetIncident(ctx, job.IncidentID)
	if err != nil {
		t.Fatal(err)
	}
	ready := httptest.NewRecorder()
	publicServer.Handler().ServeHTTP(ready, englishRequest(http.MethodGet, "/en/incidents/"+formatID(job.IncidentID), nil))
	if ready.Code != http.StatusOK || !strings.Contains(ready.Body.String(), presentation.SummaryEN) {
		t.Fatalf("ready public detail = %d", ready.Code)
	}
	for _, original := range []string{processedRecord.BodyDE, "Original German text"} {
		if strings.Contains(ready.Body.String(), original) {
			t.Fatalf("public detail exposed original content %q", original)
		}
	}
}

func TestPublicModeRejectsStalePromptDerivations(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	job, found, err := database.QueueAndClaimProcessingJob(ctx, processing.Operation("qwen3.5:4b"), time.Now())
	if err != nil || !found {
		t.Fatalf("claim job = %t/%v", found, err)
	}
	value := store.AIPresentation{TitleDE: "Alt", SummaryDE: "Alt.", TitleEN: "Old", SummaryEN: "Old.", PrivacyStatus: "safe"}
	if err := database.CompleteProcessingJob(ctx, job, value, "qwen3.5:4b", "incident-presentation-v1", time.Now()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	server, err := NewWithOptions(database, logger, Options{PageSize: 20, SourceMode: "fixture", PresentationMode: "public", ModelIdentity: "qwen3.5:4b", PromptVersion: processing.PromptVersion})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, englishRequest(http.MethodGet, "/en/incidents/"+formatID(job.IncidentID), nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("stale prompt detail status = %d, want 404", response.Code)
	}
}

func fixtureStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "munichbrief.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { database.Close() })
	documents, err := source.NewFixtureProvider().Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := database.UpsertDocuments(ctx, documents, time.Now()); err != nil {
		t.Fatalf("UpsertDocuments() error = %v", err)
	}
	return database
}

type failingAdminStore struct {
	*store.Store
	statsError error
	retryError error
}

func (s failingAdminStore) ProcessingQueueStats(ctx context.Context, operation string, now time.Time) (store.ProcessingStats, error) {
	if s.statsError != nil {
		return store.ProcessingStats{}, s.statsError
	}
	return s.Store.ProcessingQueueStats(ctx, operation, now)
}

func (s failingAdminStore) RetryProcessingJobs(ctx context.Context, operation string, incidentID *int64, now time.Time) (int64, error) {
	if s.retryError != nil {
		return 0, s.retryError
	}
	return s.Store.RetryProcessingJobs(ctx, operation, incidentID, now)
}

func testServer(t *testing.T, database *store.Store) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	server, err := New(database, logger, 20, "fixture")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return server
}

func adminTestServer(t *testing.T, database *store.Store, publicHosts []string) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	server, err := NewWithOptions(database, logger, Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review",
		ModelIdentity: "qwen3.5:4b", PromptVersion: processing.PromptVersion,
		SecureCookies: true, AdminEnabled: true, PublicHosts: publicHosts,
	})
	if err != nil {
		t.Fatalf("NewWithOptions() error = %v", err)
	}
	return server
}

func formatID(id int64) string {
	return strconv.FormatInt(id, 10)
}

func englishRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("Accept-Language", "en")
	return request
}

func formRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://example.com")
	return request
}
