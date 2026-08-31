package processing

import (
	"strings"
	"testing"
)

func TestPromptRegistryResolvesRegisteredVersions(t *testing.T) {
	seen := make(map[string]bool)
	for _, prompt := range promptRegistry {
		if prompt.Version == "" || prompt.UserPromptTemplate == "" || (prompt.UserOnly && prompt.SystemPrompt != "") || (!prompt.UserOnly && prompt.SystemPrompt == "") {
			t.Fatalf("incomplete prompt definition: %#v", prompt)
		}
		if prompt.Status != PromptActive && prompt.Status != PromptRetired {
			t.Fatalf("invalid prompt status: %#v", prompt)
		}
		if seen[prompt.Version] {
			t.Fatalf("duplicate prompt version %q", prompt.Version)
		}
		seen[prompt.Version] = true
		resolved, found := PromptByVersion(prompt.Version)
		if !found || resolved != prompt {
			t.Fatalf("PromptByVersion(%q) = %#v, %t", prompt.Version, resolved, found)
		}
	}
	if _, found := PromptByVersion("unknown"); found {
		t.Fatal("unknown prompt version resolved")
	}
}

func TestEnglishTranslateGemmaV2PromptUsesRegisteredLanguageIdentity(t *testing.T) {
	prompt, found := PromptByVersion(EnglishTranslationPromptVersion)
	if !found || prompt.Status != PromptActive || !prompt.UserOnly || prompt.SystemPrompt != "" {
		t.Fatalf("active English translation prompt = %#v, found=%t", prompt, found)
	}
	retired, found := PromptByVersion(EnglishTranslationV1PromptVersion)
	if !found || retired.Status != PromptRetired || retired.UserOnly {
		t.Fatalf("retired English translation prompt = %#v, found=%t", retired, found)
	}
	const payload = `{"title_de":"Titel","summary_de":"Zusammenfassung."}`
	rendered := promptUserMessage(EnglishTranslationPromptVersion, payload)
	for _, expected := range []string{
		"German (de-DE) to English (en-GB)", `"title_de"`, `"summary_de"`, `"title_en"`, `"summary_en"`,
		"Maxvorstadt", "slightly injured", "presumption-of-innocence",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("TranslateGemma prompt omitted %q: %s", expected, rendered)
		}
	}
	if strings.Count(rendered, payload) != 1 || !strings.HasSuffix(rendered, "English:\n\n\n"+payload) {
		t.Fatalf("TranslateGemma payload separator or occurrence count is invalid: %q", rendered)
	}
}

func TestEveryPipelineStepUsesItsRegisteredActivePrompt(t *testing.T) {
	for _, step := range RegisteredSteps() {
		prompt, found := PromptByVersion(step.PromptVersion)
		if !found {
			t.Fatalf("step %q uses unregistered prompt %q", step.Key, step.PromptVersion)
		}
		if prompt.Status != PromptActive || prompt.StepKey != step.Key || prompt.UserOnly != step.UserOnly || prompt.SystemPrompt != step.SystemPrompt {
			t.Fatalf("step %q prompt does not match registry: %#v", step.Key, prompt)
		}
	}
}
