package processing

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTranslateGemmaNativeAdapterUsesOfficialPlainTextPrompt(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Format) != 0 || len(payload.Messages) != 1 || payload.Messages[0].Role != "user" {
			t.Fatalf("native request unexpectedly used schema or multiple roles: %#v", payload)
		}
		if payload.Options.NumCtx != 2048 {
			t.Fatalf("TranslateGemma context = %d, want 2048", payload.Options.NumCtx)
		}
		prompt := payload.Messages[0].Content
		for _, expected := range []string{"German (de) to Turkish (tr)", "Produce only the Turkish translation", ":\n\n\nEinsatz am __MB_PLACE_0001__"} {
			if !strings.Contains(prompt, expected) {
				t.Errorf("native prompt omitted %q: %q", expected, prompt)
			}
		}
		if strings.Contains(prompt, "JSON") {
			t.Fatalf("native prompt requested JSON: %q", prompt)
		}
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(chatResponse{Model: "translategemma:test", Done: true, Message: chatMessage{Role: "assistant", Content: "__MB_PLACE_0001__ konumundaki olay"}})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
	})
	adapter, err := NewTranslateGemmaNativeAdapter("http://ollama.test:11434", "translategemma:test", "tr", time.Second, 8192, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	translated, model, err := adapter.Translate(context.Background(), "Einsatz am __MB_PLACE_0001__")
	if err != nil || model != "translategemma:test" || translated != "__MB_PLACE_0001__ konumundaki olay" {
		t.Fatalf("native translation=%q model=%q err=%v", translated, model, err)
	}
}

func TestTranslateGemmaNativeAdapterRejectsCanonicalTarget(t *testing.T) {
	if _, err := NewTranslateGemmaNativeAdapter("http://ollama.test:11434", "translategemma:test", "de", time.Second, 2048, nil); err == nil {
		t.Fatal("canonical German target was accepted")
	}
}
