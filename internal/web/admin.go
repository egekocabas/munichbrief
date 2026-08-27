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
	scope := store.PresentationScope{
		PromptVersion: s.options.PromptVersion,
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
	data := adminPage{
		ProcessingEnabled: s.options.Processor != nil,
		UnprocessedPage:   unprocessedPage,
		AllPage:           allPage,
		Models:            models,
		Runtime:           runtime,
		Unprocessed: s.adminList(unprocessed, unprocessedTotal, unprocessedPage, unprocessedPages,
			adminPaginationURL(unprocessedPage-1, allPage), adminPaginationURL(unprocessedPage+1, allPage), true, allPage),
		AllIncidents: s.adminList(allIncidents, allTotal, allPage, allPages,
			adminPaginationURL(unprocessedPage, allPage-1), adminPaginationURL(unprocessedPage, allPage+1), false, unprocessedPage),
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
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if err := s.adminTemplate.ExecuteTemplate(response, "admin", data); err != nil {
		s.logger.ErrorContext(request.Context(), "render admin page", "error", err)
	}
}

func (s *Server) adminList(records []store.IncidentRecord, total, page, totalPages int, previousURL, nextURL string, allowProcessing bool, otherPage int) adminIncidentList {
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
		})
	}
	return adminIncidentList{
		Incidents: incidents, Shown: len(incidents), Total: total, Page: page, TotalPages: totalPages,
		PreviousURL: previousURL, NextURL: nextURL, HasPrevious: page > 1, HasNext: page < totalPages,
		AllowProcessing: allowProcessing,
		OtherPage:       otherPage,
	}
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
}

func adminProcessingLabel(state string) string {
	switch state {
	case "ready":
		return "Summarized and translated"
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
