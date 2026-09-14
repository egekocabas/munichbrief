package web

import (
	"html"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/licensing"
)

func TestInformationPageDatesMatchHTMLMarkdownAndMetadata(t *testing.T) {
	s, _ := newContactServer(t)
	for _, lang := range s.languages {
		for _, page := range []string{"about", "contact", "privacy", "impressum", "licenses"} {
			date := informationPageUpdatedAt(page)
			if page == "licenses" {
				var err error
				date, err = time.Parse(time.DateOnly, licensing.CreditsUpdatedAt())
				if err != nil {
					t.Fatal(err)
				}
			}
			iso := date.Format(time.DateOnly)
			label := s.formatIncidentDate(lang.Code, date)
			for _, accept := range []string{"text/html", "text/markdown"} {
				r := httptest.NewRequest("GET", "https://munichbrief.de/"+lang.Code+"/"+page, nil)
				r.Header.Set("Accept", accept)
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				if w.Code != 200 {
					t.Fatalf("%s/%s %s: status %d", lang.Code, page, accept, w.Code)
				}
				want := s.localization.Text(lang.Code, "LegalLastUpdated") + ": " + label
				if accept == "text/html" {
					want = `<time datetime="` + iso + `">` + html.EscapeString(label) + `</time>`
					if !strings.Contains(w.Body.String(), `"dateModified":"`+iso+`"`) {
						t.Errorf("%s/%s: missing matching structured modification date", lang.Code, page)
					}
				}
				if !strings.Contains(w.Body.String(), want) {
					t.Errorf("%s/%s %s: missing localized update date", lang.Code, page, accept)
				}
			}
		}
	}
}
