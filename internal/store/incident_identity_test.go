package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/parser"
)

func TestSourceRepairPreservesIDsAcrossInsertionAndReorder(t *testing.T) {
	ctx := context.Background()
	db, err := openTestStore(ctx, filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	doc := domain.SourceDocument{ExternalID: "identity", SourceURL: "https://fixture.invalid/identity", Title: "Synthetic", PublishedAt: now, FeedFingerprint: "v1"}
	if err := db.UpsertSourceMetadata(ctx, []domain.SourceDocument{doc}, now); err != nil {
		t.Fatal(err)
	}
	var document int64
	if err := db.db.QueryRow(`SELECT id FROM source_documents WHERE external_id='identity'`).Scan(&document); err != nil {
		t.Fatal(err)
	}
	reports := []domain.Incident{{Number: "81", Position: 0, TitleDE: "One", BodyDE: "Merged body", ContentHash: "old"}, {Number: "83", Position: 1, TitleDE: "Three", BodyDE: "Body", ContentHash: "third"}}
	if err := db.ReplaceDocumentIncidents(ctx, document, "v1", reports, now); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, n := range []string{"81", "83"} {
		var id int64
		db.db.QueryRow(`SELECT id FROM incidents WHERE incident_number=?`, n).Scan(&id)
		ids[n] = id
	}
	reports = []domain.Incident{{Number: "81", Position: 0, TitleDE: "One", BodyDE: "Corrected", ContentHash: "new"}, {Number: "82", Position: 1, TitleDE: "Two", ContentHash: "second"}, {Number: "83", Position: 2, TitleDE: "Three", BodyDE: "Body", ContentHash: "third"}}
	changes, err := db.PreviewDocumentRepair(ctx, document, reports)
	if err != nil || changes[0].IncidentID != ids["81"] || changes[1].Status != "inserted" || changes[2].IncidentID != ids["83"] {
		t.Fatalf("preview %#v/%v", changes, err)
	}
	parsed := parser.ParsedRelease{SourceHash: "v2", Incidents: reports}
	if err := db.ApplyDocumentRepair(ctx, document, "stale", parsed, now); err == nil {
		t.Fatal("accepted stale repair")
	}
	if err := db.ApplyDocumentRepair(ctx, document, "v1", parsed, now); err != nil {
		t.Fatal(err)
	}
	for n, want := range ids {
		var got int64
		db.db.QueryRow(`SELECT id FROM incidents WHERE incident_number=?`, n).Scan(&got)
		if got != want {
			t.Fatalf("URL identity changed for %s", n)
		}
	}
	reports[0], reports[2] = reports[2], reports[0]
	for i := range reports {
		reports[i].Position = i
	}
	if err := db.ReplaceDocumentIncidents(ctx, document, "v3", reports, now); err != nil {
		t.Fatal(err)
	}
	reports[1].Number = reports[0].Number
	if err := db.ReplaceDocumentIncidents(ctx, document, "v4", reports, now); err == nil {
		t.Fatal("accepted duplicate identity")
	}
}

func TestStandaloneIdentityIsStableAndCannotBecomeNumbered(t *testing.T) {
	ctx := context.Background()
	db, err := openTestStore(ctx, filepath.Join(t.TempDir(), "standalone.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	doc := domain.SourceDocument{ExternalID: "standalone", SourceURL: "https://fixture.invalid/standalone", Title: "Synthetic", PublishedAt: now, FeedFingerprint: "v1"}
	if err := db.UpsertSourceMetadata(ctx, []domain.SourceDocument{doc}, now); err != nil {
		t.Fatal(err)
	}
	var document, id, got int64
	if err := db.db.QueryRow(`SELECT id FROM source_documents WHERE external_id='standalone'`).Scan(&document); err != nil {
		t.Fatal(err)
	}
	reports := []domain.Incident{{Position: 0, TitleDE: "Standalone", BodyDE: "Original", ContentHash: "v1"}}
	if err := db.ReplaceDocumentIncidents(ctx, document, "v1", reports, now); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT id FROM incidents WHERE source_document_id=?`, document).Scan(&id); err != nil {
		t.Fatal(err)
	}
	reports[0].BodyDE, reports[0].ContentHash = "Changed", "v2"
	if err := db.ReplaceDocumentIncidents(ctx, document, "v2", reports, now); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT id FROM incidents WHERE source_document_id=?`, document).Scan(&got); err != nil || got != id {
		t.Fatalf("identity changed: %d/%v", got, err)
	}
	reports[0].Number = "123"
	if err := db.ReplaceDocumentIncidents(ctx, document, "v3", reports, now); err == nil {
		t.Fatal("accepted ambiguous standalone conversion")
	}
}
