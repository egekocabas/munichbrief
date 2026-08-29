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
	scope := store.PresentationScope{PromptVersion: s.options.PromptVersion, Language: canonicalReaderLanguage().Code}
	if translations := translatedReaderLanguages(); len(translations) > 0 {
		scope.TranslationLanguage = translations[0].Code
	}
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
	data := adminPage{
		ProcessingEnabled:    s.options.Processor != nil,
		UnprocessedPage:      unprocessedPage,
		AllPage:              allPage,
		Models:               models,
		Runtime:              runtime,
		TranslationLanguages: adminTranslationLanguages(),
		Unprocessed: s.adminList(unprocessed, unprocessedTotal, unprocessedPage, unprocessedPages,
			adminPaginationURL(unprocessedPage-1, allPage), adminPaginationURL(unprocessedPage+1, allPage), true, allPage, "unprocessed", translationViews, categoryViews),
		AllIncidents: s.adminList(allIncidents, allTotal, allPage, allPages,
			adminPaginationURL(unprocessedPage, allPage-1), adminPaginationURL(unprocessedPage, allPage+1), false, unprocessedPage, "all", translationViews, categoryViews),
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
	if queued, ok := nonNegativeQueryInt(request, "translations_queued"); ok {
		language := request.URL.Query().Get("translation_language")
		displayName := language
		if language == "all" {
			displayName = "all registered languages"
		} else if definition, found := processing.TranslationByLanguage(language); found {
			displayName = definition.DisplayName
		}
		data.Notice = fmt.Sprintf("Queued %d %s translation job(s). Canonical German presentations remain available independently.", queued, displayName)
		data.NoticeIsWarning = queued == 0
	}
	if queued, ok := nonNegativeQueryInt(request, "category_verifications_queued"); ok {
		data.Notice = fmt.Sprintf("Queued %d category verification job(s). Existing German presentations remain available while checks run.", queued)
		data.NoticeIsWarning = queued == 0
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if err := s.adminTemplate.ExecuteTemplate(response, "admin", data); err != nil {
		s.logger.ErrorContext(request.Context(), "render admin page", "error", err)
	}
}

func (s *Server) adminList(records []store.IncidentRecord, total, page, totalPages int, previousURL, nextURL string, allowProcessing bool, otherPage int, idPrefix string, translations map[int64][]adminTranslationView, categories map[int64]adminCategoryVerificationView) adminIncidentList {
	incidents := make([]adminIncidentView, 0, len(records))
	for _, record := range records {
		state := record.ProcessingState()
		presentationLabel := ""
		if record.HasAI {
			presentationLabel = "Legacy presentation retained"
			if record.AIPipelineVersion == processing.PipelineVersion {
				presentationLabel = "Pipeline v2 presentation"
			} else if record.AIPipelineVersion == store.PreviousPipelineVersion {
				presentationLabel = "Pipeline v1 fallback"
			}
		}
		primaryProvenanceLabel := "Presentation provenance"
		if record.AIPipelineVersion == processing.PipelineVersion || record.AIPipelineVersion == store.PreviousPipelineVersion {
			primaryProvenanceLabel = "German provenance"
		}
		publicView := s.incidentForLanguage(record, "en")
		incidents = append(incidents, adminIncidentView{
			Record: record, ProcessingState: strings.ReplaceAll(state, "_", "-"),
			ProcessingLabel: adminProcessingLabel(state), PresentationLabel: presentationLabel,
			CategoryLabel: processing.CategoryLabel(record.AICategory, "en"), CanProcess: state != "running",
			EventText: publicView.EventText, EventLabel: publicView.EventLabel, ReportKindLabel: publicView.ReportKindLabel,
			PublicAssistanceTypes: publicView.PublicAssistanceTypes, PrimaryProvenanceLabel: primaryProvenanceLabel,
			Translations:         translations[record.ID],
			CategoryVerification: categories[record.ID],
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
	results, err := s.store.ListAdminCategoryVerifications(request.Context(), incidentIDs)
	if err != nil {
		return nil, err
	}
	for _, result := range results {
		verdict := "Not checked"
		if result.IsCorrect != nil {
			if *result.IsCorrect {
				verdict = "Confirmed"
			} else {
				verdict = "Corrected"
			}
		}
		status := result.Status
		if status == "" {
			status = "missing"
		} else if status == "pending" && result.Attempts > 0 {
			status = "retrying"
		}
		views[result.IncidentID] = adminCategoryVerificationView{
			OriginalCategory: result.OriginalCategory, EffectiveCategory: result.EffectiveCategory,
			OriginalLabel: processing.CategoryLabel(result.OriginalCategory, "en"), EffectiveLabel: processing.CategoryLabel(result.EffectiveCategory, "en"),
			Verdict: verdict, Status: status, Attempts: result.Attempts, FailureKind: result.FailureKind,
			Model: result.Model, PromptVersion: result.PromptVersion, GeneratedAt: result.GeneratedAt,
			CanRetry: result.PresentationRunID > 0 && result.OriginalCategory != "" && result.Status != "pending" && result.Status != "running",
		}
	}
	return views, nil
}

func (s *Server) adminTranslations(request *http.Request, records []store.IncidentRecord) (map[int64][]adminTranslationView, error) {
	views := make(map[int64][]adminTranslationView)
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
	definitions := processing.RegisteredTranslations()
	languages := make([]string, 0, len(definitions))
	displayNames := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		languages = append(languages, definition.Language)
		displayNames[definition.Language] = definition.DisplayName
	}
	translations, err := s.store.ListAdminTranslations(request.Context(), incidentIDs, languages, s.options.PromptVersion)
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
			StatusLabel: adminTranslationLabel(translation), FailureKind: translation.FailureKind,
			CanRetry: canonicalReady[translation.IncidentID] && (translation.Model == "" || translation.Fallback) && translation.Status != "pending" && translation.Status != "running",
		})
	}
	return views, nil
}

func adminTranslationLanguages() []adminTranslationLanguage {
	definitions := processing.RegisteredTranslations()
	languages := make([]adminTranslationLanguage, 0, len(definitions))
	for _, definition := range definitions {
		languages = append(languages, adminTranslationLanguage{Code: definition.Language, DisplayName: definition.DisplayName})
	}
	return languages
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
	translationModel := strings.TrimSpace(request.PostForm.Get("model_" + processing.TranslationModelStep))
	models[processing.TranslationModelStep] = translationModel
	models[processing.CategoryVerificationStep] = strings.TrimSpace(request.PostForm.Get("model_" + processing.CategoryVerificationStep))
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

func (s *Server) retryTranslation(response http.ResponseWriter, request *http.Request) {
	s.translationMutation(response, request, false)
}

func (s *Server) backfillTranslations(response http.ResponseWriter, request *http.Request) {
	s.translationMutation(response, request, true)
}

func (s *Server) retryCategoryVerification(response http.ResponseWriter, request *http.Request) {
	s.categoryVerificationMutation(response, request, false)
}

func (s *Server) backfillCategoryVerifications(response http.ResponseWriter, request *http.Request) {
	s.categoryVerificationMutation(response, request, true)
}

func (s *Server) processTranslationsOnly(response http.ResponseWriter, request *http.Request) {
	if !s.preparePostProcessingMutation(response, request, "translation") {
		return
	}
	model := strings.TrimSpace(request.PostForm.Get("model"))
	language := strings.TrimSpace(request.PostForm.Get("language"))
	if model == "" || language == "" {
		http.Error(response, "language and model are required", http.StatusBadRequest)
		return
	}
	languages := []string{language}
	if language == "all" {
		languages = languages[:0]
		for _, definition := range processing.RegisteredTranslations() {
			languages = append(languages, definition.Language)
		}
	}
	incidentID, ok := postProcessingIncidentID(response, request.PostForm)
	if !ok {
		return
	}
	queued, err := s.options.Processor.RequestTranslations(request.Context(), incidentID, languages, model)
	if s.handlePostProcessingError(response, request, "queue translation work", err) {
		return
	}
	s.redirectPostProcessing(response, request, "translations_queued", queued, "translation_language", language)
}

func (s *Server) processCategoryVerificationsOnly(response http.ResponseWriter, request *http.Request) {
	if !s.preparePostProcessingMutation(response, request, "category verification") {
		return
	}
	model := strings.TrimSpace(request.PostForm.Get("model"))
	if model == "" {
		http.Error(response, "model is required", http.StatusBadRequest)
		return
	}
	incidentID, ok := postProcessingIncidentID(response, request.PostForm)
	if !ok {
		return
	}
	queued, err := s.options.Processor.RequestCategoryVerifications(request.Context(), incidentID, model)
	if s.handlePostProcessingError(response, request, "queue category verification work", err) {
		return
	}
	s.redirectPostProcessing(response, request, "category_verifications_queued", queued, "", "")
}

func (s *Server) categoryVerificationMutation(response http.ResponseWriter, request *http.Request, backfill bool) {
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
		http.Error(response, "category verification confirmation is required", http.StatusBadRequest)
		return
	}
	if s.options.Processor == nil {
		http.Error(response, "AI processing is disabled", http.StatusServiceUnavailable)
		return
	}
	model := strings.TrimSpace(request.PostForm.Get("model"))
	if model == "" {
		http.Error(response, "model is required", http.StatusBadRequest)
		return
	}
	var queued int
	var err error
	if backfill {
		queued, err = s.options.Processor.BackfillCategoryVerifications(request.Context(), model)
	} else {
		incidentID, parseErr := strconv.ParseInt(request.PostForm.Get("incident_id"), 10, 64)
		if parseErr != nil || incidentID < 1 {
			http.Error(response, "incident_id must be a positive integer", http.StatusBadRequest)
			return
		}
		queued, err = s.options.Processor.RetryCategoryVerification(request.Context(), incidentID, model)
	}
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(response, request)
		return
	}
	if errors.Is(err, processing.ErrModelUnavailable) {
		http.Error(response, "selected Ollama model is unavailable", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		s.internalError(response, request, "queue category verification work", err)
		return
	}
	target, _ := url.Parse(adminPaginationURL(positiveFormInt(request.PostForm.Get("unprocessed_page")), positiveFormInt(request.PostForm.Get("all_page"))))
	query := target.Query()
	query.Set("category_verifications_queued", strconv.Itoa(queued))
	target.RawQuery = query.Encode()
	http.Redirect(response, request, target.RequestURI(), http.StatusSeeOther)
}

func (s *Server) translationMutation(response http.ResponseWriter, request *http.Request, backfill bool) {
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
		http.Error(response, "translation confirmation is required", http.StatusBadRequest)
		return
	}
	if s.options.Processor == nil {
		http.Error(response, "AI processing is disabled", http.StatusServiceUnavailable)
		return
	}
	language := strings.TrimSpace(request.PostForm.Get("language"))
	model := strings.TrimSpace(request.PostForm.Get("model"))
	if language == "" || model == "" {
		http.Error(response, "language and model are required", http.StatusBadRequest)
		return
	}
	var queued int
	var err error
	if backfill {
		queued, err = s.options.Processor.BackfillTranslations(request.Context(), language, model)
	} else {
		incidentID, parseErr := strconv.ParseInt(request.PostForm.Get("incident_id"), 10, 64)
		if parseErr != nil || incidentID < 1 {
			http.Error(response, "incident_id must be a positive integer", http.StatusBadRequest)
			return
		}
		queued, err = s.options.Processor.RetryTranslation(request.Context(), incidentID, language, model)
	}
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(response, request)
		return
	}
	if errors.Is(err, processing.ErrModelUnavailable) {
		http.Error(response, "selected Ollama model is unavailable", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		s.internalError(response, request, "queue translation work", err)
		return
	}
	target, _ := url.Parse(adminPaginationURL(positiveFormInt(request.PostForm.Get("unprocessed_page")), positiveFormInt(request.PostForm.Get("all_page"))))
	query := target.Query()
	query.Set("translations_queued", strconv.Itoa(queued))
	query.Set("translation_language", language)
	target.RawQuery = query.Encode()
	http.Redirect(response, request, target.RequestURI(), http.StatusSeeOther)
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
	switch form.Get("scope") {
	case "all":
		return nil, true
	case "incident":
		incidentID, err := strconv.ParseInt(form.Get("incident_id"), 10, 64)
		if err != nil || incidentID < 1 {
			http.Error(response, "incident_id must be a positive integer", http.StatusBadRequest)
			return nil, false
		}
		return &incidentID, true
	default:
		http.Error(response, "scope must be incident or all", http.StatusBadRequest)
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

func (s *Server) redirectPostProcessing(response http.ResponseWriter, request *http.Request, countKey string, queued int, detailKey, detailValue string) {
	target, _ := url.Parse(adminPaginationURL(positiveFormInt(request.PostForm.Get("unprocessed_page")), positiveFormInt(request.PostForm.Get("all_page"))))
	query := target.Query()
	query.Set(countKey, strconv.Itoa(queued))
	if detailKey != "" {
		query.Set(detailKey, detailValue)
	}
	target.RawQuery = query.Encode()
	http.Redirect(response, request, target.RequestURI(), http.StatusSeeOther)
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
	Notice               string
	NoticeIsWarning      bool
	Unprocessed          adminIncidentList
	AllIncidents         adminIncidentList
	ProcessingEnabled    bool
	UnprocessedPage      int
	AllPage              int
	Models               processing.PipelineModelStatus
	Runtime              processing.PipelineRuntimeStatus
	TranslationLanguages []adminTranslationLanguage
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
	Record                 store.IncidentRecord
	ProcessingState        string
	ProcessingLabel        string
	PresentationLabel      string
	CategoryLabel          string
	EventText              string
	EventLabel             string
	ReportKindLabel        string
	PublicAssistanceTypes  []string
	PrimaryProvenanceLabel string
	CanProcess             bool
	Translations           []adminTranslationView
	CategoryVerification   adminCategoryVerificationView
}

type adminCategoryVerificationView struct {
	OriginalCategory  string
	EffectiveCategory string
	OriginalLabel     string
	EffectiveLabel    string
	Verdict           string
	Status            string
	Attempts          int
	FailureKind       string
	Model             string
	PromptVersion     string
	GeneratedAt       *time.Time
	CanRetry          bool
}

type adminTranslationLanguage struct {
	Code        string
	DisplayName string
}

type adminTranslationView struct {
	Language      string
	DisplayName   string
	Title         string
	Summary       string
	Model         string
	PromptVersion string
	StatusLabel   string
	FailureKind   string
	CanRetry      bool
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
	label := ""
	if translation.Model != "" && !translation.Fallback {
		label = "Completed"
	} else {
		switch translation.Status {
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
		default:
			label = "Missing"
		}
	}
	if translation.Fallback {
		label += " · older translated fallback"
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
