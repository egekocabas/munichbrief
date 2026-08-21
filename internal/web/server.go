package web

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egekocabas/munichbrief/internal/store"
)

//go:embed templates/layout.html
var layoutTemplate string

//go:embed templates/timeline.html
var timelineTemplate string

//go:embed templates/detail.html
var detailTemplate string

//go:embed templates/about.html
var aboutTemplate string

//go:embed static/app.css
var stylesheet []byte

type incidentStore interface {
	ListTimelineEntries(context.Context, int, int, string) ([]store.IncidentRecord, int, error)
	GetIncident(context.Context, int64) (store.IncidentRecord, error)
	Ready(context.Context) error
}

type Server struct {
	store            incidentStore
	logger           *slog.Logger
	pageSize         int
	sourceMode       string
	location         *time.Location
	timelineTemplate *template.Template
	detailTemplate   *template.Template
	aboutTemplate    *template.Template
}

type timelinePage struct {
	Groups      []dayGroup
	Page        int
	TotalPages  int
	Total       int
	Previous    int
	Next        int
	HasPrevious bool
	HasNext     bool
	Fixture     bool
	ModeLabel   string
}

type dayGroup struct {
	ID        string
	Label     string
	Incidents []store.IncidentRecord
}

type detailPage struct {
	Incident  store.IncidentRecord
	Fixture   bool
	ModeLabel string
}

type aboutPage struct {
	Fixture   bool
	ModeLabel string
}

func New(database incidentStore, logger *slog.Logger, pageSize int, sourceMode string) (*Server, error) {
	if database == nil {
		return nil, errors.New("incident store is required")
	}
	if logger == nil {
		return nil, errors.New("logger is required")
	}
	if pageSize < 1 {
		return nil, errors.New("page size must be positive")
	}
	if sourceMode != "fixture" && sourceMode != "live" {
		return nil, errors.New("source mode must be fixture or live")
	}

	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return nil, fmt.Errorf("load Europe/Berlin timezone: %w", err)
	}
	functions := template.FuncMap{
		"excerpt": func(value string) string { return excerpt(value, 190) },
		"formatDateTime": func(value time.Time) string {
			return value.In(location).Format("02 January 2006, 15:04 MST")
		},
	}

	timeline, err := template.New("layout").Funcs(functions).Parse(layoutTemplate + timelineTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse timeline templates: %w", err)
	}
	detail, err := template.New("layout").Funcs(functions).Parse(layoutTemplate + detailTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse detail templates: %w", err)
	}
	about, err := template.New("layout").Funcs(functions).Parse(layoutTemplate + aboutTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse about template: %w", err)
	}

	return &Server{
		store:            database,
		logger:           logger,
		pageSize:         pageSize,
		sourceMode:       sourceMode,
		location:         location,
		timelineTemplate: timeline,
		detailTemplate:   detail,
		aboutTemplate:    about,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.timeline)
	mux.HandleFunc("GET /incidents/{id}", s.detail)
	mux.HandleFunc("GET /about", s.about)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /static/app.css", s.css)
	return s.requestLogger(mux)
}

func (s *Server) about(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.aboutTemplate.ExecuteTemplate(response, "layout", aboutPage{
		Fixture:   s.sourceMode == "fixture",
		ModeLabel: modeLabel(s.sourceMode),
	}); err != nil {
		s.logger.Error("render about page", "error", err)
	}
}

func (s *Server) health(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("ok\n"))
}

func (s *Server) ready(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), time.Second)
	defer cancel()
	if err := s.store.Ready(ctx); err != nil {
		s.logger.ErrorContext(request.Context(), "readiness check failed", "error", err)
		http.Error(response, "not ready", http.StatusServiceUnavailable)
		return
	}
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("ready\n"))
}

func (s *Server) timeline(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(response, request)
		return
	}

	page, err := requestedPage(request)
	if err != nil {
		http.Error(response, "invalid page", http.StatusBadRequest)
		return
	}
	incidents, total, err := s.store.ListTimelineEntries(request.Context(), s.pageSize, (page-1)*s.pageSize, s.sourceMode)
	if err != nil {
		s.internalError(response, request, "list incidents", err)
		return
	}

	totalPages := max(1, (total+s.pageSize-1)/s.pageSize)
	if page > totalPages && total > 0 {
		http.NotFound(response, request)
		return
	}
	data := timelinePage{
		Groups:      s.groupByDay(incidents),
		Page:        page,
		TotalPages:  totalPages,
		Total:       total,
		Previous:    page - 1,
		Next:        page + 1,
		HasPrevious: page > 1,
		HasNext:     page < totalPages,
		Fixture:     s.sourceMode == "fixture",
		ModeLabel:   modeLabel(s.sourceMode),
	}

	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if err := s.timelineTemplate.ExecuteTemplate(response, "layout", data); err != nil {
		s.logger.ErrorContext(request.Context(), "render timeline", "error", err)
	}
}

func (s *Server) detail(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(response, request)
		return
	}

	incident, err := s.store.GetIncident(request.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(response, request)
		return
	}
	if err != nil {
		s.internalError(response, request, "get incident", err)
		return
	}
	if (s.sourceMode == "fixture" && incident.FetchStatus != "fixture") ||
		(s.sourceMode == "live" && incident.FetchStatus == "fixture") {
		http.NotFound(response, request)
		return
	}

	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if err := s.detailTemplate.ExecuteTemplate(response, "layout", detailPage{
		Incident:  incident,
		Fixture:   s.sourceMode == "fixture",
		ModeLabel: modeLabel(s.sourceMode),
	}); err != nil {
		s.logger.ErrorContext(request.Context(), "render incident detail", "incident_id", id, "error", err)
	}
}

func modeLabel(mode string) string {
	if mode == "live" {
		return "Live source"
	}
	return "Fixture mode"
}

func (s *Server) css(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/css; charset=utf-8")
	response.Header().Set("Cache-Control", "public, max-age=3600")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = response.Write(stylesheet)
}

func (s *Server) groupByDay(incidents []store.IncidentRecord) []dayGroup {
	groups := make([]dayGroup, 0)
	var currentDate string
	for _, incident := range incidents {
		localTime := incident.PublishedAt.In(s.location)
		date := localTime.Format("2006-01-02")
		if date != currentDate {
			groups = append(groups, dayGroup{
				ID:    date,
				Label: localTime.Format("Monday, 02 January 2006"),
			})
			currentDate = date
		}
		groups[len(groups)-1].Incidents = append(groups[len(groups)-1].Incidents, incident)
	}
	return groups
}

func (s *Server) internalError(response http.ResponseWriter, request *http.Request, message string, err error) {
	s.logger.ErrorContext(request.Context(), message, "error", err)
	http.Error(response, "internal server error", http.StatusInternalServerError)
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		requestID := validIdentifier(request.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = randomIdentifier()
		}
		correlationID := validIdentifier(request.Header.Get("X-Correlation-ID"))
		if correlationID == "" {
			correlationID = requestID
		}
		response.Header().Set("X-Request-ID", requestID)
		response.Header().Set("X-Correlation-ID", correlationID)
		response.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Frame-Options", "DENY")
		recorder := &statusRecorder{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		s.logger.InfoContext(request.Context(), "http request",
			"method", request.Method,
			"path", request.URL.Path,
			"status", recorder.status,
			"duration_ms", time.Since(startedAt).Milliseconds(),
			"request_id", requestID,
			"correlation_id", correlationID,
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(contents []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(contents)
}

func validIdentifier(value string) string {
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return ""
	}
	return value
}

func randomIdentifier() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}

func requestedPage(request *http.Request) (int, error) {
	raw := request.URL.Query().Get("page")
	if raw == "" {
		return 1, nil
	}
	page, err := strconv.Atoi(raw)
	if err != nil || page < 1 {
		return 0, errors.New("page must be a positive integer")
	}
	return page, nil
}

func excerpt(value string, limit int) string {
	normalized := strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(normalized) <= limit {
		return normalized
	}
	runes := []rune(normalized)
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
