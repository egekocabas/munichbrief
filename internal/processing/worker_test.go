package processing

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/source"
	"github.com/egekocabas/munichbrief/internal/store"
)

type fakeGenerator struct{}

func (fakeGenerator) ModelIdentity() string { return "test-model" }

func (fakeGenerator) Generate(context.Context, string, string) (store.AIPresentation, string, error) {
	return store.AIPresentation{
		TitleDE:   "Kurzer deutscher Titel",
		SummaryDE: "Eine kurze deutsche Zusammenfassung.",
		TitleEN:   "Short English title",
		SummaryEN: "A short English summary.",
	}, "test-model", nil
}

func TestWorkerProcessesExistingIncidentsAndPersistsPresentation(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "worker.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	documents, err := source.NewFixtureProvider().Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := database.UpsertDocuments(ctx, documents, time.Now()); err != nil {
		t.Fatalf("UpsertDocuments() error = %v", err)
	}

	worker, err := NewWorker(database, fakeGenerator{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, time.Now)
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	worker.processAvailable(ctx)

	incidents, _, err := database.ListIncidents(ctx, 20, 0)
	if err != nil {
		t.Fatalf("ListIncidents() error = %v", err)
	}
	for _, incident := range incidents {
		if !incident.HasAI || incident.AITitleDE != "Kurzer deutscher Titel" || incident.AISummaryEN == "" {
			t.Errorf("incident %d has incomplete AI presentation: %#v", incident.ID, incident)
		}
		if incident.BodyDE == "" {
			t.Errorf("incident %d original body was removed before quality review", incident.ID)
		}
	}
	depth, err := database.ProcessingQueueDepth(ctx, worker.operation)
	if err != nil || depth != 0 {
		t.Errorf("queue depth = %d, err=%v; want 0", depth, err)
	}
}
