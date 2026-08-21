package ingest

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/source"
	"github.com/egekocabas/munichbrief/internal/store"
)

type fakeLiveClient struct {
	feedResults      []source.FeedResult
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
	result := f.feedResults[f.feedCalls]
	f.feedCalls++
	return result, nil
}

func (f *fakeLiveClient) FetchArticle(context.Context, string) ([]byte, error) {
	f.articleCalls++
	return f.article, f.articleError
}

func TestSyncerIngestsThreeDayWindowAndUsesConditionalState(t *testing.T) {
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
					testDocument("107400", now.AddDate(0, 0, -3)),
				},
			},
			{NotModified: true},
		},
		article: []byte(`<section class="bp-template bp-presse"><div class="bp-iwe2"><h2>1300. Test incident – Munich</h2><p>Body text.</p></div></section>`),
	}
	clockValue := now
	syncer, err := NewSyncer(database, client, 6*time.Hour, func() time.Time { return clockValue })
	if err != nil {
		t.Fatalf("NewSyncer() error = %v", err)
	}

	first, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("first Sync() error = %v", err)
	}
	if first.Discovered != 1 || first.Fetched != 1 || first.ArticleFailures != 0 {
		t.Fatalf("first result = %#v", first)
	}
	entries, total, err := database.ListTimelineEntries(ctx, 20, 0, "live")
	if err != nil {
		t.Fatalf("ListTimelineEntries() error = %v", err)
	}
	if total != 1 || len(entries) != 1 || entries[0].Number != "1300" {
		t.Fatalf("timeline entries = %#v, total %d", entries, total)
	}

	clockValue = now.Add(15 * time.Minute)
	second, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("second Sync() error = %v", err)
	}
	if !second.NotModified || client.articleCalls != 1 {
		t.Fatalf("second result = %#v, article calls = %d", second, client.articleCalls)
	}
	if client.receivedETag != `"feed-v1"` || client.receivedModified == "" {
		t.Errorf("conditional state = %q/%q", client.receivedETag, client.receivedModified)
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
	syncer, err := NewSyncer(database, client, 6*time.Hour, func() time.Time { return now })
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
	syncer, err := NewSyncer(database, client, 6*time.Hour, func() time.Time { return now })
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
	syncer, err := NewSyncer(database, client, 6*time.Hour, func() time.Time { return clockValue })
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
	syncer, err := NewSyncer(database, client, 6*time.Hour, func() time.Time { return now })
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

func testDocument(id string, publishedAt time.Time) domain.SourceDocument {
	return domain.SourceDocument{
		ExternalID:      id,
		SourceURL:       "https://www.polizei.bayern.de/aktuelles/pressemitteilungen/" + id + "/index.html",
		Title:           "Media information " + id,
		PublishedAt:     publishedAt,
		FeedFingerprint: "feed-" + id,
	}
}
