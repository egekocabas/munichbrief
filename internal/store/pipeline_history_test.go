package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPipelineHistoryCombinesCanonicalAndPostProcessingJobsWithStableCursors(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "pipeline-history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	insertPipelineDocuments(t, ctx, database, now, "one")
	plans := testPipelinePlans()
	request, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, nil, nil, true, now)
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
	translationPlan := PostProcessingPlan{ProcessorKey: "translation", ScopeKey: "en", PromptVersion: "incident-translation-en-v1", Model: "translate:4b", InputKinds: []string{"title_de", "summary_de"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, german.PresentationRunID, []PostProcessingPlan{translationPlan}, "manual", false, now); err != nil || queued != 1 {
		t.Fatalf("queue translation = %d/%v", queued, err)
	}
	translation, found, err := database.ClaimPostProcessingJob(ctx, "translation", false, nil, now)
	if err != nil || !found {
		t.Fatalf("claim translation = %#v/%t/%v", translation, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, translation, []PipelineValue{{Kind: "title", Value: "Title"}, {Kind: "summary", Value: "Summary."}}, translation.ModelIdentity, translation.InputHash, now); err != nil {
		t.Fatal(err)
	}
	verificationPlan := PostProcessingPlan{ProcessorKey: "category_verification", ScopeKey: "default", PromptVersion: "incident-category-verification-v1", Model: "verify:4b", InputKinds: []string{"title_de", "summary_de", "category"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, german.PresentationRunID, []PostProcessingPlan{verificationPlan}, "manual", false, now); err != nil || queued != 1 {
		t.Fatalf("queue category verification = %d/%v", queued, err)
	}
	verification, found, err := database.ClaimPostProcessingJob(ctx, "category_verification", false, nil, now)
	if err != nil || !found {
		t.Fatalf("claim category verification = %#v/%t/%v", verification, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, verification, []PipelineValue{{Kind: "is_correct", Value: "true"}, {Kind: "corrected_category", Value: "other"}}, verification.ModelIdentity, verification.InputHash, now); err != nil {
		t.Fatal(err)
	}

	first, err := database.ListPipelineHistory(ctx, "fixture", 2, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 2 || first.HasNewer || !first.HasOlder {
		t.Fatalf("first pipeline history page = %#v", first)
	}
	verified := first.Entries[0]
	if verified.Kind != pipelineHistoryKindPostProcessing || verified.ProcessorKey != "category_verification" || verified.ExecutionKey != "category_verification/default" || verified.ScopeKey != "default" || verified.RequestKind != "manual" || verified.Status != "succeeded" || verified.ModelIdentity != "verify:4b" || verified.CycleID != request.CycleID {
		t.Fatalf("category verification history entry = %#v", verified)
	}
	translated := first.Entries[1]
	if translated.Kind != pipelineHistoryKindPostProcessing || translated.ProcessorKey != "translation" || translated.ExecutionKey != "translation/en" || translated.ScopeKey != "en" || translated.RequestKind != "manual" || translated.Status != "succeeded" || translated.ModelIdentity != "translate:4b" || translated.CycleID != request.CycleID {
		t.Fatalf("translation history entry = %#v", translated)
	}

	boundary := first.Entries[len(first.Entries)-1]
	older, err := database.ListPipelineHistory(ctx, "fixture", 2, &PipelineHistoryCursor{UpdatedAt: boundary.UpdatedAt, Kind: boundary.Kind, JobID: boundary.JobID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Entries) != 2 || !older.HasNewer || older.HasOlder || older.Entries[0].StepKey != "german_presentation" || older.Entries[1].StepKey != "incident_metadata" {
		t.Fatalf("older pipeline history page = %#v", older)
	}

	oldest := older.Entries[1]
	newer, err := database.ListPipelineHistory(ctx, "fixture", 2, nil, &PipelineHistoryCursor{UpdatedAt: oldest.UpdatedAt, Kind: oldest.Kind, JobID: oldest.JobID})
	if err != nil {
		t.Fatal(err)
	}
	if len(newer.Entries) != 2 || newer.Entries[0].Kind != pipelineHistoryKindPostProcessing || newer.Entries[1].StepKey != "german_presentation" {
		t.Fatalf("newer pipeline history page = %#v, want %#v", newer, first)
	}

	snapshot, err := database.PipelineSnapshot(ctx, "fixture", []string{"incident_metadata", "german_presentation"}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.RecentEvents) != 4 || snapshot.RecentEvents[0].Kind != pipelineHistoryKindPostProcessing || snapshot.RecentEvents[0].ProcessorKey != "category_verification" || snapshot.RecentEvents[0].ExecutionKey != "category_verification/default" || snapshot.RecentEvents[0].CycleID != request.CycleID || snapshot.RecentEvents[1].Kind != pipelineHistoryKindPostProcessing {
		t.Fatalf("combined recent events = %#v", snapshot.RecentEvents)
	}
}
