package ingest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/source"
	"github.com/egekocabas/munichbrief/internal/store"
)

type fakeLiveClient struct {
	feedResults      []source.FeedResult
	feedError        error
	feedCalls        int
	article          []byte
	articleError     error
	articleCalls     int
	receivedETag     string
	receivedModified string
}

func (f *fakeLiveClient) FetchFeed(_ context.Context, etag, lastModified string) (source.FeedResult, error) {
	f.receivedETag = etag
	f.receivedModified = lastModified
	if f.feedError != nil {
		return source.FeedResult{}, f.feedError
	}
	result := f.feedResults[f.feedCalls]
	f.feedCalls++
	return result, nil
}

func (f *fakeLiveClient) FetchArticle(context.Context, string) ([]byte, error) {
	f.articleCalls++
	return f.article, f.articleError
}

func TestSyncerIngestsSevenDayWindowAndUsesConditionalState(t *testing.T) {
	ctx := context.Background()
	database := testStore(t)
	now := time.Date(2026, time.August, 22, 10, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	client := &fakeLiveClient{
		feedResults: []source.FeedResult{
			{
				ETag:         `"feed-v1"`,
				LastModified: "Sat, 22 Aug 2026 07:00:00 GMT",
				Documents: []domain.SourceDocument{
					testDocument("107500", now.Add(-time.Hour)),
					testDocument("107400", now.AddDate(0, 0, -6)),
					testDocument("107300", now.AddDate(0, 0, -7)),
				},
			},
			{NotModified: true},
		},
		article: []byte(`<section class="bp-template bp-presse"><div class="bp-iwe2"><h2>1300. Test incident – Munich</h2><p>Body text.</p></div></section>`),
	}
	clockValue := now
	var logs bytes.Buffer
	syncer, err := NewSyncer(database, client, 6*time.Hour, func() time.Time { return clockValue }, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("NewSyncer() error = %v", err)
	}

	first, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("first Sync() error = %v", err)
	}
	if first.Discovered != 2 || first.Fetched != 2 || first.ArticleFailures != 0 {
		t.Fatalf("first result = %#v", first)
	}
	wantWindowStart := time.Date(2026, time.August, 16, 0, 0, 0, 0, first.WindowStart.Location())
	wantWindowEnd := time.Date(2026, time.August, 23, 0, 0, 0, 0, first.WindowEnd.Location())
	if !first.WindowStart.Equal(wantWindowStart) || !first.WindowEnd.Equal(wantWindowEnd) {
		t.Fatalf("window = %v to %v, want %v to %v", first.WindowStart, first.WindowEnd, wantWindowStart, wantWindowEnd)
	}
	entries, total, err := database.ListTimelineEntries(ctx, 20, 0, "live")
	if err != nil {
		t.Fatalf("ListTimelineEntries() error = %v", err)
	}
	if total != 2 || len(entries) != 2 || entries[0].Number != "1300" || entries[1].Number != "1300" {
		t.Fatalf("timeline entries = %#v, total %d", entries, total)
	}

	clockValue = now.Add(15 * time.Minute)
	second, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("second Sync() error = %v", err)
	}
	if !second.NotModified || client.articleCalls != 2 {
		t.Fatalf("second result = %#v, article calls = %d", second, client.articleCalls)
	}
	if client.receivedETag != `"feed-v1"` || client.receivedModified == "" {
		t.Errorf("conditional state = %q/%q", client.receivedETag, client.receivedModified)
	}
	history, err := database.ListRSSSyncHistory(ctx, 10, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Entries) != 2 || history.Entries[0].Status != "succeeded" || !history.Entries[0].NotModified || history.Entries[1].FeedDocuments != 3 || history.Entries[1].Fetched != 2 {
		t.Fatalf("RSS sync history = %#v", history)
	}
	logText := logs.String()
	for _, expected := range []string{"RSS synchronization started", "rss_attempt_id=", "RSS feed request started", "RSS feed response received", "press release request started", "press release response received", "press release processed and stored", "duration_seconds="} {
		if !strings.Contains(logText, expected) {
			t.Errorf("ingestion lifecycle logs do not contain %q: %s", expected, logText)
		}
	}
	if strings.Contains(logText, "Body text.") {
		t.Fatalf("ingestion lifecycle logs contain press release body text: %s", logText)
	}
}

func TestSyncerPersistsFailedRSSAttempt(t *testing.T) {
	ctx := context.Background()
	database := testStore(t)
	now := time.Date(2026, time.August, 22, 10, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	client := &fakeLiveClient{feedError: errors.New("upstream unavailable")}
	syncer, err := NewSyncer(database, client, 6*time.Hour, func() time.Time { return now }, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := syncer.Sync(ctx); err == nil {
		t.Fatal("Sync() error = nil, want feed failure")
	}
	history, err := database.ListRSSSyncHistory(ctx, 10, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Entries) != 1 || history.Entries[0].Status != "failed" || history.Entries[0].ErrorMessage != "upstream unavailable" || history.Entries[0].CompletedAt == nil {
		t.Fatalf("failed RSS sync history = %#v", history)
	}
}

func TestSyncerKeepsMetadataWhenArticleFetchFails(t *testing.T) {
	ctx := context.Background()
	database := testStore(t)
	now := time.Date(2026, time.August, 22, 10, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	client := &fakeLiveClient{
		feedResults:  []source.FeedResult{{Documents: []domain.SourceDocument{testDocument("107500", now)}}},
		articleError: errors.New("source unavailable"),
	}
	syncer, err := NewSyncer(database, client, 6*time.Hour, func() time.Time { return now }, discardLogger())
	if err != nil {
		t.Fatalf("NewSyncer() error = %v", err)
	}

	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.ArticleFailures != 1 {
		t.Fatalf("ArticleFailures = %d, want 1", result.ArticleFailures)
	}
	if result.FetchFailures != 1 || result.ParserFailures != 0 {
		t.Fatalf("failure breakdown = fetch %d/parser %d", result.FetchFailures, result.ParserFailures)
	}
	entries, total, err := database.ListTimelineEntries(ctx, 20, 0, "live")
	if err != nil {
		t.Fatalf("ListTimelineEntries() error = %v", err)
	}
	if total != 1 || entries[0].HasIncident || entries[0].FetchStatus != "error" {
		t.Fatalf("metadata fallback = %#v", entries)
	}
}

func TestSyncerReportsParserFailuresSeparately(t *testing.T) {
	ctx := context.Background()
	database := testStore(t)
	now := time.Date(2026, time.August, 22, 10, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	client := &fakeLiveClient{
		feedResults: []source.FeedResult{{Documents: []domain.SourceDocument{testDocument("107500", now)}}},
		article:     []byte(`<main>unexpected page format</main>`),
	}
	syncer, err := NewSyncer(database, client, 6*time.Hour, func() time.Time { return now }, discardLogger())
	if err != nil {
		t.Fatalf("NewSyncer() error = %v", err)
	}
	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.FetchFailures != 0 || result.ParserFailures != 1 || result.ArticleFailures != 1 {
		t.Fatalf("failure breakdown = %#v", result)
	}
}

func TestSyncerStoresUnnumberedStandaloneRelease(t *testing.T) {
	ctx := context.Background()
	database := testStore(t)
	now := time.Date(2026, time.September, 10, 15, 0, 0, 0, time.UTC)
	client := &fakeLiveClient{
		feedResults: []source.FeedResult{{Documents: []domain.SourceDocument{testDocument("108358", now)}}},
		article: []byte(`<main id="readspeaker_lesen"><section class="bp-template bp-presse">
			<bp-headline title="Synthetic road-safety event – Example South"></bp-headline>
			<section class="bp-flex bp-textblock-image"><div class="bp-iwe2">
				<h3>Synthetic road-safety event – Example South</h3><p>Invented event details.</p>
			</div></section>
		</section></main>`),
	}
	syncer, err := NewSyncer(database, client, 6*time.Hour, func() time.Time { return now }, discardLogger())
	if err != nil {
		t.Fatal(err)
	}

	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.Fetched != 1 || result.FetchFailures != 0 || result.ParserFailures != 0 || result.ArticleFailures != 0 {
		t.Fatalf("sync result = %#v", result)
	}
	entries, total, err := database.ListTimelineEntries(ctx, 10, 0, "live")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(entries) != 1 || !entries[0].HasIncident || entries[0].Number != "" || entries[0].TitleDE != "Synthetic road-safety event – Example South" {
		t.Fatalf("stored timeline entries = %#v, total %d", entries, total)
	}
	history, err := database.ListRSSSyncHistory(ctx, 10, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Entries) != 1 || history.Entries[0].Status != "succeeded" || history.Entries[0].Fetched != 1 || history.Entries[0].ParserFailures != 0 || history.Entries[0].Inserted != 1 {
		t.Fatalf("RSS history = %#v", history)
	}
	details, err := database.RSSCheckDetails(ctx, history.Entries[0].ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(details.Documents) != 1 || details.Documents[0].FetchStatus != "stored" || details.Documents[0].Inserted != 1 || len(details.Documents[0].Incidents) != 0 {
		t.Fatalf("RSS details = %#v", details)
	}
	snapshot, err := database.RSSDocumentSnapshot(ctx, history.Entries[0].ID, details.Documents[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Incidents) != 1 || snapshot.Incidents[0].Number != "" || snapshot.Incidents[0].BodyDE != "Invented event details." {
		t.Fatalf("RSS snapshot = %#v", snapshot)
	}
}

func TestSyncerRefetchesChangedFeedEntryWithoutValidators(t *testing.T) {
	ctx := context.Background()
	database := testStore(t)
	now := time.Date(2026, time.August, 22, 10, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	firstDocument := testDocument("107500", now)
	secondDocument := firstDocument
	secondDocument.Title = "Amended media information"
	secondDocument.FeedFingerprint = "feed-107500-amended"
	client := &fakeLiveClient{
		feedResults: []source.FeedResult{
			{Documents: []domain.SourceDocument{firstDocument}},
			{Documents: []domain.SourceDocument{secondDocument}},
		},
		article: []byte(`<section class="bp-template bp-presse"><h2>1300. Test incident</h2><p>Body text.</p></section>`),
	}
	clockValue := now
	syncer, err := NewSyncer(database, client, 6*time.Hour, func() time.Time { return clockValue }, discardLogger())
	if err != nil {
		t.Fatalf("NewSyncer() error = %v", err)
	}
	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("first Sync() error = %v", err)
	}
	clockValue = now.Add(15 * time.Minute)
	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("second Sync() error = %v", err)
	}
	if result.Fetched != 1 || client.articleCalls != 2 {
		t.Fatalf("amendment result = %#v, article calls = %d", result, client.articleCalls)
	}
}

func TestSyncerSerializesConcurrentTriggers(t *testing.T) {
	ctx := context.Background()
	database := testStore(t)
	now := time.Date(2026, time.August, 22, 10, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	client := &fakeLiveClient{
		feedResults: []source.FeedResult{
			{ETag: `"feed-v1"`, Documents: []domain.SourceDocument{testDocument("107500", now)}},
			{NotModified: true},
		},
		article: []byte(`<section class="bp-template bp-presse"><h2>1300. Concurrent test</h2><p>Body.</p></section>`),
	}
	syncer, err := NewSyncer(database, client, 6*time.Hour, func() time.Time { return now }, discardLogger())
	if err != nil {
		t.Fatalf("NewSyncer() error = %v", err)
	}

	errorsChannel := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := syncer.Sync(ctx)
			errorsChannel <- err
		}()
	}
	for range 2 {
		if err := <-errorsChannel; err != nil {
			t.Fatalf("concurrent Sync() error = %v", err)
		}
	}
	if client.feedCalls != 2 || client.articleCalls != 1 {
		t.Fatalf("calls = feed %d/article %d, want 2/1", client.feedCalls, client.articleCalls)
	}
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "munichbrief.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testDocument(id string, publishedAt time.Time) domain.SourceDocument {
	return domain.SourceDocument{
		ExternalID:      id,
		SourceURL:       "https://www.polizei.bayern.de/aktuelles/pressemitteilungen/" + id + "/index.html",
		Title:           "Media information " + id,
		PublishedAt:     publishedAt,
		FeedFingerprint: "feed-" + id,
	}
}

func TestRSSHistoryRecordsRetryAfterNotModifiedFeed(t *testing.T) {
	ctx := context.Background()
	db := testStore(t)
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	client := &fakeLiveClient{feedResults: []source.FeedResult{{Documents: []domain.SourceDocument{testDocument("synthetic-retry", now)}}, {NotModified: true}}, articleError: errors.New("synthetic fetch failure")}
	syncer, err := NewSyncer(db, client, time.Hour, func() time.Time { return now }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	client.articleError = nil
	client.article = []byte(`<section class="bp-template bp-presse"><h2>1. Synthetic retry</h2><p>Recovered text</p></section>`)
	now = now.Add(time.Minute)
	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	history, err := db.ListRSSSyncHistory(ctx, 10, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	latest := history.Entries[0]
	if !result.NotModified || latest.NewDocuments != 0 || latest.ExistingDocuments != 1 || latest.Inserted != 1 || latest.Fetched != 1 {
		t.Fatalf("retry=%+v %+v", result, latest)
	}
	details, err := db.RSSCheckDetails(ctx, latest.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	d := details.Documents[0]
	if d.FromFeed || d.FetchReason != "retry" || !d.Existed || d.FetchStatus != "stored" {
		t.Fatalf("retry detail=%+v", d)
	}
}
