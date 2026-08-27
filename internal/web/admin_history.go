package web

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

func (s *Server) adminHistory(response http.ResponseWriter, request *http.Request) {
	before, err := requestedPipelineHistoryCursor(request, "before")
	if err != nil {
		http.Error(response, "invalid pipeline history cursor", http.StatusBadRequest)
		return
	}
	after, err := requestedPipelineHistoryCursor(request, "after")
	if err != nil {
		http.Error(response, "invalid pipeline history cursor", http.StatusBadRequest)
		return
	}
	if before != nil && after != nil {
		http.Error(response, "choose either before or after", http.StatusBadRequest)
		return
	}
	page, err := s.store.ListPipelineHistory(request.Context(), s.options.SourceMode, s.options.PageSize, before, after)
	if err != nil {
		s.internalError(response, request, "list pipeline history", err)
		return
	}
	if (before != nil || after != nil) && len(page.Entries) == 0 {
		http.NotFound(response, request)
		return
	}

	data := adminHistoryPage{Entries: page.Entries, HasNewer: page.HasNewer, HasOlder: page.HasOlder}
	if len(page.Entries) > 0 {
		first := page.Entries[0]
		last := page.Entries[len(page.Entries)-1]
		data.NewerURL = pipelineHistoryURL("after", store.PipelineHistoryCursor{UpdatedAt: first.UpdatedAt, JobID: first.JobID})
		data.OlderURL = pipelineHistoryURL("before", store.PipelineHistoryCursor{UpdatedAt: last.UpdatedAt, JobID: last.JobID})
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if err := s.adminHistoryTemplate.ExecuteTemplate(response, "admin_history", data); err != nil {
		s.logger.ErrorContext(request.Context(), "render admin pipeline history", "error", err)
	}
}

type adminHistoryPage struct {
	Entries  []store.PipelineHistoryEntry
	HasNewer bool
	HasOlder bool
	NewerURL string
	OlderURL string
}

func requestedPipelineHistoryCursor(request *http.Request, name string) (*store.PipelineHistoryCursor, error) {
	raw := request.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 128 {
		return nil, errors.New("cursor is too long")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(decoded), ":")
	if len(parts) != 2 {
		return nil, errors.New("invalid cursor fields")
	}
	nanoseconds, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, err
	}
	jobID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || jobID < 1 {
		return nil, errors.New("invalid cursor job")
	}
	return &store.PipelineHistoryCursor{UpdatedAt: time.Unix(0, nanoseconds).UTC(), JobID: jobID}, nil
}

func pipelineHistoryURL(direction string, cursor store.PipelineHistoryCursor) string {
	payload := strconv.FormatInt(cursor.UpdatedAt.UnixNano(), 10) + ":" + strconv.FormatInt(cursor.JobID, 10)
	return "/admin/history?" + direction + "=" + base64.RawURLEncoding.EncodeToString([]byte(payload))
}
