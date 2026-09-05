package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const testFeedURL = "https://www.polizei.bayern.de/rss/polizeiprasidium-munchen.xml"

const testUserAgent = "MunichBrief/test (+https://github.com/egekocabas/munichbrief)"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestHTTPClientFetchesConditionalFeedAndArticle(t *testing.T) {
	var feedCalls int
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("User-Agent") != testUserAgent {
			t.Errorf("User-Agent = %q", request.Header.Get("User-Agent"))
		}
		switch request.URL.Path {
		case "/rss/polizeiprasidium-munchen.xml":
			feedCalls++
			if feedCalls == 2 {
				if request.Header.Get("If-None-Match") != `"feed-v1"` {
					t.Errorf("If-None-Match = %q", request.Header.Get("If-None-Match"))
				}
				return response(request, http.StatusNotModified, "", nil), nil
			}
			body := fmt.Sprintf(`<?xml version="1.0"?><rss version="2.0"><channel><item>
				<title>Media information</title>
				<link>%s?ignored=yes</link>
				<description>Test description</description>
				<category>Polizeipräsidium München</category>
				<pubDate>Sat, 22 Aug 2026 09:15:00</pubDate>
			</item></channel></rss>`, "https://polizei.bayern.de/aktuelles/pressemitteilungen/107500/index.html")
			result := response(request, http.StatusOK, "application/xml; charset=UTF-8", strings.NewReader(body))
			result.Header.Set("ETag", `"feed-v1"`)
			result.Header.Set("Last-Modified", "Sat, 22 Aug 2026 07:00:00 GMT")
			return result, nil
		case "/aktuelles/pressemitteilungen/107500/index.html":
			return response(request, http.StatusOK, "text/html; charset=utf-8", strings.NewReader(`<section class="bp-template bp-presse"><h2>1. Test</h2></section>`)), nil
		default:
			return response(request, http.StatusNotFound, "text/plain", nil), nil
		}
	})
	client := testHTTPClient(t, transport, 64<<10, 64<<10)
	var observedResponses []string
	client.SetResponseObserver(func(resource string, status int) {
		observedResponses = append(observedResponses, fmt.Sprintf("%s:%d", resource, status))
	})

	feed, err := client.FetchFeed(context.Background(), "", "")
	if err != nil {
		t.Fatalf("FetchFeed() error = %v", err)
	}
	if len(feed.Documents) != 1 || feed.Documents[0].ExternalID != "107500" {
		t.Fatalf("feed documents = %#v", feed.Documents)
	}
	if strings.Contains(feed.Documents[0].SourceURL, "?") {
		t.Errorf("canonical source URL contains query: %q", feed.Documents[0].SourceURL)
	}
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	wantPublication := time.Date(2026, time.August, 22, 9, 15, 0, 0, berlin)
	if !feed.Documents[0].PublishedAt.Equal(wantPublication) {
		t.Errorf("PublishedAt = %v, want %v", feed.Documents[0].PublishedAt, wantPublication)
	}

	article, err := client.FetchArticle(context.Background(), feed.Documents[0].SourceURL)
	if err != nil {
		t.Fatalf("FetchArticle() error = %v", err)
	}
	if !strings.Contains(string(article), "bp-presse") {
		t.Errorf("article body = %q", article)
	}

	notModified, err := client.FetchFeed(context.Background(), feed.ETag, feed.LastModified)
	if err != nil {
		t.Fatalf("conditional FetchFeed() error = %v", err)
	}
	if !notModified.NotModified {
		t.Error("conditional feed result is not marked NotModified")
	}
	if got := strings.Join(observedResponses, ","); got != "feed:200,article:200,feed:304" {
		t.Errorf("observed responses = %q", got)
	}
}

func TestHTTPClientSkipsInvalidFeedEntries(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `<?xml version="1.0"?><rss><channel><item>
			<title>Outside source</title><link>https://example.com/aktuelles/pressemitteilungen/1/index.html</link>
			<pubDate>Sat, 22 Aug 2026 09:15:00</pubDate>
		</item></channel></rss>`
		return response(request, http.StatusOK, "application/rss+xml", strings.NewReader(body)), nil
	})
	client := testHTTPClient(t, transport, 4096, 4096)

	feed, err := client.FetchFeed(context.Background(), "", "")
	if err != nil {
		t.Fatalf("FetchFeed() error = %v", err)
	}
	if feed.Skipped != 1 || len(feed.Documents) != 0 {
		t.Fatalf("feed result = %#v", feed)
	}
}

func TestHTTPClientDeduplicatesCanonicalFeedEntries(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `<?xml version="1.0"?><rss><channel>
			<item><title>First</title><link>https://polizei.bayern.de/aktuelles/pressemitteilungen/107500/index.html</link><pubDate>Sat, 22 Aug 2026 09:15:00</pubDate></item>
			<item><title>Duplicate</title><link>https://polizei.bayern.de/aktuelles/pressemitteilungen/107500/index.html?tracking=yes</link><pubDate>Sat, 22 Aug 2026 09:15:00</pubDate></item>
		</channel></rss>`
		return response(request, http.StatusOK, "application/rss+xml", strings.NewReader(body)), nil
	})
	client := testHTTPClient(t, transport, 4096, 4096)

	feed, err := client.FetchFeed(context.Background(), "", "")
	if err != nil {
		t.Fatalf("FetchFeed() error = %v", err)
	}
	if len(feed.Documents) != 1 || feed.Skipped != 1 {
		t.Fatalf("feed result = %#v, want one document and one skipped duplicate", feed)
	}
}

func TestHTTPClientRejectsMalformedAndOversizedFeeds(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		limit int64
	}{
		{name: "malformed XML", body: `<rss><channel>`, limit: 4096},
		{name: "oversized", body: strings.Repeat("x", 128), limit: 32},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, http.StatusOK, "application/rss+xml", strings.NewReader(test.body)), nil
			})
			client := testHTTPClient(t, transport, test.limit, 4096)
			if _, err := client.FetchFeed(context.Background(), "", ""); err == nil {
				t.Fatal("FetchFeed() error = nil, want failure")
			}
		})
	}
}

func TestHTTPClientInterpretsBerlinTimezoneAbbreviation(t *testing.T) {
	client := testHTTPClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport should not be called")
		return nil, nil
	}), 4096, 4096)
	parsed, err := client.parsePublicationTime("Sat, 22 Aug 2026 09:15:00 CEST")
	if err != nil {
		t.Fatalf("parsePublicationTime() error = %v", err)
	}
	_, offset := parsed.Zone()
	if offset != 2*60*60 {
		t.Fatalf("timezone offset = %d, want CEST (+02:00)", offset)
	}
}

func TestHTTPClientAcceptsUnpaddedPublicationDay(t *testing.T) {
	client := testHTTPClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport should not be called")
		return nil, nil
	}), 4096, 4096)
	parsed, err := client.parsePublicationTime("Fri, 4 Sep 2026 12:53:00")
	if err != nil {
		t.Fatalf("parsePublicationTime() error = %v", err)
	}
	want := time.Date(2026, time.September, 4, 12, 53, 0, 0, client.location)
	if !parsed.Equal(want) {
		t.Fatalf("publication time = %v, want %v", parsed, want)
	}
}

func TestHTTPClientRejectsOversizedArticle(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusOK, "text/html", strings.NewReader(strings.Repeat("x", 128))), nil
	})
	client := testHTTPClient(t, transport, 4096, 32)

	_, err := client.FetchArticle(context.Background(), "https://www.polizei.bayern.de/aktuelles/pressemitteilungen/107500/index.html")
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("FetchArticle() error = %v, want response size error", err)
	}
}

func TestHTTPClientRedirectsStayOnExactOrigin(t *testing.T) {
	client := testHTTPClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport should not be called")
		return nil, nil
	}), 4096, 4096)
	original, err := http.NewRequest(http.MethodGet, testFeedURL, nil)
	if err != nil {
		t.Fatalf("create original request: %v", err)
	}

	sameOrigin, err := http.NewRequest(http.MethodGet, "https://www.polizei.bayern.de/rss/redirected.xml", nil)
	if err != nil {
		t.Fatalf("create same-origin request: %v", err)
	}
	if err := client.client.CheckRedirect(sameOrigin, []*http.Request{original}); err != nil {
		t.Errorf("same-origin redirect rejected: %v", err)
	}

	otherOfficialHost, err := http.NewRequest(http.MethodGet, "https://polizei.bayern.de/rss/redirected.xml", nil)
	if err != nil {
		t.Fatalf("create cross-origin request: %v", err)
	}
	if err := client.client.CheckRedirect(otherOfficialHost, []*http.Request{original}); err == nil {
		t.Error("cross-origin redirect was accepted")
	}
}

func TestHTTPClientRequiresRepositoryIdentifyingUserAgent(t *testing.T) {
	_, err := NewHTTPClient(testFeedURL, "anonymous-client", time.Second, 4096, 4096, nil)
	if err == nil {
		t.Fatal("NewHTTPClient() error = nil, want repository-identifying user-agent error")
	}
}

func TestHTTPClientCanonicalizesOfficialArticleHost(t *testing.T) {
	client := testHTTPClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport should not be called")
		return nil, nil
	}), 4096, 4096)
	canonical, externalID, err := client.validateArticleURL("https://polizei.bayern.de/aktuelles/pressemitteilungen/107500/index.html?tracking=yes")
	if err != nil {
		t.Fatalf("validateArticleURL() error = %v", err)
	}
	if canonical != "https://www.polizei.bayern.de/aktuelles/pressemitteilungen/107500/index.html" || externalID != "107500" {
		t.Fatalf("canonical identity = %q/%q", canonical, externalID)
	}
}

func testHTTPClient(t *testing.T, transport http.RoundTripper, feedLimit, articleLimit int64) *HTTPClient {
	t.Helper()
	client, err := NewHTTPClient(
		testFeedURL,
		testUserAgent,
		time.Second,
		feedLimit,
		articleLimit,
		&http.Client{Transport: transport},
	)
	if err != nil {
		t.Fatalf("NewHTTPClient() error = %v", err)
	}
	return client
}

func response(request *http.Request, status int, contentType string, body io.Reader) *http.Response {
	if body == nil {
		body = strings.NewReader("")
	}
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     header,
		Body:       io.NopCloser(body),
		Request:    request,
	}
}
