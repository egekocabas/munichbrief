package web

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/store"
)

func (s *Server) admin(response http.ResponseWriter, request *http.Request) {
	unprocessedPage, err := requestedPageParameter(request, "unprocessed_page")
	if err != nil {
		http.Error(response, "invalid unprocessed_page", http.StatusBadRequest)
		return
	}
	allPage, err := requestedPageParameter(request, "all_page")
	if err != nil {
		http.Error(response, "invalid all_page", http.StatusBadRequest)
		return
	}
	scope := store.PresentationScope{Language: s.canonicalLanguage().Code}
	models := processing.PipelineModelStatus{}
	runtime := processing.PipelineRuntimeStatus{}
	if s.options.Processor != nil {
		models, err = s.options.Processor.ModelStatus(request.Context())
		if err != nil {
			s.internalError(response, request, "read AI model status", err)
			return
		}
		runtime, err = s.options.Processor.Status(request.Context())
		if err != nil {
			s.internalError(response, request, "read staged AI runtime status", err)
			return
		}
	}
	unprocessed, unprocessedTotal, err := s.store.ListAdminIncidents(
		request.Context(), s.options.PageSize, (unprocessedPage-1)*s.options.PageSize,
		s.options.SourceMode, scope, store.AdminIncidentsUnprocessed,
	)
	if err != nil {
		s.internalError(response, request, "list unprocessed admin incidents", err)
		return
	}
	allIncidents, allTotal, err := s.store.ListAdminIncidents(
		request.Context(), s.options.PageSize, (allPage-1)*s.options.PageSize,
		s.options.SourceMode, scope, store.AdminIncidentsAll,
	)
	if err != nil {
		s.internalError(response, request, "list all admin incidents", err)
		return
	}
	unprocessedPages := max(1, (unprocessedTotal+s.options.PageSize-1)/s.options.PageSize)
	allPages := max(1, (allTotal+s.options.PageSize-1)/s.options.PageSize)
	if unprocessedPage > unprocessedPages || allPage > allPages {
		http.NotFound(response, request)
		return
	}
	translationViews, err := s.adminTranslations(request, append(append([]store.IncidentRecord{}, unprocessed...), allIncidents...))
	if err != nil {
		s.internalError(response, request, "load admin translations", err)
		return
	}
	categoryViews, err := s.adminCategoryVerifications(request, append(append([]store.IncidentRecord{}, unprocessed...), allIncidents...))
	if err != nil {
		s.internalError(response, request, "load admin category verifications", err)
		return
	}
	assistanceViews, err := s.adminPublicAssistanceVerifications(request, append(append([]store.IncidentRecord{}, unprocessed...), allIncidents...))
	if err != nil {
		s.internalError(response, request, "load admin public assistance verifications", err)
		return
	}
	data := adminPage{
		ProcessingEnabled: s.options.Processor != nil,
		UnprocessedPage:   unprocessedPage,
		AllPage:           allPage,
		Models:            models,
		Runtime:           runtime,
		Unprocessed: s.adminList(unprocessed, unprocessedTotal, unprocessedPage, unprocessedPages,
			adminPaginationURL(unprocessedPage-1, allPage), adminPaginationURL(unprocessedPage+1, allPage), true, allPage, "unprocessed", translationViews, categoryViews, assistanceViews),
		AllIncidents: s.adminList(allIncidents, allTotal, allPage, allPages,
			adminPaginationURL(unprocessedPage, allPage-1), adminPaginationURL(unprocessedPage, allPage+1), false, unprocessedPage, "all", translationViews, categoryViews, assistanceViews),
	}
	data.Unprocessed.Models = models
	data.AllIncidents.Models = models
	if requested, ok := nonNegativeQueryInt(request, "requested"); ok {
		current, _ := nonNegativeQueryInt(request, "current")
		cycleID, _ := nonNegativeQueryInt(request, "cycle")
		data.Notice = fmt.Sprintf("Queued priority pipeline cycle #%d for %d incident(s). Already current: %d. It will start after the active healthy cycle finishes.", cycleID, requested, current)
		data.NoticeIsWarning = requested == 0
	}
	if updated := request.URL.Query().Get("step_model"); updated != "" {
		data.Notice = fmt.Sprintf("Preferred model for %s updated. Future scheduled cycles will use it.", updated)
	}
	if queued, ok := nonNegativeQueryInt(request, "post_processing_queued"); ok {
		processor := request.URL.Query().Get("processor")
		scope := request.URL.Query().Get("scope")
		data.Notice = fmt.Sprintf("Queued %d %s/%s post-processing job(s). Existing successful results remain available while replacements run.", queued, processor, scope)
		data.NoticeIsWarning = queued == 0
	}
	if state := request.URL.Query().Get("automatic_processing"); state == "enabled" || state == "disabled" {
		data.Notice = "Automatic AI processing " + state + ". Explicit admin and CLI requests remain available."
	}
	if canonical, ok := nonNegativeQueryInt(request, "canceled_canonical"); ok {
		cycles, _ := nonNegativeQueryInt(request, "canceled_cycles")
		postProcessing, _ := nonNegativeQueryInt(request, "canceled_post_processing")
		data.Notice = fmt.Sprintf("Canceled %d unfinished canonical job(s) across %d cycle(s) and %d post-processing job(s). Automatic processing remains disabled.", canonical, cycles, postProcessing)
		data.NoticeIsWarning = canonical+postProcessing == 0
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if err := s.adminTemplate.ExecuteTemplate(response, "admin", data); err != nil {
		s.logger.ErrorContext(request.Context(), "render admin page", "error", err)
	}
}

func (s *Server) adminList(records []store.IncidentRecord, total, page, totalPages int, previousURL, nextURL string, allowProcessing bool, otherPage int, idPrefix string, translations map[int64][]adminTranslationView, categories map[int64]adminCategoryVerificationView, assistance map[int64]adminPublicAssistanceVerificationView) adminIncidentList {
	incidents := make([]adminIncidentView, 0, len(records))
	for _, record := range records {
		state := record.ProcessingState()
		presentationLabel := ""
		if record.HasAI {
			presentationLabel = "Pipeline v2 presentation"
		}
		primaryProvenanceLabel := "German provenance"
		publicView := s.incidentForLanguage(record, adminLocaleCode)
		translationItems := translations[record.ID]
		incidents = append(incidents, adminIncidentView{
			Record: record, ProcessingState: strings.ReplaceAll(state, "_", "-"),
			ProcessingLabel: adminProcessingLabel(state), PresentationLabel: presentationLabel,
			CategoryLabel: s.metadataCodeLabel(adminLocaleCode, "Category", record.AICategory), CanProcess: state != "running",
			EventText: publicView.EventText, EventLabel: publicView.EventLabel, ReportKindLabel: publicView.ReportKindLabel,
			PublicAssistanceTypes: publicView.PublicAssistanceTypes, PrimaryProvenanceLabel: primaryProvenanceLabel,
			Translations:                 translationItems,
			TranslationRollup:            adminTranslationRollupFor(translationItems),
			CategoryVerification:         categories[record.ID],
			PublicAssistanceVerification: assistance[record.ID],
		})
	}
	return adminIncidentList{
		Incidents: incidents, Shown: len(incidents), Total: total, Page: page, TotalPages: totalPages,
		PreviousURL: previousURL, NextURL: nextURL, HasPrevious: page > 1, HasNext: page < totalPages,
		AllowProcessing: allowProcessing,
		OtherPage:       otherPage,
		IDPrefix:        idPrefix,
	}
}

func (s *Server) adminCategoryVerifications(request *http.Request, records []store.IncidentRecord) (map[int64]adminCategoryVerificationView, error) {
	views := make(map[int64]adminCategoryVerificationView)
	results, err := s.store.ListAdminCategoryVerifications(request.Context(), adminIncidentIDs(records))
	if err != nil {
		return nil, err
	}
	for _, result := range results {
		views[result.IncidentID] = adminCategoryVerificationView{
			OriginalCategory: result.OriginalCategory, EffectiveCategory: result.EffectiveCategory,
			OriginalLabel: s.metadataCodeLabel(adminLocaleCode, "Category", result.OriginalCategory), EffectiveLabel: s.metadataCodeLabel(adminLocaleCode, "Category", result.EffectiveCategory),
			Verdict: adminVerificationVerdict(result.IsCorrect), Status: adminPostProcessingStatus(result.Status, result.StatusReason, result.Attempts),
			StatusReason: result.StatusReason, StatusDetail: result.StatusDetail, Attempts: result.Attempts, FailureKind: result.FailureKind,
			Model: visiblePostProcessingModel(result.Model, result.Status, result.Attempts, result.GeneratedAt), PromptVersion: result.PromptVersion, GeneratedAt: result.GeneratedAt,
			CanRetry: result.PresentationRunID > 0 && result.OriginalCategory != "" && canRetryPostProcessing(result.Status, result.StatusReason),
		}
	}
	return views, nil
}

func (s *Server) adminPublicAssistanceVerifications(request *http.Request, records []store.IncidentRecord) (map[int64]adminPublicAssistanceVerificationView, error) {
	views := make(map[int64]adminPublicAssistanceVerificationView)
	results, err := s.store.ListAdminPublicAssistanceVerifications(request.Context(), adminIncidentIDs(records))
	if err != nil {
		return nil, err
	}
	for _, result := range results {
		views[result.IncidentID] = adminPublicAssistanceVerificationView{
			OriginalStatus: result.OriginalStatus, OriginalTypes: result.OriginalTypes,
			EffectiveStatus: result.EffectiveStatus, EffectiveTypes: result.EffectiveTypes,
			OriginalTypeLabels: s.adminAssistanceTypeLabels(result.OriginalTypes), EffectiveTypeLabels: s.adminAssistanceTypeLabels(result.EffectiveTypes),
			Verdict: adminVerificationVerdict(result.IsCorrect), Status: adminPostProcessingStatus(result.Status, result.StatusReason, result.Attempts),
			StatusReason: result.StatusReason, StatusDetail: result.StatusDetail, Attempts: result.Attempts, FailureKind: result.FailureKind,
			Model: visiblePostProcessingModel(result.Model, result.Status, result.Attempts, result.GeneratedAt), PromptVersion: result.PromptVersion, GeneratedAt: result.GeneratedAt, NextRetryAt: result.NextRetryAt,
			CanRetry: result.PresentationRunID > 0 && result.OriginalStatus != "" && canRetryPostProcessing(result.Status, result.StatusReason),
		}
	}
	return views, nil
}

func adminIncidentIDs(records []store.IncidentRecord) []int64 {
	seen := make(map[int64]struct{}, len(records))
	incidentIDs := make([]int64, 0, len(records))
	for _, record := range records {
		if record.ID < 1 {
			continue
		}
		if _, exists := seen[record.ID]; exists {
			continue
		}
		seen[record.ID] = struct{}{}
		incidentIDs = append(incidentIDs, record.ID)
	}
	return incidentIDs
}

func adminVerificationVerdict(isCorrect *bool) string {
	if isCorrect == nil {
		return "Not checked"
	}
	if *isCorrect {
		return "Confirmed"
	}
	return "Corrected"
}

func adminPostProcessingStatus(status, reason string, attempts int) string {
	if status == "" {
		return "missing"
	}
	if reason == store.ProcessingStatusReasonOperatorCanceled {
		return "canceled"
	}
	if status == "pending" && attempts > 0 {
		return "retrying"
	}
	return status
}

func canRetryPostProcessing(status, statusReason string) bool {
	return status != "pending" && status != "running" && !(status == "skipped" && statusReason == store.PostProcessingStatusReasonMissingInput)
}

func visiblePostProcessingModel(model, status string, attempts int, generatedAt *time.Time) string {
	if status == "skipped" && attempts == 0 && generatedAt == nil {
		return ""
	}
	return model
}

func postProcessingStatusReasonLabel(reason, detail string) string {
	if reason == store.ProcessingStatusReasonOperatorCanceled {
		return "operator canceled"
	}
	if reason != store.PostProcessingStatusReasonMissingInput {
		return strings.ReplaceAll(reason, "_", " ")
	}
	if detail == "incident_body" {
		return "Original incident text unavailable"
	}
	if detail == "" {
		return "Required input unavailable"
	}
	return "Required input " + strings.ReplaceAll(detail, "_", " ") + " unavailable"
}

func pipelineStatusLabel(status, failureKind, statusReason string) string {
	if failureKind == store.ProcessingStatusReasonOperatorCanceled || statusReason == store.ProcessingStatusReasonOperatorCanceled {
		return "Canceled"
	}
	return strings.ReplaceAll(status, "_", " ")
}

func (s *Server) adminAssistanceTypeLabels(encoded string) []string {
	var types []string
	if json.Unmarshal([]byte(encoded), &types) != nil {
		return nil
	}
	labels := make([]string, 0, len(types))
	for _, assistanceType := range types {
		if label := s.metadataCodeLabel(adminLocaleCode, "AssistanceType", assistanceType); label != "" {
			labels = append(labels, label)
		}
	}
	return labels
}

func (s *Server) adminTranslations(request *http.Request, records []store.IncidentRecord) (map[int64][]adminTranslationView, error) {
	views := make(map[int64][]adminTranslationView)
	definitions := processing.RegisteredTranslations()
	languages := make([]string, 0, len(definitions))
	displayNames := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		languages = append(languages, definition.Language)
		displayNames[definition.Language] = definition.DisplayName
	}
	translations, err := s.store.ListAdminTranslations(request.Context(), adminIncidentIDs(records), languages)
	if err != nil {
		return nil, err
	}
	canonicalReady := make(map[int64]bool, len(records))
	for _, record := range records {
		canonicalReady[record.ID] = record.HasAI
	}
	for _, translation := range translations {
		views[translation.IncidentID] = append(views[translation.IncidentID], adminTranslationView{
			Language: translation.Language, DisplayName: displayNames[translation.Language],
			Title: translation.Title, Summary: translation.Summary,
			Model: translation.Model, PromptVersion: translation.PromptVersion,
			StatusLabel: adminTranslationLabel(translation), StatusReason: translation.StatusReason, StatusDetail: translation.StatusDetail,
			FailureKind: translation.FailureKind,
			Status:      translation.Status,
			CanRetry:    canonicalReady[translation.IncidentID] && canRetryPostProcessing(translation.Status, translation.StatusReason),
		})
	}
	return views, nil
}

func adminTranslationRollupFor(translations []adminTranslationView) adminTranslationRollup {
	rollup := adminTranslationRollup{Total: len(translations)}
	for _, translation := range translations {
		published := translation.Title != "" && translation.Summary != ""
		if published {
			rollup.Published++
		}
		switch translation.Status {
		case "pending", "running":
			rollup.Active++
		case "needs_review", "failed", "skipped":
			rollup.Attention++
			if published {
				rollup.ReplacementAttention++
			}
		}
	}
	return rollup
}

func (s *Server) processIncidentNow(response http.ResponseWriter, request *http.Request) {
	s.processNow(response, request, true, false)
}

func (s *Server) processAllNow(response http.ResponseWriter, request *http.Request) {
	s.processNow(response, request, false, false)
}

func (s *Server) reprocessAll(response http.ResponseWriter, request *http.Request) {
	s.processNow(response, request, false, true)
}

func (s *Server) processNow(response http.ResponseWriter, request *http.Request, single, reprocessAll bool) {
	if !validAdminMutation(request) {
		http.Error(response, "cross-site request blocked", http.StatusForbidden)
		return
	}
	if !isFormPost(request) {
		http.Error(response, "form content type required", http.StatusUnsupportedMediaType)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 4096)
	if err := request.ParseForm(); err != nil {
		http.Error(response, "invalid form", http.StatusBadRequest)
		return
	}
	if request.PostForm.Get("confirmed") != "true" {
		http.Error(response, "processing confirmation is required", http.StatusBadRequest)
		return
	}
	if s.options.Processor == nil {
		http.Error(response, "AI processing is disabled", http.StatusServiceUnavailable)
		return
	}
	var incidentID *int64
	if single {
		parsed, err := strconv.ParseInt(request.PostForm.Get("incident_id"), 10, 64)
		if err != nil || parsed < 1 {
			http.Error(response, "incident_id must be a positive integer", http.StatusBadRequest)
			return
		}
		incidentID = &parsed
	}
	models := make(map[string]string)
	for _, step := range processing.RegisteredSteps() {
		model := strings.TrimSpace(request.PostForm.Get("model_" + step.Key))
		if model == "" {
			http.Error(response, "a model is required for every pipeline step", http.StatusBadRequest)
			return
		}
		models[step.Key] = model
	}
	modelStatus, err := s.options.Processor.ModelStatus(request.Context())
	if err != nil {
		s.internalError(response, request, "read post-processing registry", err)
		return
	}
	for _, processor := range modelStatus.PostProcessors {
		models[processor.ModelSettingKey] = strings.TrimSpace(request.PostForm.Get("model_" + processor.ModelSettingKey))
	}
	result, err := s.options.Processor.RequestNow(request.Context(), s.options.SourceMode, models, incidentID, reprocessAll)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(response, request)
		return
	}
	if err != nil {
		if errors.Is(err, processing.ErrModelUnavailable) {
			http.Error(response, "selected Ollama model is unavailable", http.StatusServiceUnavailable)
			return
		}
		s.internalError(response, request, "request immediate AI processing", err)
		return
	}
	unprocessedPage := positiveFormInt(request.PostForm.Get("unprocessed_page"))
	allPage := positiveFormInt(request.PostForm.Get("all_page"))
	target, _ := url.Parse(adminPaginationURL(unprocessedPage, allPage))
	query := target.Query()
	query.Set("requested", strconv.Itoa(result.Requested))
	query.Set("current", strconv.Itoa(result.Current))
	query.Set("cycle", strconv.FormatInt(result.CycleID, 10))
	target.RawQuery = query.Encode()
	http.Redirect(response, request, target.RequestURI(), http.StatusSeeOther)
}

func (s *Server) processPostProcessing(response http.ResponseWriter, request *http.Request) {
	if !s.preparePostProcessingMutation(response, request, "post-processing") {
		return
	}
	processorKey := strings.TrimSpace(request.PostForm.Get("processor"))
	scopeKey := strings.TrimSpace(request.PostForm.Get("scope"))
	model := strings.TrimSpace(request.PostForm.Get("model"))
	if processorKey == "" || scopeKey == "" || model == "" {
		http.Error(response, "processor, scope, and model are required", http.StatusBadRequest)
		return
	}
	models, err := s.options.Processor.ModelStatus(request.Context())
	if err != nil {
		s.internalError(response, request, "read post-processing registry", err)
		return
	}
	var selected *processing.PostProcessorModelStatus
	for index := range models.PostProcessors {
		if models.PostProcessors[index].Key == processorKey {
			selected = &models.PostProcessors[index]
			break
		}
	}
	if selected == nil || !selected.Manual {
		http.Error(response, "unsupported post-processing processor", http.StatusBadRequest)
		return
	}
	modelAvailable := false
	for _, available := range models.Models {
		modelAvailable = modelAvailable || available == model
	}
	if !models.CatalogAvailable || !modelAvailable {
		http.Error(response, "selected Ollama model is unavailable", http.StatusServiceUnavailable)
		return
	}
	var scopeKeys []string
	if scopeKey != "all" {
		valid := false
		for _, scope := range selected.Scopes {
			valid = valid || scope.Key == scopeKey
		}
		if !valid {
			http.Error(response, "unsupported post-processing scope", http.StatusBadRequest)
			return
		}
		scopeKeys = []string{scopeKey}
	}
	incidentID, ok := postProcessingIncidentID(response, request.PostForm)
	if !ok {
		return
	}
	target, returnErr := postProcessingReturnURL(request.PostForm, processorKey, scopeKey, selected.Verification != nil)
	if returnErr != nil {
		http.Error(response, returnErr.Error(), http.StatusBadRequest)
		return
	}
	queued, err := s.options.Processor.RequestPostProcessing(request.Context(), processing.PostProcessingRequest{ProcessorKey: processorKey, ScopeKeys: scopeKeys, IncidentID: incidentID, Model: model})
	if s.handlePostProcessingError(response, request, "queue post-processing work", err) {
		return
	}
	query := target.Query()
	query.Set("post_processing_queued", strconv.Itoa(queued))
	query.Set("processor", processorKey)
	query.Set("scope", scopeKey)
	target.RawQuery = query.Encode()
	http.Redirect(response, request, target.RequestURI(), http.StatusSeeOther)
}

func postProcessingReturnURL(form url.Values, processorKey, scopeKey string, verificationProcessor bool) (*url.URL, error) {
	switch strings.TrimSpace(form.Get("return_to")) {
	case "":
		return url.Parse(adminPaginationURL(positiveFormInt(form.Get("unprocessed_page")), positiveFormInt(form.Get("all_page"))))
	case "verifications":
		if !verificationProcessor {
			return nil, errors.New("verification return target requires a verification processor")
		}
		filter, ok := requestedAdminVerificationFilter(form.Get("return_status"))
		if !ok {
			return nil, errors.New("invalid verification return status")
		}
		pageRaw := strings.TrimSpace(form.Get("return_page"))
		page, err := strconv.Atoi(pageRaw)
		if err != nil || page < 1 {
			return nil, errors.New("invalid verification return page")
		}
		return url.Parse(adminVerificationsURL(processorKey, scopeKey, filter, page))
	default:
		return nil, errors.New("invalid post-processing return target")
	}
}

func (s *Server) preparePostProcessingMutation(response http.ResponseWriter, request *http.Request, label string) bool {
	if !validAdminMutation(request) {
		http.Error(response, "cross-site request blocked", http.StatusForbidden)
		return false
	}
	if !isFormPost(request) {
		http.Error(response, "form content type required", http.StatusUnsupportedMediaType)
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, 4096)
	if err := request.ParseForm(); err != nil {
		http.Error(response, "invalid form", http.StatusBadRequest)
		return false
	}
	if request.PostForm.Get("confirmed") != "true" {
		http.Error(response, label+" confirmation is required", http.StatusBadRequest)
		return false
	}
	if s.options.Processor == nil {
		http.Error(response, "AI processing is disabled", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func postProcessingIncidentID(response http.ResponseWriter, form url.Values) (*int64, bool) {
	switch form.Get("target") {
	case "all":
		if strings.TrimSpace(form.Get("incident_id")) != "" {
			http.Error(response, "incident_id is only valid for an incident target", http.StatusBadRequest)
			return nil, false
		}
		return nil, true
	case "incident":
		incidentID, err := strconv.ParseInt(form.Get("incident_id"), 10, 64)
		if err != nil || incidentID < 1 {
			http.Error(response, "incident_id must be a positive integer", http.StatusBadRequest)
			return nil, false
		}
		return &incidentID, true
	default:
		http.Error(response, "target must be incident or all", http.StatusBadRequest)
		return nil, false
	}
}

func (s *Server) handlePostProcessingError(response http.ResponseWriter, request *http.Request, operation string, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(response, request)
		return true
	}
	if errors.Is(err, processing.ErrModelUnavailable) {
		http.Error(response, "selected Ollama model is unavailable", http.StatusServiceUnavailable)
		return true
	}
	s.internalError(response, request, operation, err)
	return true
}

func (s *Server) updatePreferredStepModel(response http.ResponseWriter, request *http.Request) {
	if !validAdminMutation(request) {
		http.Error(response, "cross-site request blocked", http.StatusForbidden)
		return
	}
	if !isFormPost(request) {
		http.Error(response, "form content type required", http.StatusUnsupportedMediaType)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 4096)
	if err := request.ParseForm(); err != nil {
		http.Error(response, "invalid form", http.StatusBadRequest)
		return
	}
	if s.options.Processor == nil {
		http.Error(response, "AI processing is disabled", http.StatusServiceUnavailable)
		return
	}
	stepKey := strings.TrimSpace(request.PostForm.Get("step"))
	model := strings.TrimSpace(request.PostForm.Get("model"))
	if stepKey == "" || model == "" {
		http.Error(response, "step and model are required", http.StatusBadRequest)
		return
	}
	if err := s.options.Processor.SetPreferredStepModel(request.Context(), stepKey, model); err != nil {
		if errors.Is(err, processing.ErrModelUnavailable) {
			http.Error(response, "selected Ollama model is unavailable", http.StatusServiceUnavailable)
			return
		}
		s.internalError(response, request, "update preferred AI model", err)
		return
	}
	target, _ := url.Parse(adminPaginationURL(positiveFormInt(request.PostForm.Get("unprocessed_page")), positiveFormInt(request.PostForm.Get("all_page"))))
	query := target.Query()
	query.Set("step_model", stepKey)
	target.RawQuery = query.Encode()
	http.Redirect(response, request, target.RequestURI(), http.StatusSeeOther)
}

func (s *Server) updateAutomaticProcessing(response http.ResponseWriter, request *http.Request) {
	if !s.prepareAIControlMutation(response, request, false) {
		return
	}
	raw := request.PostForm.Get("enabled")
	if raw != "true" && raw != "false" {
		http.Error(response, "enabled must be true or false", http.StatusBadRequest)
		return
	}
	enabled := raw == "true"
	if err := s.options.Processor.SetAutomaticProcessing(request.Context(), enabled); err != nil {
		s.internalError(response, request, "update automatic AI processing", err)
		return
	}
	target, _ := url.Parse(adminPaginationURL(positiveFormInt(request.PostForm.Get("unprocessed_page")), positiveFormInt(request.PostForm.Get("all_page"))))
	query := target.Query()
	if enabled {
		query.Set("automatic_processing", "enabled")
	} else {
		query.Set("automatic_processing", "disabled")
	}
	target.RawQuery = query.Encode()
	http.Redirect(response, request, target.RequestURI(), http.StatusSeeOther)
}

func (s *Server) cancelAllProcessing(response http.ResponseWriter, request *http.Request) {
	if !s.prepareAIControlMutation(response, request, true) {
		return
	}
	result, err := s.options.Processor.CancelAll(request.Context())
	if err != nil {
		s.internalError(response, request, "cancel unfinished AI processing", err)
		return
	}
	target, _ := url.Parse(adminPaginationURL(positiveFormInt(request.PostForm.Get("unprocessed_page")), positiveFormInt(request.PostForm.Get("all_page"))))
	query := target.Query()
	query.Set("canceled_cycles", strconv.Itoa(result.Cycles))
	query.Set("canceled_canonical", strconv.Itoa(result.CanonicalJobs))
	query.Set("canceled_post_processing", strconv.Itoa(result.PostProcessingJobs))
	target.RawQuery = query.Encode()
	http.Redirect(response, request, target.RequestURI(), http.StatusSeeOther)
}

func (s *Server) prepareAIControlMutation(response http.ResponseWriter, request *http.Request, requireConfirmation bool) bool {
	if !validAdminMutation(request) {
		http.Error(response, "cross-site request blocked", http.StatusForbidden)
		return false
	}
	if !isFormPost(request) {
		http.Error(response, "form content type required", http.StatusUnsupportedMediaType)
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, 4096)
	if err := request.ParseForm(); err != nil {
		http.Error(response, "invalid form", http.StatusBadRequest)
		return false
	}
	if requireConfirmation && request.PostForm.Get("confirmed") != "true" {
		http.Error(response, "cancellation confirmation is required", http.StatusBadRequest)
		return false
	}
	if s.options.Processor == nil {
		http.Error(response, "AI processing is disabled", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (s *Server) pipelineStatus(response http.ResponseWriter, request *http.Request) {
	if s.options.Processor == nil {
		http.Error(response, "AI processing is disabled", http.StatusServiceUnavailable)
		return
	}
	status, err := s.options.Processor.Status(request.Context())
	if err != nil {
		s.internalError(response, request, "read staged AI status", err)
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if err := json.NewEncoder(response).Encode(status); err != nil {
		s.logger.ErrorContext(request.Context(), "encode staged AI status", "error", err)
	}
}

type adminPage struct {
	Notice            string
	NoticeIsWarning   bool
	Unprocessed       adminIncidentList
	AllIncidents      adminIncidentList
	ProcessingEnabled bool
	UnprocessedPage   int
	AllPage           int
	Models            processing.PipelineModelStatus
	Runtime           processing.PipelineRuntimeStatus
}

type adminIncidentList struct {
	Incidents       []adminIncidentView
	Shown           int
	Total           int
	Page            int
	TotalPages      int
	PreviousURL     string
	NextURL         string
	HasPrevious     bool
	HasNext         bool
	AllowProcessing bool
	OtherPage       int
	Models          processing.PipelineModelStatus
	IDPrefix        string
}

type adminIncidentView struct {
	Record                       store.IncidentRecord
	ProcessingState              string
	ProcessingLabel              string
	PresentationLabel            string
	CategoryLabel                string
	EventText                    string
	EventLabel                   string
	ReportKindLabel              string
	PublicAssistanceTypes        []string
	PrimaryProvenanceLabel       string
	CanProcess                   bool
	Translations                 []adminTranslationView
	TranslationRollup            adminTranslationRollup
	CategoryVerification         adminCategoryVerificationView
	PublicAssistanceVerification adminPublicAssistanceVerificationView
}

type adminCategoryVerificationView struct {
	OriginalCategory  string
	EffectiveCategory string
	OriginalLabel     string
	EffectiveLabel    string
	Verdict           string
	Status            string
	StatusReason      string
	StatusDetail      string
	Attempts          int
	FailureKind       string
	Model             string
	PromptVersion     string
	GeneratedAt       *time.Time
	CanRetry          bool
}

type adminPublicAssistanceVerificationView struct {
	OriginalStatus      string
	OriginalTypes       string
	EffectiveStatus     string
	EffectiveTypes      string
	OriginalTypeLabels  []string
	EffectiveTypeLabels []string
	Verdict             string
	Status              string
	StatusReason        string
	StatusDetail        string
	Attempts            int
	FailureKind         string
	Model               string
	PromptVersion       string
	GeneratedAt         *time.Time
	NextRetryAt         *time.Time
	CanRetry            bool
}

type adminTranslationView struct {
	Language      string
	DisplayName   string
	Title         string
	Summary       string
	Model         string
	PromptVersion string
	StatusLabel   string
	StatusReason  string
	StatusDetail  string
	FailureKind   string
	Status        string
	CanRetry      bool
}

type adminTranslationRollup struct {
	Total                int
	Published            int
	Active               int
	Attention            int
	ReplacementAttention int
}

func adminProcessingLabel(state string) string {
	switch state {
	case "ready":
		return "German presentation ready"
	case "queued":
		return "Queued"
	case "running":
		return "Processing"
	case "retrying":
		return "Retrying"
	case "needs_review":
		return "Needs review"
	case "failed":
		return "Failed"
	case "waiting":
		return "Waiting for previous step"
	case "superseded":
		return "Superseded"
	default:
		return "Not processed"
	}
}

func adminTranslationLabel(translation store.AdminTranslation) string {
	label := "Missing"
	switch translation.Status {
	case "succeeded":
		label = "Completed"
	case "pending":
		if translation.Attempts > 0 {
			label = "Retrying"
		} else {
			label = "Pending"
		}
	case "running":
		label = "Running"
	case "needs_review":
		label = "Needs review"
	case "failed":
		label = "Failed"
	case "skipped":
		label = "Skipped"
	case "superseded":
		if translation.StatusReason == store.ProcessingStatusReasonOperatorCanceled {
			label = "Canceled"
		}
	default:
		if translation.Model != "" {
			label = "Completed"
		}
	}
	if translation.Model != "" && translation.Status != "" && translation.Status != "succeeded" {
		label += " · previous success retained"
	}
	return label
}

func adminPaginationURL(unprocessedPage, allPage int) string {
	query := url.Values{}
	query.Set("unprocessed_page", strconv.Itoa(max(unprocessedPage, 1)))
	query.Set("all_page", strconv.Itoa(max(allPage, 1)))
	return "/admin?" + query.Encode()
}

func positiveFormInt(raw string) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 1
	}
	return value
}

func nonNegativeQueryInt(request *http.Request, name string) (int, bool) {
	raw := request.URL.Query().Get(name)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value >= 0
}

func validAdminMutation(request *http.Request) bool {
	fetchSite := strings.ToLower(strings.TrimSpace(request.Header.Get("Sec-Fetch-Site")))
	if fetchSite == "cross-site" {
		return false
	}
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if strings.EqualFold(origin, "null") {
		// A no-referrer navigation can serialize a legitimate form origin as
		// opaque. Only browser-controlled same-origin Fetch Metadata may vouch
		// for that otherwise unverifiable value.
		return fetchSite == "same-origin"
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme != "" && parsed.Hostname() != "" && strings.EqualFold(parsed.Hostname(), requestHostname(request))
}

func isFormPost(request *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/x-www-form-urlencoded"
}
