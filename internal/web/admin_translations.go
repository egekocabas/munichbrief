package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/store"
)

const adminLocaleCode = "en"

var adminTranslationFilters = []adminTranslationFilterView{
	{Key: store.AdminTranslationsUnpublished, Label: "Unpublished"},
	{Key: store.AdminTranslationsAll, Label: "All"},
	{Key: store.AdminTranslationsPublished, Label: "Published"},
	{Key: store.AdminTranslationsNeverQueued, Label: "Never queued"},
	{Key: store.AdminTranslationsActive, Label: "Active"},
	{Key: store.AdminTranslationsAttention, Label: "Attention"},
}

func (s *Server) adminTranslationsPage(response http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	incidentValue := strings.TrimSpace(query.Get("incident"))
	languageCode := strings.TrimSpace(query.Get("language"))
	if incidentValue != "" && (languageCode != "" || query.Has("status") || query.Has("page")) {
		http.Error(response, "incident cannot be combined with language, status, or page", http.StatusBadRequest)
		return
	}
	if incidentValue == "" && languageCode == "" && (query.Has("status") || query.Has("page")) {
		http.Error(response, "status and page require a language", http.StatusBadRequest)
		return
	}

	runtime := processing.PipelineRuntimeStatus{}
	models := processing.PipelineModelStatus{}
	var err error
	if s.options.Processor != nil {
		runtime, err = s.options.Processor.Status(request.Context())
		models = runtime.Models
		if err != nil {
			s.internalError(response, request, "read translation model status", err)
			return
		}
	}
	translationProcessor := translationProcessorStatus(models)
	languages := s.translatedLanguages()
	languageCodes := make([]string, 0, len(languages))
	for _, language := range languages {
		languageCodes = append(languageCodes, language.Code)
	}
	canonical, coverage, err := s.store.AdminTranslationCoverage(request.Context(), s.options.SourceMode, languageCodes)
	if err != nil {
		s.internalError(response, request, "read translation coverage", err)
		return
	}
	data := adminTranslationsPage{
		Canonical:         canonical,
		CanonicalBacklog:  max(0, canonical.Total-canonical.Published),
		ProcessingEnabled: s.options.Processor != nil,
		Models:            models,
		Runtime:           runtime,
		TranslationModel:  translationProcessor,
		Filters:           append([]adminTranslationFilterView(nil), adminTranslationFilters...),
		UpdatedAt:         time.Now().In(s.location),
	}
	data.TranslationActionsAvailable = data.translationActionsAvailable()
	routes := adminTranslationRoutes(translationProcessor, models.Models)
	displayNames := make(map[string]string, len(languages))
	for _, language := range languages {
		displayNames[language.Code] = language.DisplayName
	}
	for _, item := range coverage {
		percent := 0
		if item.Eligible > 0 {
			percent = item.Published * 100 / item.Eligible
		}
		data.Languages = append(data.Languages, adminLanguageCoverageView{
			AdminLanguageCoverage: item,
			DisplayName:           displayNames[item.Language],
			CoveragePercent:       percent,
			ManageURL:             adminTranslationsLanguageURL(item.Language, store.AdminTranslationsUnpublished, 1),
			Route:                 routes[item.Language],
		})
	}

	if incidentValue != "" {
		incidentID, parseErr := strconv.ParseInt(incidentValue, 10, 64)
		if parseErr != nil || incidentID < 1 {
			http.Error(response, "incident must be a positive integer", http.StatusBadRequest)
			return
		}
		if err := s.populateAdminTranslationIncident(request, &data, incidentID, languages, routes); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.NotFound(response, request)
				return
			}
			s.internalError(response, request, "read translation incident", err)
			return
		}
	} else if languageCode != "" {
		language, registered := s.languageByCode(languageCode)
		if !registered || language.Canonical {
			http.Error(response, "language must be a registered translation language", http.StatusBadRequest)
			return
		}
		filter, ok := requestedAdminTranslationFilter(query.Get("status"))
		if !ok {
			http.Error(response, "invalid translation status", http.StatusBadRequest)
			return
		}
		page, pageErr := requestedPageParameter(request, "page")
		if pageErr != nil {
			http.Error(response, "invalid page", http.StatusBadRequest)
			return
		}
		items, total, listErr := s.store.ListAdminTranslationIncidents(request.Context(), s.options.PageSize, (page-1)*s.options.PageSize, s.options.SourceMode, languageCode, filter)
		if listErr != nil {
			s.internalError(response, request, "list translation incidents", listErr)
			return
		}
		totalPages := max(1, (total+s.options.PageSize-1)/s.options.PageSize)
		if page > totalPages {
			http.NotFound(response, request)
			return
		}
		selected := adminTranslationLanguagePage{
			Language: language, Filter: filter, FilterLabel: adminTranslationFilterLabel(filter), Page: page,
			TotalPages: totalPages, Total: total, HasPrevious: page > 1, HasNext: page < totalPages,
			Route: routes[languageCode],
		}
		selected.PreviousURL = adminTranslationsLanguageURL(languageCode, filter, page-1)
		selected.NextURL = adminTranslationsLanguageURL(languageCode, filter, page+1)
		for index := range data.Filters {
			data.Filters[index].Selected = data.Filters[index].Key == filter
			data.Filters[index].URL = adminTranslationsLanguageURL(languageCode, data.Filters[index].Key, 1)
		}
		for _, item := range items {
			selected.Incidents = append(selected.Incidents, adminTranslationIncidentView{
				AdminTranslationIncident: item,
				Published:                item.Title != "" && item.Summary != "",
				StatusLabel:              adminTranslationAttemptLabel(item.Status, item.StatusReason, item.Attempts),
				CanProcess:               data.translationActionsAvailable() && item.Status != "pending" && item.Status != "running",
				LanguageTag:              language.Tag.String(),
				DetailsURL:               "/admin/translations?incident=" + strconv.FormatInt(item.IncidentID, 10),
			})
		}
		data.SelectedLanguage = &selected
	}
	data.setNotice(query)

	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if err := s.adminTranslationsTemplate.ExecuteTemplate(response, "admin_translations", data); err != nil {
		s.logger.ErrorContext(request.Context(), "render translation operations page", "error", err)
	}
}

func (s *Server) populateAdminTranslationIncident(request *http.Request, data *adminTranslationsPage, incidentID int64, languages []readerLanguage, routes map[string]adminTranslationRouteView) error {
	record, err := s.store.GetPresentationIncident(request.Context(), incidentID, store.PresentationScope{Language: s.canonicalLanguage().Code})
	if err != nil {
		return err
	}
	if !record.HasAI || !adminSourceStatusMatches(s.options.SourceMode, record.FetchStatus) {
		return store.ErrNotFound
	}
	translations, err := s.store.ListAdminTranslations(request.Context(), []int64{incidentID}, translatedLanguageCodes(languages))
	if err != nil {
		return err
	}
	byLanguage := make(map[string]store.AdminTranslation, len(translations))
	for _, translation := range translations {
		byLanguage[translation.Language] = translation
	}
	view := adminTranslationIncidentPage{Record: record}
	for _, language := range languages {
		translation := byLanguage[language.Code]
		view.Translations = append(view.Translations, adminTranslationDetailView{
			AdminTranslation: translation,
			Language:         language,
			Published:        translation.Title != "" && translation.Summary != "",
			StatusLabel:      adminTranslationAttemptLabel(translation.Status, translation.StatusReason, translation.Attempts),
			CanProcess:       data.translationActionsAvailable() && translation.Status != "pending" && translation.Status != "running",
			Route:            routes[language.Code],
		})
	}
	data.SelectedIncident = &view
	return nil
}

func adminSourceStatusMatches(sourceMode, fetchStatus string) bool {
	if sourceMode == "fixture" {
		return fetchStatus == "fixture"
	}
	return fetchStatus == "pending" || fetchStatus == "fetched" || fetchStatus == "error"
}

func (s *Server) processTranslations(response http.ResponseWriter, request *http.Request) {
	if !s.preparePostProcessingMutation(response, request, "translation processing") {
		return
	}
	languageCode := strings.TrimSpace(request.PostForm.Get("language"))
	language, registered := s.languageByCode(languageCode)
	if !registered || language.Canonical {
		http.Error(response, "language must be a registered translation language", http.StatusBadRequest)
		return
	}
	model := strings.TrimSpace(request.PostForm.Get("model"))
	adapter := strings.TrimSpace(request.PostForm.Get("adapter"))
	if adapter == "" {
		adapter = processing.TranslationAdapterStructured
	}
	models, err := s.options.Processor.ModelStatus(request.Context())
	if err != nil {
		s.internalError(response, request, "read translation model status", err)
		return
	}
	processor := translationProcessorStatus(models)
	if processor == nil || !processor.Manual {
		http.Error(response, "translation processing is unavailable", http.StatusServiceUnavailable)
		return
	}
	if !models.CatalogAvailable || !containsString(models.Models, model) {
		http.Error(response, "selected Ollama model is unavailable", http.StatusServiceUnavailable)
		return
	}
	if !processing.TranslationAdapterSupports(adapter, languageCode) {
		http.Error(response, "selected translation adapter does not support this language", http.StatusBadRequest)
		return
	}

	action := strings.TrimSpace(request.PostForm.Get("action"))
	processingRequest := processing.PostProcessingRequest{
		ProcessorKey: processing.TranslationModelStep,
		ScopeKeys:    []string{languageCode},
		Model:        model,
		AdapterKey:   adapter,
	}
	switch action {
	case "incident":
		incidentID, parseErr := strconv.ParseInt(request.PostForm.Get("incident_id"), 10, 64)
		if parseErr != nil || incidentID < 1 {
			http.Error(response, "incident_id must be a positive integer", http.StatusBadRequest)
			return
		}
		processingRequest.IncidentID = &incidentID
		processingRequest.Selection = processing.PostProcessingSelectionAll
	case "unpublished":
		if strings.TrimSpace(request.PostForm.Get("incident_id")) != "" {
			http.Error(response, "incident_id is only valid for an incident action", http.StatusBadRequest)
			return
		}
		processingRequest.Selection = processing.PostProcessingSelectionUnpublished
	case "all":
		if strings.TrimSpace(request.PostForm.Get("incident_id")) != "" {
			http.Error(response, "incident_id is only valid for an incident action", http.StatusBadRequest)
			return
		}
		processingRequest.Selection = processing.PostProcessingSelectionAll
	default:
		http.Error(response, "action must be incident, unpublished, or all", http.StatusBadRequest)
		return
	}
	queued, err := s.options.Processor.RequestPostProcessing(request.Context(), processingRequest)
	if s.handlePostProcessingError(response, request, "queue translation work", err) {
		return
	}
	target := translationReturnURL(request.PostForm, languageCode)
	query := target.Query()
	query.Set("queued", strconv.Itoa(queued))
	query.Set("queued_action", action)
	query.Set("queued_language", languageCode)
	target.RawQuery = query.Encode()
	http.Redirect(response, request, target.RequestURI(), http.StatusSeeOther)
}

func (s *Server) updateTranslationPreference(response http.ResponseWriter, request *http.Request) {
	if !s.preparePostProcessingMutation(response, request, "translation preference") {
		return
	}
	languageCode := strings.TrimSpace(request.PostForm.Get("language"))
	language, registered := s.languageByCode(languageCode)
	if !registered || language.Canonical {
		http.Error(response, "language must be a registered translation language", http.StatusBadRequest)
		return
	}
	model := strings.TrimSpace(request.PostForm.Get("model"))
	adapter := strings.TrimSpace(request.PostForm.Get("adapter"))
	if !processing.TranslationAdapterSupports(adapter, languageCode) {
		http.Error(response, "selected translation adapter does not support this language", http.StatusBadRequest)
		return
	}
	if err := s.options.Processor.SetTranslationLanguageSetting(request.Context(), languageCode, model, adapter); err != nil {
		if errors.Is(err, processing.ErrModelUnavailable) {
			http.Error(response, "selected Ollama model is unavailable", http.StatusServiceUnavailable)
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			http.Error(response, "translation language or adapter is unavailable", http.StatusBadRequest)
			return
		}
		s.internalError(response, request, "update translation preference", err)
		return
	}
	target := &url.URL{Path: "/admin/translations"}
	if strings.TrimSpace(request.PostForm.Get("return_language")) == languageCode {
		target, _ = url.Parse(adminTranslationsLanguageURL(languageCode, store.AdminTranslationsUnpublished, 1))
	}
	query := target.Query()
	query.Set("preference_language", languageCode)
	target.RawQuery = query.Encode()
	http.Redirect(response, request, target.RequestURI(), http.StatusSeeOther)
}

func requestedAdminTranslationFilter(value string) (store.AdminTranslationFilter, bool) {
	if value == "" {
		return store.AdminTranslationsUnpublished, true
	}
	filter := store.AdminTranslationFilter(value)
	for _, candidate := range adminTranslationFilters {
		if candidate.Key == filter {
			return filter, true
		}
	}
	return "", false
}

func adminTranslationsLanguageURL(language string, filter store.AdminTranslationFilter, page int) string {
	values := url.Values{"language": {language}, "status": {string(filter)}}
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	return "/admin/translations?" + values.Encode()
}

func translationReturnURL(form url.Values, language string) *url.URL {
	if incidentID, err := strconv.ParseInt(form.Get("return_incident"), 10, 64); err == nil && incidentID > 0 {
		return &url.URL{Path: "/admin/translations", RawQuery: url.Values{"incident": {strconv.FormatInt(incidentID, 10)}}.Encode()}
	}
	filter, ok := requestedAdminTranslationFilter(form.Get("return_status"))
	if !ok {
		filter = store.AdminTranslationsUnpublished
	}
	page := positiveFormInt(form.Get("return_page"))
	target, _ := url.Parse(adminTranslationsLanguageURL(language, filter, page))
	return target
}

func translationProcessorStatus(models processing.PipelineModelStatus) *processing.PostProcessorModelStatus {
	for index := range models.PostProcessors {
		if models.PostProcessors[index].Key == processing.TranslationModelStep {
			return &models.PostProcessors[index]
		}
	}
	return nil
}

func adminTranslationRoutes(processor *processing.PostProcessorModelStatus, models []string) map[string]adminTranslationRouteView {
	routes := make(map[string]adminTranslationRouteView)
	if processor == nil {
		return routes
	}
	for _, scope := range processor.Scopes {
		route := adminTranslationRouteView{
			PreferredModel: scope.PreferredModel,
			AdapterKey:     scope.AdapterKey,
			Available:      scope.PreferredAvailable,
			Enabled:        scope.Enabled,
		}
		preferredListed := false
		for _, model := range models {
			selected := model == scope.PreferredModel
			preferredListed = preferredListed || selected
			route.Models = append(route.Models, adminTranslationModelOption{Value: model, Selected: selected})
		}
		if scope.PreferredModel != "" && !preferredListed {
			// Keep an unavailable configured identity visible and selected instead
			// of letting the browser silently select another installed model.
			route.Models = append([]adminTranslationModelOption{{Value: scope.PreferredModel, Selected: true}}, route.Models...)
		}
		for _, adapter := range processing.TranslationAdapterOptions(scope.Key) {
			route.Adapters = append(route.Adapters, adminTranslationAdapterOption{Value: adapter.Key, Label: adapter.DisplayName, Selected: adapter.Key == scope.AdapterKey})
			if adapter.Key == scope.AdapterKey {
				route.AdapterLabel = adapter.DisplayName
			}
		}
		routes[scope.Key] = route
	}
	return routes
}

func translatedLanguageCodes(languages []readerLanguage) []string {
	codes := make([]string, 0, len(languages))
	for _, language := range languages {
		codes = append(codes, language.Code)
	}
	return codes
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func adminTranslationFilterLabel(filter store.AdminTranslationFilter) string {
	for _, candidate := range adminTranslationFilters {
		if candidate.Key == filter {
			return candidate.Label
		}
	}
	return string(filter)
}

func adminTranslationAttemptLabel(status, reason string, attempts int) string {
	if status == "" {
		return "Never queued"
	}
	label := adminPostProcessingStatus(status, reason, attempts)
	if label == "" {
		return ""
	}
	return strings.ToUpper(label[:1]) + label[1:]
}

func (data *adminTranslationsPage) setNotice(query url.Values) {
	if enabledValue := query.Get("processor_automatic_enabled"); enabledValue != "" {
		enabled := enabledValue == "true"
		skipped, _ := nonNegativeQueryIntValues(query, "processor_automatic_skipped")
		state := "disabled"
		if enabled {
			state = "enabled"
		}
		data.Notice = fmt.Sprintf("Automatic translations are %s. %d queued automatic job(s) were skipped; running and manual jobs remain unaffected.", state, skipped)
		return
	}
	if scope := query.Get("automatic_scope"); scope != "" && query.Get("automatic_processor") == processing.TranslationModelStep {
		label := scope
		for _, item := range data.Languages {
			if item.Language == scope {
				label = item.DisplayName
				break
			}
		}
		enabled := query.Get("automatic_enabled") == "true"
		skipped, _ := nonNegativeQueryIntValues(query, "automatic_skipped")
		state := "disabled"
		if enabled {
			state = "enabled"
		}
		data.Notice = fmt.Sprintf("Automatic translation for %s is %s. %d queued automatic job(s) were skipped; manual translation actions remain available.", label, state, skipped)
		return
	}
	if language := query.Get("preference_language"); language != "" {
		label := language
		for _, item := range data.Languages {
			if item.Language == language {
				label = item.DisplayName
				break
			}
		}
		data.Notice = fmt.Sprintf("Preferred translation model and adapter updated for %s. New jobs will use this route; queued jobs remain frozen.", label)
		return
	}
	queued, ok := nonNegativeQueryIntValues(query, "queued")
	if !ok {
		return
	}
	action := query.Get("queued_action")
	language := query.Get("queued_language")
	label := map[string]string{"incident": "incident translation", "unpublished": "unpublished translation", "all": "translation rerun"}[action]
	if label == "" {
		label = "translation"
	}
	data.Notice = fmt.Sprintf("Queued %d %s job(s) for %s. Published results remain available while replacements run.", queued, label, language)
	data.NoticeIsWarning = queued == 0
}

func (data adminTranslationsPage) translationActionsAvailable() bool {
	return data.ProcessingEnabled && data.TranslationModel != nil && data.TranslationModel.Manual && data.Models.CatalogAvailable && len(data.Models.Models) > 0
}

func nonNegativeQueryIntValues(query url.Values, key string) (int, bool) {
	value := query.Get(key)
	parsed, err := strconv.Atoi(value)
	return parsed, value != "" && err == nil && parsed >= 0
}

type adminTranslationsPage struct {
	Runtime                     processing.PipelineRuntimeStatus
	Canonical                   store.AdminCanonicalCoverage
	CanonicalBacklog            int
	Languages                   []adminLanguageCoverageView
	SelectedLanguage            *adminTranslationLanguagePage
	SelectedIncident            *adminTranslationIncidentPage
	Filters                     []adminTranslationFilterView
	ProcessingEnabled           bool
	Models                      processing.PipelineModelStatus
	TranslationModel            *processing.PostProcessorModelStatus
	TranslationActionsAvailable bool
	Notice                      string
	NoticeIsWarning             bool
	UpdatedAt                   time.Time
}

type adminTranslationModelOption struct {
	Value    string
	Selected bool
}

type adminTranslationAdapterOption struct {
	Value    string
	Label    string
	Selected bool
}

type adminTranslationRouteView struct {
	PreferredModel string
	AdapterKey     string
	AdapterLabel   string
	Available      bool
	Enabled        bool
	Models         []adminTranslationModelOption
	Adapters       []adminTranslationAdapterOption
}

type adminLanguageCoverageView struct {
	store.AdminLanguageCoverage
	DisplayName     string
	CoveragePercent int
	ManageURL       string
	Route           adminTranslationRouteView
}

type adminTranslationFilterView struct {
	Key      store.AdminTranslationFilter
	Label    string
	URL      string
	Selected bool
}

type adminTranslationLanguagePage struct {
	Language    readerLanguage
	Filter      store.AdminTranslationFilter
	FilterLabel string
	Incidents   []adminTranslationIncidentView
	Page        int
	TotalPages  int
	Total       int
	PreviousURL string
	NextURL     string
	HasPrevious bool
	HasNext     bool
	Route       adminTranslationRouteView
}

type adminTranslationIncidentView struct {
	store.AdminTranslationIncident
	Published   bool
	StatusLabel string
	CanProcess  bool
	LanguageTag string
	DetailsURL  string
}

type adminTranslationIncidentPage struct {
	Record       store.IncidentRecord
	Translations []adminTranslationDetailView
}

type adminTranslationDetailView struct {
	store.AdminTranslation
	Language    readerLanguage
	Published   bool
	StatusLabel string
	CanProcess  bool
	Route       adminTranslationRouteView
}
