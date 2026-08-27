package web

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"io"
	"testing"

	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/store"
)

func TestAdminIsDisabledByDefault(t *testing.T) {
	handler := testServer(t, fixtureStore(t)).Handler()
	for _, test := range []struct {
		method, path string
	}{
		{method: http.MethodGet, path: "/admin"},
		{method: http.MethodPost, path: "/api/admin/ai/process-all-now"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404", test.method, test.path, response.Code)
		}
	}
}

func TestAdminRendersStatsAndRequestsImmediateProcessing(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	records, _, err := database.ListIncidents(ctx, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	server := adminTestServer(t, database, nil)
	handler := server.Handler()

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if page.Code != http.StatusOK || page.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("admin page = %d/%q", page.Code, page.Header().Get("Cache-Control"))
	}
	for _, expected := range []string{"AI processing", "Registered pipeline steps", "qwen3.5:4b", "Installed and ready", "name=\"model_incident_metadata\"", "name=\"model_german_presentation\"", "name=\"model_translation\"", "/api/admin/ai/process-now", "/api/admin/ai/process-all-now", "/api/admin/ai/reprocess-all", "/api/admin/ai/step-model", "/api/admin/ai/translation-backfill", "/api/admin/ai/status", "Confirm AI request", "/static/admin.js", "Active stage", "Canonical pipeline", "New outside cycle", "Ready after stage", "All-cycle history", "Waiting jobs are durable", "Automatic v2 cutover", "Independent translation queues"} {
		if !strings.Contains(page.Body.String(), expected) {
			t.Errorf("admin page does not contain %q", expected)
		}
	}

	for _, attack := range []struct {
		name, fetchSite, origin string
	}{
		{name: "fetch metadata", fetchSite: "cross-site", origin: ""},
		{name: "origin", fetchSite: "same-site", origin: "https://attacker.example"},
		{name: "opaque origin without same-origin metadata", fetchSite: "same-site", origin: "null"},
	} {
		crossSite := formRequest(http.MethodPost, "/api/admin/ai/process-now", stagedModelForm("confirmed=true&incident_id="+formatID(records[0].ID)))
		crossSite.Header.Set("Sec-Fetch-Site", attack.fetchSite)
		crossSite.Header.Set("Origin", attack.origin)
		crossSiteResponse := httptest.NewRecorder()
		handler.ServeHTTP(crossSiteResponse, crossSite)
		if crossSiteResponse.Code != http.StatusForbidden {
			t.Errorf("%s cross-site processing status = %d, want 403", attack.name, crossSiteResponse.Code)
		}
	}

	opaqueSameOrigin := formRequest(http.MethodPost, "/api/admin/ai/process-now", stagedModelForm("confirmed=true&incident_id="+formatID(records[0].ID)))
	opaqueSameOrigin.Header.Set("Sec-Fetch-Site", "same-origin")
	opaqueSameOrigin.Header.Set("Origin", "null")
	opaqueSameOriginResponse := httptest.NewRecorder()
	handler.ServeHTTP(opaqueSameOriginResponse, opaqueSameOrigin)
	if opaqueSameOriginResponse.Code != http.StatusSeeOther || !strings.Contains(opaqueSameOriginResponse.Header().Get("Location"), "requested=1") {
		t.Fatalf("opaque same-origin processing = %d/%q, want 303 with one requested job", opaqueSameOriginResponse.Code, opaqueSameOriginResponse.Header().Get("Location"))
	}

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, formRequest(http.MethodPost, "/api/admin/ai/process-now", stagedModelForm("confirmed=true&incident_id=zero")))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid process status = %d, want 400", invalid.Code)
	}

	unconfirmed := httptest.NewRecorder()
	handler.ServeHTTP(unconfirmed, formRequest(http.MethodPost, "/api/admin/ai/process-now", stagedModelForm("incident_id="+formatID(records[0].ID))))
	if unconfirmed.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed processing status = %d, want 400", unconfirmed.Code)
	}
	processAll := httptest.NewRecorder()
	handler.ServeHTTP(processAll, formRequest(http.MethodPost, "/api/admin/ai/process-all-now", stagedModelForm("confirmed=true&unprocessed_page=1&all_page=1")))
	if processAll.Code != http.StatusSeeOther || !strings.Contains(processAll.Header().Get("Location"), "requested=28") || !strings.Contains(processAll.Header().Get("Location"), "all_page=1") || !strings.Contains(processAll.Header().Get("Location"), "unprocessed_page=1") {
		t.Fatalf("process all = %d/%q", processAll.Code, processAll.Header().Get("Location"))
	}
	notice := httptest.NewRecorder()
	handler.ServeHTTP(notice, httptest.NewRequest(http.MethodGet, processAll.Header().Get("Location"), nil))
	if !strings.Contains(notice.Body.String(), "Queued priority pipeline cycle") || !strings.Contains(notice.Body.String(), "28 incident(s)") {
		t.Fatalf("processing redirect notice = %q", notice.Body.String())
	}
	reprocess := httptest.NewRecorder()
	handler.ServeHTTP(reprocess, formRequest(http.MethodPost, "/api/admin/ai/reprocess-all", stagedModelForm("confirmed=true&unprocessed_page=1&all_page=1")))
	if reprocess.Code != http.StatusSeeOther || !strings.Contains(reprocess.Header().Get("Location"), "current=28") {
		t.Fatalf("duplicate full reprocessing request = %d/%q", reprocess.Code, reprocess.Header().Get("Location"))
	}
	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/admin/ai/status", nil))
	if status.Code != http.StatusOK || status.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(status.Body.String(), `"manual_cycles"`) || !strings.Contains(status.Body.String(), `"recent_events"`) || !strings.Contains(status.Body.String(), `"active_step_completed"`) || !strings.Contains(status.Body.String(), `"active_steps"`) || !strings.Contains(status.Body.String(), `"waiting"`) || !strings.Contains(status.Body.String(), `"ready_after_stage"`) {
		t.Fatalf("pipeline status = %d/%q/%q", status.Code, status.Header().Get("Cache-Control"), status.Body.String())
	}
	for _, forbidden := range []string{"incident_body", "system_prompt", "title_de", "summary_de", "title_en", "summary_en"} {
		if strings.Contains(status.Body.String(), forbidden) {
			t.Fatalf("pipeline status exposed %q: %s", forbidden, status.Body.String())
		}
	}

	preference := httptest.NewRecorder()
	handler.ServeHTTP(preference, formRequest(http.MethodPost, "/api/admin/ai/step-model", "step=german_presentation&model=granite4%3A3b&unprocessed_page=1&all_page=1"))
	if preference.Code != http.StatusSeeOther || !strings.Contains(preference.Header().Get("Location"), "step_model=german_presentation") {
		t.Fatalf("preferred model update = %d/%q", preference.Code, preference.Header().Get("Location"))
	}
	preferred, err := database.PreferredPipelineModels(ctx, []string{processing.GermanPresentationStep})
	if err != nil || preferred[processing.GermanPresentationStep] != "granite4:3b" {
		t.Fatalf("stored preferred model = %q/%v", preferred, err)
	}
}

func TestAdminProcessingReturnsUnavailableWhenAIIsDisabled(t *testing.T) {
	database := fixtureStore(t)
	server, err := NewWithOptions(database, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review",
		PromptVersion: processing.PipelineVersion, AdminEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, formRequest(http.MethodPost, "/api/admin/ai/process-all-now", "confirmed=true"))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("AI-disabled processing status = %d, want 503", response.Code)
	}
}

func TestAdminRetriesMissingTranslationAndConfirmsHistoricalBackfill(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	records, _, err := database.ListIncidents(ctx, 1, 0)
	if err != nil || len(records) != 1 {
		t.Fatalf("fixture incident = %d/%v", len(records), err)
	}
	plans, err := processing.StepPlans(map[string]string{
		processing.IncidentMetadataStep:   "qwen3.5:4b",
		processing.GermanPresentationStep: "qwen3.5:4b",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 13, 0, 0, 0, time.UTC)
	request, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, "", &records[0].ID, false, now)
	if err != nil {
		t.Fatal(err)
	}
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", nil, false, now)
	if err != nil || !found || cycle.ID != request.CycleID {
		t.Fatalf("activate canonical cycle = %#v/%t/%v", cycle, found, err)
	}
	metadata, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, now)
	if err != nil || !found {
		t.Fatalf("claim metadata = %#v/%t/%v", metadata, found, err)
	}
	if err := database.CompletePipelineJob(ctx, metadata, []store.PipelineValue{{Kind: "category", Value: "other"}, {Kind: "report_kind", Value: "incident"}, {Kind: "public_assistance_status", Value: "not_requested"}, {Kind: "public_assistance_types", Value: "[]"}}, metadata.ModelIdentity, store.HashPipelineInput("metadata"), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	advance, err := database.AdvancePipelineCycle(ctx, cycle, len(plans), now.Add(2*time.Minute))
	if err != nil || !advance.Advanced {
		t.Fatalf("advance metadata = %#v/%v", advance, err)
	}
	cycle.ActiveStep = 1
	german, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 1, now.Add(3*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim German = %#v/%t/%v", german, found, err)
	}
	if err := database.CompletePipelineJob(ctx, german, []store.PipelineValue{{Kind: "title_de", Value: "Kanonischer Titel"}, {Kind: "summary_de", Value: "Kanonische Zusammenfassung."}, {Kind: "privacy_status", Value: "safe"}, {Kind: "privacy_flags", Value: "[]"}}, german.ModelIdentity, store.HashPipelineInput("german"), now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AdvancePipelineCycle(ctx, cycle, len(plans), now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	handler := adminTestServer(t, database, nil).Handler()
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/admin", nil))
	for _, expected := range []string{"Kanonischer Titel", "English state", "Missing", "/api/admin/ai/translation-retry"} {
		if !strings.Contains(page.Body.String(), expected) {
			t.Errorf("admin missing-translation page does not contain %q", expected)
		}
	}
	unconfirmed := httptest.NewRecorder()
	handler.ServeHTTP(unconfirmed, formRequest(http.MethodPost, "/api/admin/ai/translation-retry", "incident_id="+formatID(records[0].ID)+"&language=en&model=qwen3.5%3A4b"))
	if unconfirmed.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed translation retry = %d", unconfirmed.Code)
	}
	retry := httptest.NewRecorder()
	handler.ServeHTTP(retry, formRequest(http.MethodPost, "/api/admin/ai/translation-retry", "confirmed=true&incident_id="+formatID(records[0].ID)+"&language=en&model=qwen3.5%3A4b&unprocessed_page=1&all_page=1"))
	if retry.Code != http.StatusSeeOther || !strings.Contains(retry.Header().Get("Location"), "translations_queued=1") {
		t.Fatalf("translation retry = %d/%q", retry.Code, retry.Header().Get("Location"))
	}
	backfill := httptest.NewRecorder()
	handler.ServeHTTP(backfill, formRequest(http.MethodPost, "/api/admin/ai/translation-backfill", "confirmed=true&language=en&model=qwen3.5%3A4b&unprocessed_page=1&all_page=1"))
	if backfill.Code != http.StatusSeeOther || !strings.Contains(backfill.Header().Get("Location"), "translations_queued=0") {
		t.Fatalf("translation backfill = %d/%q", backfill.Code, backfill.Header().Get("Location"))
	}
}

func TestAdminDistinguishesUnavailableAndMissingPreferredModels(t *testing.T) {
	for _, test := range []struct {
		name, expected string
		status         processing.PipelineModelStatus
	}{
		{name: "unavailable", expected: "Ollama catalog is unavailable", status: processing.PipelineModelStatus{CatalogError: "connection refused"}},
		{name: "missing", expected: "Select an installed model", status: processing.PipelineModelStatus{Models: []string{"granite4:3b"}, CatalogAvailable: true, Steps: defaultTestStepStatus("")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := fixtureStore(t)
			server, err := NewWithOptions(database, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
				PageSize: 20, SourceMode: "fixture", PresentationMode: "review", PromptVersion: processing.PipelineVersion,
				AdminEnabled: true, Processor: fakeProcessingRequester{database: database, status: &test.status},
			})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin", nil))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.expected) {
				t.Fatalf("admin model status = %d/%q", response.Code, response.Body.String())
			}
		})
	}
}

func TestAdminRendersPaginatedIncidentReviewLists(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	job, found, err := database.QueueAndClaimProcessingJob(ctx, legacyOperation("qwen3.5:4b"), time.Now())
	if err != nil || !found {
		t.Fatalf("claim admin presentation job = %t/%v", found, err)
	}
	presentation := store.AIPresentation{
		TitleDE: "Deutscher Admin-Titel", SummaryDE: "Deutsche Admin-Zusammenfassung.",
		TitleEN: "English admin title", SummaryEN: "English admin summary.", PrivacyStatus: "safe",
	}
	if err := database.CompleteProcessingJob(ctx, job, presentation, "qwen3.5:4b", processing.LegacyBilingualPromptVersion, time.Now()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	server, err := NewWithOptions(database, logger, Options{
		PageSize: 2, SourceMode: "fixture", PresentationMode: "public",
		PromptVersion: processing.PipelineVersion, AdminEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("admin review page status = %d", first.Code)
	}
	for _, expected := range []string{
		"Unprocessed incidents", "2 shown / 28 total", "All incidents", "2 shown / 28 total",
		presentation.TitleDE, presentation.SummaryDE, presentation.TitleEN, presentation.SummaryEN,
		job.TitleDE, job.BodyDE, "Original German text", "German presentation ready",
		"all_page=1&amp;unprocessed_page=2", "all_page=2&amp;unprocessed_page=1",
	} {
		if !strings.Contains(first.Body.String(), expected) {
			t.Errorf("admin review page does not contain %q", expected)
		}
	}

	expectedUnprocessed, _, err := database.ListAdminIncidents(ctx, 2, 2, "fixture", store.PresentationScope{
		Operation: legacyOperation("qwen3.5:4b"), ModelIdentity: "qwen3.5:4b", PromptVersion: processing.PipelineVersion,
	}, store.AdminIncidentsUnprocessed)
	if err != nil {
		t.Fatal(err)
	}
	expectedAll, _, err := database.ListAdminIncidents(ctx, 2, 4, "fixture", store.PresentationScope{
		Operation: legacyOperation("qwen3.5:4b"), ModelIdentity: "qwen3.5:4b", PromptVersion: processing.PipelineVersion,
	}, store.AdminIncidentsAll)
	if err != nil {
		t.Fatal(err)
	}
	paginated := httptest.NewRecorder()
	handler.ServeHTTP(paginated, httptest.NewRequest(http.MethodGet, "/admin?unprocessed_page=2&all_page=3", nil))
	if paginated.Code != http.StatusOK {
		t.Fatalf("paginated admin status = %d", paginated.Code)
	}
	for _, expected := range []string{
		expectedUnprocessed[0].TitleDE, expectedAll[0].TitleDE,
		"all_page=3&amp;unprocessed_page=1", "all_page=3&amp;unprocessed_page=3",
		"all_page=2&amp;unprocessed_page=2", "all_page=4&amp;unprocessed_page=2",
	} {
		if !strings.Contains(paginated.Body.String(), expected) {
			t.Errorf("paginated admin page does not contain %q", expected)
		}
	}

	for _, test := range []struct {
		target string
		status int
	}{
		{target: "/admin?unprocessed_page=zero", status: http.StatusBadRequest},
		{target: "/admin?all_page=0", status: http.StatusBadRequest},
		{target: "/admin?all_page=99", status: http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.target, nil))
		if response.Code != test.status {
			t.Errorf("GET %s status = %d, want %d", test.target, response.Code, test.status)
		}
	}
}

func TestAdminReportsStoreErrors(t *testing.T) {
	database := fixtureStore(t)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	retryServer, err := NewWithOptions(database, logger, Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review",
		PromptVersion: processing.PipelineVersion, AdminEnabled: true, Processor: fakeProcessingRequester{err: errors.New("processing unavailable")},
	})
	if err != nil {
		t.Fatal(err)
	}
	retryResponse := httptest.NewRecorder()
	retryServer.Handler().ServeHTTP(retryResponse, formRequest(http.MethodPost, "/api/admin/ai/process-all-now", stagedModelForm("confirmed=true")))
	if retryResponse.Code != http.StatusInternalServerError {
		t.Fatalf("admin process error status = %d, want 500", retryResponse.Code)
	}
}
