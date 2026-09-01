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

func TestFrenchTranslateGemmaPromptProtectsTransitLabels(t *testing.T) {
	prompt, found := PromptByVersion(FrenchTranslationPromptVersion)
	if !found || prompt.Status != PromptActive || !prompt.UserOnly || prompt.SystemPrompt != "" {
		t.Fatalf("active French translation prompt = %#v, found=%t", prompt, found)
	}
	for _, expected := range []string{
		"incident-translation-fr-v1", "protected proper names", "recopier exactement chaque chaîne “U-Bahn” et “S-Bahn”",
		"Ne jamais les remplacer par « métro »", "même trait d’union ASCII",
	} {
		text := prompt.UserPromptTemplate
		if expected == "incident-translation-fr-v1" {
			text = prompt.Version
		}
		if !strings.Contains(text, expected) {
			t.Errorf("French v2 prompt omitted %q", expected)
		}
	}
}

func TestRegisteredTranslateGemmaPromptsUseTargetLanguageIdentities(t *testing.T) {
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
		} {
			if !strings.Contains(rendered, expected) {
				t.Errorf("%s TranslateGemma prompt omitted %q", translation.Language, expected)
			}
		}
		if strings.Count(rendered, payload) != 1 {
			t.Errorf("%s TranslateGemma payload occurrence count = %d", translation.Language, strings.Count(rendered, payload))
		}
		for language, expected := range map[string][]string{
			"tr": {"recognizable Latin spelling", "generic street-type word", "rather than relying on an example list", "hafif yaralandı"},
			"hr": {"recognizable Latin spelling", "generic street-type word", "rather than relying on an example list", "opsežna policijska akcija"},
			"it": {"original Latin spelling", "complete street-type suffix", "rather than relying on an example list", "appello ai testimoni"},
			"uk": {"standard, consistent Ukrainian transliteration", "including a street-type suffix", "rather than relying on an example list", "зазнала легких травм"},
			"bs": {"recognizable Latin spelling", "generic street-type word", "rather than relying on an example list", "lakše povrijeđen/a"},
			"zh": {"standard, consistent Chinese transliteration", "including a street-type suffix", "rather than relying on an example list", "受轻伤"},
			"hi": {"standard, consistent Hindi transliteration", "including a street-type suffix", "rather than relying on an example list", "मामूली रूप से घायल", "प्रत्यक्षदर्शियों से अपील"},
			"es": {"original Latin spelling", "complete street-type suffix", "rather than relying on an example list", "amplio operativo policial", "presuntamente"},
			"fr": {"original Latin spelling", "complete street-type suffix", "rather than relying on an example list", "important dispositif policier", "appel à témoins", "protected proper names", "Consigne obligatoire pour la sortie française", "Ne jamais les remplacer par « métro »"},
			"el": {"standard, consistent Greek transliteration", "including a street-type suffix", "rather than relying on an example list", "τραυματίστηκε ελαφρά", "φέρεται να"},
			"ro": {"original Latin spelling", "complete street-type suffix", "indivisible protected proper name", "Regula obligatorie pentru rezultatul în limba română", "nu înlocui numele complet cu „strada”", "amplă operațiune a poliției", "se presupune că"},
			"pl": {"original Latin spelling", "complete street-type suffix", "zakrojona na szeroką skalę akcja policyjna", "prawdopodobnie"},
			"ru": {"standard, consistent Russian transliteration", "including a street-type suffix", "rather than relying on an example list", "лёгкие травмы", "предположительно"},
		} {
			if translation.Language != language {
				continue
			}
			for _, fragment := range expected {
				if !strings.Contains(rendered, fragment) {
					t.Errorf("%s TranslateGemma prompt omitted target guidance %q", language, fragment)
				}
			}
		}
		if translation.Language != EnglishLanguage {
			for _, expected := range []string{
				"streets, squares, parks, bridges, and stations", "street-type", "rather than relying on an example list",
				"copy each complete source spelling at least once, character for character", "If the input contains the literal token “U-Bahn”", "if it contains “S-Bahn”", "reproduce every listed name individually",
			} {
				if !strings.Contains(rendered, expected) {
					t.Errorf("%s TranslateGemma prompt omitted generic place-name contract %q", translation.Language, expected)
				}
			}
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
