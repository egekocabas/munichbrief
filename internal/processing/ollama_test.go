package processing

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestOllamaClientRequestsStructuredBilingualPresentation(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/chat" || request.Method != http.MethodPost {
			t.Errorf("request = %s %s, want POST /api/chat", request.Method, request.URL.Path)
		}
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.Model != "qwen3.5:4b" || payload.Stream || payload.Think || payload.Options.NumCtx != 8192 {
			t.Errorf("unexpected request options: %+v", payload)
		}
		if len(payload.Format) == 0 || len(payload.Messages) != 2 {
			t.Errorf("request is missing schema or messages: %+v", payload)
		}
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(chatResponse{
			Model: "qwen3.5:4b",
			Done:  true,
			Message: chatMessage{Role: "assistant", Content: `{
				"title_de":"Polizeieinsatz in der Altstadt",
				"summary_de":"Am Abend fand in der Altstadt ein Polizeieinsatz statt. Die Absperrungen wurden später aufgehoben.",
				"title_en":"Police operation in the old town",
				"summary_en":"A police operation took place in the old town in the evening. The cordons were later lifted."
			}`},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(response.Bytes())),
		}, nil
	})

	client, err := NewOllamaClient("http://ollama.test:11434", "qwen3.5:4b", time.Second, 8192, &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("NewOllamaClient() error = %v", err)
	}
	result, model, err := client.Generate(context.Background(), "Originaltitel", "Am Abend fand ein Einsatz statt.")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if model != "qwen3.5:4b" || result.TitleDE != "Polizeieinsatz in der Altstadt" || result.SummaryEN == "" {
		t.Fatalf("result = %#v model=%q", result, model)
	}
}

func TestOllamaClientRejectsMalformedModelOutput(t *testing.T) {
	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(chatResponse{
			Model:   "qwen3.5:4b",
			Done:    true,
			Message: chatMessage{Role: "assistant", Content: `{"title_de":"Only one field"}`},
		})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
	})
	client, err := NewOllamaClient("http://ollama.test:11434", "qwen3.5:4b", time.Second, 8192, &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("NewOllamaClient() error = %v", err)
	}
	if _, _, err := client.Generate(context.Background(), "Titel", "Text"); err == nil {
		t.Fatal("Generate() error = nil, want invalid output error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
