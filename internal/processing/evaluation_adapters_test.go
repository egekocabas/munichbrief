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

func TestLLaMAX3NativeAdapterUsesOfficialAlpacaShape(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/generate" {
			t.Fatalf("request = %s %s, want POST /api/generate", request.Method, request.URL.Path)
		}
		var payload generateRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		want := "Below is an instruction that describes a task, paired with an input that provides further context. Write a response that appropriately completes the request.\n### Instruction:\nTranslate the following sentences from German to Greek. Output only the translation. Preserve every placeholder matching `__MB_[A-Z_]+_[0-9]{4}__` exactly, character-for-character; translate only the surrounding text.\n### Input:\nEinsatz an der __MB_STREET_0001__\n### Response:"
		if !payload.Raw || payload.Prompt != want || payload.Options.Temperature != 0 || payload.Options.NumPredict != evaluationMaxOutputTokens || payload.Options.NumCtx != llamax3ContextLimit {
			t.Fatalf("generate payload = %#v", payload)
		}
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(generateResponse{Model: "llamax3:test", Done: true, Response: "Intervencija u __MB_STREET_0001__"})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
	})
	adapter, err := NewLLaMAX3NativeAdapter("http://ollama.test:11434", "llamax3:test", "el", time.Second, 16384, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	translated, model, err := adapter.Translate(context.Background(), "Einsatz an der __MB_STREET_0001__")
	if err != nil || model != "llamax3:test" || translated != "Intervencija u __MB_STREET_0001__" {
		t.Fatalf("translation=%q model=%q err=%v", translated, model, err)
	}
}

func TestLabelledNativeAdaptersUseUserOnlyChatML(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		context     int
		constructor func(string, string, string, time.Duration, int, *http.Client) (*evaluationNativeAdapter, error)
	}{
		{name: "EuroLLM", target: "hr", context: euroLLMContextLimit, constructor: NewEuroLLMNativeAdapter},
		{name: "Tower+", target: "hi", context: towerPlusContextLimit, constructor: NewTowerPlusNativeAdapter},
		{name: "Gemma 4", target: "bs", context: gemma4ContextLimit, constructor: NewGemma4NativeAdapter},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodPost || request.URL.Path != "/api/chat" {
					t.Fatalf("request = %s %s, want POST /api/chat", request.Method, request.URL.Path)
				}
				var payload chatRequest
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if len(payload.Messages) != 1 || payload.Messages[0].Role != "user" || len(payload.Format) != 0 {
					t.Fatalf("chat payload = %#v", payload)
				}
				if !strings.Contains(payload.Messages[0].Content, "German: Einsatz an der __MB_STREET_0001__") || !strings.Contains(payload.Messages[0].Content, "Preserve every placeholder matching") {
					t.Fatalf("prompt = %q", payload.Messages[0].Content)
				}
				if test.name == "Gemma 4" && (!strings.Contains(payload.Messages[0].Content, "navodno") || !strings.Contains(payload.Messages[0].Content, "pretpostavka nevinosti") || !strings.Contains(payload.Messages[0].Content, "inpatient care") || !strings.Contains(payload.Messages[0].Content, "saobraćajna policija")) {
					t.Fatalf("Gemma 4 guidance missing from prompt = %q", payload.Messages[0].Content)
				}
				if payload.Options.Temperature != 0 || payload.Options.NumPredict != evaluationMaxOutputTokens || payload.Options.NumCtx != test.context {
					t.Fatalf("generation options = %#v", payload.Options)
				}
				var response bytes.Buffer
				_ = json.NewEncoder(&response).Encode(chatResponse{Model: "model:test", Done: true, Message: chatMessage{Role: "assistant", Content: "translation __MB_STREET_0001__"}})
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
			})
			adapter, err := test.constructor("http://ollama.test:11434", "model:test", test.target, time.Second, 16384, &http.Client{Transport: transport})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := adapter.Translate(context.Background(), "Einsatz an der __MB_STREET_0001__"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMADLAD400NativeAdapterUsesOnlyOfficialTargetTokenAndSource(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/generate" {
			t.Fatalf("request = %s %s, want POST /api/generate", request.Method, request.URL.Path)
		}
		var payload generateRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if !payload.Raw || payload.Prompt != "<2el> Einsatz an der __MB_STREET_0001__" {
			t.Fatalf("MADLAD payload = %#v", payload)
		}
		if payload.Options.Temperature != 0 || payload.Options.NumPredict != madlad400MaxOutputTokens || payload.Options.NumCtx != madlad400ContextLimit {
			t.Fatalf("MADLAD options = %#v", payload.Options)
		}
		if strings.Contains(payload.Prompt, "Translate") || strings.Contains(payload.Prompt, "placeholder") {
			t.Fatalf("MADLAD prompt contains instructions: %q", payload.Prompt)
		}
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(generateResponse{Model: "madlad:test", Done: true, Response: "Επιχείρηση στην __MB_STREET_0001__"})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
	})
	adapter, err := NewMADLAD400NativeAdapter("http://ollama.test", "madlad:test", "el", time.Second, 8192, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	translated, model, err := adapter.Translate(context.Background(), " Einsatz an der __MB_STREET_0001__ ")
	if err != nil || model != "madlad:test" || translated != "Επιχείρηση στην __MB_STREET_0001__" {
		t.Fatalf("translation=%q model=%q err=%v", translated, model, err)
	}
}

func TestTowerInstructNativeAdapterAvoidsConvertedEmptySystemTurn(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/generate" {
			t.Fatalf("request path = %q, want raw generation", request.URL.Path)
		}
		var payload generateRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		want := "<|im_start|>user\nTranslate the following German source text to French:\nPreserve every placeholder matching `__MB_[A-Z_]+_[0-9]{4}__` exactly, character-for-character, and output only the translation.\nGerman: Einsatz an der __MB_STREET_0001__\nFrench:<|im_end|>\n<|im_start|>assistant\n"
		if !payload.Raw || payload.Prompt != want || strings.Contains(payload.Prompt, "<|im_start|>system") || payload.Options.NumCtx != towerInstructContextLimit {
			t.Fatalf("TowerInstruct payload = %#v", payload)
		}
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(generateResponse{Model: "tower-instruct:test", Done: true, Response: "Операция на __MB_STREET_0001__"})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
	})
	adapter, err := NewTowerInstructNativeAdapter("http://ollama.test", "tower-instruct:test", "fr", time.Second, 8192, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := adapter.Translate(context.Background(), "Einsatz an der __MB_STREET_0001__"); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluationNativeAdaptersRejectUnsupportedLanguagesAndEmptyInput(t *testing.T) {
	if _, err := NewEuroLLMNativeAdapter("http://ollama.test", "model:test", "bs", time.Second, 8192, nil); err == nil || !strings.Contains(err.Error(), "does not officially support") {
		t.Fatalf("EuroLLM Bosnian error = %v", err)
	}
	if _, err := NewTowerPlusNativeAdapter("http://ollama.test", "model:test", "hr", time.Second, 8192, nil); err == nil || !strings.Contains(err.Error(), "does not officially support") {
		t.Fatalf("Tower+ Croatian error = %v", err)
	}
	if _, err := NewTowerInstructNativeAdapter("http://ollama.test", "model:test", "hi", time.Second, 8192, nil); err == nil || !strings.Contains(err.Error(), "does not officially support") {
		t.Fatalf("TowerInstruct Hindi error = %v", err)
	}
	if _, err := NewMADLAD400NativeAdapter("http://ollama.test", "model:test", "en", time.Second, 2048, nil); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("MADLAD English error = %v", err)
	}
	if _, err := NewGemma4NativeAdapter("http://ollama.test", "model:test", "en", time.Second, 2048, nil); err == nil || !strings.Contains(err.Error(), "does not officially support") {
		t.Fatalf("Gemma 4 English error = %v", err)
	}
	adapter, err := NewLLaMAX3NativeAdapter("http://ollama.test", "model:test", "bs", time.Second, 8192, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := adapter.Translate(context.Background(), " \n "); err == nil || KindOf(err) != ErrorOutput {
		t.Fatalf("empty input error = %v", err)
	}
}

func TestTranslationAdapterRawTransportClassification(t *testing.T) {
	for _, adapter := range []string{TranslationAdapterSeedX, TranslationAdapterLLaMAX3, TranslationAdapterTowerInstruct, TranslationAdapterMADLAD400} {
		if !translationAdapterUsesRawGenerate(adapter) {
			t.Errorf("%s should use raw generation", adapter)
		}
	}
	for _, adapter := range []string{TranslationAdapterStructured, TranslationAdapterHyMT2, TranslationAdapterSalamandraTA, TranslationAdapterEuroLLM, TranslationAdapterTowerPlus, TranslationAdapterGemma4} {
		if translationAdapterUsesRawGenerate(adapter) {
			t.Errorf("%s should use chat", adapter)
		}
	}
}
