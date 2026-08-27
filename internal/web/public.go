package web

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
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

func (s *Server) scope(request *http.Request) store.PresentationScope {
	return store.PresentationScope{
		PromptVersion: s.options.PromptVersion, PublicOnly: s.options.PresentationMode == "public" || s.isPublicRequest(request),
	}
}

func (s *Server) redirectRoot(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(response, request)
		return
	}
	language := preferredLanguage(request)
	target := &url.URL{Path: "/" + language, RawQuery: request.URL.RawQuery}
	canonicalTarget := "/" + language
	if page, err := requestedPage(request); err == nil {
		canonicalTarget = timelineURL(language, page)
	}
	s.prepareRedirectDiscovery(response, request, canonicalTarget)
	http.Redirect(response, request, target.RequestURI(), http.StatusFound)
}

func (s *Server) redirectLegacyAbout(response http.ResponseWriter, request *http.Request) {
	language := preferredLanguage(request)
	target := &url.URL{Path: "/" + language + "/about", RawQuery: request.URL.RawQuery}
	s.prepareRedirectDiscovery(response, request, "/"+language+"/about")
	http.Redirect(response, request, target.RequestURI(), http.StatusFound)
}

func (s *Server) redirectLegacyIncident(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(response, request)
		return
	}
	language := preferredLanguage(request)
	canonicalTarget := fmt.Sprintf("/%s/incidents/%d", language, id)
	target := &url.URL{Path: canonicalTarget, RawQuery: request.URL.RawQuery}
	s.prepareRedirectDiscovery(response, request, canonicalTarget)
	http.Redirect(response, request, target.RequestURI(), http.StatusFound)
}

func (s *Server) base(request *http.Request, language, canonicalRelativeURL string) basePage {
	alternate := "en"
	alternateLabel := s.localization.Text(language, "SwitchToEnglish")
	if language == "en" {
		alternate = "de"
		alternateLabel = s.localization.Text(language, "SwitchToGerman")
	}
	canonicalOrigin := s.canonicalOrigin(request)
	germanRelativeURL := localizedRelativeURL(canonicalRelativeURL, language, "de")
	englishRelativeURL := localizedRelativeURL(canonicalRelativeURL, language, "en")
	return basePage{
		Lang: language, HomeURL: "/" + language, AboutURL: "/" + language + "/about",
		AlternateLanguage: alternate, AlternateLanguageURL: alternateLanguageURL(request.URL, language, alternate), AlternateLanguageLabel: alternateLabel,
		CanonicalOrigin: canonicalOrigin, CanonicalURL: canonicalOrigin + canonicalRelativeURL,
		GermanCanonicalURL: canonicalOrigin + germanRelativeURL, EnglishCanonicalURL: canonicalOrigin + englishRelativeURL,
		Fixture: s.options.SourceMode == "fixture", Review: s.options.PresentationMode == "review" && !s.isPublicRequest(request),
	}
}

func (s *Server) prepareHTML(response http.ResponseWriter, request *http.Request, page basePage) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Content-Language", page.Lang)
	addVary(response.Header(), "Cookie", "Accept-Language")
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	s.setDocumentLinks(response.Header(), page)
	if s.scope(request).PublicOnly {
		response.Header().Set("Content-Signal", contentSignal)
		addVary(response.Header(), "Accept")
	}
	if page.Review {
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
	scope := s.scope(request)
	incidents, total, err := s.store.ListPresentationEntries(request.Context(), s.options.PageSize, (page-1)*s.options.PageSize, s.options.SourceMode, scope)
	if err != nil {
		s.internalError(response, request, "list incidents", err)
		return
	}
	totalPages := max(1, (total+s.options.PageSize-1)/s.options.PageSize)
	if page > totalPages && total > 0 {
		http.NotFound(response, request)
		return
	}
	base := s.base(request, language, timelineURL(language, page))
	if page > 1 {
		base.PreviousCanonicalURL = base.CanonicalOrigin + timelineURL(language, page-1)
	}
	if page < totalPages {
		base.NextCanonicalURL = base.CanonicalOrigin + timelineURL(language, page+1)
	}
	data := timelinePage{
		basePage: base, Groups: s.groupByDay(incidents, base.Lang), Page: page, TotalPages: totalPages, Total: total,
		Shown:    len(incidents),
		Previous: page - 1, Next: page + 1, HasPrevious: page > 1, HasNext: page < totalPages,
	}
	if scope.PublicOnly && wantsMarkdown(request.Header.Get("Accept")) {
		s.prepareMarkdown(response, base)
		s.renderTimelineMarkdown(response, data)
		return
	}
	s.prepareHTML(response, request, base)
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
	page, err := requestedPage(request)
	if err != nil {
		http.Error(response, "invalid page", http.StatusBadRequest)
		return
	}
	scope := s.scope(request)
	incident, err := s.store.GetPresentationIncident(request.Context(), id, scope)
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
	canonicalRelativeURL := fmt.Sprintf("/%s/incidents/%d", language, id)
	base := s.base(request, language, canonicalRelativeURL)
	view := s.incidentForLanguage(incident, base.Lang)
	data := detailPage{basePage: base, Incident: view, BackURL: timelineURL(base.Lang, page), ShowOriginalSection: base.Review && incident.HasAI}
	if scope.PublicOnly && wantsMarkdown(request.Header.Get("Accept")) {
		s.prepareMarkdown(response, base)
		s.renderDetailMarkdown(response, data)
		return
	}
	s.prepareHTML(response, request, base)
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
	base := s.base(request, language, "/"+language+"/about")
	if s.scope(request).PublicOnly && wantsMarkdown(request.Header.Get("Accept")) {
		s.prepareMarkdown(response, base)
		s.renderAboutMarkdown(response, aboutPage{basePage: base})
		return
	}
	s.prepareHTML(response, request, base)
	if err := s.aboutTemplate.ExecuteTemplate(response, "layout", aboutPage{basePage: base}); err != nil {
		s.logger.ErrorContext(request.Context(), "render about page", "error", err)
	}
}

func (s *Server) setLanguagePreference(response http.ResponseWriter, language string) {
	http.SetCookie(response, &http.Cookie{
		Name: "munichbrief_language", Value: language, Path: "/", MaxAge: 365 * 24 * 60 * 60,
		Expires: time.Now().Add(365 * 24 * time.Hour), HttpOnly: true,
		Secure: s.options.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) incidentForLanguage(record store.IncidentRecord, language string) incidentView {
	state := record.ProcessingState()
	view := incidentView{
		Record: record, ContentLanguage: "de",
		ProcessingState: strings.ReplaceAll(state, "_", "-"), ProcessingLabel: s.processingLabel(state, language),
	}
	if record.HasAI {
		view.CategoryLabel = processing.CategoryLabel(record.AICategory, language)
		view.AreaName = record.AIAreaName
		view.ReportKindLabel = s.metadataCodeLabel(language, "ReportKind", record.AIReportKind)
		view.EventLabel = s.localization.Text(language, "TimeStatedInReport")
		view.EventText = s.formatIncidentTime(record, language)
		if record.AIPublicAssistanceStatus == "requested" {
			view.PublicAssistance = true
			view.PublicAssistanceLabel = s.localization.Text(language, "PublicAssistanceRequested")
			var assistanceTypes []string
			if json.Unmarshal([]byte(record.AIPublicAssistanceTypes), &assistanceTypes) == nil {
				for _, code := range assistanceTypes {
					if label := s.metadataCodeLabel(language, "AssistanceType", code); label != "" {
						view.PublicAssistanceTypes = append(view.PublicAssistanceTypes, label)
					}
				}
			}
			view.PublicAssistanceTypesText = strings.Join(view.PublicAssistanceTypes, ", ")
		}
		view.ContentLanguage = language
		if language == "en" {
			view.Title, view.Summary = record.AITitleEN, record.AISummaryEN
		} else {
			view.Title, view.Summary = record.AITitleDE, record.AISummaryDE
		}
		if record.AILegacy {
			view.ProcessingSystem = s.localization.Text(language, "LegacyPipeline")
			view.ProcessingSteps = []processingStepView{{
				Number: 1, Name: s.localization.Text(language, "LegacyBilingualStep"),
				Model: record.AIModel, PromptVersion: record.AIPromptVersion, GeneratedAt: record.AIGeneratedAt,
			}}
		} else if record.AIPipelineVersion == processing.PipelineVersion {
			view.ProcessingSystem = s.localization.Text(language, "MetadataFirstPipeline")
			view.ProcessingSteps = []processingStepView{
				{Number: 1, Name: s.localization.Text(language, "IncidentMetadataStep"), Model: record.AIMetadataModel, PromptVersion: record.AIMetadataPromptVersion, GeneratedAt: record.AIMetadataGeneratedAt},
				{Number: 2, Name: s.localization.Text(language, "GermanPresentationStep"), Model: record.AIModel, PromptVersion: record.AIPromptVersion, GeneratedAt: record.AIGeneratedAt},
			}
			if language == "en" {
				view.ProcessingSteps = append(view.ProcessingSteps, processingStepView{
					Number: 3, Name: s.localization.Text(language, "EnglishTranslationStep"),
					Model: record.AITranslationModel, PromptVersion: record.AITranslationPromptVersion, GeneratedAt: record.AITranslationGeneratedAt,
				})
			}
		} else {
			view.ProcessingSystem = s.localization.Text(language, "V1FallbackPipeline")
			view.ProcessingSteps = []processingStepView{{
				Number: 1, Name: s.localization.Text(language, "GermanAnalysisStep"),
				Model: record.AIModel, PromptVersion: record.AIPromptVersion, GeneratedAt: record.AIGeneratedAt,
			}}
			if language == "en" {
				view.ProcessingSteps = append(view.ProcessingSteps, processingStepView{
					Number: 2, Name: s.localization.Text(language, "EnglishTranslationStep"),
					Model: record.AITranslationModel, PromptVersion: record.AITranslationPromptVersion, GeneratedAt: record.AITranslationGeneratedAt,
				})
			}
		}
		return view
	}
	view.Title, view.Summary = record.TitleDE, excerpt(record.BodyDE, 190)
	view.ShowOriginalMessage = record.HasIncident
	return view
}

func (s *Server) metadataCodeLabel(language, group, code string) string {
	if code == "" || code == "unknown" || code == "unclear" || code == "not_requested" {
		return ""
	}
	keys := map[string]map[string]string{
		"ReportKind":     {"incident": "ReportKindIncident", "follow_up": "ReportKindFollowUp", "missing_person": "ReportKindMissingPerson", "wanted_person": "ReportKindWantedPerson", "public_warning": "ReportKindPublicWarning", "other": "ReportKindOther"},
		"AssistanceType": {"witness_observations": "AssistanceWitnessObservations", "identify_person": "AssistanceIdentifyPerson", "locate_person": "AssistanceLocatePerson", "photo_video_material": "AssistancePhotoVideo", "vehicle_information": "AssistanceVehicleInformation", "property_information": "AssistancePropertyInformation", "other_information": "AssistanceOtherInformation"},
		"DayPart":        {"morning": "DayPartMorning", "midday": "DayPartMidday", "afternoon": "DayPartAfternoon", "evening": "DayPartEvening", "night": "DayPartNight"},
	}
	key := keys[group][code]
	if key == "" {
		return ""
	}
	return s.localization.Text(language, key)
}

func (s *Server) formatIncidentTime(record store.IncidentRecord, language string) string {
	if record.AIEventStartDate == "" {
		return ""
	}
	startDate, err := time.ParseInLocation("2006-01-02", record.AIEventStartDate, s.location)
	if err != nil {
		return ""
	}
	dateText := formatIncidentDate(language, startDate)
	start := dateText
	if part := s.metadataCodeLabel(language, "DayPart", record.AIEventDayPart); part != "" {
		start += ", " + part
	} else if record.AIEventStartTime != "" {
		start += ", " + record.AIEventStartTime
	}
	return start
}

func formatIncidentDate(language string, value time.Time) string {
	if language == "de" {
		return value.Format("02.") + " " + germanMonths[value.Month()] + " " + value.Format("2006")
	}
	return value.Format("02 January 2006")
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

type basePage struct {
	Lang                   string
	HomeURL                string
	AboutURL               string
	AlternateLanguage      string
	AlternateLanguageURL   string
	AlternateLanguageLabel string
	CanonicalOrigin        string
	CanonicalURL           string
	GermanCanonicalURL     string
	EnglishCanonicalURL    string
	PreviousCanonicalURL   string
	NextCanonicalURL       string
	Fixture                bool
	Review                 bool
}

type incidentView struct {
	Record                    store.IncidentRecord
	Title                     string
	Summary                   string
	ContentLanguage           string
	ProcessingState           string
	ProcessingLabel           string
	CategoryLabel             string
	AreaName                  string
	EventLabel                string
	EventText                 string
	ReportKindLabel           string
	PublicAssistance          bool
	PublicAssistanceLabel     string
	PublicAssistanceTypes     []string
	PublicAssistanceTypesText string
	ProcessingSystem          string
	ProcessingSteps           []processingStepView
	ShowOriginalMessage       bool
}

type processingStepView struct {
	Number        int
	Name          string
	Model         string
	PromptVersion string
	GeneratedAt   *time.Time
}

type timelinePage struct {
	basePage
	Groups      []dayGroup
	Shown       int
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
	BackURL             string
	ShowOriginalSection bool
}

type aboutPage struct{ basePage }

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

func requestedPage(request *http.Request) (int, error) {
	return requestedPageParameter(request, "page")
}

func requestedPageParameter(request *http.Request, parameter string) (int, error) {
	raw := request.URL.Query().Get(parameter)
	if raw == "" {
		return 1, nil
	}
	page, err := strconv.Atoi(raw)
	if err != nil || page < 1 {
		return 0, errors.New("page must be a positive integer")
	}
	return page, nil
}

func incidentURL(language string, id int64, page int) string {
	path := fmt.Sprintf("/%s/incidents/%d", language, id)
	if page <= 1 {
		return path
	}
	return (&url.URL{Path: path, RawQuery: url.Values{"page": {strconv.Itoa(page)}}.Encode()}).RequestURI()
}

func timelineURL(language string, page int) string {
	path := "/" + language
	if page <= 1 {
		return path
	}
	return (&url.URL{Path: path, RawQuery: url.Values{"page": {strconv.Itoa(page)}}.Encode()}).RequestURI()
}

func excerpt(value string, limit int) string {
	normalized := strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(normalized) <= limit {
		return normalized
	}
	runes := []rune(normalized)
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
