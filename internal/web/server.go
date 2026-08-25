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
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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

//go:embed static/app.css
var stylesheet []byte

//go:embed static/htmx.min.js
var htmxScript []byte

type incidentStore interface {
	ListPresentationEntries(context.Context, int, int, string, store.PresentationScope) ([]store.IncidentRecord, int, error)
	GetPresentationIncident(context.Context, int64, store.PresentationScope) (store.IncidentRecord, error)
	Ready(context.Context) error
	ProcessingQueueStats(context.Context, string, time.Time) (store.ProcessingStats, error)
	RetryProcessingJobs(context.Context, string, *int64, time.Time) (int64, error)
}

type Options struct {
	PageSize         int
	SourceMode       string
	PresentationMode string
	ModelIdentity    string
	PromptVersion    string
	SecureCookies    bool
	AdminEnabled     bool
	PublicHosts      []string
}

type Server struct {
	store            incidentStore
	logger           *slog.Logger
	options          Options
	location         *time.Location
	localization     *localization
	timelineTemplate *template.Template
	detailTemplate   *template.Template
	aboutTemplate    *template.Template
	adminTemplate    *template.Template
}

type basePage struct {
	Lang                   string
	HomeURL                string
	AboutURL               string
	AlternateLanguage      string
	AlternateLanguageURL   string
	AlternateLanguageLabel string
	Fixture                bool
	Review                 bool
	ModeLabel              string
}

type incidentView struct {
	Record              store.IncidentRecord
	Title               string
	Summary             string
	ContentLanguage     string
	ProcessingState     string
	ProcessingLabel     string
	ShowOriginalMessage bool
}

type timelinePage struct {
	basePage
	Groups      []dayGroup
	Page        int
	TotalPages  int
	Total       int
	Previous    int
	Next        int
	HasPrevious bool
	HasNext     bool
}

type dayGroup struct {
	ID        string
	Label     string
	Incidents []incidentView
}

type detailPage struct {
	basePage
	Incident            incidentView
	ShowOriginalSection bool
}

type aboutPage struct{ basePage }

type adminPage struct {
	Stats           store.ProcessingStats
	OldestPending   string
	Notice          string
	NoticeIsWarning bool
}

func New(database incidentStore, logger *slog.Logger, pageSize int, sourceMode string) (*Server, error) {
	return NewWithOptions(database, logger, Options{
		PageSize: pageSize, SourceMode: sourceMode, PresentationMode: "review",
		ModelIdentity: "qwen3.5:4b", PromptVersion: processing.PromptVersion,
	})
}

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
	if strings.TrimSpace(options.ModelIdentity) == "" || strings.TrimSpace(options.PromptVersion) == "" {
		return nil, errors.New("presentation model and prompt version are required")
	}
	publicHosts, err := normalizePublicHosts(options.PublicHosts)
	if err != nil {
		return nil, err
	}
	options.PublicHosts = publicHosts
	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return nil, fmt.Errorf("load Europe/Berlin timezone: %w", err)
	}
	translations, err := newLocalization()
	if err != nil {
		return nil, fmt.Errorf("initialize localization: %w", err)
	}
	functions := template.FuncMap{
		"excerpt":        func(value string) string { return excerpt(value, 190) },
		"formatDateTime": func(language string, value time.Time) string { return formatDateTime(language, value.In(location)) },
		"incidentURL":    func(language string, id int64) string { return fmt.Sprintf("/%s/incidents/%d", language, id) },
		"t":              translations.Text,
		"tc":             translations.Count,
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
	admin, err := template.New("admin").Parse(adminTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse admin template: %w", err)
	}
	return &Server{store: database, logger: logger, options: options, location: location, localization: translations, timelineTemplate: timeline, detailTemplate: detail, aboutTemplate: about, adminTemplate: admin}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.redirectRoot)
	mux.HandleFunc("GET /about", s.redirectLegacyAbout)
	mux.HandleFunc("GET /incidents/{id}", s.redirectLegacyIncident)
	for _, language := range []string{"de", "en"} {
		mux.HandleFunc("GET /"+language, s.timeline)
		mux.HandleFunc("GET /"+language+"/incidents/{id}", s.detail)
		mux.HandleFunc("GET /"+language+"/about", s.about)
	}
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /static/app.css", s.css)
	mux.HandleFunc("GET /static/htmx.min.js", s.javascript)
	if s.options.AdminEnabled {
		mux.HandleFunc("GET /admin", s.admin)
		mux.HandleFunc("POST /api/admin/ai/retry", s.retryIncident)
		mux.HandleFunc("POST /api/admin/ai/retry-all", s.retryAll)
	}
	return s.requestLogger(s.accessBoundary(mux))
}

func (s *Server) scope(request *http.Request) store.PresentationScope {
	return store.PresentationScope{
		Operation: processing.Operation(s.options.ModelIdentity), ModelIdentity: s.options.ModelIdentity,
		PromptVersion: s.options.PromptVersion, PublicOnly: s.options.PresentationMode == "public" || s.isPublicRequest(request),
	}
}

func (s *Server) redirectRoot(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(response, request)
		return
	}
	target := &url.URL{Path: "/" + preferredLanguage(request), RawQuery: request.URL.RawQuery}
	http.Redirect(response, request, target.RequestURI(), http.StatusFound)
}

func (s *Server) redirectLegacyAbout(response http.ResponseWriter, request *http.Request) {
	target := &url.URL{Path: "/" + preferredLanguage(request) + "/about", RawQuery: request.URL.RawQuery}
	http.Redirect(response, request, target.RequestURI(), http.StatusFound)
}

func (s *Server) redirectLegacyIncident(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(response, request)
		return
	}
	target := &url.URL{Path: fmt.Sprintf("/%s/incidents/%d", preferredLanguage(request), id), RawQuery: request.URL.RawQuery}
	http.Redirect(response, request, target.RequestURI(), http.StatusFound)
}

func (s *Server) base(request *http.Request, language string) basePage {
	modeLabel := s.localization.Text(language, "FixtureMode")
	if s.options.SourceMode == "live" {
		modeLabel = s.localization.Text(language, "LiveSource")
	}
	if s.options.PresentationMode == "review" && !s.isPublicRequest(request) {
		modeLabel += " · " + s.localization.Text(language, "ReviewMode")
	}
	alternate := "en"
	alternateLabel := s.localization.Text(language, "SwitchToEnglish")
	if language == "en" {
		alternate = "de"
		alternateLabel = s.localization.Text(language, "SwitchToGerman")
	}
	return basePage{
		Lang: language, HomeURL: "/" + language, AboutURL: "/" + language + "/about",
		AlternateLanguage: alternate, AlternateLanguageURL: alternateLanguageURL(request.URL, language, alternate), AlternateLanguageLabel: alternateLabel,
		Fixture: s.options.SourceMode == "fixture", Review: s.options.PresentationMode == "review" && !s.isPublicRequest(request), ModeLabel: modeLabel,
	}
}

func prepareHTML(response http.ResponseWriter, language string, review bool) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Content-Language", language)
	response.Header().Set("Vary", "Cookie, Accept-Language")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if review {
		response.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	}
}

func (s *Server) timeline(response http.ResponseWriter, request *http.Request) {
	language, ok := routeLanguage(request)
	if !ok {
		http.NotFound(response, request)
		return
	}
	s.setLanguagePreference(response, language)
	page, err := requestedPage(request)
	if err != nil {
		http.Error(response, "invalid page", http.StatusBadRequest)
		return
	}
	incidents, total, err := s.store.ListPresentationEntries(request.Context(), s.options.PageSize, (page-1)*s.options.PageSize, s.options.SourceMode, s.scope(request))
	if err != nil {
		s.internalError(response, request, "list incidents", err)
		return
	}
	totalPages := max(1, (total+s.options.PageSize-1)/s.options.PageSize)
	if page > totalPages && total > 0 {
		http.NotFound(response, request)
		return
	}
	base := s.base(request, language)
	data := timelinePage{
		basePage: base, Groups: s.groupByDay(incidents, base.Lang), Page: page, TotalPages: totalPages, Total: total,
		Previous: page - 1, Next: page + 1, HasPrevious: page > 1, HasNext: page < totalPages,
	}
	prepareHTML(response, base.Lang, base.Review)
	if err := s.timelineTemplate.ExecuteTemplate(response, "layout", data); err != nil {
		s.logger.ErrorContext(request.Context(), "render timeline", "error", err)
	}
}

func (s *Server) detail(response http.ResponseWriter, request *http.Request) {
	language, ok := routeLanguage(request)
	if !ok {
		http.NotFound(response, request)
		return
	}
	s.setLanguagePreference(response, language)
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(response, request)
		return
	}
	incident, err := s.store.GetPresentationIncident(request.Context(), id, s.scope(request))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(response, request)
		return
	}
	if err != nil {
		s.internalError(response, request, "get incident", err)
		return
	}
	if (s.options.SourceMode == "fixture" && incident.FetchStatus != "fixture") || (s.options.SourceMode == "live" && incident.FetchStatus == "fixture") {
		http.NotFound(response, request)
		return
	}
	base := s.base(request, language)
	view := s.incidentForLanguage(incident, base.Lang)
	data := detailPage{basePage: base, Incident: view, ShowOriginalSection: base.Review && incident.HasAI}
	prepareHTML(response, base.Lang, base.Review)
	if err := s.detailTemplate.ExecuteTemplate(response, "layout", data); err != nil {
		s.logger.ErrorContext(request.Context(), "render incident detail", "incident_id", id, "error", err)
	}
}

func (s *Server) about(response http.ResponseWriter, request *http.Request) {
	language, ok := routeLanguage(request)
	if !ok {
		http.NotFound(response, request)
		return
	}
	s.setLanguagePreference(response, language)
	base := s.base(request, language)
	prepareHTML(response, base.Lang, base.Review)
	if err := s.aboutTemplate.ExecuteTemplate(response, "layout", aboutPage{basePage: base}); err != nil {
		s.logger.ErrorContext(request.Context(), "render about page", "error", err)
	}
}

func (s *Server) admin(response http.ResponseWriter, request *http.Request) {
	stats, err := s.store.ProcessingQueueStats(request.Context(), processing.Operation(s.options.ModelIdentity), time.Now())
	if err != nil {
		s.internalError(response, request, "read admin processing statistics", err)
		return
	}
	data := adminPage{Stats: stats}
	if stats.OldestPendingAge > 0 {
		data.OldestPending = stats.OldestPendingAge.Round(time.Second).String()
	} else {
		data.OldestPending = "None"
	}
	if raw := request.URL.Query().Get("retried"); raw != "" {
		count, err := strconv.ParseInt(raw, 10, 64)
		if err == nil && count >= 0 {
			if count == 0 {
				data.Notice = "No current failed or review-required AI jobs matched the request."
				data.NoticeIsWarning = true
			} else {
				data.Notice = fmt.Sprintf("Queued %d AI processing job(s) for retry.", count)
			}
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if err := s.adminTemplate.ExecuteTemplate(response, "admin", data); err != nil {
		s.logger.ErrorContext(request.Context(), "render admin page", "error", err)
	}
}

func (s *Server) retryIncident(response http.ResponseWriter, request *http.Request) {
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
	incidentID, err := strconv.ParseInt(request.PostForm.Get("incident_id"), 10, 64)
	if err != nil || incidentID < 1 {
		http.Error(response, "incident_id must be a positive integer", http.StatusBadRequest)
		return
	}
	count, err := s.store.RetryProcessingJobs(request.Context(), processing.Operation(s.options.ModelIdentity), &incidentID, time.Now())
	if err != nil {
		s.internalError(response, request, "retry incident AI processing", err)
		return
	}
	http.Redirect(response, request, fmt.Sprintf("/admin?retried=%d", count), http.StatusSeeOther)
}

func (s *Server) retryAll(response http.ResponseWriter, request *http.Request) {
	if !validAdminMutation(request) {
		http.Error(response, "cross-site request blocked", http.StatusForbidden)
		return
	}
	if !isFormPost(request) {
		http.Error(response, "form content type required", http.StatusUnsupportedMediaType)
		return
	}
	count, err := s.store.RetryProcessingJobs(request.Context(), processing.Operation(s.options.ModelIdentity), nil, time.Now())
	if err != nil {
		s.internalError(response, request, "retry all AI processing", err)
		return
	}
	http.Redirect(response, request, fmt.Sprintf("/admin?retried=%d", count), http.StatusSeeOther)
}

func validAdminMutation(request *http.Request) bool {
	if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme != "" && parsed.Hostname() != "" && strings.EqualFold(parsed.Hostname(), requestHostname(request))
}

func isFormPost(request *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/x-www-form-urlencoded"
}

func (s *Server) accessBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		adminPath := request.URL.Path == "/admin" || strings.HasPrefix(request.URL.Path, "/admin/") || request.URL.Path == "/api/admin" || strings.HasPrefix(request.URL.Path, "/api/admin/")
		if adminPath && (!s.options.AdminEnabled || s.isPublicRequest(request)) {
			http.NotFound(response, request)
			return
		}
		if s.isPublicRequest(request) && !isPublicPath(request.URL.Path) {
			http.NotFound(response, request)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (s *Server) isPublicRequest(request *http.Request) bool {
	host := requestHostname(request)
	for _, publicHost := range s.options.PublicHosts {
		if strings.EqualFold(host, publicHost) {
			return true
		}
	}
	return false
}

func normalizePublicHosts(values []string) ([]string, error) {
	hosts := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		host := strings.ToLower(strings.TrimSpace(value))
		parsed, err := url.Parse("//" + host)
		if host == "" || err != nil || parsed.Hostname() != host || parsed.Port() != "" || strings.ContainsAny(host, "/@") {
			return nil, errors.New("public hosts must contain only hostnames without schemes, credentials, paths, or ports")
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	return hosts, nil
}

func requestHostname(request *http.Request) string {
	host := request.Host
	if parsed, err := url.Parse("//" + host); err == nil && parsed.Hostname() != "" {
		return strings.ToLower(parsed.Hostname())
	}
	return strings.ToLower(host)
}

func isPublicPath(path string) bool {
	if path == "/" || path == "/about" || path == "/healthz" || path == "/readyz" {
		return true
	}
	for _, prefix := range []string{"/de", "/en", "/incidents", "/static"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func (s *Server) setLanguagePreference(response http.ResponseWriter, language string) {
	http.SetCookie(response, &http.Cookie{
		Name: "munichbrief_language", Value: language, Path: "/", MaxAge: 365 * 24 * 60 * 60,
		Expires: time.Now().Add(365 * 24 * time.Hour), HttpOnly: true,
		Secure: s.options.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
}

func preferredLanguage(request *http.Request) string {
	if cookie, err := request.Cookie("munichbrief_language"); err == nil && (cookie.Value == "de" || cookie.Value == "en") {
		return cookie.Value
	}
	type preference struct {
		language string
		quality  float64
		order    int
	}
	preferences := make([]preference, 0)
	for order, part := range strings.Split(request.Header.Get("Accept-Language"), ",") {
		pieces := strings.Split(strings.TrimSpace(part), ";")
		language := strings.ToLower(strings.TrimSpace(pieces[0]))
		if index := strings.IndexByte(language, '-'); index >= 0 {
			language = language[:index]
		}
		if language != "de" && language != "en" {
			continue
		}
		quality := 1.0
		for _, parameter := range pieces[1:] {
			parameter = strings.TrimSpace(parameter)
			if strings.HasPrefix(parameter, "q=") {
				if parsed, err := strconv.ParseFloat(strings.TrimPrefix(parameter, "q="), 64); err == nil {
					quality = parsed
				}
			}
		}
		preferences = append(preferences, preference{language: language, quality: quality, order: order})
	}
	sort.SliceStable(preferences, func(i, j int) bool { return preferences[i].quality > preferences[j].quality })
	if len(preferences) > 0 && preferences[0].quality > 0 {
		return preferences[0].language
	}
	return "de"
}

func routeLanguage(request *http.Request) (string, bool) {
	language := strings.SplitN(strings.TrimPrefix(request.URL.Path, "/"), "/", 2)[0]
	return language, language == "de" || language == "en"
}

func alternateLanguageURL(value *url.URL, current, alternate string) string {
	currentPrefix := "/" + current
	alternatePrefix := "/" + alternate
	path := value.Path
	if path == currentPrefix {
		path = alternatePrefix
	} else if strings.HasPrefix(path, currentPrefix+"/") {
		path = alternatePrefix + strings.TrimPrefix(path, currentPrefix)
	} else {
		path = alternatePrefix
	}
	return (&url.URL{Path: path, RawQuery: value.RawQuery}).RequestURI()
}

func (s *Server) incidentForLanguage(record store.IncidentRecord, language string) incidentView {
	state := record.ProcessingState()
	view := incidentView{Record: record, ContentLanguage: "de", ProcessingState: strings.ReplaceAll(state, "_", "-"), ProcessingLabel: s.processingLabel(state, language)}
	if record.HasAI {
		view.ContentLanguage = language
		if language == "en" {
			view.Title, view.Summary = record.AITitleEN, record.AISummaryEN
		} else {
			view.Title, view.Summary = record.AITitleDE, record.AISummaryDE
		}
		return view
	}
	view.Title, view.Summary = record.TitleDE, excerpt(record.BodyDE, 190)
	view.ShowOriginalMessage = record.HasIncident
	return view
}

func (s *Server) processingLabel(state, language string) string {
	switch state {
	case "ready":
		return s.localization.Text(language, "AISummary")
	case "queued":
		return s.localization.Text(language, "Queued")
	case "running":
		return s.localization.Text(language, "Running")
	case "retrying":
		return s.localization.Text(language, "Retrying")
	case "needs_review":
		return s.localization.Text(language, "NeedsReview")
	case "failed":
		return s.localization.Text(language, "NeedsReview")
	default:
		return s.localization.Text(language, "NotProcessed")
	}
}

func (s *Server) groupByDay(incidents []store.IncidentRecord, language string) []dayGroup {
	groups := make([]dayGroup, 0)
	var currentDate string
	for _, incident := range incidents {
		localTime := incident.PublishedAt.In(s.location)
		date := localTime.Format("2006-01-02")
		if date != currentDate {
			groups = append(groups, dayGroup{ID: date, Label: formatDay(language, localTime)})
			currentDate = date
		}
		groups[len(groups)-1].Incidents = append(groups[len(groups)-1].Incidents, s.incidentForLanguage(incident, language))
	}
	return groups
}

func formatDay(language string, value time.Time) string {
	if language == "de" {
		return germanWeekdays[value.Weekday()] + ", " + value.Format("02.") + " " + germanMonths[value.Month()] + " " + value.Format("2006")
	}
	return value.Format("Monday, 02 January 2006")
}

func formatDateTime(language string, value time.Time) string {
	if language == "de" {
		return value.Format("02.") + " " + germanMonths[value.Month()] + value.Format(" 2006, 15:04 MST")
	}
	return value.Format("02 January 2006, 15:04 MST")
}

var germanMonths = map[time.Month]string{time.January: "Januar", time.February: "Februar", time.March: "März", time.April: "April", time.May: "Mai", time.June: "Juni", time.July: "Juli", time.August: "August", time.September: "September", time.October: "Oktober", time.November: "November", time.December: "Dezember"}
var germanWeekdays = map[time.Weekday]string{time.Sunday: "Sonntag", time.Monday: "Montag", time.Tuesday: "Dienstag", time.Wednesday: "Mittwoch", time.Thursday: "Donnerstag", time.Friday: "Freitag", time.Saturday: "Samstag"}

func (s *Server) health(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
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
	_, _ = response.Write([]byte("ready\n"))
}
func (s *Server) css(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/css; charset=utf-8")
	response.Header().Set("Cache-Control", "public, max-age=3600")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = response.Write(stylesheet)
}
func (s *Server) javascript(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	response.Header().Set("Cache-Control", "public, max-age=3600")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = response.Write(htmxScript)
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
		response.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Frame-Options", "DENY")
		recorder := &statusRecorder{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		s.logger.InfoContext(request.Context(), "http request", "method", request.Method, "path", request.URL.Path, "status", recorder.status, "duration_ms", time.Since(startedAt).Milliseconds(), "request_id", requestID, "correlation_id", correlationID)
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
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
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
