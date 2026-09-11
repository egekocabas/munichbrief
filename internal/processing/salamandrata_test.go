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

func TestSalamandraTANativeAdapterUsesOfficialChatMLTranslationShape(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/chat" || request.Method != http.MethodPost {
			t.Fatalf("request = %s %s, want POST /api/chat", request.Method, request.URL.Path)
		}
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		want := "Translate the following text from German into English.\nRequirements:\n- Output only the translation.\n- Preserve every placeholder matching `__MB_[A-Z_]+_[0-9]{4}__` exactly, character-for-character; translate only the surrounding natural-language text.\nGerman: Einsatz an der __MB_STREET_0001__\nEnglish:"
		if len(payload.Messages) != 1 || payload.Messages[0].Role != "user" || payload.Messages[0].Content != want || len(payload.Format) != 0 {
			t.Fatalf("chat payload = %#v", payload)
		}
		if payload.Options.Temperature != 0 || payload.Options.NumPredict != salamandraTAMaximumOutputTokens || payload.Options.NumCtx != salamandraTAContextLimit {
			t.Fatalf("generation options = %#v", payload.Options)
		}
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(chatResponse{Model: "salamandra:test", Done: true, Message: chatMessage{Role: "assistant", Content: "Operation on __MB_STREET_0001__"}})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
	})
	adapter, err := NewSalamandraTANativeAdapter("http://ollama.test:11434", "salamandra:test", "en", time.Second, 16384, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	translated, model, err := adapter.Translate(context.Background(), "Einsatz an der __MB_STREET_0001__")
	if err != nil || model != "salamandra:test" || translated != "Operation on __MB_STREET_0001__" {
		t.Fatalf("translation=%q model=%q err=%v", translated, model, err)
	}
}

func TestSalamandraTANativeAdapterSupportsRequestedLanguages(t *testing.T) {
	for _, code := range []string{"ru", "hr", "hi"} {
		t.Run(code, func(t *testing.T) {
			if _, err := NewSalamandraTANativeAdapter("http://ollama.test:11434", "salamandra:test", code, time.Second, 8192, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := NewSalamandraTANativeAdapter("http://ollama.test:11434", "salamandra:test", "bs", time.Second, 8192, nil); err == nil || !strings.Contains(err.Error(), "does not officially support") {
		t.Fatalf("unsupported Bosnian error = %v", err)
	}
}

func TestSalamandraTANativeAdapterRejectsEmptyInput(t *testing.T) {
	adapter, err := NewSalamandraTANativeAdapter("http://ollama.test:11434", "salamandra:test", "hr", time.Second, 8192, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := adapter.Translate(context.Background(), " \n "); err == nil || KindOf(err) != ErrorOutput {
		t.Fatalf("empty input error = %v", err)
	}
}
