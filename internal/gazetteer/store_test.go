package gazetteer

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreActivatesAndRetainsARebuildableGeneration(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	definition := SourceDefinition{Key: "official", DisplayName: "Official", URL: "https://example.test/source", License: "test", Attribution: "Test"}
	entry := Entry{Name: "Schwabing", Kind: KindNeighbourhood, Priority: 10, Sources: []EntrySource{{Key: "official", ExternalID: "1", Kind: KindNeighbourhood}}}
	snapshot := SourceSnapshot{Definition: definition, ContentHash: "one", FetchedAt: time.Now(), Entries: []Entry{entry}}
	id, changed, err := store.Activate(ctx, []SourceSnapshot{snapshot}, []Entry{entry}, "aggregate-one", time.Now(), time.Now().Add(time.Hour))
	if err != nil || !changed || id == 0 {
		t.Fatalf("Activate() id=%d changed=%v err=%v", id, changed, err)
	}
	status, err := store.Status(ctx)
	if err != nil || status.ActiveGeneration != id || status.EntryCount != 1 {
		t.Fatalf("Status() = %#v, err=%v", status, err)
	}
	entries, err := store.ActiveEntries(ctx)
	if err != nil || len(entries) != 1 || entries[0].Name != "Schwabing" {
		t.Fatalf("ActiveEntries() = %#v, err=%v", entries, err)
	}
	overrides, err := store.Overrides(ctx)
	if err != nil || overrides["Brand"] != "exclude" || overrides["Haar"] != "context" {
		t.Fatalf("Overrides() = %#v, err=%v", overrides, err)
	}
}
