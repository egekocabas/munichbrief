package web

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	langregistry "github.com/egekocabas/munichbrief/internal/languages"
	"github.com/egekocabas/munichbrief/internal/store"
	"golang.org/x/text/language"
)

func publicDiscoveryServer(t *testing.T, database *store.Store, presentation testPresentation) (*Server, testPresentationJob) {
	t.Helper()
	job := seedV2Presentation(t, database, presentation, time.Now())
	server, err := NewWithOptions(database, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "public",
		SecureCookies:   true,
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
	t.Parallel()
	server, _ := publicDiscoveryServer(t, fixtureStore(t), testPresentation{
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
		TitleEN: "Safe title", SummaryEN: "Safe summary.",
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
	t.Parallel()
	server, job := publicDiscoveryServer(t, fixtureStore(t), testPresentation{
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
		TitleEN: "Safe title", SummaryEN: "Safe summary.",
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
	wantLocations := make(map[string]bool)
	for _, definition := range langregistry.Registered() {
		wantLocations["https://munichbrief.de/"+definition.Code] = false
		wantLocations["https://munichbrief.de/"+definition.Code+"/about"] = false
		wantLocations["https://munichbrief.de/"+definition.Code+"/contact"] = false
		wantLocations["https://munichbrief.de/"+definition.Code+"/privacy"] = false
		wantLocations["https://munichbrief.de/"+definition.Code+"/impressum"] = false
	}
	wantLocations["https://munichbrief.de/de/incidents/"+formatID(job.IncidentID)] = false
	wantLocations["https://munichbrief.de/en/incidents/"+formatID(job.IncidentID)] = false
	if len(document.URLs) != len(wantLocations) {
		t.Fatalf("sitemap URL count = %d, want %d available canonical URLs", len(document.URLs), len(wantLocations))
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

func TestCanonicalIncidentModifiedTimeMatchesSuccessfulPresentation(t *testing.T) {
	t.Parallel()
	database := fixtureStore(t)
	server, job := publicDiscoveryServer(t, database, testPresentation{
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
		TitleEN: "Safe title", SummaryEN: "Safe summary.",
	})
	record, err := database.GetPresentationIncident(context.Background(), job.IncidentID, store.PresentationScope{Language: "de", PublicOnly: true})
	if err != nil || record.AIGeneratedAt == nil {
		t.Fatalf("load canonical detail metadata = %#v/%v", record.AIGeneratedAt, err)
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, publicDiscoveryRequest(http.MethodGet, "/de/incidents/"+formatID(job.IncidentID)))
	want := `<meta property="article:modified_time" content="` + record.AIGeneratedAt.Format(time.RFC3339) + `">`
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), want) {
		t.Fatalf("canonical detail response = %d, does not contain %q", response.Code, want)
	}
}

func TestSyntheticReaderLanguageDrivesRoutesNegotiationAndSEO(t *testing.T) {
	t.Parallel()
	server, job := publicDiscoveryServer(t, fixtureStore(t), testPresentation{
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
		TitleEN: "Safe title", SummaryEN: "Safe summary.",
	})
	formatter := func(value time.Time) string { return value.Format("2006-01-02") }
	server.languages = append(server.languages, langregistry.Definition{
		Code: "pt", Tag: language.MustParse("pt-BR"), DisplayName: "Português", Catalog: "locales/active.pt.toml", OpenGraphLocale: "pt_BR",
		SwitchMessageID: "SwitchToPortuguese", StepMessageID: "PortugueseTranslationStep",
		FormatDate: formatter, FormatDay: formatter, FormatDateTime: formatter,
	})
	handler := server.Handler()

	page := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/pt", nil)
	request.Host = "munichbrief.de"
	handler.ServeHTTP(page, request)
	if page.Code != http.StatusOK || page.Header().Get("Content-Language") != "pt-BR" {
		t.Fatalf("synthetic language page = %d/%q", page.Code, page.Header().Get("Content-Language"))
	}
	for _, expected := range []string{
		`<html lang="pt-BR"`,
		`<link rel="alternate" hreflang="pt-BR" href="https://munichbrief.de/pt">`,
		`"inLanguage":["de-DE","en-GB","tr-TR","hr-HR","it-IT","uk-UA","bs-BA","zh-CN","hi-IN","es-ES","fr-FR","ro-RO","pl-PL","ru-RU","pt-BR"]`,
	} {
		if !strings.Contains(page.Body.String(), expected) {
			t.Errorf("synthetic language page does not contain %q", expected)
		}
	}

	redirect := httptest.NewRecorder()
	root := httptest.NewRequest(http.MethodGet, "/", nil)
	root.Host = "munichbrief.de"
	root.Header.Set("Accept-Language", "pt-PT,pt;q=0.9,en;q=0.5")
	handler.ServeHTTP(redirect, root)
	if redirect.Code != http.StatusFound || redirect.Header().Get("Location") != "/pt" {
		t.Fatalf("synthetic language negotiation = %d/%q", redirect.Code, redirect.Header().Get("Location"))
	}

	sitemap := httptest.NewRecorder()
	sitemapRequest := httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil)
	sitemapRequest.Host = "munichbrief.de"
	handler.ServeHTTP(sitemap, sitemapRequest)
	if !strings.Contains(sitemap.Body.String(), "https://munichbrief.de/pt</loc>") || !strings.Contains(sitemap.Body.String(), "https://munichbrief.de/pt/about</loc>") {
		t.Fatalf("synthetic language sitemap = %s", sitemap.Body.String())
	}

	detail := httptest.NewRecorder()
	detailRequest := httptest.NewRequest(http.MethodGet, "/de/incidents/"+formatID(job.IncidentID), nil)
	detailRequest.Host = "munichbrief.de"
	handler.ServeHTTP(detail, detailRequest)
	for _, unavailable := range []string{
		`hreflang="pt-BR"`,
		`<meta property="og:locale:alternate" content="pt_BR">`,
	} {
		if strings.Contains(detail.Body.String(), unavailable) {
			t.Errorf("detail advertised unavailable synthetic translation %q", unavailable)
		}
	}
}

func TestPublicDocumentsExposeCanonicalAndAlternateLinks(t *testing.T) {
	t.Parallel()
	database := fixtureStore(t)
	server, job := publicDiscoveryServer(t, database, testPresentation{
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
		TitleEN: "Safe title", SummaryEN: "Safe summary.",
	})

	timeline := httptest.NewRecorder()
	server.Handler().ServeHTTP(timeline, publicDiscoveryRequest(http.MethodGet, "/en?page=1"))
	for _, expected := range []string{
		`<html lang="en-GB" prefix="og: https://ogp.me/ns# article: https://ogp.me/ns/article#" data-ai-generated="true">`,
		`<meta name="robots" content="index,follow,max-image-preview:large">`,
		`<meta name="ai-generated" content="true">`,
		`<meta name="digital-source-type" content="` + iptcTrainedAlgorithmicMedia + `">`,
		`"ai_generated":true,"ai_generated_state":"true"`,
		`data-ai-generated="true" data-ai-model="qwen3.5:4b"`,
		`<link rel="canonical" href="https://munichbrief.de/en">`,
		`<link rel="alternate" hreflang="de-DE" href="https://munichbrief.de/de">`,
		`<link rel="alternate" hreflang="en-GB" href="https://munichbrief.de/en">`,
		`<link rel="alternate" hreflang="x-default" href="https://munichbrief.de/de">`,
		`<meta property="og:type" content="website">`,
		`<meta property="og:title" content="MunichBrief">`,
		`<meta property="og:url" content="https://munichbrief.de/en">`,
		`<meta property="og:locale" content="en_GB">`,
		`<meta property="og:locale:alternate" content="de_DE">`,
		`<meta property="og:image" content="https://munichbrief.de/social/en/home">`,
		`<meta property="og:image:secure_url" content="https://munichbrief.de/social/en/home">`,
		`<meta property="og:image:width" content="1200">`,
		`<meta property="og:image:height" content="630">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<meta name="twitter:image" content="https://munichbrief.de/social/en/home">`,
	} {
		if !strings.Contains(timeline.Body.String(), expected) {
			t.Errorf("timeline does not contain %q", expected)
		}
	}
	for _, expected := range []string{
		`<https://munichbrief.de/en>; rel="canonical"`,
		`<https://munichbrief.de/de>; rel="alternate"; hreflang="de-DE"`,
		`<https://munichbrief.de/de>; rel="alternate"; hreflang="x-default"`,
	} {
		if !strings.Contains(timeline.Header().Get("Link"), expected) {
			t.Errorf("Link header %q does not contain %q", timeline.Header().Get("Link"), expected)
		}
	}
	if timeline.Header().Get("Content-Signal") != contentSignal || !strings.Contains(timeline.Header().Get("Vary"), "Accept") {
		t.Errorf("public discovery headers = signal:%q vary:%q", timeline.Header().Get("Content-Signal"), timeline.Header().Get("Vary"))
	}
	if timeline.Header().Get("X-AI-Generated") != "true" || timeline.Header().Get("X-IPTC-Digital-Source-Type") != iptcTrainedAlgorithmicMedia {
		t.Errorf("timeline AI headers = generated:%q source:%q", timeline.Header().Get("X-AI-Generated"), timeline.Header().Get("X-IPTC-Digital-Source-Type"))
	}
	timelineStructured := decodeStructuredData(t, timeline.Body.String())
	if encoded, _ := json.Marshal(timelineStructured); !bytes.Contains(encoded, []byte(`"@type":"CollectionPage"`)) || !bytes.Contains(encoded, []byte(`"@type":"WebSite"`)) || !bytes.Contains(encoded, []byte(`"digitalSourceType":"`+schemaTrainedAlgorithmicMedia+`"`)) {
		t.Errorf("timeline structured data = %s", encoded)
	}

	detail := httptest.NewRecorder()
	server.Handler().ServeHTTP(detail, publicDiscoveryRequest(http.MethodGet, "/en/incidents/"+formatID(job.IncidentID)+"?page=2&tracking=x"))
	translated, err := database.GetPresentationIncident(context.Background(), job.IncidentID, store.PresentationScope{Language: "en", TranslationLanguage: "en", PublicOnly: true})
	if err != nil || translated.AITranslationGeneratedAt == nil {
		t.Fatalf("load translated detail metadata = %#v/%v", translated.AITranslationGeneratedAt, err)
	}
	canonical := "https://munichbrief.de/en/incidents/" + formatID(job.IncidentID)
	if !strings.Contains(detail.Header().Get("Link"), "<"+canonical+">; rel=\"canonical\"") || strings.Contains(detail.Header().Get("Link"), "page=2") {
		t.Errorf("detail Link header did not remove navigation query: %q", detail.Header().Get("Link"))
	}
	for _, expected := range []string{
		`<meta name="description" content="Safe summary.">`,
		`<meta name="ai-generated" content="true">`,
		`<meta name="ai-model" content="qwen3.5:4b">`,
		`"ai_generated":true,"ai_generated_state":"true","ai_model":"qwen3.5:4b"`,
		`data-ai-generated="true" data-ai-model="qwen3.5:4b"`,
		`<meta property="og:type" content="article">`,
		`<meta property="og:title" content="Safe title · MunichBrief">`,
		`<meta property="og:image" content="https://munichbrief.de/social/en/incidents/` + formatID(job.IncidentID) + `">`,
		`<meta name="twitter:title" content="Safe title · MunichBrief">`,
		`<meta property="article:published_time" content="`,
		`<meta property="article:modified_time" content="` + translated.AITranslationGeneratedAt.Format(time.RFC3339) + `">`,
	} {
		if !strings.Contains(detail.Body.String(), expected) {
			t.Errorf("detail does not contain %q", expected)
		}
	}
	detailStructured := decodeStructuredData(t, detail.Body.String())
	if encoded, _ := json.Marshal(detailStructured); !bytes.Contains(encoded, []byte(`"@type":"Article"`)) || !bytes.Contains(encoded, []byte(`"headline":"Safe title"`)) || !bytes.Contains(encoded, []byte(`"isBasedOn":`)) || !bytes.Contains(encoded, []byte(`"digitalSourceType":"`+schemaTrainedAlgorithmicMedia+`"`)) {
		t.Errorf("detail structured data = %s", encoded)
	}
	if detail.Header().Get("X-AI-Generated") != "true" || detail.Header().Get("X-AI-Model") != "qwen3.5:4b" || detail.Header().Get("X-IPTC-Digital-Source-Type") != iptcTrainedAlgorithmicMedia {
		t.Errorf("detail AI headers = generated:%q model:%q source:%q", detail.Header().Get("X-AI-Generated"), detail.Header().Get("X-AI-Model"), detail.Header().Get("X-IPTC-Digital-Source-Type"))
	}

	root := httptest.NewRecorder()
	server.Handler().ServeHTTP(root, publicDiscoveryRequest(http.MethodGet, "/?tracking=x"))
	if root.Code != http.StatusFound || !strings.Contains(root.Header().Get("Link"), `<https://munichbrief.de/en>; rel="canonical"`) || strings.Contains(root.Header().Get("Link"), "tracking") {
		t.Errorf("root discovery redirect = %d/%q", root.Code, root.Header().Get("Link"))
	}
}

func decodeStructuredData(t *testing.T, document string) map[string]any {
	t.Helper()
	const opening = `<script type="application/ld+json">`
	start := strings.Index(document, opening)
	if start < 0 {
		t.Fatal("document has no JSON-LD script")
	}
	start += len(opening)
	end := strings.Index(document[start:], `</script>`)
	if end < 0 {
		t.Fatal("JSON-LD script is not closed")
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(document[start:start+end]), &result); err != nil {
		t.Fatalf("decode JSON-LD: %v", err)
	}
	return result
}

func TestTimelinePaginationPublishesPrevAndNextLinks(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	database := fixtureStore(t)
	records, _, err := database.ListIncidents(context.Background(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	presentation := testPresentation{
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
		TitleEN: "[Safe] # title", SummaryEN: "Summary with *untrusted* _markup_ and [link].",
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
	if about.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || !strings.Contains(about.Body.String(), "# Über MunichBrief") || !strings.Contains(about.Body.String(), "1. **Entdecken**") || !strings.Contains(about.Body.String(), "[Auf GitHub melden](https://github.com/egekocabas/munichbrief/issues)") || !strings.Contains(about.Body.String(), "[contact@munichbrief.de](mailto:contact@munichbrief.de)") {
		t.Errorf("about Markdown = %q/%q", about.Header().Get("Content-Type"), about.Body.String())
	}
}

func TestCanonicalOriginMustMatchPublicHost(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	for _, options := range []Options{
		{PageSize: 20, SourceMode: "fixture", PresentationMode: "public", PublicHosts: []string{"munichbrief.de"}},
		{PageSize: 20, SourceMode: "fixture", PresentationMode: "public", PublicHosts: []string{"munichbrief.de"}, CanonicalOrigin: "http://munichbrief.de"},
		{PageSize: 20, SourceMode: "fixture", PresentationMode: "public", PublicHosts: []string{"munichbrief.de"}, CanonicalOrigin: "https://example.com"},
	} {
		if _, err := NewWithOptions(fixtureStore(t), logger, options); err == nil {
			t.Fatalf("NewWithOptions(%#v) error = nil", options)
		}
	}
}
