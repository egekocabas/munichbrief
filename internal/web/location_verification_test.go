package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/location"
	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/store"
)

func TestLocationCorrectionAcrossReaderSurfacesAndPrivateAdmin(t *testing.T) {
	ctx := context.Background()
	db := fixtureStore(t)
	now := time.Now().UTC()
	presentation := testPresentation{TitleDE: "Synthetischer Testvorfall", SummaryDE: "Unveränderter deutscher Text.", TitleEN: "Synthetic test incident", SummaryEN: "Unchanged English text."}
	canonical := seedV2Presentation(t, db, presentation, now)
	definition := processing.LocationVerificationDefinition()
	plan := store.PostProcessingPlan{ProcessorKey: definition.Key, ScopeKey: "default", PromptVersion: definition.PromptVersion, Model: "fixture-location", InputKinds: definition.InputKinds}
	if n, err := db.QueuePostProcessingForRun(ctx, canonical.PresentationRunID, []store.PostProcessingPlan{plan}, "manual", false, now); err != nil || n != 1 {
		t.Fatalf("queue: %d/%v", n, err)
	}
	job, found, err := db.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract("default", plan.PromptVersion, definition.OutputKinds, definition.InputKinds...), store.PostProcessingClaimOptions{}, now)
	if err != nil || !found {
		t.Fatalf("claim: %t/%v", found, err)
	}
	var source location.Source
	if err := json.Unmarshal([]byte(job.InputValues["location_source"]), &source); err != nil {
		t.Fatal(err)
	}
	// Persist a synthetic validated result, without calling any model provider.
	privateEvidence := "Protected synthetic evidence at Teststraße 123."
	assessment := location.Assessment{Outcome: "corrected", Proposed: &location.Area{ID: "munich:district:maxvorstadt", Name: "Maxvorstadt", Type: "district"}, Source: source, ResolverVersion: location.CatalogVersion, Interpretation: location.Interpretation{Scope: "single", SummaryConflict: true, Reason: "Synthetic location correction", Mentions: []location.Mention{{Name: "Maxvorstadt", Kind: "area", Role: "primary", Source: "body", Evidence: privateEvidence}}}}
	encoded, _ := json.Marshal(assessment)
	proposed, _ := json.Marshal(assessment.Proposed)
	contextHash, _ := json.Marshal(source.ContextHash)
	values := []store.PipelineValue{{Kind: "is_correct", Value: "false"}, {Kind: "location_proposed", Value: string(proposed)}, {Kind: "location_assessment", Value: string(encoded)}, {Kind: "location_context_hash", Value: string(contextHash)}}
	if err := db.CompletePostProcessingJob(ctx, job, values, "fixture-location", job.InputHash, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	processor, _ := processing.DefaultPostProcessorRegistry().Definition(plan.ProcessorKey)
	status := processing.PipelineModelStatus{PostProcessors: []processing.PostProcessorModelStatus{{Key: processor.Key, DisplayName: processor.DisplayName, Verification: processor.Verification, Scopes: []processing.PostProcessorScopeStatus{{Key: "default", DisplayName: "Default"}}}}}
	server, err := NewWithOptions(db, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{PageSize: 20, SourceMode: "fixture", PresentationMode: "public", AdminEnabled: true, Processor: fakeProcessingRequester{database: db, status: &status}})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	for _, language := range []string{"de", "en"} {
		title, summary := presentation.TitleDE, presentation.SummaryDE
		if language == "en" {
			title, summary = presentation.TitleEN, presentation.SummaryEN
		}
		for _, path := range []string{"/" + language + "/incidents/" + formatID(canonical.IncidentID), "/" + language + "/search?area=Maxvorstadt", "/" + language + "/search?q=Maxvorstadt"} {
			for _, accept := range []string{"text/html", "text/markdown"} {
				request := httptest.NewRequest(http.MethodGet, path, nil)
				request.Header.Set("Accept", accept)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK {
					t.Fatalf("%s %s: %d", path, accept, response.Code)
				}
				body := response.Body.String()
				for _, want := range []string{title, summary, "Maxvorstadt"} {
					if !strings.Contains(body, want) {
						t.Errorf("%s %s lacks %q", path, accept, want)
					}
				}
				if strings.Contains(body, privateEvidence) || strings.Contains(body, "Synthetic location correction") {
					t.Error("private assessment leaked")
				}
				if strings.Contains(path, "/incidents/") && response.Header().Get("X-AI-Location-Verification-Model") != "fixture-location" {
					t.Error("missing location provenance")
				}
			}
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/verifications?processor=location_verification&scope=default&status=attention", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("admin status %d", response.Code)
	}
	for _, want := range []string{privateEvidence, "Location assessment: corrected", "Review summary wording", "Maxvorstadt", "No extracted location", location.CatalogVersion, "Wording conflicts"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("admin lacks %q", want)
		}
	}
	if response.Header().Get("Cache-Control") != "private, no-store" {
		t.Error("assessment page is not private")
	}
}
