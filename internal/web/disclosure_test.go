package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/store"
)

func TestAIDisclosureVisibilityAndComparisonAssets(t *testing.T) {
	database := fixtureStore(t)
	handler := testServer(t, database).Handler()

	firstVisit := httptest.NewRecorder()
	handler.ServeHTTP(firstVisit, englishRequest(http.MethodGet, "/en", nil))
	if firstVisit.Code != http.StatusOK || !strings.Contains(firstVisit.Body.String(), `id="ai-disclosure"`) {
		t.Fatalf("first visit disclosure = %d/%q", firstVisit.Code, firstVisit.Body.String())
	}
	for _, expected := range []string{"EU Fully AI-Generated label comparison", "AI-generated news summaries"} {
		if !strings.Contains(firstVisit.Body.String(), expected) {
			t.Errorf("comparison does not contain %q", expected)
		}
	}
	if len(euAIAssetNames) != 4 {
		t.Fatalf("EU Fully AI-Generated comparison asset count = %d, want 4", len(euAIAssetNames))
	}
	if strings.Contains(firstVisit.Body.String(), "Basic AI") || strings.Contains(firstVisit.Body.String(), "AI MODIFIED") {
		t.Fatal("comparison includes an EU label category that does not match AI-generated news summaries")
	}
	body := firstVisit.Body.String()
	latestPosition := strings.Index(body, ">Latest</p>")
	comparisonPosition := strings.Index(body, "EU Fully AI-Generated label comparison")
	if latestPosition < 0 || comparisonPosition < latestPosition {
		t.Fatal("EU label comparison is not rendered under the Latest timeline heading")
	}
	for _, expected := range []string{
		"Cyclist slightly injured in Maxvorstadt collision", "Theft and burglary", "Schwabing",
		"Incident time", "Public assistance needed", "Visual preview only", "fixture-ai:3b",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("public-style comparison card does not contain %q", expected)
		}
	}
	for _, name := range euAIAssetNames {
		asset := staticAssets[name]
		if !strings.Contains(firstVisit.Body.String(), asset.path) {
			t.Errorf("comparison does not contain %s", name)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, asset.path, nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/svg+xml" {
			t.Errorf("asset %s response = %d/%q", name, response.Code, response.Header().Get("Content-Type"))
		}
	}

	acknowledged := httptest.NewRecorder()
	acknowledgedRequest := englishRequest(http.MethodGet, "/en", nil)
	acknowledgedRequest.AddCookie(&http.Cookie{Name: aiDisclosureCookieName, Value: aiDisclosureVersion})
	handler.ServeHTTP(acknowledged, acknowledgedRequest)
	if strings.Contains(acknowledged.Body.String(), `id="ai-disclosure"`) {
		t.Fatal("acknowledged visit retained disclosure")
	}

	stale := httptest.NewRecorder()
	staleRequest := englishRequest(http.MethodGet, "/en", nil)
	staleRequest.AddCookie(&http.Cookie{Name: aiDisclosureCookieName, Value: "v0"})
	handler.ServeHTTP(stale, staleRequest)
	if !strings.Contains(stale.Body.String(), `id="ai-disclosure"`) {
		t.Fatal("stale disclosure version suppressed current disclosure")
	}

	forced := httptest.NewRecorder()
	forcedRequest := englishRequest(http.MethodGet, "/en?show-ai-disclosure=1", nil)
	forcedRequest.AddCookie(&http.Cookie{Name: aiDisclosureCookieName, Value: aiDisclosureVersion})
	handler.ServeHTTP(forced, forcedRequest)
	if !strings.Contains(forced.Body.String(), `id="ai-disclosure"`) {
		t.Fatal("review preview query did not force disclosure")
	}

	about := httptest.NewRecorder()
	handler.ServeHTTP(about, englishRequest(http.MethodGet, "/en/about", nil))
	if strings.Contains(about.Body.String(), `id="ai-disclosure"`) || strings.Contains(about.Body.String(), "EU AI label comparison") {
		t.Fatal("About page contains reader disclosure preview UI")
	}
	if !strings.Contains(about.Body.String(), `<meta name="ai-generated" content="false">`) || !strings.Contains(about.Body.String(), `"ai_generated":false,"ai_generated_state":"false"`) || about.Header().Get("X-AI-Generated") != "false" {
		t.Fatal("About page does not declare that its content is not AI-generated")
	}
	for _, expected := range []string{"AI transparency and EU guidance", "eu-icons-labelling-ai-generated-content", "code-practice-ai-generated-content"} {
		if !strings.Contains(about.Body.String(), expected) {
			t.Errorf("About page EU transparency section does not contain %q", expected)
		}
	}
}

func TestAIDisclosureAcknowledgementCookieAndRedirect(t *testing.T) {
	database := fixtureStore(t)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	server, err := NewWithOptions(database, logger, Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review",
		PromptVersion: processing.PipelineVersion, SecureCookies: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"return_to": {"/en?page=2"}}
	request := httptest.NewRequest(http.MethodPost, "/ai-disclosure/acknowledge", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	before := time.Now()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("HTMX acknowledgement = %d/%q", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("acknowledgement cookies = %#v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != aiDisclosureCookieName || cookie.Value != aiDisclosureVersion || cookie.Path != "/" || cookie.MaxAge != aiDisclosureMaxAge || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("acknowledgement cookie = %#v", cookie)
	}
	if cookie.Expires.Before(before.Add(30*24*time.Hour-time.Minute)) || cookie.Expires.After(before.Add(30*24*time.Hour+time.Minute)) {
		t.Fatalf("acknowledgement expiry = %s", cookie.Expires)
	}

	for _, test := range []struct {
		name       string
		returnTo   string
		wantTarget string
	}{
		{name: "safe reader URL", returnTo: "/en/incidents/1?page=2", wantTarget: "/en/incidents/1?page=2"},
		{name: "external URL", returnTo: "https://example.com/", wantTarget: "/en"},
		{name: "admin URL", returnTo: "/admin", wantTarget: "/en"},
		{name: "path traversal", returnTo: "/en/incidents/../../admin", wantTarget: "/en"},
	} {
		t.Run(test.name, func(t *testing.T) {
			form := url.Values{"return_to": {test.returnTo}}
			request := httptest.NewRequest(http.MethodPost, "/ai-disclosure/acknowledge", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.AddCookie(&http.Cookie{Name: "munichbrief_language", Value: "en"})
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != test.wantTarget {
				t.Fatalf("redirect = %d/%q", response.Code, response.Header().Get("Location"))
			}
		})
	}
}

func TestAIGeneratedLabelsHTMLMarkdownAndSocialCard(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	incidentID := completeDisclosureTestPresentation(t, ctx, database)
	records, _, err := database.ListIncidents(ctx, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	var unprocessedID int64
	for _, record := range records {
		if record.ID != incidentID {
			unprocessedID = record.ID
			break
		}
	}

	review := testServer(t, database).Handler()
	generated := httptest.NewRecorder()
	generatedRequest := englishRequest(http.MethodGet, "/en/incidents/"+formatID(incidentID), nil)
	generatedRequest.AddCookie(&http.Cookie{Name: aiDisclosureCookieName, Value: aiDisclosureVersion})
	review.ServeHTTP(generated, generatedRequest)
	if !strings.Contains(generated.Body.String(), staticAssets["eu-ai-generated-black.svg"].path) || !strings.Contains(generated.Body.String(), "AI-generated content") {
		t.Fatal("generated detail does not contain permanent AI label")
	}

	unprocessed := httptest.NewRecorder()
	unprocessedRequest := englishRequest(http.MethodGet, "/en/incidents/"+formatID(unprocessedID), nil)
	unprocessedRequest.AddCookie(&http.Cookie{Name: aiDisclosureCookieName, Value: aiDisclosureVersion})
	review.ServeHTTP(unprocessed, unprocessedRequest)
	if strings.Contains(unprocessed.Body.String(), staticAssets["eu-ai-generated-black.svg"].path) {
		t.Fatal("unprocessed original was labelled as AI-generated")
	}
	if !strings.Contains(unprocessed.Body.String(), `<meta name="ai-generated" content="false">`) || unprocessed.Header().Get("X-AI-Generated") != "false" || unprocessed.Header().Get("X-AI-Model") != "" {
		t.Fatal("unprocessed original has incorrect machine-readable AI metadata")
	}

	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	public, err := NewWithOptions(database, logger, Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "public", PromptVersion: processing.PipelineVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	markdown := httptest.NewRecorder()
	markdownRequest := englishRequest(http.MethodGet, "/en/incidents/"+formatID(incidentID), nil)
	markdownRequest.Header.Set("Accept", "text/markdown")
	public.Handler().ServeHTTP(markdown, markdownRequest)
	if !strings.Contains(markdown.Body.String(), "> **AI-generated content**") {
		t.Fatalf("Markdown disclosure missing: %s", markdown.Body.String())
	}
	for _, expected := range []string{
		"ai_generated: true", `ai_generated_state: "true"`, `ai_model: "qwen3.5:4b"`,
		`digital_source_type: "` + iptcTrainedAlgorithmicMedia + `"`,
	} {
		if !strings.Contains(markdown.Body.String(), expected) {
			t.Errorf("Markdown machine-readable disclosure does not contain %q", expected)
		}
	}
	publicTimeline := httptest.NewRecorder()
	publicTimelineRequest := englishRequest(http.MethodGet, "/en", nil)
	publicTimelineRequest.AddCookie(&http.Cookie{Name: aiDisclosureCookieName, Value: aiDisclosureVersion})
	public.Handler().ServeHTTP(publicTimeline, publicTimelineRequest)
	if !strings.Contains(publicTimeline.Body.String(), "Synthetic AI title") || !strings.Contains(publicTimeline.Body.String(), staticAssets["eu-ai-generated-black.svg"].path) {
		t.Fatal("generated homepage headline does not contain permanent AI label")
	}
	socialResponse := httptest.NewRecorder()
	public.Handler().ServeHTTP(socialResponse, englishRequest(http.MethodGet, "/social/en/incidents/"+formatID(incidentID), nil))
	if socialResponse.Code != http.StatusOK || socialResponse.Header().Get("X-AI-Generated") != "true" || socialResponse.Header().Get("X-AI-Model") != "qwen3.5:4b" || socialResponse.Header().Get("X-IPTC-Digital-Source-Type") != iptcTrainedAlgorithmicMedia || !bytes.Contains(socialResponse.Body.Bytes(), []byte(iptcTrainedAlgorithmicMedia)) {
		t.Fatal("generated social card does not expose HTTP and embedded XMP AI metadata")
	}

	renderer, err := newSocialCardRenderer()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := renderer.render(socialCardSpec{Title: "Synthetic report"})
	if err != nil {
		t.Fatal(err)
	}
	labelled, err := renderer.render(socialCardSpec{Title: "Synthetic report", AIGenerated: true})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(plain, labelled) {
		t.Fatal("AI-labelled social card is identical to unlabelled card")
	}
	assertSocialCardDimensions(t, labelled)
	if !bytes.Contains(labelled, []byte(iptcTrainedAlgorithmicMedia)) || bytes.Contains(plain, []byte(iptcTrainedAlgorithmicMedia)) {
		t.Fatal("social card IPTC/XMP AI provenance is missing or applied to a non-AI card")
	}
}

func TestPublicModeDoesNotForceDisclosurePreview(t *testing.T) {
	database := fixtureStore(t)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	server, err := NewWithOptions(database, logger, Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "public", PromptVersion: processing.PipelineVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := englishRequest(http.MethodGet, "/en?show-ai-disclosure=1", nil)
	request.AddCookie(&http.Cookie{Name: aiDisclosureCookieName, Value: aiDisclosureVersion})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if strings.Contains(response.Body.String(), `id="ai-disclosure"`) || strings.Contains(response.Body.String(), "EU AI label comparison") {
		t.Fatal("public request exposed review-only preview UI")
	}
}

func completeDisclosureTestPresentation(t *testing.T, ctx context.Context, database *store.Store) int64 {
	t.Helper()
	job, found, err := database.QueueAndClaimProcessingJob(ctx, legacyOperation("qwen3.5:4b"), time.Now())
	if err != nil || !found {
		t.Fatalf("claim presentation job = %t/%v", found, err)
	}
	presentation := store.AIPresentation{
		TitleDE: "Synthetischer KI-Titel", SummaryDE: "Synthetische KI-Zusammenfassung.",
		TitleEN: "Synthetic AI title", SummaryEN: "Synthetic AI summary.", PrivacyStatus: "safe",
	}
	if err := database.CompleteProcessingJob(ctx, job, presentation, "qwen3.5:4b", processing.LegacyBilingualPromptVersion, time.Now()); err != nil {
		t.Fatal(err)
	}
	return job.IncidentID
}
