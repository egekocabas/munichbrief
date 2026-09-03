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

func TestHyMT2NativeAdapterUsesOfficialContract(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Format) != 0 || len(payload.Messages) != 1 || payload.Messages[0].Role != "user" || payload.Think {
			t.Fatalf("native request unexpectedly used a schema, system role, or thinking: %#v", payload)
		}
		wantPrompt := "Translate the following text from German into Chinese. Never translate or alter code tags or variable placeholders; leave them exactly in their original code form. Note that you must ONLY output the translated result without any additional explanation:\n\nEinsatz am <KEEP>__MB_STREET_0001__</KEEP>"
		if payload.Messages[0].Content != wantPrompt {
			t.Fatalf("prompt = %q, want %q", payload.Messages[0].Content, wantPrompt)
		}
		if strings.Contains(payload.Messages[0].Content, "Simplified Chinese") || strings.Contains(payload.Messages[0].Content, "zh-CN") {
			t.Fatalf("prompt did not use Tencent's exact language name: %q", payload.Messages[0].Content)
		}
		options := payload.Options
		if options.Temperature != 0.7 || options.TopP != 0.6 || options.TopK != 20 || options.RepeatPenalty != 1.05 || options.NumPredict != 4096 || options.NumCtx != 8192 {
			t.Fatalf("generation options = %#v", options)
		}
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(chatResponse{Model: "hy-mt2:test", Done: true, Message: chatMessage{Role: "assistant", Content: "在 <KEEP>__MB_STREET_0001__</KEEP> 的行动"}})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
	})
	adapter, err := NewHyMT2NativeAdapter("http://ollama.test:11434", "hy-mt2:test", "zh", time.Second, 8192, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	translated, model, err := adapter.Translate(context.Background(), "Einsatz am <KEEP>__MB_STREET_0001__</KEEP>")
	if err != nil || model != "hy-mt2:test" || translated != "在 <KEEP>__MB_STREET_0001__</KEEP> 的行动" {
		t.Fatalf("native translation=%q model=%q err=%v", translated, model, err)
	}
}

func TestHyMT2NativeAdapterMapsEverySupportedReaderLanguage(t *testing.T) {
	supported := map[string]string{
		"en": "English", "tr": "Turkish", "it": "Italian", "uk": "Ukrainian",
		"zh": "Chinese", "hi": "Hindi", "es": "Spanish", "fr": "French",
		"pl": "Polish", "ru": "Russian",
	}
	for code, name := range supported {
		t.Run(code, func(t *testing.T) {
			adapter, err := NewHyMT2NativeAdapter("http://ollama.test:11434", "hy-mt2:test", code, time.Second, 8192, nil)
			if err != nil {
				t.Fatal(err)
			}
			if adapter.sourceName != "German" || adapter.targetName != name {
				t.Fatalf("language mapping = %q -> %q", adapter.sourceName, adapter.targetName)
			}
		})
	}
}

func TestHyMT2NativeAdapterRejectsUnsupportedReaderLanguages(t *testing.T) {
	for _, code := range []string{"hr", "bs", "el", "ro"} {
		t.Run(code, func(t *testing.T) {
			_, err := NewHyMT2NativeAdapter("http://ollama.test:11434", "hy-mt2:test", code, time.Second, 8192, nil)
			if err == nil || !strings.Contains(err.Error(), "does not officially support target language") {
				t.Fatalf("unsupported target error = %v", err)
			}
		})
	}
}

func TestHyMT2NativeAdapterRejectsCanonicalAndEmptyInput(t *testing.T) {
	if _, err := NewHyMT2NativeAdapter("http://ollama.test:11434", "hy-mt2:test", "de", time.Second, 8192, nil); err == nil {
		t.Fatal("canonical German target was accepted")
	}
	adapter, err := NewHyMT2NativeAdapter("http://ollama.test:11434", "hy-mt2:test", "en", time.Second, 8192, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := adapter.Translate(context.Background(), " \n "); err == nil || KindOf(err) != ErrorOutput {
		t.Fatalf("empty input error = %v", err)
	}
}
