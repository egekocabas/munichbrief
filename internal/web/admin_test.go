package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"time"

	"testing"

	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/source"
	"github.com/egekocabas/munichbrief/internal/store"
)

func TestAdminIsDisabledByDefault(t *testing.T) {
	handler := testServer(t, fixtureStore(t)).Handler()
	for _, test := range []struct {
		method, path string
	}{
		{method: http.MethodGet, path: "/admin"},
		{method: http.MethodGet, path: "/admin/translations"},
		{method: http.MethodGet, path: "/admin/history"},
		{method: http.MethodPost, path: "/api/admin/ai/process-all-now"},
		{method: http.MethodPost, path: "/api/admin/ai/translations/process"},
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
	if err := database.EnsurePostProcessingScopes(ctx, processing.DefaultPostProcessorRegistry().StoreScopes(), time.Now()); err != nil {
		t.Fatal(err)
	}
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
	for _, expected := range []string{"AI processing", "Registered pipeline steps", "qwen3.5:4b", "Installed and ready", "name=\"model_incident_metadata\"", "name=\"model_german_presentation\"", "name=\"model_translation\"", "/api/admin/ai/process-now", "/api/admin/ai/process-all-now", "/api/admin/ai/reprocess-all", "/api/admin/ai/step-model", "/api/admin/ai/post-processing/process", "/api/admin/ai/automatic-processing", "/api/admin/ai/cancel-all", "Cancel all unfinished work", "/api/admin/ai/status", "/admin/history", "Pipeline history", "Open reader", "View full history", "Confirm AI request", staticAssets["theme.js"].path, "data-theme-toggle", staticAssets["admin.js"].path, "Active stage", "Canonical pipeline", "New outside cycle", "Ready after stage", "All-cycle history", "Waiting jobs are durable", "Automatic v2 cutover", "Independent post-processing queues", "Post-processing only", "All registered languages", "Translations only", "Category verification only"} {
		if !strings.Contains(page.Body.String(), expected) {
			t.Errorf("admin page does not contain %q", expected)
		}
	}
	queueOrder := []string{
		`data-post-processing-stat="public_assistance_verification/default"`,
		`data-post-processing-stat="category_verification/default"`,
		`data-post-processing-stat="translation/en"`,
	}
	previous := -1
	for _, marker := range queueOrder {
		position := strings.Index(page.Body.String(), marker)
		if position < 0 || position <= previous {
			t.Fatalf("admin post-processing queue order does not follow registry priority: %q", page.Body.String())
		}
		previous = position
	}
	for _, removed := range []string{"Backfill English history", "Backfill category history", "/api/admin/ai/translation-backfill", "/api/admin/ai/category-verification-backfill"} {
		if strings.Contains(page.Body.String(), removed) {
			t.Errorf("admin page still contains removed backfill control %q", removed)
		}
	}
	translationsOnly := httptest.NewRecorder()
	handler.ServeHTTP(translationsOnly, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "confirmed=true&processor=translation&scope=all&target=all&model=qwen3.5%3A4b&unprocessed_page=1&all_page=1"))
	if translationsOnly.Code != http.StatusSeeOther || !strings.Contains(translationsOnly.Header().Get("Location"), "post_processing_queued=0") || !strings.Contains(translationsOnly.Header().Get("Location"), "processor=translation") {
		t.Fatalf("all-language focused processing = %d/%q", translationsOnly.Code, translationsOnly.Header().Get("Location"))
	}
	categoryOnly := httptest.NewRecorder()
	handler.ServeHTTP(categoryOnly, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "confirmed=true&processor=category_verification&scope=default&target=all&model=qwen3.5%3A4b&unprocessed_page=1&all_page=1"))
	if categoryOnly.Code != http.StatusSeeOther || !strings.Contains(categoryOnly.Header().Get("Location"), "post_processing_queued=0") {
		t.Fatalf("all-category focused processing = %d/%q", categoryOnly.Code, categoryOnly.Header().Get("Location"))
	}
	invalidLanguage := httptest.NewRecorder()
	handler.ServeHTTP(invalidLanguage, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "confirmed=true&processor=translation&scope=unknown&target=all&model=qwen3.5%3A4b"))
	if invalidLanguage.Code != http.StatusBadRequest {
		t.Fatalf("unknown focused translation language = %d, want 400", invalidLanguage.Code)
	}
	invalidScope := httptest.NewRecorder()
	handler.ServeHTTP(invalidScope, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "confirmed=true&processor=category_verification&scope=default&target=current&model=qwen3.5%3A4b"))
	if invalidScope.Code != http.StatusBadRequest {
		t.Fatalf("unknown focused category scope = %d, want 400", invalidScope.Code)
	}
	invalidModel := httptest.NewRecorder()
	handler.ServeHTTP(invalidModel, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "confirmed=true&processor=translation&scope=en&target=all&model=missing%3A4b"))
	if invalidModel.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable focused model = %d, want 503", invalidModel.Code)
	}
	unexpectedIncident := httptest.NewRecorder()
	handler.ServeHTTP(unexpectedIncident, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "confirmed=true&processor=translation&scope=en&target=all&incident_id=1&model=qwen3.5%3A4b"))
	if unexpectedIncident.Code != http.StatusBadRequest {
		t.Fatalf("incident ID on all target = %d, want 400", unexpectedIncident.Code)
	}
	unconfirmedFocused := httptest.NewRecorder()
	handler.ServeHTTP(unconfirmedFocused, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "processor=translation&scope=all&target=all&model=qwen3.5%3A4b"))
	if unconfirmedFocused.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed focused translation = %d, want 400", unconfirmedFocused.Code)
	}
	crossSiteFocusedRequest := formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "confirmed=true&processor=category_verification&scope=default&target=all&model=qwen3.5%3A4b")
	crossSiteFocusedRequest.Header.Set("Sec-Fetch-Site", "cross-site")
	crossSiteFocused := httptest.NewRecorder()
	handler.ServeHTTP(crossSiteFocused, crossSiteFocusedRequest)
	if crossSiteFocused.Code != http.StatusForbidden {
		t.Fatalf("cross-site focused category request = %d, want 403", crossSiteFocused.Code)
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
	previous = -1
	for _, processor := range []string{`"processor_key":"public_assistance_verification"`, `"processor_key":"category_verification"`, `"processor_key":"translation"`} {
		position := strings.Index(status.Body.String(), processor)
		if position < 0 || position <= previous {
			t.Fatalf("post-processing status order does not follow registry priority: %q", status.Body.String())
		}
		previous = position
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

func TestAdminControlsAutomaticProcessingAndCancelsAllWork(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	handler := adminTestServer(t, database, nil).Handler()

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, formRequest(http.MethodPost, "/api/admin/ai/automatic-processing", "enabled=maybe"))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid automatic state = %d", invalid.Code)
	}
	crossSite := formRequest(http.MethodPost, "/api/admin/ai/automatic-processing", "enabled=false")
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	crossSiteResponse := httptest.NewRecorder()
	handler.ServeHTTP(crossSiteResponse, crossSite)
	if crossSiteResponse.Code != http.StatusForbidden {
		t.Fatalf("cross-site automatic state = %d", crossSiteResponse.Code)
	}
	disable := httptest.NewRecorder()
	handler.ServeHTTP(disable, formRequest(http.MethodPost, "/api/admin/ai/automatic-processing", "enabled=false&unprocessed_page=1&all_page=1"))
	if disable.Code != http.StatusSeeOther || !strings.Contains(disable.Header().Get("Location"), "automatic_processing=disabled") {
		t.Fatalf("disable automatic processing = %d/%q", disable.Code, disable.Header().Get("Location"))
	}
	state, err := database.AIControl(ctx)
	if err != nil || state.AutomaticProcessingEnabled {
		t.Fatalf("disabled control = %#v/%v", state, err)
	}
	manual := httptest.NewRecorder()
	handler.ServeHTTP(manual, formRequest(http.MethodPost, "/api/admin/ai/process-all-now", stagedModelForm("confirmed=true")))
	if manual.Code != http.StatusSeeOther {
		t.Fatalf("manual processing while automatic disabled = %d/%q", manual.Code, manual.Body.String())
	}
	unconfirmed := httptest.NewRecorder()
	handler.ServeHTTP(unconfirmed, formRequest(http.MethodPost, "/api/admin/ai/cancel-all", "confirmed=false"))
	if unconfirmed.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed cancel all = %d", unconfirmed.Code)
	}
	cancel := httptest.NewRecorder()
	handler.ServeHTTP(cancel, formRequest(http.MethodPost, "/api/admin/ai/cancel-all", "confirmed=true&unprocessed_page=1&all_page=1"))
	if cancel.Code != http.StatusSeeOther || !strings.Contains(cancel.Header().Get("Location"), "canceled_canonical=") || !strings.Contains(cancel.Header().Get("Location"), "canceled_cycles=") {
		t.Fatalf("cancel all = %d/%q", cancel.Code, cancel.Header().Get("Location"))
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, cancel.Header().Get("Location"), nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Automatic AI processing disabled") || !strings.Contains(page.Body.String(), "Automatic processing remains disabled") || !strings.Contains(page.Body.String(), "Processing window open · automatic processing disabled · manual requests remain available") {
		t.Fatalf("cancel notice page = %d/%q", page.Code, page.Body.String())
	}
	manualAfterCancel := httptest.NewRecorder()
	handler.ServeHTTP(manualAfterCancel, formRequest(http.MethodPost, "/api/admin/ai/process-all-now", stagedModelForm("confirmed=true")))
	if manualAfterCancel.Code != http.StatusSeeOther {
		t.Fatalf("manual processing after cancel all = %d/%q", manualAfterCancel.Code, manualAfterCancel.Body.String())
	}
}

func TestAdminUsesInjectedPostProcessorMetadataWithoutHandlerBranches(t *testing.T) {
	database := fixtureStore(t)
	status := processing.PipelineModelStatus{
		Steps:            defaultTestStepStatus("qwen3.5:4b"),
		Models:           []string{"qwen3.5:4b"},
		CatalogAvailable: true,
		Ready:            true,
		PostProcessors: []processing.PostProcessorModelStatus{{
			Key: "quality_note", DisplayName: "Quality note", Description: "Run an independent quality note.",
			ModelSettingKey: "quality_note", Manual: true, Preferred: "qwen3.5:4b", PreferredAvailable: true,
			Scopes: []processing.PostProcessorScopeStatus{{Key: "brief", DisplayName: "Brief"}, {Key: "detailed", DisplayName: "Detailed"}},
		}},
	}
	var received processing.PostProcessingRequest
	requestCount := 0
	requester := fakeProcessingRequester{database: database, status: &status, postRequest: func(_ context.Context, request processing.PostProcessingRequest) (int, error) {
		requestCount++
		received = request
		return 2, nil
	}}
	server, err := NewWithOptions(database, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review", AdminEnabled: true, Processor: requester,
	})
	if err != nil {
		t.Fatal(err)
	}

	page := httptest.NewRecorder()
	server.Handler().ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/admin", nil))
	for _, expected := range []string{"Quality note only", "Run an independent quality note.", `name="processor" type="hidden" value="quality_note"`, `value="brief"`, `value="detailed"`} {
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), expected) {
			t.Fatalf("injected post-processor card = %d, missing %q", page.Code, expected)
		}
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "confirmed=true&processor=quality_note&scope=all&target=all&model=qwen3.5%3A4b"))
	if response.Code != http.StatusSeeOther || requestCount != 1 || received.ProcessorKey != "quality_note" || received.Model != "qwen3.5:4b" || received.IncidentID != nil || len(received.ScopeKeys) != 0 {
		t.Fatalf("injected post-processor request = %d/%#v", response.Code, received)
	}

	invalidReturn := httptest.NewRecorder()
	server.Handler().ServeHTTP(invalidReturn, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "confirmed=true&processor=quality_note&scope=all&target=all&model=qwen3.5%3A4b&return_to=verifications&return_status=attention&return_page=1"))
	if invalidReturn.Code != http.StatusBadRequest || requestCount != 1 {
		t.Fatalf("non-verification return context = %d with %d requests, want 400 with no additional request", invalidReturn.Code, requestCount)
	}
}

func TestAdminProcessingReturnsUnavailableWhenAIIsDisabled(t *testing.T) {
	database := fixtureStore(t)
	server, err := NewWithOptions(database, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review",
		AdminEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Application-level AI processing is disabled; automatic and manual requests are unavailable") || !strings.Contains(page.Body.String(), "AI processing unavailable") || strings.Contains(page.Body.String(), "data-pipeline-status") {
		t.Fatalf("AI-disabled admin page = %d/%q", page.Code, page.Body.String())
	}
	for _, test := range []struct {
		method, path, body string
	}{
		{method: http.MethodPost, path: "/api/admin/ai/process-all-now", body: "confirmed=true"},
		{method: http.MethodPost, path: "/api/admin/ai/automatic-processing", body: "enabled=false"},
		{method: http.MethodPost, path: "/api/admin/ai/cancel-all", body: "confirmed=true"},
		{method: http.MethodPost, path: "/api/admin/ai/post-processing/process", body: "confirmed=true&processor=translation&scope=en&target=all&model=qwen3.5%3A4b"},
		{method: http.MethodPost, path: "/api/admin/ai/translations/process", body: "confirmed=true&language=en&action=all&model=qwen3.5%3A4b"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, formRequest(test.method, test.path, test.body))
		if response.Code != http.StatusServiceUnavailable {
			t.Errorf("AI-disabled %s status = %d, want 503", test.path, response.Code)
		}
	}
	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/admin/ai/status", nil))
	if status.Code != http.StatusServiceUnavailable {
		t.Fatalf("AI-disabled status endpoint = %d, want 503", status.Code)
	}
}

func TestAdminRetriesPostProcessingAndRejectsRemovedBackfillRoutes(t *testing.T) {
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
	request, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, nil, &records[0].ID, false, now)
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
	for _, expected := range []string{"Kanonischer Titel", "0 / 1 published", "Manage translations", "/admin/translations?incident=", "Category verification", "Not checked"} {
		if !strings.Contains(page.Body.String(), expected) {
			t.Errorf("admin missing-translation page does not contain %q", expected)
		}
	}
	unconfirmed := httptest.NewRecorder()
	handler.ServeHTTP(unconfirmed, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "processor=translation&scope=en&target=incident&incident_id="+formatID(records[0].ID)+"&model=qwen3.5%3A4b"))
	if unconfirmed.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed translation retry = %d", unconfirmed.Code)
	}
	retry := httptest.NewRecorder()
	handler.ServeHTTP(retry, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "confirmed=true&processor=translation&scope=en&target=incident&incident_id="+formatID(records[0].ID)+"&model=qwen3.5%3A4b&unprocessed_page=1&all_page=1"))
	if retry.Code != http.StatusSeeOther || !strings.Contains(retry.Header().Get("Location"), "post_processing_queued=1") {
		t.Fatalf("translation retry = %d/%q", retry.Code, retry.Header().Get("Location"))
	}
	backfill := httptest.NewRecorder()
	handler.ServeHTTP(backfill, formRequest(http.MethodPost, "/api/admin/ai/translation-backfill", "confirmed=true&language=en&model=qwen3.5%3A4b&unprocessed_page=1&all_page=1"))
	if backfill.Code != http.StatusMethodNotAllowed {
		t.Fatalf("removed translation backfill route = %d, want 405", backfill.Code)
	}
	categoryUnconfirmed := httptest.NewRecorder()
	handler.ServeHTTP(categoryUnconfirmed, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "processor=category_verification&scope=default&target=incident&incident_id="+formatID(records[0].ID)+"&model=qwen3.5%3A4b"))
	if categoryUnconfirmed.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed category retry = %d", categoryUnconfirmed.Code)
	}
	categoryRetry := httptest.NewRecorder()
	handler.ServeHTTP(categoryRetry, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "confirmed=true&processor=category_verification&scope=default&target=incident&incident_id="+formatID(records[0].ID)+"&model=qwen3.5%3A4b&unprocessed_page=1&all_page=1"))
	if categoryRetry.Code != http.StatusSeeOther || !strings.Contains(categoryRetry.Header().Get("Location"), "post_processing_queued=1") {
		t.Fatalf("category retry = %d/%q", categoryRetry.Code, categoryRetry.Header().Get("Location"))
	}
	categoryBackfill := httptest.NewRecorder()
	handler.ServeHTTP(categoryBackfill, formRequest(http.MethodPost, "/api/admin/ai/category-verification-backfill", "confirmed=true&model=qwen3.5%3A4b&unprocessed_page=1&all_page=1"))
	if categoryBackfill.Code != http.StatusMethodNotAllowed {
		t.Fatalf("removed category backfill route = %d, want 405", categoryBackfill.Code)
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
				PageSize: 20, SourceMode: "fixture", PresentationMode: "review",
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
	presentation := testPresentation{
		TitleDE: "Deutscher Admin-Titel", SummaryDE: "Deutsche Admin-Zusammenfassung.",
		TitleEN: "English admin title", SummaryEN: "English admin summary.",
	}
	job := seedV2Presentation(t, database, presentation, time.Now())
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	server, err := NewWithOptions(database, logger, Options{
		PageSize: 2, SourceMode: "fixture", PresentationMode: "public",
		AdminEnabled: true,
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
	seenIDs := make(map[string]struct{})
	for _, match := range regexp.MustCompile(`\sid="([^"]+)"`).FindAllStringSubmatch(first.Body.String(), -1) {
		if _, duplicate := seenIDs[match[1]]; duplicate {
			t.Errorf("admin review page contains duplicate id %q", match[1])
		}
		seenIDs[match[1]] = struct{}{}
	}
	for _, expected := range []string{
		"Unprocessed incidents", "2 shown / 28 total", "All incidents", "2 shown / 28 total",
		presentation.TitleDE, presentation.SummaryDE, "1 / 1 published", "Manage translations",
		job.TitleDE, job.BodyDE, "Original German text", "German presentation ready",
		"all_page=1&amp;unprocessed_page=2", "all_page=2&amp;unprocessed_page=1",
	} {
		if !strings.Contains(first.Body.String(), expected) {
			t.Errorf("admin review page does not contain %q", expected)
		}
	}

	expectedUnprocessed, _, err := database.ListAdminIncidents(ctx, 2, 2, "fixture", store.PresentationScope{}, store.AdminIncidentsUnprocessed)
	if err != nil {
		t.Fatal(err)
	}
	expectedAll, _, err := database.ListAdminIncidents(ctx, 2, 4, "fixture", store.PresentationScope{}, store.AdminIncidentsAll)
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

func TestAdminTranslationOperationsOverviewDrilldownsAndActions(t *testing.T) {
	database := fixtureStore(t)
	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	presentation := testPresentation{
		TitleDE: "Übersetzungsübersicht", SummaryDE: "Deutsche Zusammenfassung.",
		TitleEN: "Translation overview", SummaryEN: "English summary.",
	}
	job := seedV2Presentation(t, database, presentation, now)
	translationPlan := store.PostProcessingPlan{ProcessorKey: processing.TranslationModelStep, ScopeKey: "en", PromptVersion: processing.EnglishTranslationPromptVersion, Model: "qwen3.5:4b", InputKinds: []string{"title_de", "summary_de"}}
	if queued, err := database.QueueIncidentPostProcessing(context.Background(), job.IncidentID, []store.PostProcessingPlan{translationPlan}, now.Add(time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue replacement translation = %d/%v", queued, err)
	}
	replacement, found, err := database.ClaimPostProcessingJob(context.Background(), processing.TranslationModelStep, testPostProcessingContract("en", translationPlan.PromptVersion, []string{"title", "summary"}, translationPlan.InputKinds...), false, nil, now.Add(2*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim replacement translation = %#v/%t/%v", replacement, found, err)
	}
	if err := database.FailPostProcessingJob(context.Background(), replacement, "failed", "provider", nil, now.Add(3*time.Minute), errors.New("private test failure")); err != nil {
		t.Fatal(err)
	}

	var received []processing.PostProcessingRequest
	requester := fakeProcessingRequester{database: database, postRequest: func(_ context.Context, request processing.PostProcessingRequest) (int, error) {
		received = append(received, request)
		return 3, nil
	}}
	server, err := NewWithOptions(database, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		PageSize: 1, SourceMode: "fixture", PresentationMode: "review", AdminEnabled: true, Processor: requester,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	overview := httptest.NewRecorder()
	handler.ServeHTTP(overview, httptest.NewRequest(http.MethodGet, "/admin/translations", nil))
	for _, expected := range []string{
		"Translation operations", "Current source incidents", "Published German", "German canonical backlog",
		"English", "1 / 1", "100%", "Manage", "Attention can overlap published", "1 retained replacement warning", "data-translation-operations",
		"data-translation-poll-interval=\"5000\"", "/admin/history", "aria-current=\"page\"",
	} {
		if overview.Code != http.StatusOK || !strings.Contains(overview.Body.String(), expected) {
			t.Errorf("translation overview = %d, missing %q", overview.Code, expected)
		}
	}
	if overview.Header().Get("Cache-Control") != "private, no-store" || overview.Header().Get("X-Robots-Tag") != "noindex, nofollow, noarchive" {
		t.Fatalf("translation overview headers = %q/%q", overview.Header().Get("Cache-Control"), overview.Header().Get("X-Robots-Tag"))
	}
	dashboard := httptest.NewRecorder()
	handler.ServeHTTP(dashboard, httptest.NewRequest(http.MethodGet, "/admin", nil))
	for _, expected := range []string{"1 / 1 published", "1 attention", "1 retained replacement warning", "Manage translations"} {
		if dashboard.Code != http.StatusOK || !strings.Contains(dashboard.Body.String(), expected) {
			t.Errorf("compact translation rollup = %d, missing %q", dashboard.Code, expected)
		}
	}
	if strings.Contains(dashboard.Body.String(), presentation.TitleEN) || strings.Contains(dashboard.Body.String(), presentation.SummaryEN) {
		t.Error("compact dashboard still renders full translated output")
	}

	language := httptest.NewRecorder()
	handler.ServeHTTP(language, httptest.NewRequest(http.MethodGet, "/admin/translations?language=en&status=all", nil))
	for _, expected := range []string{presentation.TitleDE, presentation.TitleEN, presentation.SummaryEN, "Published", "Latest attempt", "Failed", "Published provenance", "Attempt provenance", "Queue unpublished", "Rerun all", "All languages"} {
		if language.Code != http.StatusOK || !strings.Contains(language.Body.String(), expected) {
			t.Errorf("translation language view = %d, missing %q", language.Code, expected)
		}
	}

	incident := httptest.NewRecorder()
	handler.ServeHTTP(incident, httptest.NewRequest(http.MethodGet, "/admin/translations?incident="+formatID(job.IncidentID), nil))
	for _, expected := range []string{presentation.TitleDE, presentation.SummaryDE, presentation.TitleEN, presentation.SummaryEN, "German provenance", "Published provenance", processing.EnglishTranslationPromptVersion, "Translate now"} {
		if incident.Code != http.StatusOK || !strings.Contains(incident.Body.String(), expected) {
			t.Errorf("translation incident view = %d, missing %q", incident.Code, expected)
		}
	}

	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/admin/translations?language=de", want: http.StatusBadRequest},
		{path: "/admin/translations?language=unknown", want: http.StatusBadRequest},
		{path: "/admin/translations?language=en&status=unknown", want: http.StatusBadRequest},
		{path: "/admin/translations?language=en&page=0", want: http.StatusBadRequest},
		{path: "/admin/translations?status=attention", want: http.StatusBadRequest},
		{path: "/admin/translations?incident=zero", want: http.StatusBadRequest},
		{path: "/admin/translations?incident=999999", want: http.StatusNotFound},
		{path: "/admin/translations?incident=" + formatID(job.IncidentID) + "&language=en", want: http.StatusBadRequest},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != test.want {
			t.Errorf("GET %s = %d, want %d", test.path, response.Code, test.want)
		}
	}

	actions := []struct {
		body      string
		selection processing.PostProcessingSelection
		incident  bool
	}{
		{body: "confirmed=true&language=en&model=qwen3.5%3A4b&action=unpublished&return_status=unpublished", selection: processing.PostProcessingSelectionUnpublished},
		{body: "confirmed=true&language=en&model=qwen3.5%3A4b&action=all&return_status=all", selection: processing.PostProcessingSelectionAll},
		{body: "confirmed=true&language=en&model=qwen3.5%3A4b&action=incident&incident_id=" + formatID(job.IncidentID) + "&return_incident=" + formatID(job.IncidentID), selection: processing.PostProcessingSelectionAll, incident: true},
	}
	for index, test := range actions {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, formRequest(http.MethodPost, "/api/admin/ai/translations/process", test.body))
		if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "queued=3") {
			t.Fatalf("translation action %d = %d/%q", index, response.Code, response.Header().Get("Location"))
		}
		request := received[index]
		if request.ProcessorKey != processing.TranslationModelStep || len(request.ScopeKeys) != 1 || request.ScopeKeys[0] != "en" || request.Selection != test.selection || (request.IncidentID != nil) != test.incident {
			t.Errorf("translation action %d request = %#v", index, request)
		}
	}

	notice := httptest.NewRecorder()
	handler.ServeHTTP(notice, httptest.NewRequest(http.MethodGet, "/admin/translations?language=en&status=all&queued=3&queued_action=all&queued_language=en", nil))
	if notice.Code != http.StatusOK || !strings.Contains(notice.Body.String(), "Queued 3 translation rerun job(s) for en") {
		t.Fatalf("translation notice = %d/%q", notice.Code, notice.Body.String())
	}

	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{name: "confirmation", body: "language=en&model=qwen3.5%3A4b&action=all", want: http.StatusBadRequest},
		{name: "canonical language", body: "confirmed=true&language=de&model=qwen3.5%3A4b&action=all", want: http.StatusBadRequest},
		{name: "unknown action", body: "confirmed=true&language=en&model=qwen3.5%3A4b&action=unknown", want: http.StatusBadRequest},
		{name: "missing incident", body: "confirmed=true&language=en&model=qwen3.5%3A4b&action=incident&incident_id=zero", want: http.StatusBadRequest},
		{name: "incident on all", body: "confirmed=true&language=en&model=qwen3.5%3A4b&action=all&incident_id=1", want: http.StatusBadRequest},
		{name: "model", body: "confirmed=true&language=en&model=missing%3A4b&action=all", want: http.StatusServiceUnavailable},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, formRequest(http.MethodPost, "/api/admin/ai/translations/process", test.body))
		if response.Code != test.want {
			t.Errorf("%s translation mutation = %d, want %d", test.name, response.Code, test.want)
		}
	}
	crossSiteRequest := formRequest(http.MethodPost, "/api/admin/ai/translations/process", "confirmed=true&language=en&model=qwen3.5%3A4b&action=all")
	crossSiteRequest.Header.Set("Sec-Fetch-Site", "cross-site")
	crossSite := httptest.NewRecorder()
	handler.ServeHTTP(crossSite, crossSiteRequest)
	if crossSite.Code != http.StatusForbidden {
		t.Errorf("cross-site translation mutation = %d, want 403", crossSite.Code)
	}
	nonFormRequest := httptest.NewRequest(http.MethodPost, "/api/admin/ai/translations/process", strings.NewReader("confirmed=true"))
	nonFormRequest.Header.Set("Content-Type", "text/plain")
	nonForm := httptest.NewRecorder()
	handler.ServeHTTP(nonForm, nonFormRequest)
	if nonForm.Code != http.StatusUnsupportedMediaType {
		t.Errorf("non-form translation mutation = %d, want 415", nonForm.Code)
	}
	oversized := httptest.NewRecorder()
	handler.ServeHTTP(oversized, formRequest(http.MethodPost, "/api/admin/ai/translations/process", "confirmed=true&"+strings.Repeat("padding=x&", 600)))
	if oversized.Code != http.StatusBadRequest {
		t.Errorf("oversized translation mutation = %d, want 400", oversized.Code)
	}
}

func TestAdminTranslationOperationsTemplateScalesToTenLanguages(t *testing.T) {
	server := adminTestServer(t, fixtureStore(t), nil)
	data := adminTranslationsPage{UpdatedAt: time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)}
	for index := 1; index <= 10; index++ {
		code := fmt.Sprintf("x-%d", index)
		data.Languages = append(data.Languages, adminLanguageCoverageView{
			AdminLanguageCoverage: store.AdminLanguageCoverage{Language: code, Eligible: 100, Published: index * 9, Unpublished: 100 - index*9},
			DisplayName:           fmt.Sprintf("Language %d", index),
			CoveragePercent:       index * 9,
			ManageURL:             adminTranslationsLanguageURL(code, store.AdminTranslationsUnpublished, 1),
		})
	}
	var output bytes.Buffer
	if err := server.adminTranslationsTemplate.ExecuteTemplate(&output, "admin_translations", data); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 10; index++ {
		if !strings.Contains(output.String(), fmt.Sprintf("Language %d", index)) {
			t.Errorf("ten-language template missing language %d", index)
		}
	}
	if count := strings.Count(output.String(), ">Manage →</a>"); count != 10 {
		t.Errorf("ten-language template manage links = %d, want 10", count)
	}
}

func TestAdminTranslationAutoRefreshContract(t *testing.T) {
	script := string(adminScript)
	for _, expected := range []string{
		`[data-translation-operations]`, `window.location.href`, `document.hidden`,
		`confirmation.open`, `region.contains(focused)`, `requestInFlight`,
		`Math.min(30000`, `setConnection("Stale")`, `visibilitychange`, `window.scrollTo(scrollX, scrollY)`,
	} {
		if !strings.Contains(script, expected) {
			t.Errorf("translation auto-refresh script missing %q", expected)
		}
	}
}

func TestAdminProcessingControlsRespectStrictStylePolicy(t *testing.T) {
	script := string(adminScript)
	for _, expected := range []string{
		`new URL(form.getAttribute("action") || window.location.href, window.location.href)`,
		`activeProgress.value =`, `progress.value =`,
	} {
		if !strings.Contains(script, expected) {
			t.Errorf("admin script missing CSP-safe control %q", expected)
		}
	}
	if strings.Contains(script, `.style.width`) {
		t.Error("admin script writes inline progress styles")
	}

	server := adminTestServer(t, fixtureStore(t), nil)
	for _, test := range []struct {
		target           string
		wantProgress     bool
		wantConfirmation bool
	}{
		{target: "/admin", wantProgress: true},
		{target: "/admin/translations", wantProgress: true},
		{target: "/admin/verifications?processor=category_verification&scope=default&status=attention&page=1", wantConfirmation: true},
	} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.target, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", test.target, response.Code)
		}
		if strings.Contains(response.Body.String(), `style=`) {
			t.Errorf("GET %s renders an inline style", test.target)
		}
		if test.wantProgress && !strings.Contains(response.Body.String(), `<progress class="admin-progress`) {
			t.Errorf("GET %s does not render a CSP-safe progress element", test.target)
		}
		if test.wantConfirmation {
			for _, expected := range []string{`data-confirm-processing`, `action="/api/admin/ai/post-processing/process"`} {
				if !strings.Contains(response.Body.String(), expected) {
					t.Errorf("GET %s does not contain relative confirmation action %q", test.target, expected)
				}
			}
		}
	}
}

func TestAdminTranslationActionsRequireManualProcessorAndExplicitFallbackModel(t *testing.T) {
	processor := &processing.PostProcessorModelStatus{Key: processing.TranslationModelStep, Manual: true}
	data := adminTranslationsPage{
		ProcessingEnabled: true,
		Models: processing.PipelineModelStatus{
			CatalogAvailable: true,
			Models:           []string{"fallback:4b"},
		},
		TranslationModel:  processor,
		TranslationModels: []adminTranslationModelOption{{Value: "fallback:4b"}},
	}
	data.TranslationActionsAvailable = data.translationActionsAvailable()
	if !data.TranslationActionsAvailable {
		t.Fatal("manual translation processor with an installed model should enable actions")
	}
	processor.Manual = false
	if data.translationActionsAvailable() {
		t.Fatal("non-manual translation processor enabled actions")
	}
	if label := adminTranslationAttemptLabel("", "", 0); label != "Never queued" {
		t.Fatalf("missing attempt label = %q", label)
	}

	processor.Manual = true
	data.SelectedLanguage = &adminTranslationLanguagePage{Language: readerLanguage{Code: "en", DisplayName: "English"}, Filter: store.AdminTranslationsUnpublished, Page: 1, TotalPages: 1}
	server := adminTestServer(t, fixtureStore(t), nil)
	var output bytes.Buffer
	if err := server.adminTranslationsTemplate.ExecuteTemplate(&output, "admin_translations", data); err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(output.String(), "Choose installed model"); count != 2 {
		t.Fatalf("fallback model prompts = %d, want two bulk-action prompts", count)
	}
}

func TestAdminPipelineHistoryCombinesCanonicalAndPostProcessingJobs(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	records, _, err := database.ListIncidents(ctx, 1, 0)
	if err != nil || len(records) != 1 {
		t.Fatalf("history fixture incident = %d/%v", len(records), err)
	}
	plans, err := processing.StepPlans(map[string]string{
		processing.IncidentMetadataStep:   "qwen3.5:4b",
		processing.GermanPresentationStep: "granite4:3b",
	})
	if err != nil {
		t.Fatal(err)
	}
	requestedAt := time.Date(2026, 8, 27, 14, 0, 0, 0, time.UTC)
	result, err := database.CreateManualPipelineCycle(ctx, "fixture", plans, nil, &records[0].ID, false, requestedAt)
	if err != nil || result.Requested != 1 {
		t.Fatalf("create history fixture = %#v/%v", result, err)
	}
	cycle, found, err := database.ActivateNextPipelineCycle(ctx, "fixture", nil, false, requestedAt)
	if err != nil || !found {
		t.Fatalf("activate history cycle = %#v/%t/%v", cycle, found, err)
	}
	metadata, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 0, requestedAt)
	if err != nil || !found {
		t.Fatalf("claim history metadata = %#v/%t/%v", metadata, found, err)
	}
	metadataValues := []store.PipelineValue{{Kind: "category", Value: "other"}, {Kind: "report_kind", Value: "incident"}, {Kind: "public_assistance_status", Value: "not_requested"}, {Kind: "public_assistance_types", Value: "[]"}}
	if err := database.CompletePipelineJob(ctx, metadata, metadataValues, metadata.ModelIdentity, store.HashPipelineInput(metadata.SourceHash), requestedAt); err != nil {
		t.Fatal(err)
	}
	if advanced, err := database.AdvancePipelineCycle(ctx, cycle, len(plans), requestedAt); err != nil || !advanced.Advanced {
		t.Fatalf("advance history metadata = %#v/%v", advanced, err)
	}
	cycle.ActiveStep = 1
	german, found, err := database.ClaimPipelineJob(ctx, cycle.ID, 1, requestedAt)
	if err != nil || !found {
		t.Fatalf("claim history German = %#v/%t/%v", german, found, err)
	}
	germanValues := []store.PipelineValue{{Kind: "title_de", Value: "Titel"}, {Kind: "summary_de", Value: "Zusammenfassung."}, {Kind: "privacy_status", Value: "safe"}, {Kind: "privacy_flags", Value: "[]"}}
	if err := database.CompletePipelineJob(ctx, german, germanValues, german.ModelIdentity, store.HashPipelineInput(german.SourceHash), requestedAt); err != nil {
		t.Fatal(err)
	}
	if completed, err := database.AdvancePipelineCycle(ctx, cycle, len(plans), requestedAt); err != nil || !completed.Completed {
		t.Fatalf("complete history cycle = %#v/%v", completed, err)
	}
	translationPlan := store.PostProcessingPlan{ProcessorKey: "translation", ScopeKey: "en", PromptVersion: "incident-translation-en-v1", Model: "translategemma:4b", InputKinds: []string{"title_de", "summary_de"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, german.PresentationRunID, []store.PostProcessingPlan{translationPlan}, "manual", false, requestedAt); err != nil || queued != 1 {
		t.Fatalf("queue history translation = %d/%v", queued, err)
	}
	translation, found, err := database.ClaimPostProcessingJob(ctx, "translation", testPostProcessingContract("en", translationPlan.PromptVersion, []string{"title", "summary"}, "title_de", "summary_de"), false, nil, requestedAt)
	if err != nil || !found {
		t.Fatalf("claim history translation = %#v/%t/%v", translation, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, translation, []store.PipelineValue{{Kind: "title", Value: "Title"}, {Kind: "summary", Value: "Summary."}}, translation.ModelIdentity, translation.InputHash, requestedAt); err != nil {
		t.Fatal(err)
	}
	verificationPlan := store.PostProcessingPlan{ProcessorKey: "category_verification", ScopeKey: "default", PromptVersion: processing.CategoryVerificationPromptVersion, Model: "qwen3.5:4b", InputKinds: []string{"title_de", "summary_de", "category"}}
	if queued, err := database.QueuePostProcessingForRun(ctx, german.PresentationRunID, []store.PostProcessingPlan{verificationPlan}, "manual", false, requestedAt); err != nil || queued != 1 {
		t.Fatalf("queue history category verification = %d/%v", queued, err)
	}
	verification, found, err := database.ClaimPostProcessingJob(ctx, "category_verification", testPostProcessingContract("default", verificationPlan.PromptVersion, []string{"is_correct", "corrected_category"}, "title_de", "summary_de", "category"), false, nil, requestedAt)
	if err != nil || !found {
		t.Fatalf("claim history category verification = %#v/%t/%v", verification, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, verification, []store.PipelineValue{{Kind: "is_correct", Value: "true"}, {Kind: "corrected_category", Value: "other"}}, verification.ModelIdentity, verification.InputHash, requestedAt); err != nil {
		t.Fatal(err)
	}
	server, err := NewWithOptions(database, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		PageSize: 2, SourceMode: "fixture", PresentationMode: "review",
		AdminEnabled: true,
		Processor:    fakeProcessingRequester{database: database},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/admin/history", nil))
	if first.Code != http.StatusOK || first.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("pipeline history = %d/%q", first.Code, first.Header().Get("Cache-Control"))
	}
	for _, expected := range []string{"Pipeline history", "Open reader", `aria-current="page"`, "2 job states shown", "category_verification/default", "post-processing · manual", "qwen3.5:4b", "translation/en", "translategemma:4b", "Older →", `data-pipeline-history`, `data-history-poll-interval="2000"`, staticAssets["admin.js"].path} {
		if !strings.Contains(first.Body.String(), expected) {
			t.Errorf("pipeline history does not contain %q", expected)
		}
	}
	if strings.Contains(first.Body.String(), "Back to dashboard") {
		t.Error("pipeline history retained redundant dashboard button")
	}
	dashboard := httptest.NewRecorder()
	handler.ServeHTTP(dashboard, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if dashboard.Code != http.StatusOK || !strings.Contains(dashboard.Body.String(), "category_verification/default") || !strings.Contains(dashboard.Body.String(), "translation/en") || !strings.Contains(dashboard.Body.String(), "data-post-processing-elapsed") || !strings.Contains(dashboard.Body.String(), "data-post-processing-retry-state") || !strings.Contains(dashboard.Body.String(), "data-post-processing-queue-age") || !strings.Contains(dashboard.Body.String(), "Cycle #"+formatID(result.CycleID)) {
		t.Fatalf("admin dashboard post-processing history = %d/%q", dashboard.Code, dashboard.Body.String())
	}
	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/admin/ai/status", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"kind":"post_processing"`) || !strings.Contains(status.Body.String(), `"processor_key":"category_verification"`) || !strings.Contains(status.Body.String(), `"execution_key":"category_verification/default"`) || !strings.Contains(status.Body.String(), `"execution_key":"translation/en"`) {
		t.Fatalf("live admin post-processing history = %d/%q", status.Code, status.Body.String())
	}
	match := regexp.MustCompile(`href="(/admin/history\?before=[^"]+)"`).FindStringSubmatch(first.Body.String())
	if len(match) != 2 {
		t.Fatalf("pipeline history has no older cursor: %s", first.Body.String())
	}

	older := httptest.NewRecorder()
	handler.ServeHTTP(older, httptest.NewRequest(http.MethodGet, match[1], nil))
	if older.Code != http.StatusOK || !strings.Contains(older.Body.String(), "← Newer") || !strings.Contains(older.Body.String(), "german_presentation") {
		t.Fatalf("older pipeline history = %d/%q", older.Code, older.Body.String())
	}

	for _, target := range []string{
		"/admin/history?before=not-a-cursor",
		match[1] + "&after=not-a-cursor",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("GET %s status = %d, want 400", target, response.Code)
		}
	}
}

func TestAdminShowsPublicAssistanceVerificationControlsAndSafeHistory(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	now := time.Date(2026, time.August, 29, 16, 0, 0, 0, time.UTC)
	job := seedV2Presentation(t, database, testPresentation{
		TitleDE: "Deutscher Titel", SummaryDE: "Deutsche Zusammenfassung.",
		TitleEN: "English title", SummaryEN: "English summary.",
	}, now)
	if err := database.EnsurePostProcessingScopes(ctx, []store.PostProcessingScope{{ProcessorKey: processing.PublicAssistanceVerificationStep, ScopeKey: "default"}}, now); err != nil {
		t.Fatal(err)
	}
	plan := store.PostProcessingPlan{
		ProcessorKey: processing.PublicAssistanceVerificationStep, ScopeKey: "default", PromptVersion: processing.PublicAssistanceVerificationPromptVersion,
		Model: "qwen3.5:4b", InputKinds: []string{"original_title", "incident_body", "public_assistance_status", "public_assistance_types"},
	}
	if queued, err := database.QueuePostProcessingForRun(ctx, job.PresentationRunID, []store.PostProcessingPlan{plan}, "manual", false, now.Add(time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue assistance verification = %d/%v", queued, err)
	}
	verification, found, err := database.ClaimPostProcessingJob(ctx, processing.PublicAssistanceVerificationStep, testPostProcessingContract("default", plan.PromptVersion, []string{"is_correct", "corrected_public_assistance_status", "corrected_public_assistance_types"}, plan.InputKinds...), false, nil, now.Add(2*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim assistance verification = %#v/%t/%v", verification, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, verification, []store.PipelineValue{
		{Kind: "is_correct", Value: "false"},
		{Kind: "corrected_public_assistance_status", Value: "requested"},
		{Kind: "corrected_public_assistance_types", Value: `["identify_person"]`},
	}, verification.ModelIdentity, verification.InputHash, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}

	server, err := NewWithOptions(database, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review", AdminEnabled: true,
		Processor: fakeProcessingRequester{database: database},
	})
	if err != nil {
		t.Fatal(err)
	}
	dashboard := httptest.NewRecorder()
	server.Handler().ServeHTTP(dashboard, httptest.NewRequest(http.MethodGet, "/admin", nil))
	body := dashboard.Body.String()
	for _, expected := range []string{
		"Independent public-assistance verification runs next", "Public assistance verification", processing.PublicAssistanceVerificationPromptVersion,
		"public_assistance_verification/default", "Corrected · succeeded", "not_requested / []", `requested / [&#34;identify_person&#34;]`,
		"Identify a person", "Public assistance verifier provenance", "Recheck public assistance now",
		`name="processor" type="hidden" value="public_assistance_verification"`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("admin dashboard does not contain %q", expected)
		}
	}

	history := httptest.NewRecorder()
	server.Handler().ServeHTTP(history, httptest.NewRequest(http.MethodGet, "/admin/history", nil))
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), "public_assistance_verification/default") {
		t.Fatalf("assistance history = %d/%q", history.Code, history.Body.String())
	}
	if strings.Contains(history.Body.String(), job.TitleDE) || strings.Contains(history.Body.String(), job.BodyDE) || strings.Contains(history.Body.String(), `identify_person`) {
		t.Error("pipeline history exposed incident source or verifier output")
	}
}

func TestAdminVerificationOperationsShowsRetainedResultAndProtectedFailureDetail(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	now := time.Date(2026, time.August, 31, 18, 0, 0, 0, time.UTC)
	presentation := seedV2Presentation(t, database, testPresentation{
		TitleDE: "Verifizierter Titel", SummaryDE: "Verifizierte Zusammenfassung.",
		TitleEN: "Verified title", SummaryEN: "Verified summary.",
	}, now)
	plan := store.PostProcessingPlan{
		ProcessorKey: processing.CategoryVerificationStep, ScopeKey: processing.DefaultPostProcessingScope,
		PromptVersion: processing.CategoryVerificationPromptVersion, Model: "qwen3.5:4b",
		InputKinds: []string{"title_de", "summary_de", "category"},
	}
	if queued, err := database.QueuePostProcessingForRun(ctx, presentation.PresentationRunID, []store.PostProcessingPlan{plan}, "manual", false, now.Add(time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue category verification = %d/%v", queued, err)
	}
	job, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract(plan.ScopeKey, plan.PromptVersion, []string{"is_correct", "corrected_category"}, plan.InputKinds...), false, nil, now.Add(2*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim category verification = %#v/%t/%v", job, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, job, []store.PipelineValue{{Kind: "is_correct", Value: "false"}, {Kind: "corrected_category", Value: "traffic"}}, job.ModelIdentity, job.InputHash, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if queued, err := database.QueueIncidentPostProcessing(ctx, presentation.IncidentID, []store.PostProcessingPlan{plan}, now.Add(4*time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue category replacement = %d/%v", queued, err)
	}
	replacement, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract(plan.ScopeKey, plan.PromptVersion, []string{"is_correct", "corrected_category"}, plan.InputKinds...), false, nil, now.Add(5*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim category replacement = %#v/%t/%v", replacement, found, err)
	}
	privateFailure := errors.New(`<script>alert("private model output")</script>`)
	if err := database.FailPostProcessingJob(ctx, replacement, "needs_review", "output", nil, now.Add(6*time.Minute), privateFailure); err != nil {
		t.Fatal(err)
	}

	server := adminTestServer(t, database, nil)
	handler := server.Handler()
	overview := httptest.NewRecorder()
	handler.ServeHTTP(overview, httptest.NewRequest(http.MethodGet, "/admin/verifications", nil))
	for _, expected := range []string{"Verification operations", "Registered verification scopes", "Category verification", "Public assistance verification", "category_verification/default", "1 attention", `data-verification-operations`, `data-verification-poll-interval="5000"`, `aria-current="page"`} {
		if overview.Code != http.StatusOK || !strings.Contains(overview.Body.String(), expected) {
			t.Errorf("verification overview does not contain %q (status %d)", expected, overview.Code)
		}
	}
	if overview.Header().Get("Cache-Control") != "private, no-store" || overview.Header().Get("X-Robots-Tag") != "noindex, nofollow, noarchive" {
		t.Fatalf("verification overview privacy headers = %#v", overview.Header())
	}

	detailPath := "/admin/verifications?processor=category_verification&scope=default&status=attention&page=1"
	detail := httptest.NewRecorder()
	handler.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, detailPath, nil))
	body := detail.Body.String()
	for _, expected := range []string{
		"Verifizierter Titel", "Corrected", "needs review · attempts 1", "The latest replacement needs attention",
		"Other", "Traffic", "Failure detail", "private model output", "Sensitive administrative diagnostic",
		`name="return_to" type="hidden" value="verifications"`, `name="return_status" type="hidden" value="attention"`,
		"Effective result provenance", "Attempt provenance", "Recheck now", "Recheck all incidents",
	} {
		if detail.Code != http.StatusOK || !strings.Contains(body, expected) {
			t.Errorf("verification detail does not contain %q (status %d)", expected, detail.Code)
		}
	}
	if strings.Contains(body, privateFailure.Error()) || !strings.Contains(body, `&lt;script&gt;alert`) {
		t.Error("verification detail did not HTML-escape the protected error")
	}

	for _, target := range []string{
		"/admin/verifications?processor=category_verification",
		"/admin/verifications?scope=default",
		"/admin/verifications?status=all",
		"/admin/verifications?processor=translation&scope=en",
		"/admin/verifications?processor=category_verification&scope=default&status=unknown",
		"/admin/verifications?processor=category_verification&scope=default&page=0",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("GET %s status = %d, want 400", target, response.Code)
		}
	}

	history := httptest.NewRecorder()
	handler.ServeHTTP(history, httptest.NewRequest(http.MethodGet, "/admin/history", nil))
	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/admin/ai/status", nil))
	reader := httptest.NewRecorder()
	handler.ServeHTTP(reader, httptest.NewRequest(http.MethodGet, "/de/incidents/"+formatID(presentation.IncidentID), nil))
	for name, response := range map[string]*httptest.ResponseRecorder{"history": history, "status": status, "reader": reader} {
		if strings.Contains(response.Body.String(), "private model output") {
			t.Errorf("%s exposed focused verification failure detail", name)
		}
	}

	retry := httptest.NewRecorder()
	retryBody := "confirmed=true&processor=category_verification&scope=default&target=incident&incident_id=" + formatID(presentation.IncidentID) + "&model=qwen3.5%3A4b&return_to=verifications&return_status=attention&return_page=1"
	handler.ServeHTTP(retry, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", retryBody))
	location := retry.Header().Get("Location")
	if retry.Code != http.StatusSeeOther || !strings.HasPrefix(location, "/admin/verifications?") || !strings.Contains(location, "post_processing_queued=1") || !strings.Contains(location, "status=attention") {
		t.Fatalf("verification retry redirect = %d/%q", retry.Code, location)
	}

	invalidReturn := httptest.NewRecorder()
	handler.ServeHTTP(invalidReturn, formRequest(http.MethodPost, "/api/admin/ai/post-processing/process", "confirmed=true&processor=category_verification&scope=default&target=all&model=qwen3.5%3A4b&return_to=https%3A%2F%2Fevil.invalid"))
	if invalidReturn.Code != http.StatusBadRequest {
		t.Fatalf("invalid verification return target = %d, want 400", invalidReturn.Code)
	}

	adminScript := httptest.NewRecorder()
	handler.ServeHTTP(adminScript, httptest.NewRequest(http.MethodGet, staticAssets["admin.js"].path, nil))
	for _, expected := range []string{"data-verification-operations", "verificationPollInterval", "data-verification-connection"} {
		if adminScript.Code != http.StatusOK || !strings.Contains(adminScript.Body.String(), expected) {
			t.Errorf("admin script does not contain %q", expected)
		}
	}
}

func TestAdminVerificationOperationsDiscoversFutureRegisteredVerifier(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	now := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	presentation := seedV2Presentation(t, database, testPresentation{
		TitleDE: "Zukünftige Prüfung", SummaryDE: "Future original value",
		TitleEN: "Future verifier", SummaryEN: "Future verifier summary.",
	}, now)
	plan := store.PostProcessingPlan{
		ProcessorKey: "quality_note", ScopeKey: processing.DefaultPostProcessingScope,
		PromptVersion: "quality-note-v1", Model: "qwen3.5:4b", InputKinds: []string{"summary_de"},
	}
	if queued, err := database.QueuePostProcessingForRun(ctx, presentation.PresentationRunID, []store.PostProcessingPlan{plan}, "manual", false, now.Add(time.Minute)); err != nil || queued != 1 {
		t.Fatalf("queue future verification = %d/%v", queued, err)
	}
	job, found, err := database.ClaimPostProcessingJob(ctx, plan.ProcessorKey, testPostProcessingContract(plan.ScopeKey, plan.PromptVersion, []string{"is_correct", "corrected_quality_note"}, plan.InputKinds...), false, nil, now.Add(2*time.Minute))
	if err != nil || !found {
		t.Fatalf("claim future verification = %#v/%t/%v", job, found, err)
	}
	if err := database.CompletePostProcessingJob(ctx, job, []store.PipelineValue{{Kind: "is_correct", Value: "false"}, {Kind: "corrected_quality_note", Value: "Future corrected value"}}, job.ModelIdentity, job.InputHash, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	verification := &processing.PostProcessorVerification{
		VerdictKind: "is_correct",
		Fields:      []processing.PostProcessorVerificationField{{DisplayName: "Quality note", OriginalKind: "summary_de", CorrectedKind: "corrected_quality_note"}},
	}
	status := processing.PipelineModelStatus{
		CatalogAvailable: true, Models: []string{"qwen3.5:4b"},
		PostProcessors: []processing.PostProcessorModelStatus{
			{
				Key: "quality_note", DisplayName: "Quality note verification", Description: "Review a future quality note.",
				ModelSettingKey: "quality_note", Manual: true, Preferred: "qwen3.5:4b", PreferredAvailable: true,
				Scopes:       []processing.PostProcessorScopeStatus{{Key: "default", DisplayName: "Default", StepKey: "quality_note/default", PromptVersion: "quality-note-v1"}},
				Verification: verification,
			},
			{
				Key: processing.CategoryVerificationStep, DisplayName: "Unannotated category processor",
				Scopes: []processing.PostProcessorScopeStatus{{Key: processing.DefaultPostProcessingScope, DisplayName: "Default"}},
			},
		},
	}
	server, err := NewWithOptions(database, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review", AdminEnabled: true,
		Processor: fakeProcessingRequester{database: database, status: &status},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/verifications", nil))
	for _, expected := range []string{"Quality note verification", "quality_note/default", "Review a future quality note"} {
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), expected) {
			t.Errorf("future verification overview does not contain %q (status %d)", expected, response.Code)
		}
	}
	if strings.Contains(response.Body.String(), "Translations") && strings.Contains(response.Body.String(), "translation/en") {
		t.Error("future verification page included a non-verification post-processor scope")
	}
	if strings.Contains(response.Body.String(), "Unannotated category processor") || strings.Contains(response.Body.String(), "category_verification/default") {
		t.Error("future verification page inferred verification metadata from a processor key")
	}

	detail := httptest.NewRecorder()
	server.Handler().ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/admin/verifications?processor=quality_note&scope=default&status=corrected&page=1", nil))
	for _, expected := range []string{`<dd class="mt-1 break-words font-mono">Future original value`, `<dd class="mt-1 break-words font-mono font-semibold text-civic">Future corrected value`} {
		if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), expected) {
			t.Errorf("future verification detail does not contain fallback %q (status %d)", expected, detail.Code)
		}
	}
}

func TestAdminShowsUnavailableSourceVerificationAsSkipped(t *testing.T) {
	ctx := context.Background()
	database := fixtureStore(t)
	now := time.Date(2026, time.August, 29, 16, 30, 0, 0, time.UTC)
	job := seedV2Presentation(t, database, testPresentation{
		TitleDE: "Deutscher Titel", SummaryDE: "Deutsche Zusammenfassung.",
		TitleEN: "English title", SummaryEN: "English summary.",
	}, now)
	documents, err := source.NewFixtureProvider().Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundSource := false
	for documentIndex := range documents {
		for incidentIndex := range documents[documentIndex].Incidents {
			incident := &documents[documentIndex].Incidents[incidentIndex]
			if incident.TitleDE == job.TitleDE {
				incident.BodyDE = ""
				foundSource = true
			}
		}
	}
	if !foundSource {
		t.Fatal("test incident source not found")
	}
	if err := database.UpsertDocuments(ctx, documents, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsurePostProcessingScopes(ctx, []store.PostProcessingScope{{ProcessorKey: processing.PublicAssistanceVerificationStep, ScopeKey: "default"}}, now); err != nil {
		t.Fatal(err)
	}
	plan := store.PostProcessingPlan{
		ProcessorKey: processing.PublicAssistanceVerificationStep, ScopeKey: "default", PromptVersion: processing.PublicAssistanceVerificationPromptVersion,
		Model: "qwen3.5:4b", InputKinds: []string{"original_title", "incident_body", "public_assistance_status", "public_assistance_types"},
	}
	if queued, err := database.QueuePostProcessingForRun(ctx, job.PresentationRunID, []store.PostProcessingPlan{plan}, "manual", false, now.Add(time.Minute)); err != nil || queued != 0 {
		t.Fatalf("skip assistance verification = %d/%v", queued, err)
	}
	server, err := NewWithOptions(database, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review", AdminEnabled: true,
		Processor: fakeProcessingRequester{database: database},
	})
	if err != nil {
		t.Fatal(err)
	}

	dashboard := httptest.NewRecorder()
	server.Handler().ServeHTTP(dashboard, httptest.NewRequest(http.MethodGet, "/admin", nil))
	for _, expected := range []string{"Not checked · skipped · attempts 0 · Original incident text unavailable", "Skipped 1", "public_assistance_verification/default · skipped (Original incident text unavailable)"} {
		if !strings.Contains(dashboard.Body.String(), expected) {
			t.Errorf("admin skipped state does not contain %q", expected)
		}
	}
	for _, unexpected := range []string{"Public assistance verifier provenance", "Recheck public assistance now"} {
		if strings.Contains(dashboard.Body.String(), unexpected) {
			t.Errorf("admin skipped state unexpectedly contains %q", unexpected)
		}
	}

	history := httptest.NewRecorder()
	server.Handler().ServeHTTP(history, httptest.NewRequest(http.MethodGet, "/admin/history", nil))
	for _, expected := range []string{"public_assistance_verification/default", "skipped", "Attempts 0 · Original incident text unavailable", "Not invoked"} {
		if !strings.Contains(history.Body.String(), expected) {
			t.Errorf("skipped history does not contain %q", expected)
		}
	}

	status := httptest.NewRecorder()
	server.Handler().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/admin/ai/status", nil))
	for _, expected := range []string{`"status":"skipped"`, `"status_reason":"missing_input"`, `"status_detail":"incident_body"`} {
		if !strings.Contains(status.Body.String(), expected) {
			t.Errorf("status API does not contain %q", expected)
		}
	}

	adminScript := httptest.NewRecorder()
	server.Handler().ServeHTTP(adminScript, httptest.NewRequest(http.MethodGet, staticAssets["admin.js"].path, nil))
	for _, expected := range []string{"event.status_reason", "event.status_detail", "Original incident text unavailable", "processor.queue_started_at", "Oldest unfinished job queued", "automatic_retry_ready", "retry_failure_kinds", "attention_failure_kinds", "paused until the next processing window"} {
		if adminScript.Code != http.StatusOK || !strings.Contains(adminScript.Body.String(), expected) {
			t.Errorf("live admin event renderer does not preserve %q", expected)
		}
	}
}

func TestAdminTranslationLabelKeepsCurrentFailureVisibleWithPreviousSuccess(t *testing.T) {
	translation := store.AdminTranslation{Model: "translate:4b", Status: "failed", FailureKind: "output"}
	if label := adminTranslationLabel(translation); label != "Failed · previous success retained" {
		t.Fatalf("retained translation label = %q", label)
	}
}

func TestVisiblePostProcessingModelDistinguishesZeroAttemptSkip(t *testing.T) {
	model := "verify:4b"
	if visible := visiblePostProcessingModel(model, "skipped", 0, nil); visible != "" {
		t.Fatalf("zero-attempt skipped model = %q", visible)
	}
	if visible := visiblePostProcessingModel(model, "skipped", 1, nil); visible != model {
		t.Fatalf("attempted skipped model = %q", visible)
	}
	generatedAt := time.Now()
	if visible := visiblePostProcessingModel(model, "skipped", 0, &generatedAt); visible != model {
		t.Fatalf("retained successful model = %q", visible)
	}
}

func TestAdminReportsStoreErrors(t *testing.T) {
	database := fixtureStore(t)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	retryServer, err := NewWithOptions(database, logger, Options{
		PageSize: 20, SourceMode: "fixture", PresentationMode: "review",
		AdminEnabled: true, Processor: fakeProcessingRequester{err: errors.New("processing unavailable")},
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
