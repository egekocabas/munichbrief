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

func TestTranslationAdapterSupportMatchesNativeContracts(t *testing.T) {
	for _, code := range []string{"en", "tr", "it", "uk", "zh", "hi", "es", "fr", "pl", "ru"} {
		if !TranslationAdapterSupports(TranslationAdapterHyMT2, code) {
			t.Errorf("HY-MT2 should support %s", code)
		}
	}
	for _, code := range []string{"hr", "bs", "el", "ro"} {
		if TranslationAdapterSupports(TranslationAdapterHyMT2, code) {
			t.Errorf("HY-MT2 should not be offered for %s", code)
		}
	}
	for _, code := range []string{"en", "tr", "hr", "it", "uk", "zh", "es", "fr", "ro", "pl", "ru"} {
		if !TranslationAdapterSupports(TranslationAdapterSeedX, code) {
			t.Errorf("Seed-X should support %s", code)
		}
	}
	for _, code := range []string{"bs", "hi", "el"} {
		if TranslationAdapterSupports(TranslationAdapterSeedX, code) {
			t.Errorf("Seed-X should not be offered for %s", code)
		}
	}
	for _, code := range []string{"en", "tr", "hr", "it", "uk", "zh", "hi", "es", "fr", "ro", "pl", "el", "ru"} {
		if !TranslationAdapterSupports(TranslationAdapterSalamandraTA, code) {
			t.Errorf("SalamandraTA should support %s", code)
		}
	}
	if TranslationAdapterSupports(TranslationAdapterSalamandraTA, "bs") {
		t.Error("SalamandraTA should not be offered for bs")
	}
	for _, translation := range RegisteredTranslations() {
		if !TranslationAdapterSupports(TranslationAdapterStructured, translation.Language) || !TranslationAdapterSupports(TranslationAdapterTranslateGemma, translation.Language) {
			t.Errorf("common adapters missing for %s", translation.Language)
		}
	}
}

func TestSalamandraTATranslationGeneratorUsesProductionNativeLoop(t *testing.T) {
	var prompts []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/chat" {
			t.Fatalf("request path = %q", request.URL.Path)
		}
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		prompts = append(prompts, payload.Messages[0].Content)
		translated := "Policijska intervencija"
		if len(prompts) == 2 {
			translated = "Policija je izvijestila o intervenciji."
		}
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(chatResponse{Model: "salamandra:test", Done: true, Message: chatMessage{Role: "assistant", Content: translated}})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
	})
	provider, err := NewOllamaGeneratorProvider("http://ollama.test:11434", time.Second, 8192, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	generator, err := provider.StepGeneratorFor("salamandra:test", TranslationAdapterSalamandraTA)
	if err != nil {
		t.Fatal(err)
	}
	input := StepInput{Values: map[string]string{"title_de": "Polizeieinsatz", "summary_de": "Die Polizei berichtete über einen Einsatz."}}
	output, model, err := generator.GenerateStep(context.Background(), TranslationByLanguageMust(t, "hr").Step, input)
	if err != nil {
		t.Fatal(err)
	}
	if model != "salamandra:test" || output.Values["title"] != "Policijska intervencija" || output.Values["summary"] != "Policija je izvijestila o intervenciji." {
		t.Fatalf("output=%#v model=%q", output, model)
	}
	if len(prompts) != 2 || !strings.Contains(prompts[0], "German: Polizeieinsatz\nCroatian:") || !strings.Contains(prompts[1], "German: Die Polizei berichtete über einen Einsatz.\nCroatian:") {
		t.Fatalf("separate SalamandraTA prompts = %#v", prompts)
	}
}

func TestSeedXTranslationGeneratorUsesProductionNativeLoop(t *testing.T) {
	var prompts []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/generate" {
			t.Fatalf("request path = %q", request.URL.Path)
		}
		var payload generateRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		prompts = append(prompts, payload.Prompt)
		translated := "Policijska intervencija"
		if len(prompts) == 2 {
			translated = "Policija je izvijestila o intervenciji."
		}
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(generateResponse{Model: "seed-x:test", Done: true, Response: translated})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
	})
	provider, err := NewOllamaGeneratorProvider("http://ollama.test:11434", time.Second, 4096, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	generator, err := provider.StepGeneratorFor("seed-x:test", TranslationAdapterSeedX)
	if err != nil {
		t.Fatal(err)
	}
	input := StepInput{Values: map[string]string{"title_de": "Polizeieinsatz", "summary_de": "Die Polizei berichtete über einen Einsatz."}}
	output, model, err := generator.GenerateStep(context.Background(), TranslationByLanguageMust(t, "hr").Step, input)
	if err != nil {
		t.Fatal(err)
	}
	if model != "seed-x:test" || output.Values["title"] != "Policijska intervencija" || output.Values["summary"] != "Policija je izvijestila o intervenciji." {
		t.Fatalf("output=%#v model=%q", output, model)
	}
	if len(prompts) != 2 || !strings.HasSuffix(prompts[0], "Polizeieinsatz <hr>") || !strings.HasSuffix(prompts[1], "Die Polizei berichtete über einen Einsatz. <hr>") {
		t.Fatalf("separate Seed-X prompts = %#v", prompts)
	}
}

func TestNativeTranslationGeneratorSendsTitleAndSummarySeparately(t *testing.T) {
	var prompts []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		prompts = append(prompts, payload.Messages[0].Content)
		translated := "Operation near Munich Central Station"
		if len(prompts) == 2 {
			translated = "Police reported an operation near the station."
		}
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(chatResponse{Model: "hy-mt2:test", Done: true, Message: chatMessage{Role: "assistant", Content: translated}})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
	})
	provider, err := NewOllamaGeneratorProvider("http://ollama.test:11434", time.Second, 8192, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	generator, err := provider.StepGeneratorFor("hy-mt2:test", TranslationAdapterHyMT2)
	if err != nil {
		t.Fatal(err)
	}
	input := StepInput{Values: map[string]string{
		"title_de":   "Einsatz am Hauptbahnhof",
		"summary_de": "Die Polizei berichtete über einen Einsatz am Bahnhof.",
	}}
	output, model, err := generator.GenerateStep(context.Background(), TranslationByLanguageMust(t, "en").Step, input)
	if err != nil {
		t.Fatal(err)
	}
	if model != "hy-mt2:test" || output.Values["title"] != "Operation near Munich Central Station" || output.Values["summary"] != "Police reported an operation near the station." {
		t.Fatalf("output=%#v model=%q", output, model)
	}
	if len(prompts) != 2 || !strings.HasSuffix(prompts[0], "\nEinsatz am Hauptbahnhof") || !strings.HasSuffix(prompts[1], "\nDie Polizei berichtete über einen Einsatz am Bahnhof.") {
		t.Fatalf("separate prompts = %#v", prompts)
	}
	if strings.Contains(prompts[0], input.Value("summary_de")) || strings.Contains(prompts[1], input.Value("title_de")) {
		t.Fatalf("title and summary were combined: %#v", prompts)
	}
}

func TestNativeTranslationGeneratorRejectsUnsupportedRoute(t *testing.T) {
	provider, err := NewOllamaGeneratorProvider("http://ollama.test:11434", time.Second, 8192, nil)
	if err != nil {
		t.Fatal(err)
	}
	generator, err := provider.StepGeneratorFor("hy-mt2:test", TranslationAdapterHyMT2)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = generator.GenerateStep(context.Background(), TranslationByLanguageMust(t, "ro").Step, StepInput{Values: map[string]string{"title_de": "Titel", "summary_de": "Zusammenfassung."}})
	if err == nil || KindOf(err) != ErrorConfiguration {
		t.Fatalf("unsupported route error = %v", err)
	}
}
