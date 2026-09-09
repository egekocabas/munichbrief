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

	langregistry "github.com/egekocabas/munichbrief/internal/languages"
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

func TestTranslateGemmaNativeAdapterAddsOnlyConfiguredTargetGuidance(t *testing.T) {
	definitions := langregistry.Registered()
	source := langregistry.Canonical(definitions)
	greek, found := langregistry.ByCode(definitions, "el")
	if !found {
		t.Fatal("Greek language is not registered")
	}
	turkish, found := langregistry.ByCode(definitions, "tr")
	if !found {
		t.Fatal("Turkish language is not registered")
	}
	greekPrompt := translateGemmaNativePrompt(source, greek, translateGemmaLanguageGuidance["el"], "Quelle")
	for _, expected := range []string{"Target-language guidance:", "standard Modern Greek", "φέρεται να", "εξετάστηκε ιατρικά", "ρώτησε έναν μάρτυρα", "μετά την ασφάλιση", "λεωφορείο τακτικής γραμμής", "το βράδυ της Παρασκευής", "never add successful escape", ":\n\n\nQuelle"} {
		if !strings.Contains(greekPrompt, expected) {
			t.Errorf("Greek guidance omitted %q: %q", expected, greekPrompt)
		}
	}
	croatian, found := langregistry.ByCode(definitions, "hr")
	if !found {
		t.Fatal("Croatian language is not registered")
	}
	croatianPrompt := translateGemmaNativePrompt(source, croatian, translateGemmaLanguageGuidance["hr"], "Quelle")
	for _, expected := range []string{"standardnim hrvatskim", "Još nije jasno je li bio uključen", "Ne izostavljaj ‘osuda nije donesena’", "policijska intervencija", "automobil se sudario sa zidom", "uhićen bez otpora", "prometna nesreća", "nikada ‘uhapšen’"} {
		if !strings.Contains(croatianPrompt, expected) {
			t.Errorf("Croatian guidance omitted %q: %q", expected, croatianPrompt)
		}
		if strings.Contains(greekPrompt, expected) {
			t.Errorf("Croatian guidance leaked into Greek prompt: %q", greekPrompt)
		}
	}
	bosnian, found := langregistry.ByCode(definitions, "bs")
	if !found {
		t.Fatal("Bosnian language is not registered")
	}
	bosnianPrompt := translateGemmaNativePrompt(source, bosnian, translateGemmaLanguageGuidance["bs"], "Quelle")
	for _, expected := range []string{"standardnim bosanskim", "bez srpske ćirilice", "__MB_MUNICIPALITY_0001__ mora ostati __MB_MUNICIPALITY_0001__", "koristeći ‘navodno’", "‘tačno u’", "ljekar", "uhapšen bez otpora", "nikada potvrđena upotreba"} {
		if !strings.Contains(bosnianPrompt, expected) {
			t.Errorf("Bosnian guidance omitted %q: %q", expected, bosnianPrompt)
		}
		if strings.Contains(croatianPrompt, expected) || strings.Contains(greekPrompt, expected) {
			t.Errorf("Bosnian guidance leaked into another prompt")
		}
	}
	if !strings.Contains(bosnianPrompt, "saobraćajna policija") {
		t.Errorf("Bosnian guidance omitted traffic-police wording: %q", bosnianPrompt)
	}
	turkishPrompt := translateGemmaNativePrompt(source, turkish, translateGemmaLanguageGuidance["tr"], "Quelle")
	if strings.Contains(turkishPrompt, "Target-language guidance:") || strings.Contains(turkishPrompt, "Modern Greek") {
		t.Fatalf("Greek guidance leaked into Turkish prompt: %q", turkishPrompt)
	}
}

func TestTranslateGemmaNativeAdapterRejectsCanonicalTarget(t *testing.T) {
	if _, err := NewTranslateGemmaNativeAdapter("http://ollama.test:11434", "translategemma:test", "de", time.Second, 2048, nil); err == nil {
		t.Fatal("canonical German target was accepted")
	}
}
