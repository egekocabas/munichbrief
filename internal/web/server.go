package web

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/processing"
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

//go:embed templates/admin.html
var adminTemplate string

//go:embed templates/admin_history.html
var adminHistoryTemplate string

//go:embed static/app.css
var stylesheet []byte

//go:embed static/htmx.min.js
var htmxScript []byte

//go:embed static/admin.js
var adminScript []byte

type incidentStore interface {
	ListPresentationEntries(context.Context, int, int, string, store.PresentationScope) ([]store.IncidentRecord, int, error)
	ListPublicIncidentLinks(context.Context, string, store.PresentationScope) ([]store.PublicIncidentLink, error)
	GetPresentationIncident(context.Context, int64, store.PresentationScope) (store.IncidentRecord, error)
	ListAdminIncidents(context.Context, int, int, string, store.PresentationScope, store.AdminIncidentFilter) ([]store.IncidentRecord, int, error)
	ListAdminTranslations(context.Context, []int64, []string, string) ([]store.AdminTranslation, error)
	ListPipelineHistory(context.Context, string, int, *store.PipelineHistoryCursor, *store.PipelineHistoryCursor) (store.PipelineHistoryPage, error)
	Ready(context.Context) error
}

// ProcessingRequester is the narrow pipeline surface used by review routes.
// Keeping it as an interface lets route tests exercise access rules without an
// Ollama server or background worker.
type ProcessingRequester interface {
	RequestNow(context.Context, string, map[string]string, *int64, bool) (store.PipelineRequestResult, error)
	ModelStatus(context.Context) (processing.PipelineModelStatus, error)
	SetPreferredStepModel(context.Context, string, string) error
	RetryTranslation(context.Context, int64, string, string) (int, error)
	BackfillTranslations(context.Context, string, string) (int, error)
	Status(context.Context) (processing.PipelineRuntimeStatus, error)
}

// Options controls presentation and access behavior for a Server.
// PublicHosts identifies requests that must never reach review-only routes.
type Options struct {
	PageSize         int
	SourceMode       string
	PresentationMode string
	PromptVersion    string
	SecureCookies    bool
	AdminEnabled     bool
	PublicHosts      []string
	CanonicalOrigin  string
	Processor        ProcessingRequester
}

// Server owns MunichBrief's HTTP route tree and parsed embedded templates.
type Server struct {
	store                incidentStore
	logger               *slog.Logger
	options              Options
	location             *time.Location
	localization         *localization
	timelineTemplate     *template.Template
	detailTemplate       *template.Template
	aboutTemplate        *template.Template
	adminTemplate        *template.Template
	adminHistoryTemplate *template.Template
}

// NewWithOptions validates all route-affecting configuration before constructing
// a server. Invalid host or presentation settings fail startup rather than
// weakening the request boundary at runtime.
func NewWithOptions(database incidentStore, logger *slog.Logger, options Options) (*Server, error) {
	if database == nil {
		return nil, errors.New("incident store is required")
	}
	if logger == nil {
		return nil, errors.New("logger is required")
	}
	if options.PageSize < 1 {
		return nil, errors.New("page size must be positive")
	}
	if options.SourceMode != "fixture" && options.SourceMode != "live" {
		return nil, errors.New("source mode must be fixture or live")
	}
	if options.PresentationMode != "review" && options.PresentationMode != "public" {
		return nil, errors.New("presentation mode must be review or public")
	}
	if strings.TrimSpace(options.PromptVersion) == "" {
		return nil, errors.New("presentation prompt version is required")
	}
	if err := validateReaderLanguages(); err != nil {
		return nil, fmt.Errorf("validate reader languages: %w", err)
	}
	publicHosts, err := normalizePublicHosts(options.PublicHosts)
	if err != nil {
		return nil, err
	}
	options.PublicHosts = publicHosts
	canonicalOrigin, err := normalizeCanonicalOrigin(options.CanonicalOrigin, publicHosts)
	if err != nil {
		return nil, err
	}
	options.CanonicalOrigin = canonicalOrigin
	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return nil, fmt.Errorf("load Europe/Berlin timezone: %w", err)
	}
	translations, err := newLocalization(readerLanguages)
	if err != nil {
		return nil, fmt.Errorf("initialize localization: %w", err)
	}
	functions := template.FuncMap{
		"excerpt":        func(value string) string { return excerpt(value, 190) },
		"formatDateTime": func(language string, value time.Time) string { return formatDateTime(language, value.In(location)) },
		"incidentURL":    incidentURL,
		"t":              translations.Text,
		"tc":             translations.Count,
		"shownTotal":     translations.ShownTotal,
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
	admin, err := template.New("admin").Funcs(functions).Parse(adminTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse admin template: %w", err)
	}
	adminHistory, err := template.New("admin_history").Funcs(functions).Parse(adminHistoryTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse admin history template: %w", err)
	}
	return &Server{store: database, logger: logger, options: options, location: location, localization: translations, timelineTemplate: timeline, detailTemplate: detail, aboutTemplate: about, adminTemplate: admin, adminHistoryTemplate: adminHistory}, nil
}

// Handler returns the complete public and optional review route tree.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.redirectRoot)
	mux.HandleFunc("GET /about", s.redirectLegacyAbout)
	mux.HandleFunc("GET /incidents/{id}", s.redirectLegacyIncident)
	mux.HandleFunc("GET /robots.txt", s.robots)
	mux.HandleFunc("GET /sitemap.xml", s.sitemap)
	for _, language := range readerLanguages {
		mux.HandleFunc("GET /"+language.Code, s.timeline)
		mux.HandleFunc("GET /"+language.Code+"/incidents/{id}", s.detail)
		mux.HandleFunc("GET /"+language.Code+"/about", s.about)
	}
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /static/app.css", s.css)
	mux.HandleFunc("GET /static/htmx.min.js", s.javascript)
	mux.HandleFunc("GET /static/admin.js", s.adminJavascript)
	if s.options.AdminEnabled {
		mux.HandleFunc("GET /admin", s.admin)
		mux.HandleFunc("GET /admin/history", s.adminHistory)
		mux.HandleFunc("GET /api/admin/ai/status", s.pipelineStatus)
		mux.HandleFunc("POST /api/admin/ai/process-now", s.processIncidentNow)
		mux.HandleFunc("POST /api/admin/ai/process-all-now", s.processAllNow)
		mux.HandleFunc("POST /api/admin/ai/reprocess-all", s.reprocessAll)
		mux.HandleFunc("POST /api/admin/ai/step-model", s.updatePreferredStepModel)
		mux.HandleFunc("POST /api/admin/ai/translation-retry", s.retryTranslation)
		mux.HandleFunc("POST /api/admin/ai/translation-backfill", s.backfillTranslations)
	}
	return s.requestLogger(s.accessBoundary(mux))
}
