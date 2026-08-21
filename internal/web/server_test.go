package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/source"
	"github.com/egekocabas/munichbrief/internal/store"
)

func TestTimelineAndDetailRenderFixtureData(t *testing.T) {
	database := fixtureStore(t)
	server := testServer(t, database)
	handler := server.Handler()

	timeline := httptest.NewRecorder()
	handler.ServeHTTP(timeline, httptest.NewRequest(http.MethodGet, "/", nil))
	if timeline.Code != http.StatusOK {
		t.Fatalf("timeline status = %d, want 200", timeline.Code)
	}
	for _, expected := range []string{
		"Fixture incidents",
		"Fahrradunfall; eine Person leicht verletzt",
		"Größerer Polizeieinsatz",
		"Fixture mode",
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
	handler.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/incidents/"+formatID(records[0].ID), nil))
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

	recorder := httptest.NewRecorder()
	testServer(t, database).Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	body := recorder.Body.String()
	if strings.Contains(body, "<script>") || strings.Contains(body, "<img src=x") {
		t.Fatalf("timeline rendered unescaped hostile markup: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, "&lt;img") {
		t.Error("timeline did not render escaped hostile content")
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
	server.Handler().ServeHTTP(timeline, httptest.NewRequest(http.MethodGet, "/", nil))
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
	server.Handler().ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/incidents/"+formatID(incidentID), nil))
	for _, expected := range []string{"Last processed", "original Bavarian Police report remains authoritative", documents[0].SourceURL} {
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
	request := httptest.NewRequest(http.MethodGet, "/about", nil)
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
