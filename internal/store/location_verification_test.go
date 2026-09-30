package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/location"
)

func TestLocationPublicationFallbackAndStaleContext(t *testing.T) {
	ctx := context.Background()
	db, err := openTestStore(ctx, filepath.Join(t.TempDir(), "location.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	insertPipelineDocuments(t, ctx, db, now, "location")
	var id int64
	var hash string
	if err := db.db.QueryRow(`SELECT id,content_hash FROM incidents LIMIT 1`).Scan(&id, &hash); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"title_de": "Ein synthetischer Vorfall", "summary_de": "Ein erfundener Vorfall für den Test.", "category": "other", "report_kind": "incident", "public_assistance_status": "not_requested", "public_assistance_types": "[]", "privacy_status": "safe", "privacy_flags": "[]", "area_name": "München", "area_type": "broad_area"}
	run := insertCompletedPresentationRun(t, ctx, db, id, hash, PipelineVersion, now, values)
	inputs := []string{"original_title", "incident_body", "location_original", "location_source", "title_de", "summary_de"}
	outputs := []string{"is_correct", "location_proposed", "location_assessment", "location_context_hash"}
	plan := PostProcessingPlan{ProcessorKey: "location_verification", ScopeKey: "default", PromptVersion: "incident-location-verification-v1", Model: "test", InputKinds: inputs}
	if err := db.EnsurePostProcessingScopes(ctx, []PostProcessingScope{{ProcessorKey: plan.ProcessorKey, ScopeKey: plan.ScopeKey}}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// Existing archive is outside the automatic cutover; manual checks work.
	if n, err := db.QueuePostProcessingForAll(ctx, "fixture", []PostProcessingPlan{plan}, false, now.Add(2*time.Second)); err != nil || n != 0 {
		t.Fatalf("backfilled archive: %d/%v", n, err)
	}
	get := func(want string) {
		t.Helper()
		r, err := db.GetPresentationIncident(ctx, id, PresentationScope{TranslationLanguage: "en"})
		if err != nil || r.AIAreaName != want || !r.HasAI {
			t.Fatalf("public area %q, wanted %q (AI=%t): %v", r.AIAreaName, want, r.HasAI, err)
		}
		var area string
		if err := db.db.QueryRow(`SELECT area FROM reader_documents WHERE incident_id=? AND language='de'`, id).Scan(&area); err != nil || area != want {
			t.Fatalf("reader projection %q: %v", area, err)
		}
	}
	claim := func() PostProcessingJob {
		t.Helper()
		now = now.Add(time.Minute)
		if _, err := db.QueueIncidentPostProcessing(ctx, id, []PostProcessingPlan{plan}, now); err != nil {
			t.Fatal(err)
		}
		job, ok, err := db.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract("default", plan.PromptVersion, outputs, inputs...), PostProcessingClaimOptions{}, now)
		if err != nil || !ok {
			t.Fatalf("claim %t %v", ok, err)
		}
		return job
	}
	result := func(job PostProcessingJob, outcome string) []PipelineValue {
		var src location.Source
		if err := json.Unmarshal([]byte(job.InputValues["location_source"]), &src); err != nil {
			t.Fatal(err)
		}
		a := location.Assessment{Outcome: outcome, Source: src, ResolverVersion: location.CatalogVersion}
		verdict := "unresolved"
		if outcome == "corrected" {
			a.SummaryConflict = true
			a.Proposed = &location.Area{ID: "munich:district:maxvorstadt", Name: "Maxvorstadt", Type: "district"}
			verdict = "false"
		}
		data, _ := json.Marshal(a)
		proposed, _ := json.Marshal(a.Proposed)
		contextHash, _ := json.Marshal(src.ContextHash)
		return []PipelineValue{{Kind: "is_correct", Value: verdict}, {Kind: "location_proposed", Value: string(proposed)}, {Kind: "location_assessment", Value: string(data)}, {Kind: "location_context_hash", Value: string(contextHash)}}
	}
	get("München")
	job := claim()
	get("München")
	if err := db.CompletePostProcessingJob(ctx, job, result(job, "ambiguous"), "test", job.InputHash, now); err != nil {
		t.Fatal(err)
	}
	get("München")
	spec := AdminVerificationSpec{ProcessorKey: plan.ProcessorKey, ScopeKey: "default", VerdictKind: "is_correct", Fields: []AdminVerificationFieldSpec{{OriginalKind: "location_original", CorrectedKind: "location_proposed"}}}
	coverage, err := db.AdminVerificationCoverageFor(ctx, "fixture", spec)
	if err != nil || coverage.Unresolved != 1 || coverage.Attention != 1 || coverage.Verified != 0 {
		t.Fatalf("coverage %#v / %v", coverage, err)
	}
	items, _, err := db.ListAdminVerificationIncidents(ctx, 20, 0, "fixture", spec, AdminVerificationsAttention)
	if err != nil || len(items) != 1 || items[0].LocationAssessment == nil || items[0].Status != "needs_review" {
		t.Fatalf("admin assessment %v", err)
	}
	job = claim()
	if err := db.CompletePostProcessingJob(ctx, job, result(job, "corrected"), "test", job.InputHash, now); err != nil {
		t.Fatal(err)
	}
	get("Maxvorstadt")
	job = claim()
	get("Maxvorstadt")
	coverage, err = db.AdminVerificationCoverageFor(ctx, "fixture", spec)
	if err != nil || coverage.WordingConflicts != 1 {
		t.Fatalf("pending replacement hides wording conflict: %#v / %v", coverage, err)
	}
	if err := db.CompletePostProcessingJob(ctx, job, result(job, "ambiguous"), "test", job.InputHash, now); err != nil {
		t.Fatal(err)
	}
	get("Maxvorstadt")
	items, _, err = db.ListAdminVerificationIncidents(ctx, 20, 0, "fixture", spec, AdminVerificationsAttention)
	if err != nil || len(items) != 1 || !items[0].RetainedSuccess || items[0].Status != "needs_review" {
		t.Fatalf("unresolved replacement lost retained-success status: %v", err)
	}
	var original string
	if err := db.db.QueryRow(`SELECT value FROM presentation_values WHERE presentation_run_id=? AND kind='area_name'`, run).Scan(&original); err != nil || original != "München" {
		t.Fatal("canonical extraction overwritten")
	}
	job = claim()
	if _, err := db.db.Exec(`UPDATE incidents SET context_hash='changed',updated_at=? WHERE id=?`, formatTime(now.Add(time.Second)), id); err != nil {
		t.Fatal(err)
	}
	get("München")
	links, err := db.ListPublicIncidentLinks(ctx, "fixture", PresentationScope{Language: "de"})
	if err != nil || len(links) != 1 || links[0].ModifiedAt.Before(now.Add(time.Second)) {
		t.Fatalf("context invalidation missing from sitemap modification time: %#v / %v", links, err)
	}
	if err := db.CompletePostProcessingJob(ctx, job, result(job, "corrected"), "test", job.InputHash, now); !errors.Is(err, ErrJobNotRunning) {
		t.Fatalf("accepted stale result: %v", err)
	}
	items, _, err = db.ListAdminVerificationIncidents(ctx, 20, 0, "fixture", spec, AdminVerificationsAll)
	if err != nil || len(items) != 1 || !items[0].LocationAssessmentStale {
		t.Fatalf("stale assessment is not identified: %v", err)
	}
	// Omitted optional canonical fields must remain eligible inputs.
	if _, err := db.db.Exec(`DELETE FROM presentation_values WHERE presentation_run_id=? AND kind IN ('area_name','area_type')`, run); err != nil {
		t.Fatal(err)
	}
	job = claim()
	var missing location.Area
	if err := json.Unmarshal([]byte(job.InputValues["location_original"]), &missing); err != nil || missing.Name != "" {
		t.Fatal("missing area not accepted")
	}
}
