package processing

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOllamaModelCatalogRefreshesSortedExactModels(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/tags" || request.Header.Get("Accept") != "application/json" {
			t.Fatalf("catalog request = %s accept=%q", request.URL.Path, request.Header.Get("Accept"))
		}
		return catalogResponse(http.StatusOK, `{"models":[{"name":"qwen3.5:4b"},{"name":" granite4:3b "},{"name":"qwen3.5:4b"},{"name":""}]}`), nil
	})
	catalog, err := NewOllamaModelCatalog("http://ollama.test:11434", time.Second, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := catalog.Refresh(context.Background())
	if snapshot.Err != nil || !snapshot.Available() || !snapshot.Has("granite4:3b") || !snapshot.Has("qwen3.5:4b") || snapshot.Has("qwen3.5") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if got := strings.Join(snapshot.Models, ","); got != "granite4:3b,qwen3.5:4b" {
		t.Fatalf("models = %q", got)
	}

	snapshot.Models[0] = "mutated"
	if catalog.Snapshot().Models[0] != "granite4:3b" {
		t.Fatal("catalog snapshot exposed mutable model storage")
	}
}

func TestOllamaModelCatalogFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name      string
		transport roundTripFunc
		timeout   time.Duration
	}{
		{name: "malformed", transport: func(*http.Request) (*http.Response, error) { return catalogResponse(http.StatusOK, `{`), nil }, timeout: time.Second},
		{name: "status", transport: func(*http.Request) (*http.Response, error) {
			return catalogResponse(http.StatusServiceUnavailable, "no"), nil
		}, timeout: time.Second},
		{name: "oversized", transport: func(*http.Request) (*http.Response, error) {
			return catalogResponse(http.StatusOK, strings.Repeat("x", modelCatalogMaxBytes+1)), nil
		}, timeout: time.Second},
		{name: "timeout", transport: func(request *http.Request) (*http.Response, error) {
			select {
			case <-time.After(30 * time.Millisecond):
				return catalogResponse(http.StatusOK, `{"models":[]}`), nil
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		}, timeout: 5 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog, err := NewOllamaModelCatalog("http://ollama.test:11434", test.timeout, &http.Client{Transport: test.transport})
			if err != nil {
				t.Fatal(err)
			}
			snapshot := catalog.Refresh(context.Background())
			if snapshot.Err == nil || snapshot.Available() || len(snapshot.Models) != 0 {
				t.Fatalf("failed-closed snapshot = %#v", snapshot)
			}
		})
	}
}

func catalogResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
