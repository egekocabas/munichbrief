package web

import (
	"bytes"
	"context"
	"encoding/xml"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/store"
)

func publicDiscoveryServer(t *testing.T, database *store.Store, presentation store.AIPresentation) (*Server, store.ProcessingJob) {
	t.Helper()
	job, found, err := database.QueueAndClaimProcessingJob(context.Background(), legacyOperation("qwen3.5:4b"), time.Now())
	if err != nil || !found {
		t.Fatalf("claim presentation job = %t/%v", found, err)
	}
	if err := database.CompleteProcessingJob(context.Background(), job, presentation, "qwen3.5:4b", processing.LegacyBilingualPromptVersion, time.Now()); err != nil {
		t.Fatal(err)
	}
	server, err := NewWithOptions(database, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "public",
		PromptVersion: processing.PipelineVersion, SecureCookies: true,
		PublicHosts:     []string{"munichbrief.egekocabas.com", "munichbrief.de"},
		CanonicalOrigin: "https://munichbrief.de",
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, job
}

func publicDiscoveryRequest(method, target string) *http.Request {
	request := englishRequest(method, target, nil)
	request.Host = "munichbrief.egekocabas.com"
	return request
}

func TestRobotsAdvertisesCrawlAndContentUsePolicy(t *testing.T) {
	server, _ := publicDiscoveryServer(t, fixtureStore(t), store.AIPresentation{
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
		TitleEN: "Safe title", SummaryEN: "Safe summary.", PrivacyStatus: "safe",
	})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, publicDiscoveryRequest(http.MethodGet, "/robots.txt"))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("robots response = %d/%q", response.Code, response.Header().Get("Content-Type"))
	}
	for _, expected := range []string{
		"User-agent: *", "Allow: /", "Disallow: /admin", "Disallow: /api/admin",
		"Disallow: /healthz", "Disallow: /readyz", "User-agent: OAI-SearchBot",
		"User-agent: ChatGPT-User", "User-agent: Claude-SearchBot", "User-agent: Claude-User",
		"User-agent: GPTBot", "User-agent: ClaudeBot", "User-agent: Claude-Web",
		"User-agent: Google-Extended", "Content-Signal: search=yes, ai-input=yes, ai-train=no",
		"Sitemap: https://munichbrief.de/sitemap.xml",
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("robots.txt does not contain %q", expected)
		}
	}
	if !strings.Contains(response.Header().Get("Cache-Control"), "public") {
		t.Errorf("robots cache policy = %q", response.Header().Get("Cache-Control"))
	}
}

func TestSitemapContainsOnlyCanonicalPublicDocuments(t *testing.T) {
	server, job := publicDiscoveryServer(t, fixtureStore(t), store.AIPresentation{
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
		TitleEN: "Safe title", SummaryEN: "Safe summary.", PrivacyStatus: "safe",
	})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, publicDiscoveryRequest(http.MethodGet, "/sitemap.xml"))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/xml; charset=utf-8" {
		t.Fatalf("sitemap response = %d/%q/%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var document sitemapDocument
	if err := xml.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode sitemap: %v", err)
	}
	if len(document.URLs) != 6 {
		t.Fatalf("sitemap URL count = %d, want four static and two incident URLs", len(document.URLs))
	}
	wantLocations := map[string]bool{
		"https://munichbrief.de/de":                                       false,
		"https://munichbrief.de/en":                                       false,
		"https://munichbrief.de/de/about":                                 false,
		"https://munichbrief.de/en/about":                                 false,
		"https://munichbrief.de/de/incidents/" + formatID(job.IncidentID): false,
		"https://munichbrief.de/en/incidents/" + formatID(job.IncidentID): false,
	}
	for _, entry := range document.URLs {
		if _, ok := wantLocations[entry.Location]; !ok {
			t.Errorf("unexpected sitemap location %q", entry.Location)
			continue
		}
		wantLocations[entry.Location] = true
		if strings.Contains(entry.Location, "?") || strings.Contains(entry.Location, "health") || strings.Contains(entry.Location, "admin") {
			t.Errorf("non-canonical sitemap location %q", entry.Location)
		}
		if strings.Contains(entry.Location, "/incidents/") {
			if _, err := time.Parse(time.RFC3339, entry.LastMod); err != nil {
				t.Errorf("incident lastmod %q is invalid: %v", entry.LastMod, err)
			}
		}
	}
	for location, found := range wantLocations {
		if !found {
			t.Errorf("sitemap does not contain %q", location)
		}
	}
	if strings.Contains(response.Body.String(), "munichbrief.egekocabas.com") {
		t.Fatal("sitemap used the alternate hostname")
	}
}

func TestPublicDocumentsExposeCanonicalAndAlternateLinks(t *testing.T) {
	server, job := publicDiscoveryServer(t, fixtureStore(t), store.AIPresentation{
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
		TitleEN: "Safe title", SummaryEN: "Safe summary.", PrivacyStatus: "safe",
	})

	timeline := httptest.NewRecorder()
	server.Handler().ServeHTTP(timeline, publicDiscoveryRequest(http.MethodGet, "/en?page=1"))
	for _, expected := range []string{
		`<link rel="canonical" href="https://munichbrief.de/en">`,
		`<link rel="alternate" hreflang="de" href="https://munichbrief.de/de">`,
		`<link rel="alternate" hreflang="en" href="https://munichbrief.de/en">`,
	} {
		if !strings.Contains(timeline.Body.String(), expected) {
			t.Errorf("timeline does not contain %q", expected)
		}
	}
	for _, expected := range []string{
		`<https://munichbrief.de/en>; rel="canonical"`,
		`<https://munichbrief.de/de>; rel="alternate"; hreflang="de"`,
	} {
		if !strings.Contains(timeline.Header().Get("Link"), expected) {
			t.Errorf("Link header %q does not contain %q", timeline.Header().Get("Link"), expected)
		}
	}
	if timeline.Header().Get("Content-Signal") != contentSignal || !strings.Contains(timeline.Header().Get("Vary"), "Accept") {
		t.Errorf("public discovery headers = signal:%q vary:%q", timeline.Header().Get("Content-Signal"), timeline.Header().Get("Vary"))
	}

	detail := httptest.NewRecorder()
	server.Handler().ServeHTTP(detail, publicDiscoveryRequest(http.MethodGet, "/en/incidents/"+formatID(job.IncidentID)+"?page=2&tracking=x"))
	canonical := "https://munichbrief.de/en/incidents/" + formatID(job.IncidentID)
	if !strings.Contains(detail.Header().Get("Link"), "<"+canonical+">; rel=\"canonical\"") || strings.Contains(detail.Header().Get("Link"), "page=2") {
		t.Errorf("detail Link header did not remove navigation query: %q", detail.Header().Get("Link"))
	}

	root := httptest.NewRecorder()
	server.Handler().ServeHTTP(root, publicDiscoveryRequest(http.MethodGet, "/?tracking=x"))
	if root.Code != http.StatusFound || !strings.Contains(root.Header().Get("Link"), `<https://munichbrief.de/en>; rel="canonical"`) || strings.Contains(root.Header().Get("Link"), "tracking") {
		t.Errorf("root discovery redirect = %d/%q", root.Code, root.Header().Get("Link"))
	}
}

func TestTimelinePaginationPublishesPrevAndNextLinks(t *testing.T) {
	server := adminTestServer(t, fixtureStore(t), []string{"munichbrief.egekocabas.com", "munichbrief.de"})
	request := englishRequest(http.MethodGet, "/en?page=2", nil)
	request.Host = "munichbrief.internal.example"
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if !strings.Contains(response.Header().Get("Link"), `<https://munichbrief.de/en>; rel="prev"`) {
		t.Errorf("page-two Link header = %q", response.Header().Get("Link"))
	}
	if strings.Contains(response.Header().Get("Link"), `rel="next"`) {
		t.Errorf("last page unexpectedly advertised next: %q", response.Header().Get("Link"))
	}
	if !strings.Contains(response.Body.String(), `<link rel="prev" href="https://munichbrief.de/en">`) {
		t.Error("page-two HTML does not contain rel=prev")
	}
}

func TestPublicMarkdownNegotiationPreservesPrivacyBoundary(t *testing.T) {
	database := fixtureStore(t)
	records, _, err := database.ListIncidents(context.Background(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	presentation := store.AIPresentation{
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
		TitleEN: "[Safe] # title", SummaryEN: "Summary with *untrusted* _markup_ and [link].", PrivacyStatus: "safe",
	}
	server, job := publicDiscoveryServer(t, database, presentation)
	target := "/en/incidents/" + formatID(job.IncidentID)

	markdown := httptest.NewRecorder()
	markdownRequest := publicDiscoveryRequest(http.MethodGet, target)
	markdownRequest.Header.Set("Accept", "text/markdown")
	server.Handler().ServeHTTP(markdown, markdownRequest)
	if markdown.Code != http.StatusOK || markdown.Header().Get("Content-Type") != "text/markdown; charset=utf-8" {
		t.Fatalf("markdown response = %d/%q/%q", markdown.Code, markdown.Header().Get("Content-Type"), markdown.Body.String())
	}
	for _, expected := range []string{`# \[Safe\] \# title`, `Summary with \*untrusted\* \_markup\_ and \[link\].`, "canonical: \"https://munichbrief.de/en/incidents/"} {
		if !strings.Contains(markdown.Body.String(), expected) {
			t.Errorf("markdown body does not contain %q: %q", expected, markdown.Body.String())
		}
	}
	if strings.Contains(markdown.Body.String(), records[0].BodyDE) || strings.Contains(markdown.Body.String(), "Original German text") {
		t.Fatal("public Markdown exposed review-only source content")
	}
	if markdown.Header().Get("Content-Signal") != contentSignal || markdown.Header().Get("X-Markdown-Tokens") != "" {
		t.Errorf("markdown policy headers = signal:%q tokens:%q", markdown.Header().Get("Content-Signal"), markdown.Header().Get("X-Markdown-Tokens"))
	}

	for _, test := range []struct {
		accept, wantType string
	}{
		{accept: "text/markdown;q=0.9, text/html;q=1", wantType: "text/html; charset=utf-8"},
		{accept: "text/markdown, text/html", wantType: "text/html; charset=utf-8"},
		{accept: "text/*", wantType: "text/html; charset=utf-8"},
		{accept: "application/json", wantType: "text/html; charset=utf-8"},
		{accept: "text/markdown;q=1, text/html;q=0.5", wantType: "text/markdown; charset=utf-8"},
	} {
		response := httptest.NewRecorder()
		request := publicDiscoveryRequest(http.MethodGet, target)
		request.Header.Set("Accept", test.accept)
		server.Handler().ServeHTTP(response, request)
		if response.Header().Get("Content-Type") != test.wantType {
			t.Errorf("Accept %q returned %q, want %q", test.accept, response.Header().Get("Content-Type"), test.wantType)
		}
	}

	about := httptest.NewRecorder()
	aboutRequest := publicDiscoveryRequest(http.MethodGet, "/de/about")
	aboutRequest.Header.Set("Accept", "text/markdown")
	server.Handler().ServeHTTP(about, aboutRequest)
	if about.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || !strings.Contains(about.Body.String(), "# So funktioniert MunichBrief") || !strings.Contains(about.Body.String(), "1. **Entdecken**") || !strings.Contains(about.Body.String(), "[Auf GitHub melden](https://github.com/egekocabas/munichbrief/issues)") || !strings.Contains(about.Body.String(), "[ege.kocabas.dev@gmail.com](mailto:ege.kocabas.dev@gmail.com)") {
		t.Errorf("about Markdown = %q/%q", about.Header().Get("Content-Type"), about.Body.String())
	}
}

func TestCanonicalOriginMustMatchPublicHost(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	for _, options := range []Options{
		{PageSize: 20, SourceMode: "fixture", PresentationMode: "public", PromptVersion: processing.PipelineVersion, PublicHosts: []string{"munichbrief.de"}},
		{PageSize: 20, SourceMode: "fixture", PresentationMode: "public", PromptVersion: processing.PipelineVersion, PublicHosts: []string{"munichbrief.de"}, CanonicalOrigin: "http://munichbrief.de"},
		{PageSize: 20, SourceMode: "fixture", PresentationMode: "public", PromptVersion: processing.PipelineVersion, PublicHosts: []string{"munichbrief.de"}, CanonicalOrigin: "https://example.com"},
	} {
		if _, err := NewWithOptions(fixtureStore(t), logger, options); err == nil {
			t.Fatalf("NewWithOptions(%#v) error = nil", options)
		}
	}
}
