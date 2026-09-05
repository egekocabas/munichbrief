package web

import (
	"net/http"
	"time"

	"github.com/egekocabas/munichbrief/internal/gazetteer"
)

func (s *Server) adminGazetteer(response http.ResponseWriter, request *http.Request) {
	data := adminGazetteerPage{Enabled: s.options.Gazetteer != nil, UpdatedAt: time.Now().In(s.location)}
	if s.options.Gazetteer != nil {
		snapshot, err := s.options.Gazetteer.AdminSnapshot(request.Context())
		if err != nil {
			s.internalError(response, request, "read gazetteer status", err)
			return
		}
		data.Snapshot = snapshot
		data.Ready = snapshot.Status.ActiveGeneration > 0 && snapshot.Status.EntryCount > 0
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if err := s.adminGazetteerTemplate.ExecuteTemplate(response, "admin_gazetteer", data); err != nil {
		s.logger.ErrorContext(request.Context(), "render gazetteer operations page", "error", err)
	}
}

type adminGazetteerPage struct {
	Enabled   bool
	Ready     bool
	Snapshot  gazetteer.AdminSnapshot
	UpdatedAt time.Time
}
