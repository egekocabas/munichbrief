package web

import (
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

const (
	aiDisclosureCookieName = "munichbrief_ai_disclosure"
	aiDisclosureVersion    = "v1"
	aiDisclosureMaxAge     = 30 * 24 * 60 * 60
)

type aiLabelPreview struct {
	AssetName string
	Variant   string
	Incident  incidentView
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
	groups := make([]aiLabelPreviewGroup, 0, len(specifications))
	recordIndex := 0
	for _, specification := range specifications {
		group := aiLabelPreviewGroup{Name: specification.name}
		for _, asset := range specification.assets {
			group.Items = append(group.Items, aiLabelPreview{
				AssetName: asset.name,
				Variant:   asset.variant,
				Incident:  s.incidentForLanguage(records[recordIndex%len(records)], language),
			})
			recordIndex++
		}
		groups = append(groups, group)
	}
	return groups
}
