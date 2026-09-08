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
		wantPrompt := "### Task\n" +
			"Translate the following text from German into Chinese.\n\n" +
			"### Strict Rules\n" +
			"1. Output only the translated plain text. Do not add explanations, notes, headings, commentary, Markdown, or URLs.\n" +
			"2. Treat every token matching `__MB_[A-Z_]+_[0-9]{4}__` as an immutable placeholder. Copy every occurrence exactly, character-for-character, and preserve the same number of occurrences.\n" +
			"3. Never translate, transliterate, inflect, decline, conjugate, modify, split, remove, duplicate, or replace a placeholder.\n" +
			"4. When a placeholder contains an entity type such as `STREET`, `DISTRICT`, `TRAIN_STATION`, `COMMUTER_TRAIN`, or `SUBWAY_SYSTEM`, use that type only to understand the sentence and produce natural grammar around the placeholder. Do not alter the placeholder itself.\n" +
			"5. If the target language would normally require changing the hidden entity, restructure the surrounding sentence so the placeholder remains unchanged.\n" +
			"6. Preserve the original meaning, tone, factual details, numbers, dates, times, negation, uncertainty, attribution, and relationships. Do not add or infer information.\n" +
			"7. Produce natural, fluent Chinese rather than a word-for-word translation.\n\n" +
			"### Source Data\nEinsatz am <KEEP>__MB_STREET_0001__</KEEP>"
		if payload.Messages[0].Content != wantPrompt {
			t.Fatalf("prompt = %q, want %q", payload.Messages[0].Content, wantPrompt)
		}
		if strings.Contains(payload.Messages[0].Content, "Simplified Chinese") || strings.Contains(payload.Messages[0].Content, "zh-CN") {
			t.Fatalf("prompt did not use Tencent's exact language name: %q", payload.Messages[0].Content)
		}
		if strings.Count(payload.Messages[0].Content, "__MB_[A-Z_]+_[0-9]{4}__") != 1 || !strings.Contains(payload.Messages[0].Content, "### Source Data\nEinsatz") {
			t.Fatalf("prompt omitted the exact placeholder pattern or source separator: %q", payload.Messages[0].Content)
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

func TestHyMT2NativeAdapterAddsOnlyConfiguredTargetGuidance(t *testing.T) {
	english := hyMT2NativePrompt("German", "English", hyMT2LanguageGuidance["en"], "Quelle")
	for _, expected := range []string{"neutral German person labels", "admitted to hospital as an inpatient", "police stop signals or orders"} {
		if !strings.Contains(english, expected) {
			t.Errorf("English guidance omitted %q: %s", expected, english)
		}
	}
	if !strings.HasSuffix(english, "### Source Data\nQuelle") {
		t.Fatalf("English source boundary changed: %q", english)
	}
	spanish := hyMT2NativePrompt("German", "Spanish", hyMT2LanguageGuidance["es"], "Quelle")
	for _, expected := range []string{"vehicle glass generic", "presunción de inocencia", "not ‘interrogó’", "never ‘trasladada de forma permanente’"} {
		if !strings.Contains(spanish, expected) {
			t.Errorf("Spanish guidance omitted %q: %s", expected, spanish)
		}
	}
	if strings.Contains(spanish, "Betroffene") {
		t.Fatalf("English guidance leaked into Spanish prompt: %q", spanish)
	}
	italian := hyMT2NativePrompt("German", "Italian", hyMT2LanguageGuidance["it"], "Quelle")
	if strings.Contains(italian, "Target-language guidance") || strings.Contains(italian, "Scheibe") {
		t.Fatalf("configured guidance leaked into fallback prompt: %q", italian)
	}
	if !strings.Contains(italian, "7. Produce natural, fluent Italian rather than a word-for-word translation.\n\n### Source Data\nQuelle") {
		t.Fatalf("fallback prompt structure changed: %q", italian)
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
