package source_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/parser"
	"github.com/egekocabas/munichbrief/internal/source"
	"golang.org/x/net/html"
)

func TestLiveMunichSource(t *testing.T) {
	if os.Getenv("MUNICHBRIEF_LIVE_TEST") != "1" {
		t.Skip("set MUNICHBRIEF_LIVE_TEST=1 to contact the official source")
	}
	client := newLiveTestClient(t)
	feed, err := client.FetchFeed(context.Background(), "", "")
	if err != nil {
		t.Fatalf("FetchFeed() error = %v", err)
	}
	if len(feed.Documents) == 0 {
		t.Fatal("official feed returned no valid documents")
	}
	article, err := client.FetchArticle(context.Background(), feed.Documents[0].SourceURL)
	if err != nil {
		t.Fatalf("FetchArticle() error = %v", err)
	}
	parsed, err := parser.ParsePoliceRelease(article)
	if err != nil {
		prefix := article
		if len(prefix) > 160 {
			prefix = prefix[:160]
		}
		t.Fatalf("ParsePoliceRelease() error = %v; bytes=%d prefix=%q headings=%q", err, len(article), prefix, diagnosticHeadings(article))
	}
	if len(parsed.Incidents) == 0 {
		t.Fatal("official article parsed with no incidents")
	}
}

func TestLiveKnownPageShapes(t *testing.T) {
	if os.Getenv("MUNICHBRIEF_KNOWN_PAGE_TEST") != "1" {
		t.Skip("set MUNICHBRIEF_KNOWN_PAGE_TEST=1 to contact the known official pages")
	}
	client := newLiveTestClient(t)
	tests := []struct {
		articleID string
		wantCount int
	}{
		{articleID: "107252", wantCount: 7},
		{articleID: "107292", wantCount: 8},
		{articleID: "107230", wantCount: 1},
		{articleID: "108358", wantCount: 1},
	}
	for _, test := range tests {
		t.Run(test.articleID, func(t *testing.T) {
			articleURL := "https://www.polizei.bayern.de/aktuelles/pressemitteilungen/" + test.articleID + "/index.html"
			article, err := client.FetchArticle(context.Background(), articleURL)
			if err != nil {
				t.Fatalf("FetchArticle() error = %v", err)
			}
			parsed, err := parser.ParsePoliceRelease(article)
			if err != nil {
				t.Fatalf("ParsePoliceRelease() error = %v; headings=%q", err, diagnosticHeadings(article))
			}
			if len(parsed.Incidents) != test.wantCount {
				t.Fatalf("incident count = %d, want %d", len(parsed.Incidents), test.wantCount)
			}
		})
	}
}

func newLiveTestClient(t *testing.T) *source.HTTPClient {
	t.Helper()
	client, err := source.NewHTTPClient(
		"https://www.polizei.bayern.de/rss/polizeiprasidium-munchen.xml",
		"MunichBrief/integration-test (+https://github.com/egekocabas/munichbrief)",
		10*time.Second,
		1<<20,
		3<<20,
		nil,
	)
	if err != nil {
		t.Fatalf("NewHTTPClient() error = %v", err)
	}
	return client
}

func diagnosticHeadings(article []byte) []string {
	document, err := html.Parse(bytes.NewReader(article))
	if err != nil {
		return []string{"parse error: " + err.Error()}
	}
	var headings []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if len(headings) >= 12 {
			return
		}
		if node.Type == html.ElementNode && (node.Data == "h2" || node.Data == "h3") {
			headings = append(headings, strings.Join(strings.Fields(diagnosticText(node)), " "))
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return headings
}

func diagnosticText(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return builder.String()
}
