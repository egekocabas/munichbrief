package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/parser"
	"github.com/egekocabas/munichbrief/internal/store"
)

func TestRSSDetailsAreLazyEscapedAndProtected(t *testing.T) {
	ctx := context.Background()
	db := fixtureStore(t)
	now := time.Now().UTC()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	check, err := db.RecordSyncAttempt(ctx, now, now.Add(-time.Hour), now.Add(time.Hour))
	must(err)
	doc := domain.SourceDocument{SourceURL: "https://fixture.invalid/detail", ExternalID: "synthetic-detail", Title: "Synthetic <release>", PublishedAt: now, FeedFingerprint: "v1"}
	must(db.ObserveRSSDocuments(ctx, check, []domain.SourceDocument{doc}, now, now.Add(-time.Hour), now.Add(time.Hour)))
	docs, err := db.ListDocumentsForFetch(ctx, now.Add(-time.Hour), now.Add(time.Hour), now, true)
	must(err)
	must(db.QueueRSSFetches(ctx, check, docs, false))
	parsed, err := parser.ParsePoliceRelease([]byte(`<section class="bp-template bp-presse"><h2>1. Synthetic</h2><p>snapshot-only-marker &lt;script&gt;alert(1)&lt;/script&gt;</p></section>`))
	must(err)
	must(db.StoreRSSFetch(ctx, check, docs[0].ID, parsed, now))
	must(db.RecordSyncSuccess(ctx, check, "", "", now, store.SyncRunResult{Fetched: 1}))
	detail, err := db.RSSCheckDetails(ctx, check, 10, 0)
	must(err)
	checkURL := fmt.Sprintf("/admin/rss-history/%d", check)
	docURL := fmt.Sprintf("%s/documents/%d", checkURL, detail.Documents[0].ID)
	handler := adminTestServer(t, db, []string{"munichbrief.de"}).Handler()
	request := func(url string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest(http.MethodGet, url, nil))
		return r
	}
	summary := request("/admin/rss-history")
	for _, want := range []string{"1 new · 0 existing", "1 inserted · 0 updated · 0 unchanged", "aria-expanded=\"false\""} {
		if !strings.Contains(summary.Body.String(), want) {
			t.Errorf("missing summary %q", want)
		}
	}
	if strings.Contains(summary.Body.String(), "snapshot-only-marker") {
		t.Fatal("eagerly loaded text")
	}
	listing := request(checkURL + "?fragment=1")
	if listing.Code != 200 || strings.Contains(listing.Body.String(), "<!doctype") || !strings.Contains(listing.Body.String(), "Synthetic &lt;release&gt;") || strings.Contains(listing.Body.String(), "snapshot-only-marker") {
		t.Fatalf("detail listing=%d %s", listing.Code, listing.Body.String())
	}
	for _, suffix := range []string{"", "?fragment=1"} {
		response := request(docURL + suffix)
		body := response.Body.String()
		if response.Code != 200 || response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("X-Robots-Tag") == "" {
			t.Fatalf("snapshot headers=%d %v", response.Code, response.Header())
		}
		for _, want := range []string{"Original extracted text", "Parsed incidents", "snapshot-only-marker &lt;script&gt;alert(1)&lt;/script&gt;", "inserted"} {
			if !strings.Contains(body, want) {
				t.Errorf("missing snapshot %q", want)
			}
		}
		if strings.Contains(body, "<script>alert(1)") {
			t.Fatal("unescaped source")
		}
		if suffix == "" && !strings.Contains(body, "<!doctype html>") {
			t.Fatal("missing full page fallback")
		}
	}
	for _, url := range []string{checkURL, docURL + "?fragment=1"} {
		r := request("https://munichbrief.de" + url)
		if r.Code == 200 || strings.Contains(r.Body.String(), "snapshot-only-marker") {
			t.Fatalf("public host exposed details=%d", r.Code)
		}
	}
	for url, status := range map[string]int{"/admin/rss-history/nope": 400, checkURL + "?after=-1": 400, checkURL + "/documents/999999": 404, "/admin/rss-history/999999": 404} {
		if r := request(url); r.Code != status {
			t.Errorf("%s=%d want %d", url, r.Code, status)
		}
	}
}

func TestRSSLegacyHistoryShowsUnknownCounts(t *testing.T) {
	server := adminTestServer(t, fixtureStore(t), nil)
	var body strings.Builder
	err := server.adminRSSHistoryTemplate.ExecuteTemplate(&body, "admin_rss_history", adminRSSHistoryPage{Entries: []store.RSSSyncHistoryEntry{{ID: 1, Status: "succeeded", Fetched: 7}}})
	if err != nil {
		t.Fatal(err)
	}
	text := body.String()
	if !strings.Contains(text, "Details not recorded") || !strings.Contains(text, "Parsed and stored 7") || strings.Contains(text, "Documents: 0 new") {
		t.Fatalf("legacy summary=%s", text)
	}
	body.Reset()
	err = server.adminRSSHistoryTemplate.ExecuteTemplate(&body, "rss_check_details", adminRSSDetailsPage{Check: &store.RSSCheckDetails{ID: 1, Status: "succeeded"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), "cannot be reconstructed") {
		t.Fatal("missing legacy detail explanation")
	}
}
