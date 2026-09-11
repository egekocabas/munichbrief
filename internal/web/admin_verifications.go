package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/store"
)

var adminVerificationFilters = []adminVerificationFilterView{
	{Key: store.AdminVerificationsAttention, Label: "Attention"},
	{Key: store.AdminVerificationsUnverified, Label: "Unverified"},
	{Key: store.AdminVerificationsAll, Label: "All"},
	{Key: store.AdminVerificationsNeverQueued, Label: "Never queued"},
	{Key: store.AdminVerificationsActive, Label: "Active"},
	{Key: store.AdminVerificationsConfirmed, Label: "Confirmed"},
	{Key: store.AdminVerificationsCorrected, Label: "Corrected"},
	{Key: store.AdminVerificationsSkipped, Label: "Skipped"},
}

func (s *Server) adminVerificationsPage(response http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	processorKey := strings.TrimSpace(query.Get("processor"))
	scopeKey := strings.TrimSpace(query.Get("scope"))
	if (processorKey == "") != (scopeKey == "") {
		http.Error(response, "processor and scope must be provided together", http.StatusBadRequest)
		return
	}
	if processorKey == "" && (query.Has("status") || query.Has("page")) {
		http.Error(response, "status and page require a processor and scope", http.StatusBadRequest)
		return
	}

	models := processing.PipelineModelStatus{}
	var err error
	if s.options.Processor != nil {
		models, err = s.options.Processor.ModelStatus(request.Context())
		if err != nil {
			s.internalError(response, request, "read verification model status", err)
			return
		}
	}
	processors := verificationProcessorStatuses(models)
	data := adminVerificationsPage{
		ProcessingEnabled: s.options.Processor != nil,
		Models:            models,
		UpdatedAt:         time.Now().In(s.location),
	}
	for _, processor := range processors {
		for _, scope := range processor.Scopes {
			spec := adminVerificationSpec(processor, scope.Key)
			coverage, coverageErr := s.store.AdminVerificationCoverageFor(request.Context(), s.options.SourceMode, spec)
			if coverageErr != nil {
				s.internalError(response, request, "read verification coverage", coverageErr)
				return
			}
			data.Scopes = append(data.Scopes, adminVerificationScopeView{
				Processor: processor, Scope: scope, Coverage: coverage,
				ManageURL: adminVerificationsURL(processor.Key, scope.Key, store.AdminVerificationsAttention, 1),
			})
		}
	}

	if processorKey != "" {
		processor, scope, found := findVerificationScope(processors, processorKey, scopeKey)
		if !found {
			http.Error(response, "unsupported verification processor or scope", http.StatusBadRequest)
			return
		}
		filter, ok := requestedAdminVerificationFilter(query.Get("status"))
		if !ok {
			http.Error(response, "invalid verification status", http.StatusBadRequest)
			return
		}
		page, pageErr := requestedPageParameter(request, "page")
		if pageErr != nil {
			http.Error(response, "invalid page", http.StatusBadRequest)
			return
		}
		spec := adminVerificationSpec(processor, scope.Key)
		items, total, listErr := s.store.ListAdminVerificationIncidents(request.Context(), s.options.PageSize, (page-1)*s.options.PageSize, s.options.SourceMode, spec, filter)
		if listErr != nil {
			s.internalError(response, request, "list verification incidents", listErr)
			return
		}
		totalPages := max(1, (total+s.options.PageSize-1)/s.options.PageSize)
		if page > totalPages {
			http.NotFound(response, request)
			return
		}
		selected := adminVerificationSelectionView{
			Processor: processor, Scope: scope, Filter: filter,
			FilterLabel: adminVerificationFilterLabel(filter), Page: page, TotalPages: totalPages, Total: total,
			HasPrevious: page > 1, HasNext: page < totalPages,
		}
		for _, overview := range data.Scopes {
			if overview.Processor.Key == processor.Key && overview.Scope.Key == scope.Key {
				selected.Coverage = overview.Coverage
				break
			}
		}
		selected.PreviousURL = adminVerificationsURL(processor.Key, scope.Key, filter, page-1)
		selected.NextURL = adminVerificationsURL(processor.Key, scope.Key, filter, page+1)
		for _, definition := range adminVerificationFilters {
			definition.Selected = definition.Key == filter
			definition.URL = adminVerificationsURL(processor.Key, scope.Key, definition.Key, 1)
			selected.Filters = append(selected.Filters, definition)
		}
		for _, model := range models.Models {
			selected.Models = append(selected.Models, adminVerificationModelOption{Value: model, Selected: model == processor.Preferred})
		}
		selected.ActionsAvailable = data.ProcessingEnabled && processor.Manual && models.CatalogAvailable && len(models.Models) > 0
		for _, item := range items {
			view := adminVerificationIncidentView{
				AdminVerificationIncident: item,
				Verdict:                   adminVerificationVerdict(item.IsCorrect),
				StatusLabel:               adminVerificationAttemptLabel(item.Status, item.StatusReason, item.Attempts),
				RetainedSuccess:           item.IsCorrect != nil && item.Status != "" && item.Status != "succeeded",
				CanProcess:                selected.ActionsAvailable && canRetryPostProcessing(item.Status, item.StatusReason),
			}
			for index, field := range processor.Verification.Fields {
				view.Fields = append(view.Fields, adminVerificationFieldView{
					DisplayName: field.DisplayName, Original: item.OriginalValues[index], Effective: item.EffectiveValues[index],
					OriginalDisplay:  s.adminVerificationValueDisplay(field.OriginalKind, item.OriginalValues[index]),
					EffectiveDisplay: s.adminVerificationValueDisplay(field.OriginalKind, item.EffectiveValues[index]),
					Changed:          item.OriginalValues[index] != item.EffectiveValues[index],
					Monospace:        adminVerificationValueUsesFallback(field.OriginalKind),
				})
			}
			selected.Incidents = append(selected.Incidents, view)
		}
		data.Selected = &selected
	}
	data.setNotice(query)

	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if err := s.adminVerificationsTemplate.ExecuteTemplate(response, "admin_verifications", data); err != nil {
		s.logger.ErrorContext(request.Context(), "render verification operations page", "error", err)
	}
}

func verificationProcessorStatuses(models processing.PipelineModelStatus) []processing.PostProcessorModelStatus {
	defaultDefinitions := processing.DefaultPostProcessorRegistry().Definitions()
	statuses := append([]processing.PostProcessorModelStatus(nil), models.PostProcessors...)
	if len(statuses) == 0 {
		for _, definition := range defaultDefinitions {
			status := processing.PostProcessorModelStatus{
				Key: definition.Key, DisplayName: definition.DisplayName, Description: definition.Description,
				ModelSettingKey: definition.ModelSettingKey, Manual: definition.Manual, Verification: definition.Verification,
			}
			for _, scope := range definition.Scopes {
				status.Scopes = append(status.Scopes, processing.PostProcessorScopeStatus{Key: scope.Key, DisplayName: scope.DisplayName, StepKey: scope.Step.Key, PromptVersion: scope.Step.PromptVersion})
			}
			statuses = append(statuses, status)
		}
	}
	verification := make([]processing.PostProcessorModelStatus, 0, len(statuses))
	for _, status := range statuses {
		if status.Verification != nil {
			verification = append(verification, status)
		}
	}
	return verification
}

func findVerificationScope(processors []processing.PostProcessorModelStatus, processorKey, scopeKey string) (processing.PostProcessorModelStatus, processing.PostProcessorScopeStatus, bool) {
	for _, processor := range processors {
		if processor.Key != processorKey || processor.Verification == nil {
			continue
		}
		for _, scope := range processor.Scopes {
			if scope.Key == scopeKey {
				return processor, scope, true
			}
		}
	}
	return processing.PostProcessorModelStatus{}, processing.PostProcessorScopeStatus{}, false
}

func adminVerificationSpec(processor processing.PostProcessorModelStatus, scopeKey string) store.AdminVerificationSpec {
	spec := store.AdminVerificationSpec{ProcessorKey: processor.Key, ScopeKey: scopeKey, VerdictKind: processor.Verification.VerdictKind}
	for _, field := range processor.Verification.Fields {
		spec.Fields = append(spec.Fields, store.AdminVerificationFieldSpec{OriginalKind: field.OriginalKind, CorrectedKind: field.CorrectedKind})
	}
	return spec
}

func requestedAdminVerificationFilter(raw string) (store.AdminVerificationFilter, bool) {
	if raw == "" {
		return store.AdminVerificationsAttention, true
	}
	filter := store.AdminVerificationFilter(raw)
	for _, definition := range adminVerificationFilters {
		if definition.Key == filter {
			return filter, true
		}
	}
	return "", false
}

func adminVerificationFilterLabel(filter store.AdminVerificationFilter) string {
	for _, definition := range adminVerificationFilters {
		if definition.Key == filter {
			return definition.Label
		}
	}
	return string(filter)
}

func adminVerificationsURL(processorKey, scopeKey string, filter store.AdminVerificationFilter, page int) string {
	query := url.Values{}
	query.Set("processor", processorKey)
	query.Set("scope", scopeKey)
	query.Set("status", string(filter))
	query.Set("page", strconv.Itoa(max(page, 1)))
	return "/admin/verifications?" + query.Encode()
}

func adminVerificationAttemptLabel(status, reason string, attempts int) string {
	if status == "" {
		return "Never queued"
	}
	return strings.ReplaceAll(adminPostProcessingStatus(status, reason, attempts), "_", " ")
}

func (s *Server) adminVerificationValueDisplay(kind, value string) string {
	if value == "" {
		return "Unavailable"
	}
	switch kind {
	case "category":
		return s.metadataCodeLabel(adminLocaleCode, "Category", value)
	case "public_assistance_types":
		labels := s.adminAssistanceTypeLabels(value)
		if len(labels) > 0 {
			return strings.Join(labels, ", ")
		}
	case "public_assistance_status":
		return strings.ReplaceAll(value, "_", " ")
	default:
		var values []string
		if json.Unmarshal([]byte(value), &values) == nil && len(values) > 0 {
			return strings.Join(values, ", ")
		}
	}
	return value
}

func adminVerificationValueUsesFallback(kind string) bool {
	switch kind {
	case "category", "public_assistance_types", "public_assistance_status":
		return false
	default:
		return true
	}
}

type adminVerificationsPage struct {
	Notice            string
	NoticeIsWarning   bool
	ProcessingEnabled bool
	Models            processing.PipelineModelStatus
	Scopes            []adminVerificationScopeView
	Selected          *adminVerificationSelectionView
	UpdatedAt         time.Time
}

func (page *adminVerificationsPage) setNotice(query url.Values) {
	if scope := query.Get("automatic_scope"); scope != "" {
		enabled := query.Get("automatic_enabled") == "true"
		skipped, _ := nonNegativeQueryIntFromValues(query, "automatic_skipped")
		state := "disabled"
		if enabled {
			state = "enabled"
		}
		page.Notice = fmt.Sprintf("Automatic %s/%s processing is %s. %d queued automatic job(s) were skipped; manual verification actions remain available.", query.Get("automatic_processor"), scope, state, skipped)
		return
	}
	queued, ok := nonNegativeQueryIntFromValues(query, "post_processing_queued")
	if !ok {
		return
	}
	page.Notice = fmt.Sprintf("Queued %d %s/%s verification job(s). Existing successful results remain effective while replacements run.", queued, query.Get("processor"), query.Get("scope"))
	page.NoticeIsWarning = queued == 0
}

func nonNegativeQueryIntFromValues(query url.Values, name string) (int, bool) {
	raw := query.Get(name)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value >= 0
}

type adminVerificationScopeView struct {
	Processor processing.PostProcessorModelStatus
	Scope     processing.PostProcessorScopeStatus
	Coverage  store.AdminVerificationCoverage
	ManageURL string
}

type adminVerificationSelectionView struct {
	Processor        processing.PostProcessorModelStatus
	Scope            processing.PostProcessorScopeStatus
	Filter           store.AdminVerificationFilter
	FilterLabel      string
	Filters          []adminVerificationFilterView
	Models           []adminVerificationModelOption
	ActionsAvailable bool
	Coverage         store.AdminVerificationCoverage
	Incidents        []adminVerificationIncidentView
	Page             int
	TotalPages       int
	Total            int
	HasPrevious      bool
	HasNext          bool
	PreviousURL      string
	NextURL          string
}

type adminVerificationFilterView struct {
	Key      store.AdminVerificationFilter
	Label    string
	URL      string
	Selected bool
}

type adminVerificationModelOption struct {
	Value    string
	Selected bool
}

type adminVerificationIncidentView struct {
	store.AdminVerificationIncident
	Verdict         string
	StatusLabel     string
	RetainedSuccess bool
	CanProcess      bool
	Fields          []adminVerificationFieldView
}

type adminVerificationFieldView struct {
	DisplayName      string
	Original         string
	Effective        string
	OriginalDisplay  string
	EffectiveDisplay string
	Changed          bool
	Monospace        bool
}
