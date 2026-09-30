package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/source"
)

func TestSyncRespectsRSSControlBeforeNetworkAndHistory(t *testing.T) {
	ctx := context.Background()
	database := testStore(t)
	client := &fakeLiveClient{feedResults: []source.FeedResult{{NotModified: true}}}
	syncer, err := NewSyncer(database, client, time.Hour, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetRSSSyncEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := syncer.Sync(ctx); !errors.Is(err, ErrSyncDisabled) {
		t.Fatalf("disabled sync = %v", err)
	}
	history, err := database.ListRSSSyncHistory(ctx, 10, nil, nil)
	if err != nil || len(history.Entries) != 0 || client.feedCalls != 0 || client.articleCalls != 0 {
		t.Fatalf("disabled sync did work: history=%+v, client=%+v, err=%v", history, client, err)
	}
	if err := database.SetRSSSyncEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	history, err = database.ListRSSSyncHistory(ctx, 10, nil, nil)
	if err != nil || len(history.Entries) != 1 || client.feedCalls != 1 {
		t.Fatalf("re-enabled sync did not run: history=%+v, calls=%d, err=%v", history, client.feedCalls, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := syncer.Sync(ctx); err == nil || errors.Is(err, ErrSyncDisabled) || client.feedCalls != 1 {
		t.Fatalf("control read failure must prevent network requests: err=%v, calls=%d", err, client.feedCalls)
	}
}
