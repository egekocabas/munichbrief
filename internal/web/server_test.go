package web

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
	handler.ServeHTTP(timeline, englishRequest(http.MethodGet, "/", nil))
	if timeline.Code != http.StatusOK {
		t.Fatalf("timeline status = %d, want 200", timeline.Code)
	}
	for _, expected := range []string{
		"Fixture incidents",
		"Fahrradunfall; eine Person leicht verletzt",
		"Größerer Polizeieinsatz",
		"Fixture mode · Review mode",
		"Not yet summarized or translated",
	} {
		if !strings.Contains(timeline.Body.String(), expected) {
			t.Errorf("timeline body does not contain %q", expected)
		}
	}

	records, _, err := database.ListIncidents(context.Background(), 20, 0)
	if err != nil {
		t.Fatalf("ListIncidents() error = %v", err)
	}
	detail := httptest.NewRecorder()
	handler.ServeHTTP(detail, englishRequest(http.MethodGet, "/incidents/"+formatID(records[0].ID), nil))
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
	testServer(t, database).Handler().ServeHTTP(recorder, englishRequest(http.MethodGet, "/", nil))
	body := recorder.Body.String()
	if strings.Contains(body, "<script>") || strings.Contains(body, "<img src=x") {
		t.Fatalf("timeline rendered unescaped hostile markup: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, "&lt;img") {
		t.Error("timeline did not render escaped hostile content")
	}
	detail := httptest.NewRecorder()
	testServer(t, database).Handler().ServeHTTP(detail, englishRequest(http.MethodGet, "/incidents/"+formatID(job.IncidentID), nil))
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
	handler.ServeHTTP(timeline, englishRequest(http.MethodGet, "/", nil))
	for _, expected := range []string{"AI-generated summary", presentation.TitleEN, presentation.SummaryEN} {
		if !strings.Contains(timeline.Body.String(), expected) {
			t.Errorf("timeline body does not contain %q", expected)
		}
	}

	detail := httptest.NewRecorder()
	handler.ServeHTTP(detail, englishRequest(http.MethodGet, "/incidents/"+formatID(job.IncidentID), nil))
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
	server.Handler().ServeHTTP(timeline, englishRequest(http.MethodGet, "/", nil))
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
	server.Handler().ServeHTTP(detail, englishRequest(http.MethodGet, "/incidents/"+formatID(incidentID), nil))
	for _, expected := range []string{"Last processed", "Bavarian Police release remains authoritative", documents[0].SourceURL} {
		if !strings.Contains(detail.Body.String(), expected) {
			t.Errorf("live detail does not contain %q", expected)
		}
	}
}

func TestUnknownIncidentReturnsNotFound(t *testing.T) {
	recorder := httptest.NewRecorder()
	testServer(t, fixtureStore(t)).Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/incidents/9999", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestAboutHealthReadinessAndRequestHeaders(t *testing.T) {
	database := fixtureStore(t)
	handler := testServer(t, database).Handler()

	about := httptest.NewRecorder()
	request := englishRequest(http.MethodGet, "/about", nil)
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

func TestLanguagePreferenceCookieAndBrowserFallback(t *testing.T) {
	handler := testServer(t, fixtureStore(t)).Handler()
	browserRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	browserRequest.Header.Set("Accept-Language", "en-GB;q=0.9,de;q=0.8")
	browserResponse := httptest.NewRecorder()
	handler.ServeHTTP(browserResponse, browserRequest)
	if browserResponse.Header().Get("Content-Language") != "en" || !strings.Contains(browserResponse.Body.String(), "Recent incidents") {
		t.Fatalf("browser language response is not English")
	}
	if strings.Contains(browserResponse.Body.String(), "onchange=") || !strings.Contains(browserResponse.Body.String(), `<button type="submit">Change language</button>`) {
		t.Fatal("language form must use a visible submit button without inline JavaScript")
	}

	form := strings.NewReader("language=de&return_to=%2Fabout")
	selection := httptest.NewRequest(http.MethodPost, "/language", form)
	selection.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	selected := httptest.NewRecorder()
	handler.ServeHTTP(selected, selection)
	if selected.Code != http.StatusSeeOther || selected.Header().Get("Location") != "/about" {
		t.Fatalf("language response = %d %q", selected.Code, selected.Header().Get("Location"))
	}
	cookies := selected.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "munichbrief_language" || cookies[0].Value != "de" || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].MaxAge != 365*24*60*60 || cookies[0].Expires.Before(time.Now().Add(364*24*time.Hour)) {
		t.Fatalf("language cookie = %#v", cookies)
	}
	secureServer, err := NewWithOptions(fixtureStore(t), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review", ModelIdentity: "qwen3.5:4b",
		PromptVersion: processing.PromptVersion, SecureCookies: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	secureSelection := httptest.NewRequest(http.MethodPost, "/language", strings.NewReader("language=en&return_to=%2F"))
	secureSelection.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	secureResponse := httptest.NewRecorder()
	secureServer.Handler().ServeHTTP(secureResponse, secureSelection)
	secureCookies := secureResponse.Result().Cookies()
	if len(secureCookies) != 1 || !secureCookies[0].Secure {
		t.Fatalf("secure language cookie = %#v", secureCookies)
	}
}

func TestLocalizedProcessingStateLabels(t *testing.T) {
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
		view := incidentForLanguage(test.record, "en", localizedText["en"])
		if view.ProcessingLabel != test.expected {
			t.Errorf("state %q label = %q, want %q", test.record.ProcessingState(), view.ProcessingLabel, test.expected)
		}
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
	publicServer.Handler().ServeHTTP(empty, englishRequest(http.MethodGet, "/", nil))
	if strings.Contains(empty.Body.String(), records[0].TitleDE) {
		t.Fatal("public timeline exposed an unprocessed original")
	}
	notFound := httptest.NewRecorder()
	publicServer.Handler().ServeHTTP(notFound, englishRequest(http.MethodGet, "/incidents/"+formatID(records[0].ID), nil))
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
	publicServer.Handler().ServeHTTP(ready, englishRequest(http.MethodGet, "/incidents/"+formatID(job.IncidentID), nil))
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
	server.Handler().ServeHTTP(response, englishRequest(http.MethodGet, "/incidents/"+formatID(job.IncidentID), nil))
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

func testServer(t *testing.T, database *store.Store) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	server, err := New(database, logger, 20, "fixture")
	if err != nil {
		t.Fatalf("New() error = %v", err)
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
