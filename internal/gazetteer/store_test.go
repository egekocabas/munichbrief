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
	admin, err := store.AdminSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if admin.Status.ActiveGeneration != id || len(admin.Sources) != 1 || admin.Sources[0].Key != "official" || admin.Sources[0].ActiveRowCount != 1 {
		t.Fatalf("admin sources = %#v", admin)
	}
	if len(admin.Generations) != 1 || admin.Generations[0].ID != id || admin.Generations[0].EntryCount != 1 || admin.Generations[0].Status != "active" {
		t.Fatalf("admin generations = %#v", admin.Generations)
	}
	if len(admin.Overrides) != 2 || admin.Overrides[0].Name != "Brand" || admin.Overrides[1].Name != "Haar" {
		t.Fatalf("admin overrides = %#v", admin.Overrides)
	}
}

func TestStoreUpdatesSourceMetadataWhenGenerationContentIsUnchanged(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	definition := SourceDefinition{Key: "official", DisplayName: "Old", URL: "https://example.test/old", License: "old", Attribution: "Old"}
	entry := Entry{Name: "Schwabing", Kind: KindNeighbourhood, Priority: 17, Sources: []EntrySource{{Key: "official", ExternalID: "1", Kind: KindNeighbourhood, Priority: 17}}}
	snapshot := SourceSnapshot{Definition: definition, ContentHash: "one", FetchedAt: time.Now(), Entries: []Entry{entry}}
	if _, _, err := store.Activate(ctx, []SourceSnapshot{snapshot}, []Entry{entry}, "same", time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	snapshot.Definition.DisplayName = "New"
	snapshot.Definition.URL = "https://example.test/new"
	snapshot.Definition.License = "new"
	snapshot.Definition.Attribution = "New"
	if _, changed, err := store.Activate(ctx, []SourceSnapshot{snapshot}, []Entry{entry}, "same", time.Now(), time.Now().Add(time.Hour)); err != nil || changed {
		t.Fatalf("unchanged activation changed=%v err=%v", changed, err)
	}
	var displayName, sourceURL, license, attribution, contract string
	if err := store.db.QueryRowContext(ctx, `SELECT display_name, source_url, license, attribution, contract_version FROM gazetteer_sources WHERE source_key='official'`).Scan(&displayName, &sourceURL, &license, &attribution, &contract); err != nil {
		t.Fatal(err)
	}
	if displayName != "New" || sourceURL != snapshot.Definition.URL || license != "new" || attribution != "New" || contract != sourceContractVersion {
		t.Fatalf("source metadata = %q %q %q %q contract=%q", displayName, sourceURL, license, attribution, contract)
	}
}

func TestStoreClearsValidatorsWhenChangedSourceURLFails(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	definition := SourceDefinition{Key: "official", DisplayName: "Official", URL: "https://example.test/old", License: "test", Attribution: "Test"}
	entry := Entry{Name: "Schwabing", Kind: KindNeighbourhood, Priority: 17, Sources: []EntrySource{{Key: "official", ExternalID: "1", Kind: KindNeighbourhood, Priority: 17}}}
	snapshot := SourceSnapshot{Definition: definition, ContentHash: "old-hash", ETag: `"old"`, LastModified: "yesterday", FetchedAt: time.Now(), Entries: []Entry{entry}}
	if _, _, err := store.Activate(ctx, []SourceSnapshot{snapshot}, []Entry{entry}, "old-aggregate", time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	definition.URL = "https://example.test/new"
	if err := store.RecordSourceFailure(ctx, definition, time.Now()); err != nil {
		t.Fatal(err)
	}
	etag, modified, hash, sourceURL, _, err := store.SourceValidators(ctx, definition.Key)
	if err != nil || etag != "" || modified != "" || hash != "" || sourceURL != definition.URL {
		t.Fatalf("validators after changed URL failure = etag:%q modified:%q hash:%q URL:%q err:%v", etag, modified, hash, sourceURL, err)
	}
}
