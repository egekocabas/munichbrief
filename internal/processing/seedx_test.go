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

func TestSeedXNativeAdapterUsesRawCompletionContract(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/generate" || request.Method != http.MethodPost {
			t.Fatalf("request = %s %s, want POST /api/generate", request.Method, request.URL.Path)
		}
		var rawPayload map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&rawPayload); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"messages", "system", "format"} {
			if _, found := rawPayload[forbidden]; found {
				t.Fatalf("raw request contains %q: %#v", forbidden, rawPayload)
			}
		}
		var payload generateRequest
		encoded, _ := json.Marshal(rawPayload)
		if err := json.Unmarshal(encoded, &payload); err != nil {
			t.Fatal(err)
		}
		wantPrompt := "Translate the following text from German into Chinese. Never translate or alter code tags or variable placeholders; leave them exactly in their original code form. Only output the translated result:\n\nEinsatz am <KEEP>__MB_STREET_0001__</KEEP> <zh>"
		if !payload.Raw || payload.Stream || payload.Think || payload.Prompt != wantPrompt {
			t.Fatalf("raw generation payload = %#v", payload)
		}
		if !strings.HasSuffix(payload.Prompt, "<zh>") || strings.Contains(payload.Prompt, "Simplified Chinese") {
			t.Fatalf("target tag is not the final prompt content: %q", payload.Prompt)
		}
		if payload.Options.Temperature != 0 || payload.Options.NumPredict != 512 || payload.Options.NumCtx != 8192 {
			t.Fatalf("generation options = %#v", payload.Options)
		}
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(generateResponse{Model: "seed-x:test", Done: true, Response: "在 <KEEP>__MB_STREET_0001__</KEEP> 的行动"})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
	})
	adapter, err := NewSeedXNativeAdapter("http://ollama.test:11434", "seed-x:test", "zh", time.Second, 8192, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	translated, model, err := adapter.Translate(context.Background(), "Einsatz am <KEEP>__MB_STREET_0001__</KEEP>")
	if err != nil || model != "seed-x:test" || translated != "在 <KEEP>__MB_STREET_0001__</KEEP> 的行动" {
		t.Fatalf("native translation=%q model=%q err=%v", translated, model, err)
	}
}

func TestSeedXNativeAdapterMapsEverySupportedReaderLanguage(t *testing.T) {
	supported := map[string]string{
		"en": "English", "tr": "Turkish", "hr": "Croatian", "it": "Italian",
		"uk": "Ukrainian", "zh": "Chinese", "es": "Spanish", "fr": "French",
		"ro": "Romanian", "pl": "Polish", "ru": "Russian",
	}
	for code, name := range supported {
		t.Run(code, func(t *testing.T) {
			adapter, err := NewSeedXNativeAdapter("http://ollama.test:11434", "seed-x:test", code, time.Second, 8192, nil)
			if err != nil {
				t.Fatal(err)
			}
			if adapter.source.name != "German" || adapter.target.name != name || adapter.target.tag != code {
				t.Fatalf("language mapping = %#v -> %#v", adapter.source, adapter.target)
			}
		})
	}
}

func TestSeedXNativeAdapterRejectsUnsupportedReaderLanguages(t *testing.T) {
	for _, code := range []string{"bs", "hi", "el"} {
		t.Run(code, func(t *testing.T) {
			_, err := NewSeedXNativeAdapter("http://ollama.test:11434", "seed-x:test", code, time.Second, 8192, nil)
			if err == nil || !strings.Contains(err.Error(), "does not officially support target language") {
				t.Fatalf("unsupported target error = %v", err)
			}
		})
	}
}

func TestSeedXNativeAdapterRejectsCanonicalAndEmptyInput(t *testing.T) {
	if _, err := NewSeedXNativeAdapter("http://ollama.test:11434", "seed-x:test", "de", time.Second, 8192, nil); err == nil {
		t.Fatal("canonical German target was accepted")
	}
	adapter, err := NewSeedXNativeAdapter("http://ollama.test:11434", "seed-x:test", "en", time.Second, 8192, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := adapter.Translate(context.Background(), " \n "); err == nil || KindOf(err) != ErrorOutput {
		t.Fatalf("empty input error = %v", err)
	}
}

func TestSeedXNativeAdapterClassifiesRawGenerationFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   string
		status int
		kind   ErrorKind
	}{
		{name: "service unavailable", body: "not logged", status: http.StatusServiceUnavailable, kind: ErrorTransient},
		{name: "malformed response", body: "not json", status: http.StatusOK, kind: ErrorOutput},
		{name: "incomplete response", body: `{"model":"seed-x:test","done":false}`, status: http.StatusOK, kind: ErrorTransient},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})
			adapter, err := NewSeedXNativeAdapter("http://ollama.test:11434", "seed-x:test", "en", time.Second, 8192, &http.Client{Transport: transport})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = adapter.Translate(context.Background(), "Einsatz")
			if err == nil || KindOf(err) != test.kind {
				t.Fatalf("error = %v, kind = %q, want %q", err, KindOf(err), test.kind)
			}
		})
	}
}
