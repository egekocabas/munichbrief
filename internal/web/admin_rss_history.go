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

// RSS snapshots stay behind the same access boundary as every admin page.
func (s *Server) adminRSSDetails(response http.ResponseWriter, request *http.Request) {
	checkID, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || checkID < 1 {
		http.Error(response, "invalid check ID", http.StatusBadRequest)
		return
	}
	data := adminRSSDetailsPage{}
	templateName := "rss_check_details"
	if raw := request.PathValue("document"); raw != "" {
		detailID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || detailID < 1 {
			http.Error(response, "invalid document ID", http.StatusBadRequest)
			return
		}
		d, err := s.store.RSSDocumentSnapshot(request.Context(), checkID, detailID)
		if err != nil {
			s.rssDetailsError(response, request, err)
			return
		}
		data.Document = &d
		templateName = "rss_document_details"
	} else {
		var after int64
		if raw := request.URL.Query().Get("after"); raw != "" {
			after, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || after < 1 {
				http.Error(response, "invalid detail cursor", http.StatusBadRequest)
				return
			}
		}
		check, err := s.store.RSSCheckDetails(request.Context(), checkID, s.options.PageSize, after)
		if err != nil {
			s.rssDetailsError(response, request, err)
			return
		}
		data.Check = &check
		if check.HasOlder {
			data.NextURL = "/admin/rss-history/" + strconv.FormatInt(checkID, 10) + "?after=" + strconv.FormatInt(check.Documents[len(check.Documents)-1].ID, 10)
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if request.URL.Query().Get("fragment") != "1" {
		templateName = "rss_details_page"
	}
	if err := s.adminRSSHistoryTemplate.ExecuteTemplate(response, templateName, data); err != nil {
		s.logger.ErrorContext(request.Context(), "render RSS details", "error", err)
	}
}

type adminRSSDetailsPage struct {
	Check    *store.RSSCheckDetails
	Document *store.RSSDocumentDetail
	NextURL  string
}

func (s *Server) rssDetailsError(response http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(response, request)
		return
	}
	s.internalError(response, request, "read RSS details", err)
}

func rssFetchLabel(value string) string {
	switch value {
	case "not_needed":
		return "No fetch needed"
	case "outside_window":
		return "Outside ingestion window"
	case "pending":
		return "Pending"
	case "stored":
		return "Parsed and stored"
	case "fetch_failed":
		return "Fetch failed"
	case "parse_failed":
		return "Parse failed"
	case "storage_failed":
		return "Storage failed"
	case "interrupted":
		return "Interrupted"
	case "refresh_due":
		return "Scheduled refresh"
	case "retry":
		return "Retry after failure"
	case "pending_or_metadata_changed":
		return "First fetch or changed metadata"
	case "feed_changed":
		return "Feed changed"
	default:
		return value
	}
}
