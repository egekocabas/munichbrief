package store

import (
	"context"
	"testing"
	"time"
)

func TestListAdminIncidentsScopesAndFiltersPresentations(t *testing.T) {
	ctx := context.Background()
	database := fixtureStoreForBackup(t, ctx)
	const (
		model     = "qwen3.5:4b"
		prompt    = "incident-presentation-v2"
		operation = "incident-presentation/incident-presentation-v2/qwen3.5:4b"
	)
	scope := PresentationScope{Operation: operation, ModelIdentity: model, PromptVersion: prompt}
	now := time.Date(2026, time.August, 25, 10, 0, 0, 0, time.UTC)

	currentJob, found, err := database.QueueAndClaimProcessingJob(ctx, operation, now)
	if err != nil || !found {
		t.Fatalf("claim current job = %t/%v", found, err)
	}
	current := AIPresentation{
		TitleDE: "Aktueller Titel", SummaryDE: "Aktuelle Zusammenfassung.",
		TitleEN: "Current title", SummaryEN: "Current summary.", PrivacyStatus: "safe",
	}
	if err := database.CompleteProcessingJob(ctx, currentJob, current, model, prompt, now); err != nil {
		t.Fatal(err)
	}

	staleJob, found, err := database.QueueAndClaimProcessingJob(ctx, operation, now.Add(time.Minute))
	if err != nil || !found {
		t.Fatalf("claim stale job = %t/%v", found, err)
	}
	stale := AIPresentation{
		TitleDE: "Alter Titel", SummaryDE: "Alte Zusammenfassung.",
		TitleEN: "Old title", SummaryEN: "Old summary.", PrivacyStatus: "safe",
	}
	if err := database.CompleteProcessingJob(ctx, staleJob, stale, model, "incident-presentation-v1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	all, allTotal, err := database.ListAdminIncidents(ctx, 50, 0, "fixture", scope, AdminIncidentsAll)
	if err != nil {
		t.Fatal(err)
	}
	if allTotal != 28 || len(all) != 28 {
		t.Fatalf("all incidents = %d/%d, want 28/28", len(all), allTotal)
	}
	var currentFound, staleFound bool
	for _, record := range all {
		switch record.ID {
		case currentJob.IncidentID:
			currentFound = record.HasAI && record.AISummaryEN == current.SummaryEN
		case staleJob.IncidentID:
			staleFound = !record.HasAI && record.AISummaryEN == ""
		}
	}
	if !currentFound || !staleFound {
		t.Fatalf("scoped records current/stale = %t/%t", currentFound, staleFound)
	}

	unprocessed, unprocessedTotal, err := database.ListAdminIncidents(ctx, 50, 0, "fixture", scope, AdminIncidentsUnprocessed)
	if err != nil {
		t.Fatal(err)
	}
	if unprocessedTotal != 27 || len(unprocessed) != 27 {
		t.Fatalf("unprocessed incidents = %d/%d, want 27/27", len(unprocessed), unprocessedTotal)
	}
	for _, record := range unprocessed {
		if record.ID == currentJob.IncidentID || record.HasAI {
			t.Fatalf("unprocessed list contains ready incident %#v", record)
		}
	}

	firstPage, total, err := database.ListAdminIncidents(ctx, 1, 0, "fixture", scope, AdminIncidentsAll)
	if err != nil {
		t.Fatal(err)
	}
	secondPage, _, err := database.ListAdminIncidents(ctx, 1, 1, "fixture", scope, AdminIncidentsAll)
	if err != nil {
		t.Fatal(err)
	}
	if total != 28 || len(firstPage) != 1 || len(secondPage) != 1 || firstPage[0].ID == secondPage[0].ID {
		t.Fatalf("admin pagination = %#v/%#v total %d", firstPage, secondPage, total)
	}
	beyond, total, err := database.ListAdminIncidents(ctx, 10, 28, "fixture", scope, AdminIncidentsAll)
	if err != nil || total != 28 || len(beyond) != 0 {
		t.Fatalf("admin pagination boundary = %d/%d/%v", len(beyond), total, err)
	}
}

func TestListAdminIncidentsValidatesArguments(t *testing.T) {
	database := fixtureStoreForBackup(t, context.Background())
	scope := PresentationScope{Operation: "operation", ModelIdentity: "model", PromptVersion: "prompt"}
	for _, test := range []struct {
		limit  int
		offset int
		mode   string
		filter AdminIncidentFilter
	}{
		{limit: 0, mode: "fixture", filter: AdminIncidentsAll},
		{limit: 1, offset: -1, mode: "fixture", filter: AdminIncidentsAll},
		{limit: 1, mode: "invalid", filter: AdminIncidentsAll},
		{limit: 1, mode: "fixture", filter: "invalid"},
	} {
		if _, _, err := database.ListAdminIncidents(context.Background(), test.limit, test.offset, test.mode, scope, test.filter); err == nil {
			t.Fatalf("ListAdminIncidents(%#v) error = nil", test)
		}
	}
}
