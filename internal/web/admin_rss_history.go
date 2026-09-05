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

func (s *Server) adminRSSHistory(response http.ResponseWriter, request *http.Request) {
	before, err := requestedRSSSyncHistoryCursor(request, "before")
	if err != nil {
		http.Error(response, "invalid RSS history cursor", http.StatusBadRequest)
		return
	}
	after, err := requestedRSSSyncHistoryCursor(request, "after")
	if err != nil {
		http.Error(response, "invalid RSS history cursor", http.StatusBadRequest)
		return
	}
	if before != nil && after != nil {
		http.Error(response, "choose either before or after", http.StatusBadRequest)
		return
	}
	page, err := s.store.ListRSSSyncHistory(request.Context(), s.options.PageSize, before, after)
	if err != nil {
		s.internalError(response, request, "list RSS sync history", err)
		return
	}
	if (before != nil || after != nil) && len(page.Entries) == 0 {
		http.NotFound(response, request)
		return
	}

	data := adminRSSHistoryPage{Entries: page.Entries, HasNewer: page.HasNewer, HasOlder: page.HasOlder}
	if len(page.Entries) > 0 {
		first := page.Entries[0]
		last := page.Entries[len(page.Entries)-1]
		data.NewerURL = rssSyncHistoryURL("after", store.RSSSyncHistoryCursor{StartedAt: first.StartedAt, ID: first.ID})
		data.OlderURL = rssSyncHistoryURL("before", store.RSSSyncHistoryCursor{StartedAt: last.StartedAt, ID: last.ID})
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if err := s.adminRSSHistoryTemplate.ExecuteTemplate(response, "admin_rss_history", data); err != nil {
		s.logger.ErrorContext(request.Context(), "render admin RSS history", "error", err)
	}
}

type adminRSSHistoryPage struct {
	Entries  []store.RSSSyncHistoryEntry
	HasNewer bool
	HasOlder bool
	NewerURL string
	OlderURL string
}

func requestedRSSSyncHistoryCursor(request *http.Request, name string) (*store.RSSSyncHistoryCursor, error) {
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
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id < 1 {
		return nil, errors.New("invalid cursor attempt")
	}
	return &store.RSSSyncHistoryCursor{StartedAt: time.Unix(0, nanoseconds).UTC(), ID: id}, nil
}

func rssSyncHistoryURL(direction string, cursor store.RSSSyncHistoryCursor) string {
	payload := strconv.FormatInt(cursor.StartedAt.UnixNano(), 10) + ":" + strconv.FormatInt(cursor.ID, 10)
	return "/admin/rss-history?" + direction + "=" + base64.RawURLEncoding.EncodeToString([]byte(payload))
}
