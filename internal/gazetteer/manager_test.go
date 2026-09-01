package gazetteer

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagerKeepsLastGenerationWhenRefreshFails(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	status := http.StatusOK
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"features":[{"id":"1","properties":{"strassenname":"Ganghoferstraße"}}]}`)),
		}, nil
	})}
	definition := SourceDefinition{
		Key: "munich_streets", DisplayName: "Test", URL: "https://example.test/source",
		License: "test", Attribution: "test", MinimumRows: 1, MaximumRows: 2,
		MaximumSize: 1024, Parse: parseMunichStreets,
	}
	fetcher, err := NewFetcher(client, "MunichBrief/test", store)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(ctx, store, fetcher, []SourceDefinition{definition}, time.Hour, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(ctx); err != nil || !manager.Ready() {
		t.Fatalf("initial refresh err=%v ready=%v", err, manager.Ready())
	}

	status = http.StatusBadGateway
	if _, err := manager.Refresh(ctx); err == nil {
		t.Fatal("failed source refresh unexpectedly succeeded")
	}
	entries, err := store.ActiveEntries(ctx)
	if err != nil || len(entries) != 1 || entries[0].Name != "Ganghoferstraße" || !manager.Ready() {
		t.Fatalf("active generation after failure = %#v, ready=%v, err=%v", entries, manager.Ready(), err)
	}
}

func TestMergeEntriesAppliesAmbiguityOverridesAndShortNameContext(t *testing.T) {
	entries := mergeEntries([]SourceSnapshot{{Entries: []Entry{
		{Name: "Brand", Kind: KindNeighbourhood, Priority: 40},
		{Name: "Au", Kind: KindNeighbourhood, Priority: 40},
	}}}, map[string]string{"Brand": "exclude"})
	if len(entries) != 1 || entries[0].Name != "Au" || !entries[0].RequiresContext {
		t.Fatalf("mergeEntries() = %#v", entries)
	}
}
