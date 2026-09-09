package processing

import (
	"strings"
	"testing"

	langregistry "github.com/egekocabas/munichbrief/internal/languages"
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

func TestRegisteredTranslationPromptsUseOnePlaceholderContract(t *testing.T) {
	var sharedBody string
	for _, translation := range RegisteredTranslations() {
		prompt, found := PromptByVersion(translation.PromptVersion)
		if !found || prompt.Status != PromptActive || !prompt.UserOnly || prompt.SystemPrompt != "" {
			t.Fatalf("active %s translation prompt = %#v, found=%t", translation.Language, prompt, found)
		}
		definition, found := langregistry.ByCode(langregistry.Registered(), translation.Language)
		if !found {
			t.Fatalf("translation language %q is not registered", translation.Language)
		}
		payload := `{"title_de":"Titel","summary_de":"Zusammenfassung."}`
		rendered := promptUserMessage(translation.PromptVersion, payload)
		fieldCode := strings.ReplaceAll(translation.Language, "-", "_")
		for _, expected := range []string{
			"German (de-DE) to " + definition.TranslationName + " (" + definition.Tag.String() + ")",
			`"title_` + fieldCode + `"`, `"summary_` + fieldCode + `"`, "presumption-of-innocence",
			`__MB_[A-Z_]+_[0-9]{4}__`, "STREET, DISTRICT, TRAIN_STATION, COMMUTER_TRAIN, or SUBWAY_SYSTEM", "same token can intentionally occur more than once", "Copy every token occurrence exactly", "natural target-language word order", "apostrophe-delimited grammatical suffix", "Output plain text without Markdown or URLs",
		} {
			if !strings.Contains(rendered, expected) {
				t.Errorf("%s unified translation prompt omitted %q", translation.Language, expected)
			}
		}
		if strings.Count(rendered, payload) != 1 {
			t.Errorf("%s unified translation payload occurrence count = %d", translation.Language, strings.Count(rendered, payload))
		}
		if !strings.Contains(rendered, ":\n\n\n"+payload) || strings.Contains(rendered, ":\n\n\n\n"+payload) {
			t.Errorf("%s unified translation prompt must have exactly two blank lines before the payload", translation.Language)
		}
		body := strings.ReplaceAll(prompt.UserPromptTemplate, definition.TranslationName, "TARGET")
		if guidance := strings.TrimSpace(structuredTranslationLanguageGuidance[translation.Language]); guidance != "" {
			body = strings.Replace(prompt.UserPromptTemplate, guidance+"\n", "", 1)
			body = strings.ReplaceAll(body, definition.TranslationName, "TARGET")
		}
		body = strings.ReplaceAll(body, definition.Tag.String(), "TAG")
		body = strings.ReplaceAll(body, `title_`+fieldCode, "title_TARGET")
		body = strings.ReplaceAll(body, `summary_`+fieldCode, "summary_TARGET")
		if sharedBody == "" {
			sharedBody = body
		} else if body != sharedBody {
			t.Errorf("%s prompt diverged from the unified template", translation.Language)
		}
	}
}

func TestStructuredTranslationLanguageGuidanceIsIsolated(t *testing.T) {
	payload := `{"title_de":"Unfall an der __MB_STREET_0001__","summary_de":"Unfall an der __MB_STREET_0001__."}`
	romanian := promptUserMessage(RomanianTranslationPromptVersion, payload)
	for _, expected := range []string{"never ‘în apropierea’", "never ‘poliția criminalistică’", "plural meaning of German ‘S-Bahnen’", "both trailing underscore characters"} {
		if !strings.Contains(romanian, expected) {
			t.Fatalf("Romanian guidance missing %q: %q", expected, romanian)
		}
	}
	for _, translation := range RegisteredTranslations() {
		if translation.Language == "ro" {
			continue
		}
		if rendered := promptUserMessage(translation.PromptVersion, payload); strings.Contains(rendered, "în apropierea") || strings.Contains(rendered, "poliția criminalistică") || strings.Contains(rendered, "plural meaning of German ‘S-Bahnen’") {
			t.Fatalf("Romanian guidance leaked into %s: %q", translation.Language, rendered)
		}
	}
}

func TestPreviousTranslationPromptsRemainRetiredAndAddressable(t *testing.T) {
	versions := []string{EnglishTranslationV1PromptVersion, EnglishTranslationV2PromptVersion}
	for _, version := range versions {
		prompt, found := PromptByVersion(version)
		if !found || prompt.Status != PromptRetired {
			t.Errorf("retired prompt %q = %#v, found=%v", version, prompt, found)
		}
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
