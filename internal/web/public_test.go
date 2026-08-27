package web

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing/fstest"
	"time"

	"testing"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/processing"
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
		"Not yet summarized or translated",
		"20 shown / 28 total",
		"Page 1 of 2",
		"/en?page=2",
	} {
		if !strings.Contains(timeline.Body.String(), expected) {
			t.Errorf("timeline body does not contain %q", expected)
		}
	}
	if strings.Contains(timeline.Body.String(), "Fixture data · Review mode") {
		t.Error("timeline retained the presentation mode badge")
	}

	olderTimeline := httptest.NewRecorder()
	handler.ServeHTTP(olderTimeline, englishRequest(http.MethodGet, "/en?page=2", nil))
	if olderTimeline.Code != http.StatusOK {
		t.Fatalf("older timeline status = %d, want 200", olderTimeline.Code)
	}
	for _, expected := range []string{"Page 2 of 2", "Beschädigte Eingangstür", "← Newer", `?page=2"`} {
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
	for _, expected := range []string{`href="/en"`, `>←</span> Back</a>`, `target="_blank"`} {
		if !strings.Contains(detail.Body.String(), expected) {
			t.Errorf("direct detail body does not contain %q", expected)
		}
	}

	pageTwoDetail := httptest.NewRecorder()
	handler.ServeHTTP(pageTwoDetail, englishRequest(http.MethodGet, "/en/incidents/"+formatID(records[0].ID)+"?page=2", nil))
	if pageTwoDetail.Code != http.StatusOK || !strings.Contains(pageTwoDetail.Body.String(), `href="/en?page=2"`) {
		t.Fatalf("page-two detail did not preserve its timeline destination: %d/%q", pageTwoDetail.Code, pageTwoDetail.Body.String())
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
	job, found, err := database.QueueAndClaimProcessingJob(ctx, legacyOperation("qwen3.5:4b"), time.Now())
	if err != nil || !found {
		t.Fatalf("QueueAndClaimProcessingJob() = found:%t err:%v", found, err)
	}
	if err := database.CompleteProcessingJob(ctx, job, store.AIPresentation{
		TitleDE:       "<script>alert('title')</script>",
		SummaryDE:     "<img src=x onerror=alert('body')>",
		TitleEN:       "<script>alert('english-title')</script>",
		SummaryEN:     "<img src=x onerror=alert('english-body')>",
		PrivacyStatus: "safe",
	}, "qwen3.5:4b", processing.LegacyBilingualPromptVersion, time.Now()); err != nil {
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
	admin := httptest.NewRecorder()
	adminTestServer(t, database, nil).Handler().ServeHTTP(admin, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if strings.Contains(admin.Body.String(), "<script>") || strings.Contains(admin.Body.String(), "<img src=x") {
		t.Fatalf("admin rendered unescaped hostile incident markup: %s", admin.Body.String())
	}
	if !strings.Contains(admin.Body.String(), "&lt;script&gt;") || !strings.Contains(admin.Body.String(), "&lt;img") {
		t.Error("admin did not render escaped hostile content")
	}
}

func TestTimelineAndDetailRenderAIContentWithProvenance(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	generatedAt := time.Date(2026, time.August, 23, 9, 0, 0, 0, time.UTC)
	operation := legacyOperation("qwen3.5:4b")
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
	if err := database.CompleteProcessingJob(ctx, job, presentation, "qwen3.5:4b", processing.LegacyBilingualPromptVersion, generatedAt); err != nil {
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
		"AI processing", "Legacy pipeline", "Bilingual presentation",
		"Model", "qwen3.5:4b", "Prompt version", processing.LegacyBilingualPromptVersion,
	} {
		if !strings.Contains(detail.Body.String(), expected) {
			t.Errorf("detail body does not contain %q", expected)
		}
	}
	if strings.Contains(detail.Body.String(), "English translation") {
		t.Error("legacy presentation was rendered as a separate translation step")
	}
}

func TestTimelineAndDetailRenderStagedMetadataAndProvenance(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	records, _, err := database.ListIncidents(ctx, 1, 0)
	if err != nil || len(records) != 1 {
		t.Fatalf("ListIncidents() = %d records, err=%v", len(records), err)
	}
	incidentID := records[0].ID
	models := map[string]string{
		processing.IncidentMetadataStep:   "qwen3.5:4b",
		processing.GermanPresentationStep: "qwen3.5:4b",
		processing.TranslationModelStep:   "translate:4b",
	}
	plans, err := processing.StepPlans(models)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC)
	request, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, "translate:4b", &incidentID, false, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", nil, false, startedAt)
	if err != nil || !found || cycle.ID != request.CycleID {
		t.Fatalf("ActivateNextPipelineCycle() = %#v, found=%t, err=%v", cycle, found, err)
	}
	metadata, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, startedAt)
	if err != nil || !found {
		t.Fatalf("ClaimPipelineJob(metadata) = %#v, found=%t, err=%v", metadata, found, err)
	}
	if err := database.CompletePipelineJob(ctx, metadata, []store.PipelineValue{
		{Kind: "category", Value: "traffic"},
		{Kind: "area_name", Value: "Harras"},
		{Kind: "area_type", Value: "neighbourhood"},
		{Kind: "event_start_date", Value: "2026-08-24"},
		{Kind: "event_start_time", Value: "22:30"},
		{Kind: "report_kind", Value: "incident"},
		{Kind: "public_assistance_status", Value: "requested"},
		{Kind: "public_assistance_types", Value: `["photo_video_material","witness_observations"]`},
	}, metadata.ModelIdentity, store.HashPipelineInput("metadata"), startedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	advance, err := database.AdvancePipelineCycle(ctx, cycle, len(plans), startedAt.Add(2*time.Minute))
	if err != nil || !advance.Advanced {
		t.Fatalf("AdvancePipelineCycle(metadata) = %#v, err=%v", advance, err)
	}
	cycle.ActiveStep = 1
	german, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 1, startedAt.Add(3*time.Minute))
	if err != nil || !found {
		t.Fatalf("ClaimPipelineJob(german) = %#v, found=%t, err=%v", german, found, err)
	}
	if err := database.CompletePipelineJob(ctx, german, []store.PipelineValue{
		{Kind: "title_de", Value: "Unfall am Harras"},
		{Kind: "summary_de", Value: "Am Harras kam es zu einem Verkehrsunfall."},
		{Kind: "privacy_status", Value: "safe"},
		{Kind: "privacy_flags", Value: "[]"},
	}, german.ModelIdentity, store.HashPipelineInput("de"), startedAt.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetPresentationIncident(ctx, incidentID, store.PresentationScope{PromptVersion: processing.PipelineVersion, Language: "de", PublicOnly: true}); err != nil {
		t.Fatalf("German was not public immediately after canonical success: %v", err)
	}
	if _, err := database.GetPresentationIncident(ctx, incidentID, store.PresentationScope{PromptVersion: processing.PipelineVersion, Language: "en", PublicOnly: true}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("English was public before translation completion: %v", err)
	}
	canonicalOnlyServer, err := NewWithOptions(database, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "public", PromptVersion: processing.PipelineVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	germanOnly := httptest.NewRecorder()
	canonicalOnlyServer.Handler().ServeHTTP(germanOnly, httptest.NewRequest(http.MethodGet, "/de/incidents/"+formatID(incidentID), nil))
	if germanOnly.Code != http.StatusOK || strings.Contains(germanOnly.Body.String(), `hreflang="en"`) || strings.Contains(germanOnly.Header().Get("Link"), `hreflang="en"`) {
		t.Fatalf("German-only discovery exposed an unfinished English alternate: %d/%q", germanOnly.Code, germanOnly.Header().Get("Link"))
	}
	missingEnglish := httptest.NewRecorder()
	canonicalOnlyServer.Handler().ServeHTTP(missingEnglish, englishRequest(http.MethodGet, "/en/incidents/"+formatID(incidentID), nil))
	if missingEnglish.Code != http.StatusNotFound {
		t.Fatalf("unfinished English detail = %d, want 404", missingEnglish.Code)
	}
	advance, err = database.AdvancePipelineCycle(ctx, cycle, len(plans), startedAt.Add(5*time.Minute))
	if err != nil || !advance.Completed {
		t.Fatalf("AdvancePipelineCycle(german) = %#v, err=%v", advance, err)
	}
	if queued, err := database.QueueTranslationsForRun(ctx, german.PresentationRunID, []store.TranslationPlan{{Language: processing.EnglishLanguage, PromptVersion: processing.EnglishTranslationPromptVersion, Model: "translate:4b"}}, "manual", startedAt.Add(6*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("QueueTranslationsForRun() = %d, err=%v", queued, err)
	}
	translation, found, err := database.ClaimTranslationJob(ctx, false, nil, startedAt.Add(6*time.Minute))
	if err != nil || !found {
		t.Fatalf("ClaimTranslationJob() = %#v, found=%t, err=%v", translation, found, err)
	}
	if err := database.CompleteTranslationJob(ctx, translation, "Crash at Harras", "A traffic crash occurred at Harras.", translation.ModelIdentity, translation.InputHash, startedAt.Add(7*time.Minute)); err != nil {
		t.Fatal(err)
	}

	handler := testServer(t, database).Handler()
	timeline := httptest.NewRecorder()
	handler.ServeHTTP(timeline, englishRequest(http.MethodGet, "/en", nil))
	for _, expected := range []string{"Category", "Traffic", "Area", "Harras", "Crash at Harras", "Incident time", "24 August 2026, 22:30", "Public assistance needed"} {
		if !strings.Contains(timeline.Body.String(), expected) {
			t.Errorf("timeline body does not contain %q", expected)
		}
	}
	for _, unexpected := range []string{"Police request public assistance", "Photo or video material", "Witness observations"} {
		if strings.Contains(timeline.Body.String(), unexpected) {
			t.Errorf("timeline body unexpectedly contains public assistance detail %q", unexpected)
		}
	}

	english := httptest.NewRecorder()
	handler.ServeHTTP(english, englishRequest(http.MethodGet, "/en/incidents/"+formatID(incidentID), nil))
	for _, expected := range []string{
		"Category", "Traffic", "Area", "Harras", "Metadata-first pipeline v2",
		"Incident metadata", "qwen3.5:4b", processing.IncidentMetadataPromptVersion,
		"German presentation", processing.GermanPresentationPromptVersion,
		"English translation", "translate:4b", processing.EnglishTranslationPromptVersion,
		"Incident time", "24 August 2026, 22:30", "Report kind", "Incident", "Police request public assistance",
		"AI-generated summary", "Verify important details against the latest official information.",
		`aria-label="Public assistance"`,
	} {
		if !strings.Contains(english.Body.String(), expected) {
			t.Errorf("English detail body does not contain %q", expected)
		}
	}
	if count := strings.Count(english.Body.String(), ">Crash at Harras</"); count != 1 {
		t.Errorf("English detail renders the incident heading %d times, want 1", count)
	}
	if strings.Contains(english.Body.String(), "Machine-generated") {
		t.Error("English detail unexpectedly renders the redundant machine-generated label")
	}

	germanDetail := httptest.NewRecorder()
	handler.ServeHTTP(germanDetail, httptest.NewRequest(http.MethodGet, "/de/incidents/"+formatID(incidentID), nil))
	for _, expected := range []string{"Kategorie", "Verkehr", "Gebiet", "Harras", "Vorfallsmetadaten", "Deutsche Darstellung", "qwen3.5:4b", "Vorfallszeit", "24. August 2026, 22:30", "Polizei bittet um Mithilfe", "KI-generierte Zusammenfassung", "Wichtige Angaben bitte anhand der aktuellen offiziellen Informationen prüfen."} {
		if !strings.Contains(germanDetail.Body.String(), expected) {
			t.Errorf("German detail body does not contain %q", expected)
		}
	}
	for _, unexpected := range []string{"Englische Übersetzung", "translate:4b", processing.EnglishTranslationPromptVersion} {
		if strings.Contains(germanDetail.Body.String(), unexpected) {
			t.Errorf("German detail body unexpectedly contains %q", unexpected)
		}
	}

	markdown := httptest.NewRecorder()
	publicServer, err := NewWithOptions(database, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "public", PromptVersion: processing.PipelineVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	markdownRequest := englishRequest(http.MethodGet, "/en/incidents/"+formatID(incidentID), nil)
	markdownRequest.Header.Set("Accept", "text/markdown")
	publicServer.Handler().ServeHTTP(markdown, markdownRequest)
	for _, expected := range []string{"Incident time: 24 August 2026, 22:30", "Report kind: Incident", "Public assistance: Police request public assistance", "official source"} {
		if !strings.Contains(markdown.Body.String(), expected) {
			t.Errorf("Markdown detail does not contain %q: %s", expected, markdown.Body.String())
		}
	}

	admin := httptest.NewRecorder()
	adminTestServer(t, database, nil).Handler().ServeHTTP(admin, httptest.NewRequest(http.MethodGet, "/admin", nil))
	for _, expected := range []string{"Pipeline v2 presentation", "Incident time", "requested", "photo_video_material", processing.IncidentMetadataPromptVersion, processing.GermanPresentationPromptVersion} {
		if !strings.Contains(admin.Body.String(), expected) {
			t.Errorf("admin metadata review does not contain %q", expected)
		}
	}
}

func TestIncidentTimeFormattingSupportsClockDateAndDayPart(t *testing.T) {
	server := testServer(t, fixtureStore(t))
	cases := []struct {
		name, language, expected string
		record                   store.IncidentRecord
	}{
		{name: "clock time", language: "en", record: store.IncidentRecord{AIEventStartDate: "2026-08-24", AIEventStartTime: "21:05"}, expected: "24 August 2026, 21:05"},
		{name: "date only", language: "de", record: store.IncidentRecord{AIEventStartDate: "2026-08-24"}, expected: "24. August 2026"},
		{name: "day part", language: "en", record: store.IncidentRecord{AIEventStartDate: "2026-08-24", AIEventDayPart: "evening"}, expected: "24 August 2026, evening"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if actual := server.formatIncidentTime(test.record, test.language); actual != test.expected {
				t.Fatalf("formatIncidentTime() = %q, want %q", actual, test.expected)
			}
		})
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
	server, err := NewWithOptions(database, logger, Options{
		PageSize: 20, SourceMode: "live", PresentationMode: "review",
		PromptVersion: processing.PipelineVersion,
	})
	if err != nil {
		t.Fatalf("NewWithOptions() error = %v", err)
	}
	timeline := httptest.NewRecorder()
	server.Handler().ServeHTTP(timeline, englishRequest(http.MethodGet, "/en", nil))
	for _, expected := range []string{"Live incident", "Live metadata fallback", "Open official source"} {
		if !strings.Contains(timeline.Body.String(), expected) {
			t.Errorf("live timeline does not contain %q", expected)
		}
	}
	if strings.Contains(timeline.Body.String(), "Live source · Review mode") {
		t.Error("live timeline retained the presentation mode badge")
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

func TestPublicHostUsesFailClosedPresentationAndRejectsAdmin(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	job, found, err := database.QueueAndClaimProcessingJob(ctx, legacyOperation("qwen3.5:4b"), time.Now())
	if err != nil || !found {
		t.Fatalf("claim public fixture job = %t/%v", found, err)
	}
	presentation := store.AIPresentation{
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
		TitleEN: "Safe title", SummaryEN: "Safe summary.", PrivacyStatus: "safe",
	}
	if err := database.CompleteProcessingJob(ctx, job, presentation, "qwen3.5:4b", processing.LegacyBilingualPromptVersion, time.Now()); err != nil {
		t.Fatal(err)
	}
	publicHosts := []string{"munichbrief.egekocabas.com", "munichbrief.de"}
	server := adminTestServer(t, database, publicHosts)
	handler := server.Handler()
	path := "/en/incidents/" + formatID(job.IncidentID)

	lan := httptest.NewRecorder()
	lanRequest := englishRequest(http.MethodGet, path, nil)
	lanRequest.Host = "munichbrief.internal.example"
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

			for _, path := range []string{"/admin", "/admin/history", "/api/admin/ai/process-all-now", "/private"} {
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
	if german.Header().Get("Content-Language") != "de" || !strings.Contains(german.Body.String(), "Aktuelle Vorfälle laut Münchner Polizei") {
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
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review",
		PromptVersion: processing.PipelineVersion, SecureCookies: true,
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
	translations, err := newLocalization(readerLanguages)
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
	if actual := translations.ShownTotal("en", 20, 28); actual != "20 shown / 28 total" {
		t.Errorf("ShownTotal() = %q", actual)
	}
	if actual := translations.ShownTotal("de", 20, 28); actual != "20 angezeigt / 28 insgesamt" {
		t.Errorf("ShownTotal() German = %q", actual)
	}

	broken := fstest.MapFS{
		"de.toml": {Data: []byte("[OnlyGerman]\nother = 'Deutsch'\n")},
		"en.toml": {Data: []byte("[OnlyEnglish]\nother = 'English'\n")},
	}
	if err := validateCatalogParity(broken, "de.toml", "en.toml"); err == nil {
		t.Fatal("mismatched translation catalogs were accepted")
	}
}

func TestReaderLanguageRegistryMatchesTranslationDefinitions(t *testing.T) {
	if err := validateReaderLanguages(); err != nil {
		t.Fatal(err)
	}
	if len(translatedReaderLanguages()) != len(processing.RegisteredTranslations()) {
		t.Fatalf("translated reader registrations = %d, translation definitions = %d", len(translatedReaderLanguages()), len(processing.RegisteredTranslations()))
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
		PromptVersion: processing.PipelineVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	empty := httptest.NewRecorder()
	publicServer.Handler().ServeHTTP(empty, englishRequest(http.MethodGet, "/en", nil))
	if !strings.Contains(empty.Body.String(), "0 shown / 0 total") {
		t.Fatalf("empty public timeline count = %q", empty.Body.String())
	}
	if strings.Contains(empty.Body.String(), records[0].TitleDE) {
		t.Fatal("public timeline exposed an unprocessed original")
	}
	notFound := httptest.NewRecorder()
	publicServer.Handler().ServeHTTP(notFound, englishRequest(http.MethodGet, "/en/incidents/"+formatID(records[0].ID), nil))
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("unprocessed public detail status = %d, want 404", notFound.Code)
	}

	job, found, err := database.QueueAndClaimProcessingJob(ctx, legacyOperation("qwen3.5:4b"), time.Now())
	if err != nil || !found {
		t.Fatalf("claim current job = %t/%v", found, err)
	}
	presentation := store.AIPresentation{TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.", TitleEN: "Safe title", SummaryEN: "Safe summary.", PrivacyStatus: "safe"}
	if err := database.CompleteProcessingJob(ctx, job, presentation, "qwen3.5:4b", processing.LegacyBilingualPromptVersion, time.Now()); err != nil {
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
	readyTimeline := httptest.NewRecorder()
	publicServer.Handler().ServeHTTP(readyTimeline, englishRequest(http.MethodGet, "/en", nil))
	if !strings.Contains(readyTimeline.Body.String(), presentation.SummaryEN) || !strings.Contains(readyTimeline.Body.String(), "1 shown / 1 total") || strings.Contains(readyTimeline.Body.String(), processedRecord.BodyDE) {
		t.Fatalf("public timeline did not isolate the safe presentation: %q", readyTimeline.Body.String())
	}
	if strings.Contains(readyTimeline.Body.String(), "AI-generated summary") || strings.Contains(readyTimeline.Body.String(), "Not yet summarized or translated") {
		t.Fatal("public timeline retained a redundant processing badge")
	}
}

func TestPublicModeRetainsCompleteLegacyPromptDerivations(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	job, found, err := database.QueueAndClaimProcessingJob(ctx, legacyOperation("qwen3.5:4b"), time.Now())
	if err != nil || !found {
		t.Fatalf("claim job = %t/%v", found, err)
	}
	value := store.AIPresentation{TitleDE: "Alt", SummaryDE: "Alt.", TitleEN: "Old", SummaryEN: "Old.", PrivacyStatus: "safe"}
	if err := database.CompleteProcessingJob(ctx, job, value, "qwen3.5:4b", "incident-presentation-v1", time.Now()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	server, err := NewWithOptions(database, logger, Options{PageSize: 20, SourceMode: "fixture", PresentationMode: "public", PromptVersion: processing.PipelineVersion})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, englishRequest(http.MethodGet, "/en/incidents/"+formatID(job.IncidentID), nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), value.SummaryEN) {
		t.Fatalf("legacy prompt detail was not retained: status=%d body=%q", response.Code, response.Body.String())
	}
}
