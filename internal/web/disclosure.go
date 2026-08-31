package web

import (
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	aiDisclosureCookieName = "munichbrief_ai_disclosure"
	aiDisclosureVersion    = "v1"
	aiDisclosureMaxAge     = 30 * 24 * 60 * 60
)

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
	http.Redirect(response, request, s.safeDisclosureReturn(request.FormValue("return_to"), s.preferredLanguage(request)), http.StatusSeeOther)
}

func (s *Server) safeDisclosureReturn(raw, fallbackLanguage string) string {
	fallback := "/" + fallbackLanguage
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || path.Clean(parsed.Path) != parsed.Path {
		return fallback
	}
	for _, language := range s.languages {
		root := "/" + language.Code
		if parsed.Path == root || parsed.Path == root+"/about" || strings.HasPrefix(parsed.Path, root+"/incidents/") {
			return parsed.RequestURI()
		}
	}
	return fallback
}
