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

	"github.com/egekocabas/munichbrief/internal/gazetteer"
)

const translateGemmaComparisonOptIn = "MUNICHBRIEF_TRANSLATEGEMMA_COMPARISON_LIVE_TEST"

type translateGemmaComparisonFixture struct {
	Name, Title, Summary string
}

type translateGemmaComparisonResult struct {
	Fixture          string `json:"fixture"`
	Language         string `json:"language"`
	RequestedModel   string `json:"requested_model"`
	ReturnedModel    string `json:"returned_model,omitempty"`
	GermanTitle      string `json:"german_title"`
	GermanSummary    string `json:"german_summary"`
	ProtectedTitle   string `json:"protected_title"`
	ProtectedSummary string `json:"protected_summary"`
	RawTitle         string `json:"raw_title,omitempty"`
	RawSummary       string `json:"raw_summary,omitempty"`
	RestoredTitle    string `json:"restored_title,omitempty"`
	RestoredSummary  string `json:"restored_summary,omitempty"`
	Status           string `json:"status"`
	Error            string `json:"error,omitempty"`
	DurationMS       int64  `json:"duration_ms"`
}

func TestLiveTranslateGemmaNativeAdapterComparison(t *testing.T) {
	if os.Getenv(translateGemmaComparisonOptIn) != "1" {
		t.Skip("set " + translateGemmaComparisonOptIn + "=1")
	}
	baseURL := os.Getenv("MUNICHBRIEF_OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434"
	}
	reportPath := os.Getenv("MUNICHBRIEF_TRANSLATEGEMMA_COMPARISON_REPORT")
	if reportPath == "" {
		reportPath = "/tmp/munichbrief-translategemma-comparison.json"
	}

	models := []string{
		"translategemma:4b",
		"translategemma:4b-it-q8_0",
		"hf.co/mradermacher/translategemma-12b-it-i1-GGUF:IQ3_M",
	}
	languages := []string{"tr", "zh", "uk"}
	fixtures := []translateGemmaComparisonFixture{
		{
			Name:    "facts-and-order",
			Title:   "Geschwindigkeitsverstoß in Milbertshofen",
			Summary: "Am 29. August 2026 um 03:30 Uhr wurde ein Audi auf der Ingolstädter Straße in Milbertshofen kontrolliert. Dem Fahrer wurden nach Abzug der Messtoleranz 93 km/h in einer 50-km/h-Zone vorgeworfen. Eine Passantin rief die 110; mehrere Polizeistreifen und eine Drohne wurden eingesetzt.",
		},
		{
			Name:    "legal-uncertainty",
			Title:   "Ermittlungen an der Leopoldstraße in Schwabing-West",
			Summary: "Ein Tatverdächtiger soll an der Leopoldstraße in Schwabing-West die Scheibe eines geparkten Fahrzeugs beschädigt haben. Die Polizei nahm ihn vorläufig fest. Eine Verurteilung liegt nicht vor, und die Hintergründe werden weiterhin geprüft.",
		},
		{
			Name:    "markdown-and-transit",
			Title:   "Störungen zwischen Hauptbahnhof und Ostbahnhof",
			Summary: "Wegen eines Polizeieinsatzes fuhren mehrere S-Bahnen zwischen **Hauptbahnhof** und Ostbahnhof verspätet. Die U-Bahn war nicht betroffen. Weitere Hinweise nennt die Meldung zu [Ramersdorf-Perlach](https://munichbrief.de/de/incidents/717); zur Ursache machte die Polizei zunächst keine Angaben.",
		},
	}

	matcher, err := comparisonMatcher()
	if err != nil {
		t.Fatal(err)
	}
	results := loadTranslateGemmaComparison(t, reportPath)
	for _, model := range models {
		for _, language := range languages {
			adapter, adapterErr := NewTranslateGemmaNativeAdapter(baseURL, model, language, 10*time.Minute, 2048, nil)
			if adapterErr != nil {
				t.Fatal(adapterErr)
			}
			for _, fixture := range fixtures {
				key := translateGemmaComparisonKey(model, language, fixture.Name)
				index := comparisonResultIndex(results, key)
				if index >= 0 && results[index].Status != "PARTIAL" {
					continue
				}
				result := translateGemmaComparisonResult{
					Fixture: fixture.Name, Language: language, RequestedModel: model,
					GermanTitle: fixture.Title, GermanSummary: fixture.Summary, Status: "PARTIAL",
				}
				if index >= 0 {
					result = results[index]
				}
				protected, protectErr := matcher.Protect(fixture.Title, fixture.Summary)
				if protectErr != nil {
					t.Fatal(protectErr)
				}
				markdown := protectMarkdown(&protected)
				result.ProtectedTitle, result.ProtectedSummary = protected.Title, protected.Summary
				started := time.Now()
				if result.RawTitle == "" {
					raw, returnedModel, generateErr := adapter.Translate(context.Background(), protected.Title)
					result.DurationMS += time.Since(started).Milliseconds()
					if generateErr != nil {
						if KindOf(generateErr) == ErrorTransient {
							upsertTranslateGemmaComparison(t, reportPath, &results, result)
							t.Fatalf("transient title generation failure model=%s language=%s fixture=%s: %v", model, language, fixture.Name, generateErr)
						}
						result.Status, result.Error = "FAIL_MODEL_OUTPUT", generateErr.Error()
						upsertTranslateGemmaComparison(t, reportPath, &results, result)
						continue
					}
					result.RawTitle, result.ReturnedModel = raw, returnedModel
					upsertTranslateGemmaComparison(t, reportPath, &results, result)
				}
				started = time.Now()
				rawSummary, returnedModel, generateErr := adapter.Translate(context.Background(), protected.Summary)
				result.DurationMS += time.Since(started).Milliseconds()
				if generateErr != nil {
					if KindOf(generateErr) == ErrorTransient {
						upsertTranslateGemmaComparison(t, reportPath, &results, result)
						t.Fatalf("transient summary generation failure model=%s language=%s fixture=%s: %v", model, language, fixture.Name, generateErr)
					}
					result.Status, result.Error = "FAIL_MODEL_OUTPUT", generateErr.Error()
					upsertTranslateGemmaComparison(t, reportPath, &results, result)
					continue
				}
				result.RawSummary, result.ReturnedModel = rawSummary, returnedModel

				protectedInput := StepInput{Values: map[string]string{"title_de": protected.Title, "summary_de": protected.Summary}}
				output := StepOutput{Values: map[string]string{"title": result.RawTitle, "summary": result.RawSummary}}
				if validationErr := translationValidator(language)(protectedInput, &output); validationErr != nil {
					result.Status, result.Error = "FAIL_MODEL_VALIDATION", validationErr.Error()
				} else {
					title, summary, markdownErr := markdown.restore(output.Values["title"], output.Values["summary"])
					if markdownErr != nil {
						result.Status, result.Error = "FAIL_MARKDOWN_RESTORE", markdownErr.Error()
					} else {
						title, summary, restoreErr := gazetteer.Restore(protected, title, summary)
						if restoreErr != nil {
							result.Status, result.Error = "FAIL_PLACE_RESTORE", restoreErr.Error()
						} else {
							result.RestoredTitle, result.RestoredSummary = title, summary
							finalInput := StepInput{Values: map[string]string{"title_de": fixture.Title, "summary_de": fixture.Summary}}
							finalOutput := StepOutput{Values: map[string]string{"title": title, "summary": summary}}
							if finalErr := validateTranslation(finalInput, &finalOutput); finalErr != nil {
								result.Status, result.Error = "FAIL_FINAL_VALIDATION", finalErr.Error()
							} else {
								result.Status, result.Error = "PASS", ""
							}
						}
					}
				}
				upsertTranslateGemmaComparison(t, reportPath, &results, result)
				t.Logf("RESULT status=%s model=%s language=%s fixture=%s duration=%s\n  title=%s\n  summary=%s\n  error=%s", result.Status, model, language, fixture.Name, time.Duration(result.DurationMS)*time.Millisecond, result.RawTitle, result.RawSummary, result.Error)
			}
		}
	}

	failures := 0
	for _, result := range results {
		if result.Status != "PASS" {
			failures++
		}
	}
	t.Logf("REPORT path=%s cases=%d failures=%d summary=%s", reportPath, len(results), failures, summarizeTranslateGemmaComparison(results))
	if failures > 0 {
		t.Errorf("%d of %d native TranslateGemma comparisons failed mechanical validation", failures, len(results))
	}
}

func comparisonMatcher() (*gazetteer.Matcher, error) {
	names := []string{"Milbertshofen", "Ingolstädter Straße", "Leopoldstraße", "Schwabing-West", "Hauptbahnhof", "Ostbahnhof", "Ramersdorf-Perlach"}
	entries := make([]gazetteer.Entry, len(names))
	for index, name := range names {
		entries[index] = gazetteer.Entry{Name: name, Kind: gazetteer.KindLandmark, Priority: 0}
	}
	return gazetteer.NewMatcher(entries)
}

func translateGemmaComparisonKey(model, language, fixture string) string {
	return model + "\x00" + language + "\x00" + fixture
}

func comparisonResultIndex(results []translateGemmaComparisonResult, key string) int {
	for index, result := range results {
		if translateGemmaComparisonKey(result.RequestedModel, result.Language, result.Fixture) == key {
			return index
		}
	}
	return -1
}

func loadTranslateGemmaComparison(t *testing.T, path string) []translateGemmaComparisonResult {
	t.Helper()
	encoded, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var results []translateGemmaComparisonResult
	if err := json.Unmarshal(encoded, &results); err != nil {
		t.Fatal(err)
	}
	return results
}

func upsertTranslateGemmaComparison(t *testing.T, path string, results *[]translateGemmaComparisonResult, result translateGemmaComparisonResult) {
	t.Helper()
	key := translateGemmaComparisonKey(result.RequestedModel, result.Language, result.Fixture)
	if index := comparisonResultIndex(*results, key); index >= 0 {
		(*results)[index] = result
	} else {
		*results = append(*results, result)
	}
	encoded, err := json.MarshalIndent(*results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func summarizeTranslateGemmaComparison(results []translateGemmaComparisonResult) string {
	type count struct{ passed, total int }
	counts := make(map[string]count)
	for _, result := range results {
		key := result.RequestedModel + "/" + result.Language
		current := counts[key]
		current.total++
		if result.Status == "PASS" {
			current.passed++
		}
		counts[key] = current
	}
	var lines []string
	for key, current := range counts {
		lines = append(lines, fmt.Sprintf("%s=%d/%d", key, current.passed, current.total))
	}
	sort.Strings(lines)
	return strings.Join(lines, ", ")
}
