package source

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
)

const maxFeedItems = 100

const repositoryURL = "https://github.com/egekocabas/munichbrief"

var articlePathPattern = regexp.MustCompile(`^/aktuelles/pressemitteilungen/([0-9]+)/index\.html$`)

// FeedResult is a bounded, normalized response from the configured RSS source.
type FeedResult struct {
	NotModified  bool
	ETag         string
	LastModified string
	Documents    []domain.SourceDocument
	Skipped      int
}

// LiveClient supplies conditional feed reads and allowlisted article bodies.
type LiveClient interface {
	FetchFeed(context.Context, string, string) (FeedResult, error)
	FetchArticle(context.Context, string) ([]byte, error)
}

// HTTPClient constrains requests to the configured HTTPS source origin and
// enforces response-size limits before parsing.
type HTTPClient struct {
	feedURL          *url.URL
	allowedHosts     map[string]bool
	userAgent        string
	feedMaxBytes     int64
	articleMaxBytes  int64
	client           *http.Client
	location         *time.Location
	responseObserver func(string, int)
}

// SetResponseObserver registers an optional status-only observer. Response
// bodies and URLs are deliberately excluded from this hook.
func (c *HTTPClient) SetResponseObserver(observer func(resource string, status int)) {
	c.responseObserver = observer
}

type rssDocument struct {
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Category    string `xml:"category"`
	PubDate     string `xml:"pubDate"`
}

// NewHTTPClient validates the source trust boundary and returns a constrained
// client. baseClient is cloned before its timeout and redirect policy are set.
func NewHTTPClient(feedURL, userAgent string, timeout time.Duration, feedMaxBytes, articleMaxBytes int64, baseClient *http.Client) (*HTTPClient, error) {
	parsedFeedURL, err := url.Parse(feedURL)
	if err != nil || parsedFeedURL.Scheme != "https" || parsedFeedURL.Host == "" || parsedFeedURL.User != nil {
		return nil, errors.New("feed URL must be an absolute HTTPS URL without credentials")
	}
	if !strings.Contains(userAgent, repositoryURL) {
		return nil, fmt.Errorf("user agent must contain %s", repositoryURL)
	}
	if timeout <= 0 || feedMaxBytes <= 0 || articleMaxBytes <= 0 {
		return nil, errors.New("HTTP limits and timeout must be positive")
	}
	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return nil, fmt.Errorf("load Europe/Berlin timezone: %w", err)
	}

	client := &http.Client{}
	if baseClient != nil {
		clone := *baseClient
		client = &clone
	}
	client.Timeout = timeout
	allowedHosts := map[string]bool{parsedFeedURL.Host: true}
	if parsedFeedURL.Port() == "" {
		switch parsedFeedURL.Hostname() {
		case "www.polizei.bayern.de":
			allowedHosts["polizei.bayern.de"] = true
		case "polizei.bayern.de":
			allowedHosts["www.polizei.bayern.de"] = true
		}
	}
	// Refuse cross-origin redirects even when the destination would otherwise be
	// a valid HTTPS URL; article URLs originate in untrusted feed content.
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if request.URL.Scheme != "https" || len(via) == 0 || request.URL.Host != via[0].URL.Host {
			return errors.New("redirect left the configured HTTPS source origin")
		}
		return nil
	}

	return &HTTPClient{
		feedURL:         parsedFeedURL,
		allowedHosts:    allowedHosts,
		userAgent:       userAgent,
		feedMaxBytes:    feedMaxBytes,
		articleMaxBytes: articleMaxBytes,
		client:          client,
		location:        location,
	}, nil
}

// FetchFeed performs a conditional RSS request and discards malformed or
// non-allowlisted entries rather than handing them to ingestion.
func (c *HTTPClient) FetchFeed(ctx context.Context, etag, lastModified string) (FeedResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.feedURL.String(), nil)
	if err != nil {
		return FeedResult{}, fmt.Errorf("create feed request: %w", err)
	}
	c.setHeaders(request)
	request.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9")
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		request.Header.Set("If-Modified-Since", lastModified)
	}

	response, err := c.client.Do(request)
	if err != nil {
		c.observeResponse("feed", 0)
		return FeedResult{}, fmt.Errorf("fetch feed: %w", err)
	}
	defer response.Body.Close()
	c.observeResponse("feed", response.StatusCode)

	result := FeedResult{
		ETag:         response.Header.Get("ETag"),
		LastModified: response.Header.Get("Last-Modified"),
	}
	if response.StatusCode == http.StatusNotModified {
		result.NotModified = true
		return result, nil
	}
	if response.StatusCode != http.StatusOK {
		return FeedResult{}, fmt.Errorf("fetch feed: unexpected HTTP status %d", response.StatusCode)
	}
	if err := requireContentType(response.Header.Get("Content-Type"), "application/xml", "application/rss+xml", "text/xml"); err != nil {
		return FeedResult{}, fmt.Errorf("fetch feed: %w", err)
	}
	body, err := readBounded(response.Body, c.feedMaxBytes)
	if err != nil {
		return FeedResult{}, fmt.Errorf("read feed: %w", err)
	}

	var feed rssDocument
	decoder := xml.NewDecoder(strings.NewReader(string(body)))
	decoder.Strict = true
	if err := decoder.Decode(&feed); err != nil {
		return FeedResult{}, fmt.Errorf("decode feed XML: %w", err)
	}
	if len(feed.Channel.Items) > maxFeedItems {
		return FeedResult{}, fmt.Errorf("feed contains %d items, maximum is %d", len(feed.Channel.Items), maxFeedItems)
	}

	seenURLs := make(map[string]bool, len(feed.Channel.Items))
	for _, item := range feed.Channel.Items {
		document, err := c.normalizeItem(item)
		if err != nil {
			result.Skipped++
			continue
		}
		if seenURLs[document.SourceURL] {
			result.Skipped++
			continue
		}
		seenURLs[document.SourceURL] = true
		result.Documents = append(result.Documents, document)
	}
	return result, nil
}

func (c *HTTPClient) FetchArticle(ctx context.Context, articleURL string) ([]byte, error) {
	validatedURL, _, err := c.validateArticleURL(articleURL)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, validatedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create article request: %w", err)
	}
	c.setHeaders(request)
	request.Header.Set("Accept", "text/html, application/xhtml+xml;q=0.9")

	response, err := c.client.Do(request)
	if err != nil {
		c.observeResponse("article", 0)
		return nil, fmt.Errorf("fetch article: %w", err)
	}
	defer response.Body.Close()
	c.observeResponse("article", response.StatusCode)
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch article: unexpected HTTP status %d", response.StatusCode)
	}
	if err := requireContentType(response.Header.Get("Content-Type"), "text/html", "application/xhtml+xml"); err != nil {
		return nil, fmt.Errorf("fetch article: %w", err)
	}
	body, err := readBounded(response.Body, c.articleMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("read article: %w", err)
	}
	return body, nil
}

func (c *HTTPClient) normalizeItem(item rssItem) (domain.SourceDocument, error) {
	canonicalURL, externalID, err := c.validateArticleURL(strings.TrimSpace(item.Link))
	if err != nil {
		return domain.SourceDocument{}, err
	}
	title := strings.Join(strings.Fields(item.Title), " ")
	if title == "" {
		return domain.SourceDocument{}, errors.New("feed item title is empty")
	}
	publishedAt, err := c.parsePublicationTime(strings.TrimSpace(item.PubDate))
	if err != nil {
		return domain.SourceDocument{}, err
	}

	return domain.SourceDocument{
		ExternalID:      externalID,
		SourceURL:       canonicalURL,
		Title:           title,
		PublishedAt:     publishedAt,
		FeedFingerprint: digest(title, canonicalURL, item.Description, item.Category, publishedAt.Format(time.RFC3339Nano)),
	}, nil
}

func (c *HTTPClient) validateArticleURL(raw string) (string, string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || !c.allowedHosts[parsed.Host] || parsed.User != nil {
		return "", "", errors.New("article URL is outside the configured HTTPS source origin")
	}
	match := articlePathPattern.FindStringSubmatch(parsed.EscapedPath())
	if match == nil {
		return "", "", errors.New("article URL has an unexpected path")
	}
	if parsed.Host == "polizei.bayern.de" {
		parsed.Host = "www.polizei.bayern.de"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), match[1], nil
}

func (c *HTTPClient) parsePublicationTime(raw string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC1123Z, raw); err == nil {
		return parsed, nil
	}
	if parsed, err := time.ParseInLocation(time.RFC1123, raw, c.location); err == nil {
		return parsed, nil
	}
	if parsed, err := time.ParseInLocation("Mon, 02 Jan 2006 15:04:05", raw, c.location); err == nil {
		return parsed, nil
	}
	return time.Time{}, fmt.Errorf("unsupported feed publication time %q", raw)
}

func (c *HTTPClient) setHeaders(request *http.Request) {
	request.Header.Set("User-Agent", c.userAgent)
}

func (c *HTTPClient) observeResponse(resource string, status int) {
	if c.responseObserver != nil {
		c.responseObserver(resource, status)
	}
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	contents, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > maximum {
		return nil, fmt.Errorf("response exceeded %d bytes", maximum)
	}
	return contents, nil
}

func requireContentType(header string, allowed ...string) error {
	if header == "" {
		return errors.New("response has no Content-Type")
	}
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil {
		return errors.New("response has an invalid Content-Type")
	}
	for _, candidate := range allowed {
		if strings.EqualFold(mediaType, candidate) {
			return nil
		}
	}
	return fmt.Errorf("unexpected Content-Type %q", mediaType)
}
