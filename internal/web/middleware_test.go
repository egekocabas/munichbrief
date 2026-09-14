package web

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
)

func TestAboutHealthReadinessAndRequestHeaders(t *testing.T) {
	t.Parallel()
	database := fixtureStore(t)
	handler := testServer(t, database).Handler()

	about := httptest.NewRecorder()
	request := englishRequest(http.MethodGet, "/en/about", nil)
	request.Header.Set("X-Request-ID", "test-request")
	handler.ServeHTTP(about, request)
	if about.Code != http.StatusOK || !strings.Contains(about.Body.String(), "About MunichBrief") {
		t.Fatalf("about response = %d/%q", about.Code, about.Body.String())
	}
	for _, expected := range []string{"From release to incident", "Publish in German", "Check and translate in the background", "Accuracy and official information", "Privacy and data handling", "Independence", "Contact", `href="https://github.com/egekocabas/munichbrief/issues"`, `href="mailto:contact@munichbrief.de"`} {
		if !strings.Contains(about.Body.String(), expected) {
			t.Errorf("about response does not contain structured section %q", expected)
		}
	}
	for _, unexpected := range []string{`aria-label="Important notice"`, "automated deletion deadline", "approved AI"} {
		if strings.Contains(about.Body.String(), unexpected) {
			t.Errorf("about response unexpectedly contains %q", unexpected)
		}
	}
	for _, unexpected := range []string{"sm:grid-cols-2", "rounded-lg border border-rule bg-paper"} {
		if strings.Contains(about.Body.String(), unexpected) {
			t.Errorf("about response unexpectedly contains card layout %q", unexpected)
		}
	}
	if about.Header().Get("X-Request-ID") != "test-request" || about.Header().Get("X-Correlation-ID") != "test-request" {
		t.Errorf("request headers = %q/%q", about.Header().Get("X-Request-ID"), about.Header().Get("X-Correlation-ID"))
	}
	if about.Header().Get("Content-Security-Policy") == "" {
		t.Error("about response has no Content-Security-Policy")
	}
	if about.Header().Get("Referrer-Policy") != "same-origin" {
		t.Errorf("referrer policy = %q, want same-origin", about.Header().Get("Referrer-Policy"))
	}
	for _, expected := range []string{`script-src 'self'`, `style-src 'self'`} {
		if !strings.Contains(about.Header().Get("Content-Security-Policy"), expected) {
			t.Errorf("content security policy does not contain %q", expected)
		}
	}
	for _, expected := range []string{`src="` + staticAssets["htmx.min.js"].path + `"`, `hx-boost="true"`, `"allowEval":false`, `"includeIndicatorStyles":false`} {
		if !strings.Contains(about.Body.String(), expected) {
			t.Errorf("about response does not contain %q", expected)
		}
	}
	if !strings.Contains(about.Body.String(), `<meta name="robots" content="noindex,nofollow,noarchive">`) {
		t.Error("review page does not publish its noindex directive in HTML")
	}
	if !strings.Contains(about.Body.String(), `rel="icon" href="`+staticAssets["favicon.svg"].path+`" type="image/svg+xml"`) {
		t.Error("about response does not link the MunichBrief favicon")
	}
	for _, expected := range []string{
		`href="https://github.com/egekocabas/munichbrief"`, `target="_blank"`, `rel="noopener noreferrer"`,
		`>About</a>`, `page-shell py-6 text-center`,
	} {
		if !strings.Contains(about.Body.String(), expected) {
			t.Errorf("about response does not contain navigation/footer markup %q", expected)
		}
	}
	if strings.Contains(about.Body.String(), `href="/en">Latest</a>`) {
		t.Error("about response retained the redundant Latest navigation link")
	}

	for _, path := range []string{"/healthz", "/readyz"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", path, recorder.Code)
		}
	}

	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metrics.Code != http.StatusNotFound {
		t.Errorf("reader /metrics status = %d, want 404", metrics.Code)
	}

	for _, asset := range []struct {
		name string
		body string
	}{
		{name: "app.css", body: "--color-civic"},
		{name: "htmx.min.js", body: "htmx"},
		{name: "theme.js", body: "munichbrief-theme"},
		{name: "admin.js", body: "processing-confirmation"},
		{name: "favicon.svg", body: `<circle cx="32" cy="32" r="29" stroke="currentColor"`},
	} {
		registered := staticAssets[asset.name]
		digest := sha256.Sum256(registered.content)
		fingerprint := hex.EncodeToString(digest[:])[:12]
		extension := path.Ext(asset.name)
		expectedPath := "/static/" + strings.TrimSuffix(asset.name, extension) + "." + fingerprint + extension
		if registered.path != expectedPath {
			t.Errorf("asset %s path = %q, want %q", asset.name, registered.path, expectedPath)
		}

		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, registered.path, nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != registered.contentType || response.Header().Get("Cache-Control") != immutableAssetCacheControl || !strings.Contains(response.Body.String(), asset.body) {
			t.Errorf("asset %s response = %d/%q/%q", registered.path, response.Code, response.Header().Get("Content-Type"), response.Header().Get("Cache-Control"))
		}

		base := strings.TrimSuffix(asset.name, extension)
		for _, stalePath := range []string{"/static/" + asset.name, "/static/" + base + ".000000000000" + extension} {
			stale := httptest.NewRecorder()
			handler.ServeHTTP(stale, httptest.NewRequest(http.MethodGet, stalePath, nil))
			if stale.Code != http.StatusNotFound {
				t.Errorf("stale asset %s status = %d, want 404", stalePath, stale.Code)
			}
		}
	}
	if _, err := assetURL("missing.css"); err == nil {
		t.Error("assetURL() accepted an unregistered asset")
	}
}

func TestReadinessFailsOnlyWhenDatabaseIsUnavailable(t *testing.T) {
	t.Parallel()
	database := fixtureStore(t)
	handler := testServer(t, database).Handler()
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want 503", recorder.Code)
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", health.Code)
	}
}
