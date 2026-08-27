package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPipelineHistoryUsesStableBidirectionalCursors(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "pipeline-history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one", "two", "three")
	request, err := database.CreateManualPipelineCycle(ctx, "fixture", testPipelinePlans(), "", nil, true, now)
	if err != nil || request.Requested != 3 {
		t.Fatalf("create pipeline history cycle = %#v/%v", request, err)
	}

	first, err := database.ListPipelineHistory(ctx, "fixture", 2, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 2 || first.HasNewer || !first.HasOlder {
		t.Fatalf("first pipeline history page = %#v", first)
	}
	if first.Entries[0].JobID <= first.Entries[1].JobID {
		t.Fatalf("history order = %d then %d", first.Entries[0].JobID, first.Entries[1].JobID)
	}
	for _, entry := range first.Entries {
		if entry.CycleID != request.CycleID || entry.CycleKind != "manual" || entry.CycleStatus != "queued" || entry.StepKey != "incident_metadata" || entry.Status != "pending" || entry.ModelIdentity == "" || entry.PromptVersion == "" {
			t.Fatalf("pipeline history entry = %#v", entry)
		}
	}

	boundary := first.Entries[len(first.Entries)-1]
	older, err := database.ListPipelineHistory(ctx, "fixture", 2, &PipelineHistoryCursor{UpdatedAt: boundary.UpdatedAt, JobID: boundary.JobID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Entries) != 1 || !older.HasNewer || older.HasOlder {
		t.Fatalf("older pipeline history page = %#v", older)
	}

	oldest := older.Entries[0]
	newer, err := database.ListPipelineHistory(ctx, "fixture", 2, nil, &PipelineHistoryCursor{UpdatedAt: oldest.UpdatedAt, JobID: oldest.JobID})
	if err != nil {
		t.Fatal(err)
	}
	if len(newer.Entries) != len(first.Entries) || newer.Entries[0].JobID != first.Entries[0].JobID || newer.Entries[1].JobID != first.Entries[1].JobID {
		t.Fatalf("newer pipeline history page = %#v, want %#v", newer, first)
	}
}
