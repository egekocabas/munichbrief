package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egekocabas/munichbrief/internal/licensing"
	"github.com/egekocabas/munichbrief/internal/processing"
)

func TestAdminLicenceBoundaryEscapingAndSelectedOrdering(t *testing.T) {
	db := fixtureStore(t)
	var c licensing.Component
	for _, item := range licensing.Components() {
		if item.ModelTag == "qwen3.5:4b" {
			c = item
		}
	}
	bad := `model:<script>alert(1)</script>`
	models := processing.PipelineModelStatus{Models: []string{c.ModelTag, bad}, ModelDigests: map[string]string{c.ModelTag: c.Digest}, CatalogAvailable: true, Ready: true, Steps: []processing.StepModelStatus{{Preferred: bad, DisplayName: "German presentation"}}}
	s, err := NewWithOptions(db, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{PageSize: 20, SourceMode: "fixture", PresentationMode: "public", PublicHosts: []string{"munichbrief.de"}, CanonicalOrigin: "https://munichbrief.de", AdminEnabled: true, Processor: fakeProcessingRequester{database: db, status: &models}})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"munichbrief.de", "admin.test"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://"+host+"/admin/licenses", nil))
		if host == "munichbrief.de" {
			if w.Code != 404 {
				t.Fatal("public admin exposure", w.Code)
			}
			continue
		}
		if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
			t.Fatal(w.Code, w.Header())
		}
		if strings.Contains(w.Body.String(), bad) || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
			t.Fatal("unescaped model tag")
		}
		if !strings.Contains(w.Body.String(), "Review needed") || !strings.Contains(w.Body.String(), "Reviewed") {
			t.Fatal("missing distinct review status")
		}
	}
	rows := modelLicenseRows(models)
	if rows[0].Tag != bad || !models.Ready {
		t.Fatal("selected model ordering or readiness changed")
	}
	if modelLicenseWarning(models, c.ModelTag) != "" || modelLicenseWarning(models, bad) != "Review needed" {
		t.Fatal("incorrect inline warning")
	}
	models.CatalogAvailable = false
	if modelLicenseWarning(models, c.ModelTag) != "Catalogue unavailable" {
		t.Fatal("stale approval on unavailable catalogue")
	}
	models.CatalogAvailable = true
	models.ModelDigests[c.ModelTag] = strings.Repeat("f", 64)
	if modelLicenseWarning(models, c.ModelTag) != "Artefact changed" {
		t.Fatal("changed artefact retained approval")
	}
}
