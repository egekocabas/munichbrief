package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/processing"
	"golang.org/x/net/html"
)

func TestAdminLiveBatchAndRunningHighlights(t *testing.T) {
	database := fixtureStore(t)
	if err := database.EnsurePostProcessingScopes(context.Background(), processing.DefaultPostProcessorRegistry().StoreScopes(), time.Now()); err != nil {
		t.Fatal(err)
	}
	runtime, err := (fakeProcessingRequester{database: database}).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runtime.TranslationBatch = processing.TranslationBatchStatus{Model: "model-A", Attempts: 27, Limit: 50, NextModel: "model-A", NextScope: "tr", NextRequestKind: "manual", NextSwitchModel: "model-B"}
	server, err := NewWithOptions(database, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{PageSize: 20, SourceMode: "fixture", PresentationMode: "review", AdminEnabled: true, Processor: fakeProcessingRequester{database: database, runtime: &runtime}})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"incident_metadata", "german_presentation", "public_assistance_verification/default", "category_verification/default", "translation/en", "translation/tr", ""} {
		t.Run(key, func(t *testing.T) {
			runtime.Running = nil
			if key != "" {
				runtime.Running = &processing.PipelineExecutionStatus{Key: key, Model: "model-A", IncidentID: 1}
			}
			for _, path := range []string{"/admin", "/admin/translations"} {
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				body := response.Body.String()
				if response.Code != http.StatusOK {
					t.Fatalf("%s: status %d", path, response.Code)
				}
				for _, want := range []string{"Running now", "27 / 50 attempts", "model-A", "model-B", "Next translation", "Next model switch"} {
					if !strings.Contains(body, want) {
						t.Errorf("%s missing %q", path, want)
					}
				}
				doc, err := html.Parse(strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				var active []string
				var walk func(*html.Node)
				walk = func(n *html.Node) {
					attrs := map[string]string{}
					for _, a := range n.Attr {
						attrs[a.Key] = a.Val
					}
					// A grouped description list must contain only terms and
					// descriptions, with batch details inside the description.
					if n.Type == html.ElementNode && n.Data == "div" && n.Parent != nil && n.Parent.Data == "dl" {
						for child := n.FirstChild; child != nil; child = child.NextSibling {
							if child.Type == html.ElementNode && child.Data != "dt" && child.Data != "dd" {
								t.Errorf("%s invalid description-list child: %s", path, child.Data)
							}
						}
					}
					if attrs["data-execution-key"] != "" && attrs["data-running"] == "true" {
						active = append(active, attrs["data-execution-key"])
					}
					for child := n.FirstChild; child != nil; child = child.NextSibling {
						walk(child)
					}
				}
				walk(doc)
				expected := key != "" && (path == "/admin" || strings.HasPrefix(key, "translation/"))
				if expected && (len(active) != 1 || active[0] != key) || !expected && len(active) != 0 {
					t.Errorf("%s active=%v for %q", path, active, key)
				}
				if path == "/admin" {
					for _, want := range []string{"Cycle details and totals", "Processing controls", "Details and history"} {
						if !strings.Contains(body, want) {
							t.Errorf("missing disclosure %q", want)
						}
					}
				}
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/ai/status", nil))
			var status processing.PipelineRuntimeStatus
			if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			if status.TranslationBatch.Attempts != 27 || status.TranslationBatch.NextSwitchModel != "model-B" {
				t.Fatalf("runtime JSON missing batch: %#v", status.TranslationBatch)
			}
		})
	}
}
