package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/egekocabas/munichbrief/internal/gazetteer"
)

type attempt2Result struct {
	Fixture           string                  `json:"fixture"`
	Source            string                  `json:"source"`
	SourceURL         string                  `json:"source_url,omitempty"`
	Language          string                  `json:"language"`
	Model             string                  `json:"model"`
	GermanTitle       string                  `json:"german_title"`
	GermanSummary     string                  `json:"german_summary"`
	ProtectedTitle    string                  `json:"protected_title"`
	ProtectedSummary  string                  `json:"protected_summary"`
	Replacements      []gazetteer.Replacement `json:"replacements"`
	RawModelTitle     string                  `json:"raw_model_title,omitempty"`
	RawModelSummary   string                  `json:"raw_model_summary,omitempty"`
	TranslatedTitle   string                  `json:"translated_title,omitempty"`
	TranslatedSummary string                  `json:"translated_summary,omitempty"`
	Status            string                  `json:"status"`
	Validation        []string                `json:"validation"`
	Error             string                  `json:"error,omitempty"`
}

func TestTemporarySixCaseAttempt2(t *testing.T) {
	if os.Getenv("MUNICHBRIEF_SIX_CASE_ATTEMPT2") != "1" {
		t.Skip("set MUNICHBRIEF_SIX_CASE_ATTEMPT2=1")
	}
	baseURL := os.Getenv("MUNICHBRIEF_OLLAMA_BASE_URL")
	databasePath := os.Getenv("MUNICHBRIEF_GAZETTEER_DATABASE_PATH")
	inputPath := os.Getenv("MUNICHBRIEF_SIX_CASE_ATTEMPT1_REPORT")
	reportPath := os.Getenv("MUNICHBRIEF_SIX_CASE_ATTEMPT2_REPORT")
	if baseURL == "" || databasePath == "" || inputPath == "" || reportPath == "" {
		t.Fatal("Attempt 2 environment is incomplete")
	}

	encoded, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	var attempt1 []attempt2Result
	if err := json.Unmarshal(encoded, &attempt1); err != nil {
		t.Fatal(err)
	}
	fixtures := uniqueAttempt2Fixtures(attempt1)
	if len(fixtures) != 6 {
		t.Fatalf("fixtures=%d, want 6", len(fixtures))
	}

	ctx := context.Background()
	store, err := gazetteer.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	entries, err := store.ActiveEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	matcher, err := gazetteer.NewMatcher(entries)
	if err != nil {
		t.Fatal(err)
	}
	protector := liveMatcherProtector{Matcher: matcher}
	protected := make(map[string]gazetteer.Protected, len(fixtures))
	for _, fixture := range fixtures {
		value, protectErr := protector.Protect(fixture.GermanTitle, fixture.GermanSummary)
		if protectErr != nil {
			t.Fatal(protectErr)
		}
		protected[fixture.Fixture] = value
		t.Logf("PROTECTED fixture=%s replacements=%v\n  title=%s\n  summary=%s", fixture.Fixture, value.Replacements, value.Title, value.Summary)
	}

	groups := []struct {
		model     string
		languages []string
	}{
		{"translategemma:4b", []string{"en", "tr", "hr", "it", "bs", "zh", "es", "fr", "pl"}},
		{"qwen3.5:4b", []string{"uk", "hi", "ru"}},
		{"hf.co/bartowski/Qwen_Qwen3.5-9B-GGUF:Q3_K_M", []string{"ro"}},
	}

	results := loadAttempt2Checkpoint(t, reportPath)
	completed := make(map[string]bool, len(results))
	failures := 0
	for _, result := range results {
		completed[attempt2Key(result.Model, result.Language, result.Fixture)] = true
		if result.Status != "PASS" {
			failures++
		}
	}
	t.Logf("CHECKPOINT completed=%d failures=%d", len(results), failures)
	for _, group := range groups {
		client, clientErr := NewOllamaClient(baseURL, group.model, 10*time.Minute, 8192, nil)
		if clientErr != nil {
			t.Fatal(clientErr)
		}
		for _, language := range group.languages {
			translation, found := TranslationByLanguage(language)
			if !found {
				t.Fatalf("translation %s is not registered", language)
			}
			rawStep := translation.Step
			rawStep.Validator = func(StepInput, *StepOutput) error { return nil }
			for _, fixture := range fixtures {
				if completed[attempt2Key(group.model, language, fixture.Fixture)] {
					continue
				}
				masked := protected[fixture.Fixture]
				result := fixture
				result.Language, result.Model = language, group.model
				result.ProtectedTitle, result.ProtectedSummary, result.Replacements = masked.Title, masked.Summary, masked.Replacements
				protectedInput := StepInput{Values: map[string]string{"title_de": masked.Title, "summary_de": masked.Summary}}
				requestCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
				output, returnedModel, generateErr := client.GenerateStep(requestCtx, rawStep, protectedInput)
				cancel()
				if generateErr != nil {
					if strings.Contains(generateErr.Error(), "call Ollama") {
						t.Fatalf("Ollama transport interrupted model=%s language=%s fixture=%s: %v", group.model, language, fixture.Fixture, generateErr)
					}
					result.Status, result.Error = "FAIL_MODEL_OUTPUT", generateErr.Error()
				} else {
					result.Model = returnedModel
					result.RawModelTitle, result.RawModelSummary = output.Values["title"], output.Values["summary"]
					if validationErr := translation.Step.Validator(protectedInput, &output); validationErr != nil {
						result.Status, result.Error = "FAIL_MODEL_VALIDATION", validationErr.Error()
					} else {
						title, summary, restoreErr := gazetteer.Restore(masked, output.Values["title"], output.Values["summary"])
						if restoreErr != nil {
							result.Status, result.Error = "FAIL_RESTORE", restoreErr.Error()
						} else {
							output.Values["title"], output.Values["summary"] = title, summary
							result.TranslatedTitle, result.TranslatedSummary = title, summary
							result.Validation = validateAttempt2Result(language, fixture, masked, output)
							if len(result.Validation) == 0 {
								result.Status = "PASS"
								result.Validation = []string{"schema/length/NFC/control", "stable placeholder count/order/restoration", "numbers", "plain text without URLs", "target-script predominance", "non-identical translation"}
							} else {
								result.Status = "FAIL_FINAL_VALIDATION"
							}
						}
					}
				}
				if result.Status != "PASS" {
					failures++
				}
				results = append(results, result)
				if err := writeAttempt2Report(reportPath, results); err != nil {
					t.Fatal(err)
				}
				t.Logf("RESULT status=%s model=%s language=%s fixture=%s\n  raw_title=%s\n  raw_summary=%s\n  title=%s\n  summary=%s\n  validation=%v error=%s", result.Status, result.Model, language, fixture.Fixture, result.RawModelTitle, result.RawModelSummary, result.TranslatedTitle, result.TranslatedSummary, result.Validation, result.Error)
			}
		}
	}
	t.Logf("REPORT path=%s cases=%d failures=%d", reportPath, len(results), failures)
	if failures > 0 {
		t.Errorf("%d of %d translations failed mechanical validation", failures, len(results))
	}
}

func uniqueAttempt2Fixtures(results []attempt2Result) []attempt2Result {
	seen := make(map[string]bool)
	var fixtures []attempt2Result
	for _, result := range results {
		if seen[result.Fixture] {
			continue
		}
		seen[result.Fixture] = true
		result.Language, result.Model, result.Status, result.Error = "", "", "", ""
		result.ProtectedTitle, result.ProtectedSummary = "", ""
		result.RawModelTitle, result.RawModelSummary = "", ""
		result.TranslatedTitle, result.TranslatedSummary = "", ""
		result.Replacements, result.Validation = nil, nil
		fixtures = append(fixtures, result)
	}
	return fixtures
}

func loadAttempt2Checkpoint(t *testing.T, path string) []attempt2Result {
	t.Helper()
	encoded, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var results []attempt2Result
	if err := json.Unmarshal(encoded, &results); err != nil {
		t.Fatal(err)
	}
	return results
}

func writeAttempt2Report(path string, results []attempt2Result) error {
	encoded, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o600)
}

func attempt2Key(model, language, fixture string) string {
	return model + "\x00" + language + "\x00" + fixture
}

func validateAttempt2Result(language string, fixture attempt2Result, protected gazetteer.Protected, output StepOutput) []string {
	var failures []string
	title, summary := strings.TrimSpace(output.Values["title"]), strings.TrimSpace(output.Values["summary"])
	combined := title + "\n" + summary
	if title == fixture.GermanTitle && summary == fixture.GermanSummary {
		failures = append(failures, "German input returned unchanged")
	}
	if strings.Contains(combined, "__MB_PLACE_") {
		failures = append(failures, "unrestored placeholder")
	}
	for _, replacement := range protected.Replacements {
		field := title
		if replacement.Field == "summary" {
			field = summary
		}
		if !strings.Contains(field, replacement.Original) {
			failures = append(failures, fmt.Sprintf("missing restored place %q in %s", replacement.Original, replacement.Field))
		}
	}
	if target := map[string]*unicode.RangeTable{"zh": unicode.Han, "hi": unicode.Devanagari, "uk": unicode.Cyrillic, "ru": unicode.Cyrillic}[language]; target != nil && !strings.ContainsFunc(combined, func(character rune) bool { return unicode.Is(target, character) }) {
		failures = append(failures, "missing expected target script")
	}
	sort.Strings(failures)
	return failures
}
