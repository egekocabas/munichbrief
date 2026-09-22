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
	"github.com/egekocabas/munichbrief/internal/store"
	"golang.org/x/net/html"
)

func TestAdminProcessingOverviewStates(t *testing.T) {
	database := fixtureStore(t)
	runtime, err := (fakeProcessingRequester{database: database}).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"enabled", "disabled", "unavailable"} {
		t.Run(state, func(t *testing.T) {
			runtime.AutomaticProcessingEnabled = state == "enabled"
			runtime.WindowOpen = false // An enabled switch is distinct from an open window.
			runtime.Queue.ActiveCycle = &store.PipelineCycle{ID: 42, Kind: "manual", Status: "active"}
			runtime.Queue.ActiveStepCompleted, runtime.Queue.ActiveStepTotal = 12, 30
			runtime.Queue.CycleCompleted, runtime.Queue.CycleTotal = 42, 60
			options := Options{PageSize: 20, SourceMode: "fixture", PresentationMode: "review", AdminEnabled: true}
			if state != "unavailable" {
				options.Processor = fakeProcessingRequester{database: database, runtime: &runtime}
			}
			server, err := NewWithOptions(database, slog.New(slog.NewTextHandler(io.Discard, nil)), options)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin", nil))
			body := response.Body.String()
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			if strings.Index(body, "Live operations") > strings.Index(body, "Registered pipeline steps") || strings.Index(body, "Cycle details and totals") > strings.Index(body, "Translation batch") {
				t.Fatal("live canonical progress must precede configuration and translations")
			}
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			badges := 0
			panels := 0
			var walk func(*html.Node)
			walk = func(node *html.Node) {
				attrs := map[string]string{}
				for _, attr := range node.Attr {
					attrs[attr.Key] = attr.Val
				}
				if _, found := attrs["data-processing-state"]; found {
					badges++
					if attrs["data-state"] != state {
						t.Errorf("processing badge = %q, want %q", attrs["data-state"], state)
					}
				}
				for _, marker := range []string{"data-cycle-overview", "data-processing-controls"} {
					if _, found := attrs[marker]; found {
						panels++
						for parent := node; parent != nil; parent = parent.Parent {
							if parent.Data == "details" {
								t.Errorf("%s must remain visible without expanding a disclosure", marker)
							}
						}
					}
				}
				if _, found := attrs["data-cycle-progress"]; found && state != "unavailable" && (attrs["max"] != "60" || attrs["value"] != "42") {
					t.Errorf("initial cycle progress = %v", attrs)
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					walk(child)
				}
			}
			walk(doc)
			if badges != 2 {
				t.Fatalf("processing badges = %d, want header and controls", badges)
			}
			if panels != 2 {
				t.Fatalf("visible panels = %d, want cycle overview and controls", panels)
			}
		})
	}
}

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
