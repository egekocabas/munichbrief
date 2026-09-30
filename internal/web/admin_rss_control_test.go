package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminRSSControlWithoutAI(t *testing.T) {
	database := fixtureStore(t)
	server := adminTestServer(t, database, nil)
	server.options.Processor = nil
	handler := server.Handler()
	for _, value := range []string{"false", "true"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, formRequest(http.MethodPost, "/api/admin/rss/enabled", "enabled="+value))
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/rss-history" {
			t.Fatalf("toggle = %d/%s", response.Code, response.Body.String())
		}
		enabled, err := database.RSSSyncEnabled(context.Background())
		if err != nil || enabled != (value == "true") {
			t.Fatalf("RSS enabled = %t, %v", enabled, err)
		}
		page := httptest.NewRecorder()
		handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/admin/rss-history", nil))
		state, action := "disabled", "Enable"
		if enabled {
			state, action = "enabled", "Disable"
		}
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "RSS synchronization: "+state) || !strings.Contains(page.Body.String(), action+" RSS synchronization") {
			t.Fatalf("RSS control page = %d/%s", page.Code, page.Body.String())
		}
	}
}

func TestAdminRSSControlRejectsInvalidRequests(t *testing.T) {
	database := fixtureStore(t)
	handler := adminTestServer(t, database, []string{"munichbrief.de"}).Handler()
	for _, test := range []struct {
		name, body, header, value string
		status                    int
	}{
		{"cross-site", "enabled=false", "Sec-Fetch-Site", "cross-site", http.StatusForbidden},
		{"foreign origin", "enabled=false", "Origin", "https://elsewhere.test", http.StatusForbidden},
		{"public host", "enabled=false", "", "", http.StatusNotFound},
		{"invalid value", "enabled=no", "", "", http.StatusBadRequest},
		{"missing value", "", "", "", http.StatusBadRequest},
		{"content type", "enabled=false", "Content-Type", "application/json", http.StatusUnsupportedMediaType},
		{"oversized", "enabled=false&padding=" + strings.Repeat("x", 4096), "", "", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := formRequest(http.MethodPost, "/api/admin/rss/enabled", test.body)
			if test.header != "" {
				request.Header.Set(test.header, test.value)
			}
			if test.name == "public host" {
				request.Host = "munichbrief.de"
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if enabled, err := database.RSSSyncEnabled(context.Background()); err != nil || !enabled {
				t.Fatalf("rejected request changed control: %t, %v", enabled, err)
			}
		})
	}
}
