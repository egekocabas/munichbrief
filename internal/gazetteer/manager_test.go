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
	protected, err := manager.Protect("Einsatz an der Ganghoferstraße", "Die Ganghoferstraße blieb gesperrt.")
	if err != nil || !strings.Contains(protected.Title, "__MB_STREET_") || !strings.Contains(protected.Summary, "__MB_STREET_") {
		t.Fatalf("production protection must use typed streets: %#v / %v", protected, err)
	}
	title, summary, err := Restore(protected, protected.Title, protected.Summary)
	if err != nil || title != "Einsatz an der Ganghoferstraße" || summary != "Die Ganghoferstraße blieb gesperrt." {
		t.Fatalf("typed production roundtrip = %q / %q / %v", title, summary, err)
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

func TestAggregateSourceHashIncludesSourceContract(t *testing.T) {
	snapshot := SourceSnapshot{Definition: SourceDefinition{Key: "source", URL: "https://example.test/one"}, ContentHash: "content"}
	first := aggregateSourceHash([]SourceSnapshot{snapshot}, nil)
	snapshot.Definition.URL = "https://example.test/two"
	second := aggregateSourceHash([]SourceSnapshot{snapshot}, nil)
	if first == second {
		t.Fatal("source definition change did not invalidate generation hash")
	}
}
