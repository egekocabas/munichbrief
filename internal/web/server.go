package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/netip"
	"regexp"
	"sync"
	"time"

	contactservice "github.com/egekocabas/munichbrief/internal/contact"
	"github.com/egekocabas/munichbrief/internal/gazetteer"
	langregistry "github.com/egekocabas/munichbrief/internal/languages"
	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/store"
)

//go:embed templates/layout.html
var layoutTemplate string

//go:embed templates/timeline.html
var timelineTemplate string

//go:embed templates/detail.html
var detailTemplate string

//go:embed templates/contact.html
var contactTemplate string

//go:embed templates/about.html
var aboutTemplate string

//go:embed templates/admin.html
var adminTemplate string

//go:embed templates/admin_shared.html
var adminSharedTemplate string

//go:embed templates/admin_history.html
var adminHistoryTemplate string

//go:embed templates/admin_rss_history.html
var adminRSSHistoryTemplate string

//go:embed templates/admin_gazetteer.html
var adminGazetteerTemplate string

//go:embed templates/admin_translations.html
var adminTranslationsTemplate string

//go:embed templates/admin_verifications.html
var adminVerificationsTemplate string

//go:embed static/app.css
var stylesheet []byte

//go:embed static/htmx.min.js
var htmxScript []byte

//go:embed static/head-support.js
var headSupportScript []byte

//go:embed static/theme.js
var themeScript []byte

//go:embed static/navigation.js
var navigationScript []byte

//go:embed static/admin.js
var adminScript []byte

//go:embed static/favicon.svg
var favicon []byte

//go:embed static/olympiapark-background.png
var socialCardBackground []byte

//go:embed static/eu-ai-*.svg static/munichbrief-ai-generated-light.svg
var euAIAssetFiles embed.FS

//go:embed static/eu-ai-generated-white-50.png
var euAISocialLabel []byte

//go:embed fonts/NotoSansSC-VF.ttf
var notoSansSC []byte

//go:embed fonts/NotoSansDevanagari-VF.ttf
var notoSansDevanagari []byte

type incidentStore interface {
	ListReaderEntries(context.Context, store.ReaderQuery) (store.ReaderResult, error)
	ReaderAreas(context.Context, string, string) ([]string, error)
	ListPresentationEntries(context.Context, int, int, string, store.PresentationScope) ([]store.IncidentRecord, int, error)
	ListPublicIncidentLinks(context.Context, string, store.PresentationScope) ([]store.PublicIncidentLink, error)
	GetPresentationIncident(context.Context, int64, store.PresentationScope) (store.IncidentRecord, error)
	ListAdminIncidents(context.Context, int, int, string, store.PresentationScope, store.AdminIncidentFilter) ([]store.IncidentRecord, int, error)
	ListAdminTranslations(context.Context, []int64, []string) ([]store.AdminTranslation, error)
	AdminTranslationCoverage(context.Context, string, []string) (store.AdminCanonicalCoverage, []store.AdminLanguageCoverage, error)
	ListAdminTranslationIncidents(context.Context, int, int, string, string, store.AdminTranslationFilter) ([]store.AdminTranslationIncident, int, error)
	AdminVerificationCoverageFor(context.Context, string, store.AdminVerificationSpec) (store.AdminVerificationCoverage, error)
	ListAdminVerificationIncidents(context.Context, int, int, string, store.AdminVerificationSpec, store.AdminVerificationFilter) ([]store.AdminVerificationIncident, int, error)
	ListAdminCategoryVerifications(context.Context, []int64) ([]store.AdminCategoryVerification, error)
	ListAdminPublicAssistanceVerifications(context.Context, []int64) ([]store.AdminPublicAssistanceVerification, error)
	ListPipelineHistory(context.Context, string, int, *store.PipelineHistoryCursor, *store.PipelineHistoryCursor) (store.PipelineHistoryPage, error)
	ListRSSSyncHistory(context.Context, int, *store.RSSSyncHistoryCursor, *store.RSSSyncHistoryCursor) (store.RSSSyncHistoryPage, error)
	RSSCheckDetails(context.Context, int64, int, int64) (store.RSSCheckDetails, error)
	RSSDocumentSnapshot(context.Context, int64, int64) (store.RSSDocumentDetail, error)
	Ready(context.Context) error
}

// ProcessingRequester is the narrow pipeline surface used by review routes.
// Keeping it as an interface lets route tests exercise access rules without an
// Ollama server or background worker.
type ProcessingRequester interface {
	RequestNow(context.Context, string, map[string]string, *int64, bool) (store.PipelineRequestResult, error)
	ModelStatus(context.Context) (processing.PipelineModelStatus, error)
	SetPreferredStepModel(context.Context, string, string) error
	SetTranslationLanguageSetting(context.Context, string, string, string) error
	SetPostProcessingScopeEnabled(context.Context, string, string, bool) (int, error)
	SetPostProcessingProcessorEnabled(context.Context, string, bool) (int, error)
	RequestPostProcessing(context.Context, processing.PostProcessingRequest) (int, error)
	Status(context.Context) (processing.PipelineRuntimeStatus, error)
	SetAutomaticProcessing(context.Context, bool) error
	CancelAll(context.Context) (store.PipelineCancellationResult, error)
}

type GazetteerReader interface {
	AdminSnapshot(context.Context) (gazetteer.AdminSnapshot, error)
	RefreshHistory(context.Context, int) (gazetteer.RefreshHistoryPage, error)
	RefreshDetails(context.Context, int64) (gazetteer.RefreshRunDetails, error)
}

type GazetteerRefresher interface {
	RequestRefresh() gazetteer.RefreshRequestResult
}

// Options controls presentation and access behavior for a Server.
// PublicHosts identifies requests that must never reach review-only routes.
type Options struct {
	ContactEnabled                         bool
	ContactSecret                          string
	ContactTrustedProxies                  []netip.Prefix
	ContactMetrics                         *contactservice.Metrics
	ContactNotificationsConfigured         bool
	ContactDailyLimit, ContactMonthlyLimit int
	PageSize                               int
	SourceMode                             string
	PresentationMode                       string
	SecureCookies                          bool
	AdminEnabled                           bool
	PublicHosts                            []string
	CanonicalOrigin                        string
	Processor                              ProcessingRequester
	Gazetteer                              GazetteerReader
	GazetteerRefresher                     GazetteerRefresher
	Build                                  BuildInfo
}

// BuildInfo identifies the source revision and time used for a deployed build.
type BuildInfo struct {
	Commit  string
	BuiltAt time.Time
}

var gitCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Server owns MunichBrief's HTTP route tree and parsed embedded templates.
type Server struct {
	licensesAdminTemplate          *template.Template
	contactStore                   *store.Store
	contactMu                      sync.Mutex
	contactLimits                  map[string]contactRate
	contactAllowed, contactBlocked uint64
	legalTemplate                  *template.Template
	contactAdminTemplate           *template.Template
	store                          incidentStore
	logger                         *slog.Logger
	options                        Options
	location                       *time.Location
	languages                      []readerLanguage
	localization                   *localization
	timelineTemplate               *template.Template
	detailTemplate                 *template.Template
	aboutTemplate                  *template.Template
	contactTemplate                *template.Template
	adminTemplate                  *template.Template
	adminHistoryTemplate           *template.Template
	adminRSSHistoryTemplate        *template.Template
	adminGazetteerTemplate         *template.Template
	adminTranslationsTemplate      *template.Template
	adminVerificationsTemplate     *template.Template
	socialCards                    *socialCardRenderer
}

// NewWithOptions validates all route-affecting configuration before constructing
// a server. Invalid host or presentation settings fail startup rather than
// weakening the request boundary at runtime.
func NewWithOptions(database incidentStore, logger *slog.Logger, options Options) (*Server, error) {
	return newWithLanguages(database, logger, options, langregistry.Registered())
}

func newWithLanguages(database incidentStore, logger *slog.Logger, options Options, definitions []readerLanguage) (*Server, error) {
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
	if (options.Build.Commit == "") != options.Build.BuiltAt.IsZero() {
		return nil, errors.New("build commit and build time must be provided together")
	}
	if options.Build.Commit != "" && options.Build.Commit != "dev" && !gitCommitPattern.MatchString(options.Build.Commit) {
		return nil, errors.New("build commit must be dev or a full lowercase Git SHA")
	}
	if err := validateReaderLanguageDefinitions(definitions); err != nil {
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
	definitions = append([]readerLanguage(nil), definitions...)
	translations, err := newLocalization(definitions)
	if err != nil {
		return nil, fmt.Errorf("initialize localization: %w", err)
	}
	functions := template.FuncMap{
		"modelLicenseWarning":      modelLicenseWarning,
		"subtract":                 func(a, b int64) int64 { return a - b },
		"contactNotificationLabel": contactNotificationLabel,
		"contactDate":              func(v int64) string { return time.Unix(v, 0).In(location).Format("02 Jan 2006, 15:04 MST") },
		"rssFetchLabel":            rssFetchLabel,
		"assetURL":                 assetURL,
		"aiLabelAssetURL":          selectedAIGeneratedAssetURL,
		"aiLabelLightAssetURL":     selectedAILightThemeAssetURL,
		"excerpt":                  func(value string) string { return excerpt(value, 190) },
		"formatDateTime": func(language string, value time.Time) string {
			return formatDateTimeFor(definitions, language, value.In(location))
		},
		"formatISODate": func(value time.Time) string {
			return value.In(location).Format("2006-01-02")
		},
		"incidentURL":                incidentURL,
		"postProcessingStatusReason": postProcessingStatusReasonLabel,
		"pipelineStatusLabel":        pipelineStatusLabel,
		"t":                          translations.Text,
		"message":                    translations.Format,
		"tc":                         translations.Count,
		"shownTotal":                 translations.ShownTotal,
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
	contact, err := template.New("layout").Funcs(functions).Parse(layoutTemplate + contactTemplate)
	if err != nil {
		return nil, err
	}
	admin, err := template.New("admin").Funcs(functions).Parse(adminSharedTemplate + adminTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse admin template: %w", err)
	}
	adminHistory, err := template.New("admin_history").Funcs(functions).Parse(adminSharedTemplate + adminHistoryTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse admin history template: %w", err)
	}
	adminRSSHistory, err := template.New("admin_rss_history").Funcs(functions).Parse(adminSharedTemplate + adminRSSHistoryTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse admin RSS history template: %w", err)
	}
	adminGazetteer, err := template.New("admin_gazetteer").Funcs(functions).Parse(adminSharedTemplate + adminGazetteerTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse admin gazetteer template: %w", err)
	}
	adminTranslations, err := template.New("admin_translations").Funcs(functions).Parse(adminSharedTemplate + adminTranslationsTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse admin translations template: %w", err)
	}
	adminVerifications, err := template.New("admin_verifications").Funcs(functions).Parse(adminSharedTemplate + adminVerificationsTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse admin verifications template: %w", err)
	}
	legal, err := template.New("layout").Funcs(functions).Parse(layoutTemplate + legalTemplate)
	if err != nil {
		return nil, err
	}
	inbox, err := template.New("contact_admin").Funcs(functions).Parse(adminSharedTemplate + contactAdminTemplate)
	if err != nil {
		return nil, err
	}
	licenseAdmin, err := template.New("licenses_admin").Funcs(functions).Parse(adminSharedTemplate + licensesAdminTemplate)
	if err != nil {
		return nil, err
	}
	contactDatabase, _ := database.(*store.Store)
	if options.ContactEnabled && (contactDatabase == nil || len(options.ContactSecret) < 32 || !options.AdminEnabled) {
		return nil, errors.New("contact requires database, secret and protected admin")
	}
	if options.ContactDailyLimit <= 0 {
		options.ContactDailyLimit = 20
	}
	if options.ContactMonthlyLimit <= 0 {
		options.ContactMonthlyLimit = 300
	}
	if options.ContactMetrics == nil {
		options.ContactMetrics = &contactservice.Metrics{}
	}
	socialCards, err := newSocialCardRenderer()
	if err != nil {
		return nil, fmt.Errorf("initialize social card renderer: %w", err)
	}
	return &Server{licensesAdminTemplate: licenseAdmin, contactStore: contactDatabase, contactLimits: make(map[string]contactRate), legalTemplate: legal, contactAdminTemplate: inbox, store: database, logger: logger, options: options, location: location, languages: definitions, localization: translations, timelineTemplate: timeline, detailTemplate: detail, aboutTemplate: about, contactTemplate: contact, adminTemplate: admin, adminHistoryTemplate: adminHistory, adminRSSHistoryTemplate: adminRSSHistory, adminGazetteerTemplate: adminGazetteer, adminTranslationsTemplate: adminTranslations, adminVerificationsTemplate: adminVerifications, socialCards: socialCards}, nil
}

// Handler returns the complete public and optional review route tree.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.redirectRoot)
	mux.HandleFunc("GET /about", s.redirectLegacyAbout)
	mux.HandleFunc("GET /incidents/{id}", s.redirectLegacyIncident)
	mux.HandleFunc("GET /robots.txt", s.robots)
	mux.HandleFunc("GET /sitemap.xml", s.sitemap)
	mux.HandleFunc("POST /ai-disclosure/acknowledge", s.acknowledgeAIDisclosure)
	for _, language := range s.languages {
		mux.HandleFunc("GET /"+language.Code, s.timeline)
		mux.HandleFunc("GET /"+language.Code+"/search", s.timeline)
		mux.HandleFunc("POST /"+language.Code+"/search", s.submitSearch)
		mux.HandleFunc("POST /"+language.Code+"/search/clear", s.submitSearch)
		mux.HandleFunc("GET /"+language.Code+"/contact", s.contact)
		mux.HandleFunc("POST /"+language.Code+"/contact", s.submitContact)
		mux.HandleFunc("GET /"+language.Code+"/impressum", s.legal)
		mux.HandleFunc("GET /"+language.Code+"/privacy", s.legal)
		mux.HandleFunc("GET /"+language.Code+"/incidents/{id}", s.detail)
		mux.HandleFunc("GET /"+language.Code+"/about", s.about)
	}
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /static/{asset}", serveStaticAsset)
	mux.HandleFunc("GET /social/{language}/home", s.socialHome)
	mux.HandleFunc("GET /social/{language}/about", s.socialAbout)
	mux.HandleFunc("GET /social/{language}/incidents/{id}", s.socialIncident)
	if s.options.AdminEnabled {
		mux.HandleFunc("GET /admin", s.admin)
		mux.HandleFunc("GET /admin/licenses", s.adminLicenses)
		mux.HandleFunc("GET /admin/contact", s.contactAdmin)
		mux.HandleFunc("GET /admin/contact/{id}", s.contactAdmin)
		mux.HandleFunc("POST /admin/contact/{id}", s.contactAdminMutation)
		mux.HandleFunc("POST /admin/contact/settings", s.contactAdminSettings)
		mux.HandleFunc("POST /admin/contact/test", s.contactAdminTest)
		mux.HandleFunc("GET /admin/translations", s.adminTranslationsPage)
		mux.HandleFunc("GET /admin/verifications", s.adminVerificationsPage)
		mux.HandleFunc("GET /admin/rss-history", s.adminRSSHistory)
		mux.HandleFunc("GET /admin/rss-history/{id}", s.adminRSSDetails)
		mux.HandleFunc("GET /admin/rss-history/{id}/documents/{document}", s.adminRSSDetails)
		mux.HandleFunc("GET /admin/gazetteer", s.adminGazetteer)
		mux.HandleFunc("GET /admin/gazetteer/{id}", s.adminGazetteerDetails)
		mux.HandleFunc("POST /api/admin/gazetteer/refresh", s.refreshGazetteer)
		mux.HandleFunc("GET /admin/history", s.adminHistory)
		mux.HandleFunc("GET /api/admin/ai/status", s.pipelineStatus)
		mux.HandleFunc("POST /api/admin/ai/process-now", s.processIncidentNow)
		mux.HandleFunc("POST /api/admin/ai/process-all-now", s.processAllNow)
		mux.HandleFunc("POST /api/admin/ai/reprocess-all", s.reprocessAll)
		mux.HandleFunc("POST /api/admin/ai/step-model", s.updatePreferredStepModel)
		mux.HandleFunc("POST /api/admin/ai/post-processing/process", s.processPostProcessing)
		mux.HandleFunc("POST /api/admin/ai/post-processing/enabled", s.updatePostProcessingScopeEnabled)
		mux.HandleFunc("POST /api/admin/ai/post-processing/processor-enabled", s.updatePostProcessingProcessorEnabled)
		mux.HandleFunc("POST /api/admin/ai/translations/process", s.processTranslations)
		mux.HandleFunc("POST /api/admin/ai/translations/preference", s.updateTranslationPreference)
		mux.HandleFunc("POST /api/admin/ai/automatic-processing", s.updateAutomaticProcessing)
		mux.HandleFunc("POST /api/admin/ai/cancel-all", s.cancelAllProcessing)
	}
	return s.requestLogger(s.accessBoundary(mux))
}
