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
	}
	for _, alternate := range page.LanguageAlternates {
		links = append(links, fmt.Sprintf("<%s>; rel=\"alternate\"; hreflang=\"%s\"", alternate.URL, alternate.Tag))
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
	if _, registered := s.languageByCode(language); !registered {
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
	response.Header().Set("Content-Language", page.LanguageTag)
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	setAIResponseHeaders(response.Header(), page)
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
	origin := s.canonicalOrigin(request)
	urls := make([]sitemapURL, 0)
	for _, definition := range s.languages {
		links, err := s.store.ListPublicIncidentLinks(request.Context(), s.options.SourceMode, store.PresentationScope{
			Language:   definition.Code,
			PublicOnly: true,
		})
		if err != nil {
			s.internalError(response, request, "list sitemap incidents", err)
			return
		}
		urls = append(urls,
			sitemapURL{Location: origin + "/" + definition.Code},
			sitemapURL{Location: origin + "/" + definition.Code + "/about"},
			sitemapURL{Location: origin + "/" + definition.Code + "/contact"},
		)
		for _, link := range links {
			urls = append(urls, sitemapURL{
				Location: fmt.Sprintf("%s/%s/incidents/%d", origin, definition.Code, link.ID),
				LastMod:  link.ModifiedAt.UTC().Format(time.RFC3339),
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
	fmt.Fprintf(builder, "---\ntitle: %s\ndescription: %s\nlanguage: %s\ncanonical: %s\nai_generated: %t\nai_generated_state: %s\n",
		yamlQuoted(title+" · MunichBrief"), yamlQuoted(pageDescription(page)), yamlQuoted(page.LanguageTag), yamlQuoted(page.CanonicalURL),
		page.AIGeneratedState != "false", yamlQuoted(page.AIGeneratedState))
	if page.AIGeneratedState != "false" {
		fmt.Fprintf(builder, "digital_source_type: %s\n", yamlQuoted(iptcTrainedAlgorithmicMedia))
	}
	if page.AIModel != "" {
		fmt.Fprintf(builder, "ai_model: %s\n", yamlQuoted(page.AIModel))
	}
	if page.AIMetadataModel != "" {
		fmt.Fprintf(builder, "ai_metadata_model: %s\n", yamlQuoted(page.AIMetadataModel))
	}
	if page.AIPublicAssistanceVerificationModel != "" {
		fmt.Fprintf(builder, "ai_public_assistance_verification_model: %s\n", yamlQuoted(page.AIPublicAssistanceVerificationModel))
	}
	if page.AICategoryVerificationModel != "" {
		fmt.Fprintf(builder, "ai_category_verification_model: %s\n", yamlQuoted(page.AICategoryVerificationModel))
	}
	if page.AITranslationModel != "" {
		fmt.Fprintf(builder, "ai_translation_model: %s\n", yamlQuoted(page.AITranslationModel))
	}
	builder.WriteString("---\n\n")
}

func pageDescription(page basePage) string {
	return page.Description
}

func (s *Server) renderTimelineMarkdown(response http.ResponseWriter, data timelinePage) {
	var builder strings.Builder
	writeMarkdownFrontMatter(&builder, s.localization.Text(data.Lang, "Timeline"), data.basePage)
	fmt.Fprintf(&builder, "# %s\n\n%s\n\n%s\n",
		markdownText(s.localization.Text(data.Lang, "HeroTitle")),
		markdownText(s.localization.Text(data.Lang, "HeroCopy")),
		markdownText(s.localization.Format(data.Lang, "PageStatus", data)))
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
		if group.TimeLabel != "" {
			fmt.Fprintf(&builder, "\n%s\n", markdownText(group.TimeLabel))
		}
		for _, incident := range group.Incidents {
			link := fmt.Sprintf("%s/%s/incidents/%d", data.CanonicalOrigin, data.Lang, incident.Record.ID)
			if incident.Record.HasAI {
				fmt.Fprintf(&builder, "\n> **%s**\n", markdownText(s.localization.Text(data.Lang, "AIGeneratedContent")))
			}
			fmt.Fprintf(&builder, "\n### [%s](<%s>)\n\n", markdownText(incident.Title), markdownURL(link))
			fmt.Fprintf(&builder, "%s\n\n", markdownText(incident.Summary))
			fmt.Fprintf(&builder, "- %s: %s\n", markdownText(s.localization.Text(data.Lang, "PublishedWithin")), markdownText(incident.Record.SourceTitle))
			fmt.Fprintf(&builder, "- %s: %s\n", markdownText(s.localization.Text(data.Lang, "LastProcessed")), markdownText(s.formatDateTime(data.Lang, incident.Record.UpdatedAt.In(s.location))))
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
	if data.Incident.Record.HasAI {
		fmt.Fprintf(&builder, "> **%s**\n\n", markdownText(s.localization.Text(data.Lang, "AIGeneratedContent")))
	}
	fmt.Fprintf(&builder, "# %s\n\n", markdownText(data.Incident.Title))
	fmt.Fprintf(&builder, "%s\n\n", markdownText(data.Incident.Summary))
	fmt.Fprintf(&builder, "- %s: %s\n", markdownText(s.localization.Text(data.Lang, "PublishedWithin")), markdownText(data.Incident.Record.SourceTitle))
	fmt.Fprintf(&builder, "- %s: %s\n", markdownText(s.localization.Text(data.Lang, "LastProcessed")), markdownText(s.formatDateTime(data.Lang, data.Incident.Record.UpdatedAt.In(s.location))))
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
	fmt.Fprintf(&builder, "\n## %s\n", markdownText(s.localization.Text(data.Lang, "FromReleaseToIncident")))
	steps := []struct {
		title string
		copy  string
	}{
		{title: "Discover", copy: "DiscoverCopy"},
		{title: "Organize", copy: "OrganizeCopy"},
		{title: "Summarize", copy: "SummarizeCopy"},
		{title: "TranslatePublish", copy: "TranslatePublishCopy"},
		{title: "VerifyTranslate", copy: "VerifyTranslateCopy"},
	}
	for index, step := range steps {
		fmt.Fprintf(&builder, "\n%d. **%s**\n\n   %s\n", index+1, markdownText(s.localization.Text(data.Lang, step.title)), markdownText(s.localization.Text(data.Lang, step.copy)))
	}
	sections := []struct {
		title string
		copy  []string
	}{
		{title: "AITransparencyEU", copy: []string{"AITransparencyEUCopy"}},
		{title: "AccuracyOfficialInformation", copy: []string{"AccuracyOfficialInformationCopy", "CoverageCopy"}},
		{title: "PrivacyDataHandling", copy: []string{"PrivacyCopy", "DataHandlingCopy", "MonitoringCopy"}},
		{title: "IndependenceLegalReview", copy: []string{"IndependenceLegalReviewCopy"}},
	}
	for _, section := range sections {
		fmt.Fprintf(&builder, "\n## %s\n", markdownText(s.localization.Text(data.Lang, section.title)))
		for _, key := range section.copy {
			fmt.Fprintf(&builder, "\n%s\n", markdownText(s.localization.Text(data.Lang, key)))
		}
		if section.title == "PrivacyDataHandling" {
			fmt.Fprintf(&builder, "\n### %s\n\n", markdownText(s.localization.Text(data.Lang, "SourceMetadata")))
			builder.WriteString("- [Landeshauptstadt München – GeodatenService](https://opendata.muenchen.de/) (dl-de/by-2.0)\n- [GeoNames](https://www.geonames.org/) (CC BY 4.0)\n- [© OpenStreetMap contributors](https://www.openstreetmap.org/copyright) (ODbL 1.0)\n")
		}
	}
	fmt.Fprintf(&builder, "\n## %s\n", markdownText(s.localization.Text(data.Lang, "ContactHeading")))
	fmt.Fprintf(&builder, "\n%s [%s](https://github.com/egekocabas/munichbrief/issues).\n", markdownText(s.localization.Text(data.Lang, "ContactPublicLead")), markdownText(s.localization.Text(data.Lang, "ContactIssueLink")))
	fmt.Fprintf(&builder, "\n%s [contact@munichbrief.de](mailto:contact@munichbrief.de).\n", markdownText(s.localization.Text(data.Lang, "ContactPrivateLead")))
	_, _ = io.WriteString(response, builder.String())
}
