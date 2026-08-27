package web

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

const contentSignal = "search=yes, ai-input=yes, ai-train=no"

func normalizeCanonicalOrigin(value string, publicHosts []string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if len(publicHosts) > 0 {
			return "", errors.New("canonical origin is required when public hosts are configured")
		}
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Port() != "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("canonical origin must be an HTTPS origin without credentials, port, path, query, or fragment")
	}
	host := strings.ToLower(parsed.Hostname())
	for _, publicHost := range publicHosts {
		if host == publicHost {
			return "https://" + host, nil
		}
	}
	return "", errors.New("canonical origin host must appear in public hosts")
}

func (s *Server) canonicalOrigin(request *http.Request) string {
	if s.options.CanonicalOrigin != "" {
		return s.options.CanonicalOrigin
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	host := request.Host
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host
}

func localizedRelativeURL(value, currentLanguage, targetLanguage string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	currentPrefix := "/" + currentLanguage
	targetPrefix := "/" + targetLanguage
	if parsed.Path == currentPrefix {
		parsed.Path = targetPrefix
	} else if strings.HasPrefix(parsed.Path, currentPrefix+"/") {
		parsed.Path = targetPrefix + strings.TrimPrefix(parsed.Path, currentPrefix)
	}
	return parsed.RequestURI()
}

func addVary(header http.Header, values ...string) {
	seen := make(map[string]struct{})
	ordered := make([]string, 0, len(values)+3)
	for _, existing := range header.Values("Vary") {
		for _, value := range strings.Split(existing, ",") {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			key := strings.ToLower(value)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			ordered = append(ordered, value)
		}
	}
	for _, value := range values {
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ordered = append(ordered, value)
	}
	header.Set("Vary", strings.Join(ordered, ", "))
}

func (s *Server) setDocumentLinks(header http.Header, page basePage) {
	links := []string{
		fmt.Sprintf("<%s>; rel=\"canonical\"", page.CanonicalURL),
		fmt.Sprintf("<%s>; rel=\"alternate\"; hreflang=\"de\"", page.GermanCanonicalURL),
		fmt.Sprintf("<%s>; rel=\"alternate\"; hreflang=\"en\"", page.EnglishCanonicalURL),
	}
	if page.PreviousCanonicalURL != "" {
		links = append(links, fmt.Sprintf("<%s>; rel=\"prev\"", page.PreviousCanonicalURL))
	}
	if page.NextCanonicalURL != "" {
		links = append(links, fmt.Sprintf("<%s>; rel=\"next\"", page.NextCanonicalURL))
	}
	header.Set("Link", strings.Join(links, ", "))
}

func (s *Server) prepareRedirectDiscovery(response http.ResponseWriter, request *http.Request, target string) {
	parsed, err := url.Parse(target)
	if err != nil {
		return
	}
	language := strings.SplitN(strings.TrimPrefix(parsed.Path, "/"), "/", 2)[0]
	if language != "de" && language != "en" {
		return
	}
	page := s.base(request, language, parsed.RequestURI())
	s.setDocumentLinks(response.Header(), page)
	addVary(response.Header(), "Accept-Language", "Cookie")
	if s.scope(request).PublicOnly {
		response.Header().Set("Content-Signal", contentSignal)
	}
}

func mediaQuality(accept, target string) float64 {
	if strings.TrimSpace(accept) == "" {
		return 0
	}
	targetType, targetSubtype, _ := strings.Cut(target, "/")
	bestSpecificity := -1
	bestQuality := 0.0
	for _, raw := range strings.Split(accept, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		quality := 1.0
		if rawQuality, ok := parameters["q"]; ok {
			parsed, err := strconv.ParseFloat(rawQuality, 64)
			if err != nil || parsed < 0 || parsed > 1 {
				continue
			}
			quality = parsed
		}
		candidateType, candidateSubtype, ok := strings.Cut(strings.ToLower(mediaType), "/")
		if !ok {
			continue
		}
		specificity := -1
		switch {
		case candidateType == targetType && candidateSubtype == targetSubtype:
			specificity = 2
		case candidateType == targetType && candidateSubtype == "*":
			specificity = 1
		case candidateType == "*" && candidateSubtype == "*":
			specificity = 0
		}
		if specificity < 0 {
			continue
		}
		if specificity > bestSpecificity || (specificity == bestSpecificity && quality > bestQuality) {
			bestSpecificity = specificity
			bestQuality = quality
		}
	}
	return bestQuality
}

func wantsMarkdown(accept string) bool {
	markdownQuality := mediaQuality(accept, "text/markdown")
	htmlQuality := mediaQuality(accept, "text/html")
	return markdownQuality > 0 && markdownQuality > htmlQuality
}

func (s *Server) prepareMarkdown(response http.ResponseWriter, page basePage) {
	response.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	response.Header().Set("Content-Language", page.Lang)
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Content-Signal", contentSignal)
	addVary(response.Header(), "Cookie", "Accept-Language", "Accept")
	s.setDocumentLinks(response.Header(), page)
}

func (s *Server) robots(response http.ResponseWriter, request *http.Request) {
	origin := s.canonicalOrigin(request)
	body := `User-agent: *
Content-Signal: search=yes, ai-input=yes, ai-train=no
Allow: /
Disallow: /admin
Disallow: /api/admin
Disallow: /healthz
Disallow: /readyz

User-agent: OAI-SearchBot
User-agent: ChatGPT-User
User-agent: Claude-SearchBot
User-agent: Claude-User
Content-Signal: search=yes, ai-input=yes, ai-train=no
Allow: /
Disallow: /admin
Disallow: /api/admin
Disallow: /healthz
Disallow: /readyz

User-agent: GPTBot
User-agent: ClaudeBot
User-agent: Claude-Web
User-agent: Google-Extended
Content-Signal: search=yes, ai-input=yes, ai-train=no
Disallow: /

Sitemap: ` + origin + "/sitemap.xml\n"
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.Header().Set("Cache-Control", "public, max-age=3600")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(response, body)
}

type sitemapDocument struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Location string `xml:"loc"`
	LastMod  string `xml:"lastmod,omitempty"`
}

func (s *Server) sitemap(response http.ResponseWriter, request *http.Request) {
	links, err := s.store.ListPublicIncidentLinks(request.Context(), s.options.SourceMode, store.PresentationScope{
		PromptVersion: s.options.PromptVersion,
		PublicOnly:    true,
	})
	if err != nil {
		s.internalError(response, request, "list sitemap incidents", err)
		return
	}
	origin := s.canonicalOrigin(request)
	urls := []sitemapURL{
		{Location: origin + "/de"},
		{Location: origin + "/en"},
		{Location: origin + "/de/about"},
		{Location: origin + "/en/about"},
	}
	for _, link := range links {
		lastModified := link.ModifiedAt.UTC().Format(time.RFC3339)
		for _, language := range []string{"de", "en"} {
			urls = append(urls, sitemapURL{
				Location: fmt.Sprintf("%s/%s/incidents/%d", origin, language, link.ID),
				LastMod:  lastModified,
			})
		}
	}
	response.Header().Set("Content-Type", "application/xml; charset=utf-8")
	response.Header().Set("Cache-Control", "public, max-age=300")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(response, xml.Header)
	encoder := xml.NewEncoder(response)
	encoder.Indent("", "  ")
	if err := encoder.Encode(sitemapDocument{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: urls}); err != nil {
		s.logger.ErrorContext(request.Context(), "encode sitemap", "error", err)
	}
}

func yamlQuoted(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func markdownText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	replacer := strings.NewReplacer(
		"\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_",
		"[", "\\[", "]", "\\]", "<", "\\<", ">", "\\>",
		"#", "\\#", "|", "\\|",
	)
	return replacer.Replace(value)
}

func markdownURL(value string) string {
	return strings.ReplaceAll(value, ">", "%3E")
}

func writeMarkdownFrontMatter(builder *strings.Builder, title string, page basePage) {
	fmt.Fprintf(builder, "---\ntitle: %s\ndescription: %s\nlanguage: %s\ncanonical: %s\n---\n\n",
		yamlQuoted(title+" · MunichBrief"), yamlQuoted(pageDescription(page)), yamlQuoted(page.Lang), yamlQuoted(page.CanonicalURL))
}

func pageDescription(page basePage) string {
	if page.Lang == "en" {
		return "A local-first reader for Munich Police press releases."
	}
	return "Ein lokaler Leser für Pressemitteilungen der Münchner Polizei."
}

func (s *Server) renderTimelineMarkdown(response http.ResponseWriter, data timelinePage) {
	var builder strings.Builder
	writeMarkdownFrontMatter(&builder, s.localization.Text(data.Lang, "Timeline"), data.basePage)
	fmt.Fprintf(&builder, "# %s\n\n%s\n\n%s\n",
		markdownText(s.localization.Text(data.Lang, "HeroTitle")),
		markdownText(s.localization.Text(data.Lang, "HeroCopy")),
		markdownText(s.localization.ShownTotal(data.Lang, data.Shown, data.Total)))
	if len(data.Groups) == 0 {
		fmt.Fprintf(&builder, "\n## %s\n\n", markdownText(s.localization.Text(data.Lang, "NoIncidents")))
		copyKey := "NoLiveCopy"
		if data.Fixture {
			copyKey = "NoFixtureCopy"
		}
		fmt.Fprintf(&builder, "%s\n", markdownText(s.localization.Text(data.Lang, copyKey)))
	}
	for _, group := range data.Groups {
		fmt.Fprintf(&builder, "\n## %s\n", markdownText(group.Label))
		for _, incident := range group.Incidents {
			link := fmt.Sprintf("%s/%s/incidents/%d", data.CanonicalOrigin, data.Lang, incident.Record.ID)
			fmt.Fprintf(&builder, "\n### [%s](<%s>)\n\n", markdownText(incident.Title), markdownURL(link))
			fmt.Fprintf(&builder, "%s\n\n", markdownText(incident.Summary))
			fmt.Fprintf(&builder, "- %s: %s\n", markdownText(s.localization.Text(data.Lang, "PublishedWithin")), markdownText(incident.Record.SourceTitle))
			fmt.Fprintf(&builder, "- %s: %s\n", markdownText(s.localization.Text(data.Lang, "LastProcessed")), markdownText(formatDateTime(data.Lang, incident.Record.UpdatedAt.In(s.location))))
			if incident.CategoryLabel != "" {
				fmt.Fprintf(&builder, "- %s: %s\n", markdownText(s.localization.Text(data.Lang, "Category")), markdownText(incident.CategoryLabel))
			}
			if incident.AreaName != "" {
				fmt.Fprintf(&builder, "- %s: %s\n", markdownText(s.localization.Text(data.Lang, "Area")), markdownText(incident.AreaName))
			}
			writeIncidentMetadataMarkdown(&builder, s, data.Lang, incident)
		}
	}
	if data.HasPrevious || data.HasNext {
		builder.WriteString("\n---\n\n")
		if data.HasPrevious {
			fmt.Fprintf(&builder, "[%s](<%s>)", markdownText(s.localization.Text(data.Lang, "Newer")), markdownURL(data.PreviousCanonicalURL))
		}
		if data.HasPrevious && data.HasNext {
			builder.WriteString(" · ")
		}
		if data.HasNext {
			fmt.Fprintf(&builder, "[%s](<%s>)", markdownText(s.localization.Text(data.Lang, "Older")), markdownURL(data.NextCanonicalURL))
		}
		builder.WriteString("\n")
	}
	_, _ = io.WriteString(response, builder.String())
}

func (s *Server) renderDetailMarkdown(response http.ResponseWriter, data detailPage) {
	var builder strings.Builder
	writeMarkdownFrontMatter(&builder, data.Incident.Title, data.basePage)
	fmt.Fprintf(&builder, "# %s\n\n", markdownText(data.Incident.Title))
	fmt.Fprintf(&builder, "%s\n\n", markdownText(data.Incident.Summary))
	fmt.Fprintf(&builder, "- %s: %s\n", markdownText(s.localization.Text(data.Lang, "PublishedWithin")), markdownText(data.Incident.Record.SourceTitle))
	fmt.Fprintf(&builder, "- %s: %s\n", markdownText(s.localization.Text(data.Lang, "LastProcessed")), markdownText(formatDateTime(data.Lang, data.Incident.Record.UpdatedAt.In(s.location))))
	if data.Incident.CategoryLabel != "" {
		fmt.Fprintf(&builder, "- %s: %s\n", markdownText(s.localization.Text(data.Lang, "Category")), markdownText(data.Incident.CategoryLabel))
	}
	if data.Incident.AreaName != "" {
		fmt.Fprintf(&builder, "- %s: %s\n", markdownText(s.localization.Text(data.Lang, "Area")), markdownText(data.Incident.AreaName))
	}
	writeIncidentMetadataMarkdown(&builder, s, data.Lang, data.Incident)
	sourceCopy := "LiveSourceCopy"
	if data.Fixture {
		sourceCopy = "FixtureSourceCopy"
	}
	fmt.Fprintf(&builder, "\n## %s\n\n%s\n\n", markdownText(s.localization.Text(data.Lang, "AuthoritativeSource")), markdownText(s.localization.Text(data.Lang, sourceCopy)))
	fmt.Fprintf(&builder, "[%s](<%s>)\n", markdownText(s.localization.Text(data.Lang, "OpenOfficialSource")), markdownURL(data.Incident.Record.SourceURL))
	_, _ = io.WriteString(response, builder.String())
}

func writeIncidentMetadataMarkdown(builder *strings.Builder, s *Server, language string, incident incidentView) {
	if incident.EventText != "" {
		fmt.Fprintf(builder, "- %s: %s\n", markdownText(incident.EventLabel), markdownText(incident.EventText))
	}
	if incident.ReportKindLabel != "" {
		fmt.Fprintf(builder, "- %s: %s\n", markdownText(s.localization.Text(language, "ReportKind")), markdownText(incident.ReportKindLabel))
	}
	if incident.PublicAssistance {
		value := incident.PublicAssistanceLabel
		if len(incident.PublicAssistanceTypes) > 0 {
			value += ": " + strings.Join(incident.PublicAssistanceTypes, ", ")
		}
		fmt.Fprintf(builder, "- %s: %s. %s\n", markdownText(s.localization.Text(language, "PublicAssistance")), markdownText(value), markdownText(s.localization.Text(language, "PublicAssistanceSourceCopy")))
	}
}

func (s *Server) renderAboutMarkdown(response http.ResponseWriter, data aboutPage) {
	var builder strings.Builder
	writeMarkdownFrontMatter(&builder, s.localization.Text(data.Lang, "About"), data.basePage)
	fmt.Fprintf(&builder, "# %s\n\n%s\n", markdownText(s.localization.Text(data.Lang, "AboutTitle")), markdownText(s.localization.Text(data.Lang, "AboutIntro")))
	sections := []struct {
		title string
		copy  []string
	}{
		{title: "SourcePolicy", copy: []string{"SourcePolicyCopy", "SourceAuthorityCopy"}},
		{title: "ProcessingRetention", copy: []string{"ProcessingCopy", "AICopy", "VisibilityCopy"}},
		{title: "PrivacyIndependence", copy: []string{"PrivacyCopy", "LegalCopy"}},
	}
	for _, section := range sections {
		fmt.Fprintf(&builder, "\n## %s\n", markdownText(s.localization.Text(data.Lang, section.title)))
		for _, key := range section.copy {
			fmt.Fprintf(&builder, "\n%s\n", markdownText(s.localization.Text(data.Lang, key)))
		}
	}
	_, _ = io.WriteString(response, builder.String())
}
