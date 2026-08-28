package web

import (
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/store"
)

const (
	aiDisclosureCookieName = "munichbrief_ai_disclosure"
	aiDisclosureVersion    = "v1"
	aiDisclosureMaxAge     = 30 * 24 * 60 * 60
)

type aiLabelPreview struct {
	AssetName   string
	Variant     string
	DarkSurface bool
	Incident    incidentView
}

type aiLabelPreviewGroup struct {
	Name  string
	Items []aiLabelPreview
}

func aiDisclosureAcknowledged(request *http.Request) bool {
	cookie, err := request.Cookie(aiDisclosureCookieName)
	return err == nil && cookie.Value == aiDisclosureVersion
}

func (s *Server) acknowledgeAIDisclosure(response http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		http.Error(response, "invalid form", http.StatusBadRequest)
		return
	}
	now := time.Now()
	http.SetCookie(response, &http.Cookie{
		Name: aiDisclosureCookieName, Value: aiDisclosureVersion, Path: "/",
		MaxAge: aiDisclosureMaxAge, Expires: now.Add(30 * 24 * time.Hour),
		HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
	response.Header().Set("Cache-Control", "no-store")
	if request.Header.Get("HX-Request") == "true" {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		response.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(response, request, safeDisclosureReturn(request.FormValue("return_to"), preferredLanguage(request)), http.StatusSeeOther)
}

func safeDisclosureReturn(raw, fallbackLanguage string) string {
	fallback := "/" + fallbackLanguage
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || path.Clean(parsed.Path) != parsed.Path {
		return fallback
	}
	for _, language := range readerLanguages {
		root := "/" + language.Code
		if parsed.Path == root || parsed.Path == root+"/about" || strings.HasPrefix(parsed.Path, root+"/incidents/") {
			return parsed.RequestURI()
		}
	}
	return fallback
}

func (s *Server) aiLabelPreviewGroups(records []store.IncidentRecord, language string) []aiLabelPreviewGroup {
	if len(records) == 0 {
		return nil
	}
	specifications := []struct {
		name   string
		assets []struct{ name, variant string }
	}{
		{name: "AI GENERATED", assets: []struct{ name, variant string }{
			{name: "eu-ai-generated-black.svg", variant: "Black"},
			{name: "eu-ai-generated-white.svg", variant: "White"},
			{name: "eu-ai-generated-black-50.svg", variant: "Black 50%"},
			{name: "eu-ai-generated-white-50.svg", variant: "White 50%"},
		}},
	}
	fixtures := []struct {
		titleDE, summaryDE, titleEN, summaryEN string
		category, area, eventDate, dayPart     string
		assistance                             bool
	}{
		{
			titleDE:   "Radfahrerin bei Zusammenstoß in Maxvorstadt leicht verletzt",
			summaryDE: "Bei einem Zusammenstoß zwischen einem Fahrrad und einem Pkw wurde eine Person leicht verletzt. Die Polizei untersucht den Unfallhergang.",
			titleEN:   "Cyclist slightly injured in Maxvorstadt collision",
			summaryEN: "A collision involving a bicycle and a car left one person slightly injured. Police are investigating how the crash happened.",
			category:  "traffic", area: "Maxvorstadt", eventDate: "2026-08-21", dayPart: "morning",
		},
		{
			titleDE:   "Einbruch in Schwabinger Geschäft – Polizei sucht Zeugen",
			summaryDE: "Nach einem Einbruch in ein Geschäft in Schwabing ermittelt die Polizei und bittet Personen mit sachdienlichen Beobachtungen um Hinweise.",
			titleEN:   "Police seek witnesses after burglary at Schwabing shop",
			summaryEN: "Police are investigating a burglary at a shop in Schwabing and are asking anyone with relevant observations to come forward.",
			category:  "theft_burglary", area: "Schwabing", eventDate: "2026-08-21", dayPart: "night", assistance: true,
		},
		{
			titleDE:   "Polizeieinsatz in der Altstadt beendet",
			summaryDE: "Ein größerer Polizeieinsatz führte vorübergehend zu Absperrungen in der Altstadt. Nach Abschluss der Maßnahmen wurde der Bereich wieder freigegeben.",
			titleEN:   "Police operation in Munich Old Town concluded",
			summaryEN: "A larger police operation temporarily closed part of Munich's Old Town. The restrictions were lifted after the operation ended.",
			category:  "police_operation", area: "Altstadt", eventDate: "2026-08-20", dayPart: "evening",
		},
		{
			titleDE:   "Zwei Fahrzeuge bei Unfall in Sendling beschädigt",
			summaryDE: "Bei einem Verkehrsunfall in Sendling entstand Sachschaden an zwei Fahrzeugen. Beide konnten nach der Unfallaufnahme weiterfahren.",
			titleEN:   "Two vehicles damaged in Sendling collision",
			summaryEN: "Two vehicles were damaged in a collision in Sendling. Both could be driven away after police recorded the incident.",
			category:  "traffic", area: "Sendling", eventDate: "2026-08-19", dayPart: "morning",
		},
	}
	groups := make([]aiLabelPreviewGroup, 0, len(specifications))
	recordIndex := 0
	for _, specification := range specifications {
		group := aiLabelPreviewGroup{Name: specification.name}
		for _, asset := range specification.assets {
			fixture := fixtures[recordIndex%len(fixtures)]
			record := records[recordIndex%len(records)]
			record.HasAI = true
			record.AITitleDE, record.AISummaryDE = fixture.titleDE, fixture.summaryDE
			record.AITranslatedTitle, record.AITranslatedSummary = fixture.titleEN, fixture.summaryEN
			record.AICategory, record.AIAreaName, record.AIAreaType = fixture.category, fixture.area, "neighbourhood"
			record.AIEventStartDate, record.AIEventDayPart = fixture.eventDate, fixture.dayPart
			record.AIReportKind = "incident"
			record.AIPublicAssistanceStatus = "not_requested"
			record.AIPublicAssistanceTypes = "[]"
			if fixture.assistance {
				record.AIPublicAssistanceStatus = "requested"
				record.AIPublicAssistanceTypes = `["witness_observations"]`
			}
			record.AIMetadataModel, record.AIModel = "fixture-ai:3b", "fixture-ai:3b"
			record.AITranslationModel = "fixture-translate:4b"
			record.AIPipelineVersion = processing.PipelineVersion
			group.Items = append(group.Items, aiLabelPreview{
				AssetName:   asset.name,
				Variant:     asset.variant,
				DarkSurface: strings.Contains(asset.name, "-white"),
				Incident:    s.incidentForLanguage(record, language),
			})
			recordIndex++
		}
		groups = append(groups, group)
	}
	return groups
}
