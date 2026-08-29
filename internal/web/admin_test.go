package web

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
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
		{method: http.MethodGet, path: "/admin/history"},
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
	for _, expected := range []string{"AI processing", "Registered pipeline steps", "qwen3.5:4b", "Installed and ready", "name=\"model_incident_metadata\"", "name=\"model_german_presentation\"", "name=\"model_translation\"", "/api/admin/ai/process-now", "/api/admin/ai/process-all-now", "/api/admin/ai/reprocess-all", "/api/admin/ai/step-model", "/api/admin/ai/post-processing/process", "/api/admin/ai/status", "/admin/history", "Pipeline history", "Open reader", "View full history", "Confirm AI request", staticAssets["theme.js"].path, "data-theme-toggle", staticAssets["admin.js"].path, "Active stage", "Canonical pipeline", "New outside cycle", "Ready after stage", "All-cycle history", "Waiting jobs are durable", "Automatic v2 cutover", "Independent post-processing queues", "Post-processing only", "All registered languages", "Translations only", "Category verification only"} {
		if !strings.Contains(page.Body.String(), expected) {
			t.Errorf("admin page does not contain %q", expected)
		}
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
	requester := fakeProcessingRequester{database: database, status: &status, postRequest: func(_ context.Context, request processing.PostProcessingRequest) (int, error) {
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
	if response.Code != http.StatusSeeOther || received.ProcessorKey != "quality_note" || received.Model != "qwen3.5:4b" || received.IncidentID != nil || len(received.ScopeKeys) != 0 {
		t.Fatalf("injected post-processor request = %d/%#v", response.Code, received)
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
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, formRequest(http.MethodPost, "/api/admin/ai/process-all-now", "confirmed=true"))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("AI-disabled processing status = %d, want 503", response.Code)
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
	for _, expected := range []string{"Kanonischer Titel", "English state", "Missing", "/api/admin/ai/post-processing/process", "Category verification", "Not checked"} {
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
		presentation.TitleDE, presentation.SummaryDE, presentation.TitleEN, presentation.SummaryEN,
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
	if dashboard.Code != http.StatusOK || !strings.Contains(dashboard.Body.String(), "category_verification/default") || !strings.Contains(dashboard.Body.String(), "translation/en") || !strings.Contains(dashboard.Body.String(), "data-post-processing-elapsed") || !strings.Contains(dashboard.Body.String(), "Cycle #"+formatID(result.CycleID)) {
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

func TestAdminTranslationLabelKeepsCurrentFailureVisibleWithPreviousSuccess(t *testing.T) {
	translation := store.AdminTranslation{Model: "translate:4b", Status: "failed", FailureKind: "output"}
	if label := adminTranslationLabel(translation); label != "Failed · previous success retained" {
		t.Fatalf("retained translation label = %q", label)
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
