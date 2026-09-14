package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egekocabas/munichbrief/internal/licensing"
)

func TestCreditsAllLocalesAndMarkdown(t *testing.T) {
	s, _ := newContactServer(t)
	for _, lang := range s.languages {
		for _, accept := range []string{"text/html", "text/markdown"} {
			r := httptest.NewRequest("GET", "https://munichbrief.de/"+lang.Code+"/licenses", nil)
			r.Header.Set("Accept", accept)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("%s %s: %d", lang.Code, accept, w.Code)
			}
			body := w.Body.String()
			for _, text := range []string{s.localization.Text(lang.Code, "LicensesTitle"), s.localization.Text(lang.Code, "LicensesOriginal"), s.localization.Text(lang.Code, "LicensesModelIntro"), "Noto Sans", licensing.CreditsUpdatedAt(), "https://munichbrief.de/" + lang.Code + "/licenses"} {
				if !strings.Contains(body, text) {
					t.Errorf("%s %s missing %q", lang.Code, accept, text)
				}
			}
			for _, private := range []string{"salamandraTA", "Tower-Plus", "192.168.178.102", "Noncommercial eligibility has not been established"} {
				if strings.Contains(body, private) {
					t.Errorf("public %s contains internal review %q", lang.Code, private)
				}
			}
			if accept == "text/html" && (!strings.Contains(body, "<details") || !strings.Contains(body, `rel="canonical"`) || !strings.Contains(body, `hreflang="tr-TR"`)) {
				t.Errorf("%s: missing SSR disclosures or SEO metadata", lang.Code)
			}
		}
	}
}

func TestLicenceDownloadsAndHTMXMetadata(t *testing.T) {
	s, _ := newContactServer(t)
	n, _ := licensing.PublicNotice("munichbrief-mit")
	r := httptest.NewRequest("GET", "https://munichbrief.de/tr/licenses/text/munichbrief-mit", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != n.Text || w.Header().Get("X-Robots-Tag") != "noindex" || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatal("incorrect original notice response", w.Code, w.Header())
	}
	for _, id := range []string{"unknown", "debian-base-files", "%2e%2e", "%2Fetc%2Fpasswd"} {
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "https://munichbrief.de/en/licenses/text/"+id, nil))
		if w.Code != 404 {
			t.Errorf("%s = %d", id, w.Code)
		}
	}
	for _, history := range []bool{false, true} {
		r = httptest.NewRequest("GET", "https://munichbrief.de/de/licenses", nil)
		r.Header.Set("HX-Request", "true")
		if history {
			r.Header.Set("HX-History-Restore-Request", "true")
		}
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		for _, text := range []string{`<html lang="de-DE"`, `https://munichbrief.de/de/licenses`, `"dateModified":"` + licensing.CreditsUpdatedAt() + `"`} {
			if !strings.Contains(w.Body.String(), text) {
				t.Errorf("history=%v: missing %s", history, text)
			}
		}
	}
}
