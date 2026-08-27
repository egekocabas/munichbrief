package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPipelineHistoryCombinesCanonicalAndTranslationJobsWithStableCursors(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "pipeline-history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	plans := testPipelinePlans()
	request, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, "translate:4b", nil, true, now)
	if err != nil || request.Requested != 1 {
		t.Fatalf("create pipeline history cycle = %#v/%v", request, err)
	}
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", nil, false, now)
	if err != nil || !found {
		t.Fatalf("activate pipeline history cycle = %#v/%t/%v", cycle, found, err)
	}
	metadata, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now)
	if err != nil || !found {
		t.Fatalf("claim metadata = %#v/%t/%v", metadata, found, err)
	}
	metadataValues := []PipelineValue{{Kind: "category", Value: "other"}, {Kind: "report_kind", Value: "incident"}, {Kind: "public_assistance_status", Value: "not_requested"}, {Kind: "public_assistance_types", Value: "[]"}}
	if err := database.CompletePipelineJob(ctx, metadata, metadataValues, metadata.ModelIdentity, HashPipelineInput(metadata.SourceHash), now); err != nil {
		t.Fatal(err)
	}
	advanced, err := database.AdvancePipelineCycle(ctx, cycle, len(plans), now)
	if err != nil || !advanced.Advanced {
		t.Fatalf("advance metadata = %#v/%v", advanced, err)
	}
	cycle.ActiveStep = 1
	german, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 1, now)
	if err != nil || !found {
		t.Fatalf("claim German presentation = %#v/%t/%v", german, found, err)
	}
	germanValues := []PipelineValue{{Kind: "title_de", Value: "Titel"}, {Kind: "summary_de", Value: "Zusammenfassung."}, {Kind: "privacy_status", Value: "safe"}, {Kind: "privacy_flags", Value: "[]"}}
	if err := database.CompletePipelineJob(ctx, german, germanValues, german.ModelIdentity, HashPipelineInput(german.SourceHash), now); err != nil {
		t.Fatal(err)
	}
	completed, err := database.AdvancePipelineCycle(ctx, cycle, len(plans), now)
	if err != nil || !completed.Completed {
		t.Fatalf("complete canonical cycle = %#v/%v", completed, err)
	}
	if queued, err := database.QueueTranslationsForRun(ctx, german.PresentationRunID, []TranslationPlan{{Language: "en", PromptVersion: "incident-translation-en-v1", Model: "translate:4b"}}, "manual", now); err != nil || queued != 1 {
		t.Fatalf("queue translation = %d/%v", queued, err)
	}
	translation, found, err := database.ClaimTranslationJob(ctx, false, nil, now)
	if err != nil || !found {
		t.Fatalf("claim translation = %#v/%t/%v", translation, found, err)
	}
	if err := database.CompleteTranslationJob(ctx, translation, "Title", "Summary.", translation.ModelIdentity, translation.InputHash, now); err != nil {
		t.Fatal(err)
	}

	first, err := database.ListPipelineHistory(ctx, "fixture", 2, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 2 || first.HasNewer || !first.HasOlder {
		t.Fatalf("first pipeline history page = %#v", first)
	}
	translated := first.Entries[0]
	if translated.Kind != pipelineHistoryKindTranslation || translated.StepKey != "translation:en" || translated.Language != "en" || translated.RequestKind != "manual" || translated.Status != "succeeded" || translated.ModelIdentity != "translate:4b" || translated.CycleID != request.CycleID {
		t.Fatalf("translation history entry = %#v", translated)
	}
	if canonical := first.Entries[1]; canonical.Kind != pipelineHistoryKindCycle || canonical.StepKey != "german_presentation" || canonical.Status != "succeeded" {
		t.Fatalf("canonical history entry = %#v", canonical)
	}

	boundary := first.Entries[len(first.Entries)-1]
	older, err := database.ListPipelineHistory(ctx, "fixture", 2, &PipelineHistoryCursor{UpdatedAt: boundary.UpdatedAt, Kind: boundary.Kind, JobID: boundary.JobID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Entries) != 1 || !older.HasNewer || older.HasOlder || older.Entries[0].StepKey != "incident_metadata" {
		t.Fatalf("older pipeline history page = %#v", older)
	}

	oldest := older.Entries[0]
	newer, err := database.ListPipelineHistory(ctx, "fixture", 2, nil, &PipelineHistoryCursor{UpdatedAt: oldest.UpdatedAt, Kind: oldest.Kind, JobID: oldest.JobID})
	if err != nil {
		t.Fatal(err)
	}
	if len(newer.Entries) != len(first.Entries) || newer.Entries[0].Kind != pipelineHistoryKindTranslation || newer.Entries[1].StepKey != "german_presentation" {
		t.Fatalf("newer pipeline history page = %#v, want %#v", newer, first)
	}

	snapshot, err := database.PipelineSnapshot(ctx, "fixture", []string{"incident_metadata", "german_presentation"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.RecentEvents) != 3 || snapshot.RecentEvents[0].Kind != pipelineHistoryKindTranslation || snapshot.RecentEvents[0].StepKey != "translation:en" || snapshot.RecentEvents[0].CycleID != request.CycleID {
		t.Fatalf("combined recent events = %#v", snapshot.RecentEvents)
	}
}
