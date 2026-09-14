package web

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/gazetteer"
)

func (s *Server) adminGazetteer(response http.ResponseWriter, request *http.Request) {
	page, err := requestedPageParameter(request, "page")
	if err != nil {
		http.Error(response, "invalid gazetteer history page", http.StatusBadRequest)
		return
	}
	data := adminGazetteerPage{Enabled: s.options.Gazetteer != nil, CanRefresh: s.options.GazetteerRefresher != nil, UpdatedAt: time.Now().In(s.location), Notice: gazetteerNotice(request.URL.Query())}
	if s.options.Gazetteer != nil {
		snapshot, err := s.options.Gazetteer.AdminSnapshot(request.Context())
		if err != nil {
			s.internalError(response, request, "read gazetteer status", err)
			return
		}
		data.Snapshot = snapshot
		data.Ready = snapshot.Status.ActiveGeneration > 0 && snapshot.Status.EntryCount > 0
		history, err := s.options.Gazetteer.RefreshHistory(request.Context(), page)
		if err != nil {
			s.internalError(response, request, "read gazetteer refresh history", err)
			return
		}
		if page > 1 && len(history.Entries) == 0 {
			http.NotFound(response, request)
			return
		}
		data.History = history
		if history.HasNewer {
			data.NewerURL = gazetteerHistoryURL(page - 1)
		}
		if history.HasOlder {
			data.OlderURL = gazetteerHistoryURL(page + 1)
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if err := s.adminGazetteerTemplate.ExecuteTemplate(response, "admin_gazetteer", data); err != nil {
		s.logger.ErrorContext(request.Context(), "render gazetteer operations page", "error", err)
	}
}

type adminGazetteerPage struct {
	Enabled    bool
	CanRefresh bool
	Ready      bool
	Notice     string
	Snapshot   gazetteer.AdminSnapshot
	UpdatedAt  time.Time
	History    gazetteer.RefreshHistoryPage
	NewerURL   string
	OlderURL   string
}

func gazetteerNotice(query url.Values) string {
	switch query.Get("notice") {
	case "refresh_requested":
		return "Gazetteer refresh requested. This page will show source progress as it is recorded."
	case "refresh_running":
		return "A Gazetteer refresh is already running; no duplicate refresh was queued."
	case "refresh_pending":
		return "A manual Gazetteer refresh is already queued; no duplicate refresh was added."
	default:
		return ""
	}
}

func (s *Server) refreshGazetteer(response http.ResponseWriter, request *http.Request) {
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
		http.Error(response, "gazetteer refresh confirmation is required", http.StatusBadRequest)
		return
	}
	if s.options.Gazetteer == nil || s.options.GazetteerRefresher == nil {
		http.Error(response, "gazetteer refresh is disabled", http.StatusServiceUnavailable)
		return
	}
	notice := "refresh_requested"
	switch s.options.GazetteerRefresher.RequestRefresh() {
	case gazetteer.RefreshRequestRunning:
		notice = "refresh_running"
	case gazetteer.RefreshRequestPending:
		notice = "refresh_pending"
	}
	http.Redirect(response, request, "/admin/gazetteer?notice="+notice, http.StatusSeeOther)
}

func gazetteerHistoryURL(page int) string {
	if page <= 1 {
		return "/admin/gazetteer"
	}
	return "/admin/gazetteer?page=" + strconv.Itoa(page)
}

func (s *Server) adminGazetteerDetails(response http.ResponseWriter, request *http.Request) {
	if s.options.Gazetteer == nil {
		http.NotFound(response, request)
		return
	}
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(response, "invalid gazetteer refresh ID", http.StatusBadRequest)
		return
	}
	details, err := s.options.Gazetteer.RefreshDetails(request.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(response, request)
			return
		}
		s.internalError(response, request, "read gazetteer refresh details", err)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	templateName := "gazetteer_refresh_details"
	if request.URL.Query().Get("fragment") != "1" {
		templateName = "gazetteer_details_page"
	}
	if err := s.adminGazetteerTemplate.ExecuteTemplate(response, templateName, details); err != nil {
		s.logger.ErrorContext(request.Context(), "render gazetteer refresh details", "error", err)
	}
}

func gazetteerDiagnosticHint(run gazetteer.RefreshRunStatus) string {
	if run.Status == "interrupted" {
		return "The refresh has no final result. This can happen when the application stops or restarts; it does not indicate invalid source data."
	}
	if run.FailureStage == "http_status" {
		if strings.HasSuffix(run.ErrorMessage, "HTTP 429") {
			return "The source rate-limited the request. Automatic retries use a backoff."
		}
		if strings.HasSuffix(run.ErrorMessage, "HTTP 504") {
			return "The source gateway could not serve the request. Overpass may return this when shared resources are busy."
		}
	}
	return ""
}
