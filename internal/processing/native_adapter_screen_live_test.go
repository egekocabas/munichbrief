package processing

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/gazetteer"
)

const (
	nativeAdapterScreenOptIn  = "MUNICHBRIEF_NATIVE_ADAPTER_SCREEN_LIVE_TEST"
	nativeAdapterScreenFilter = "MUNICHBRIEF_NATIVE_ADAPTER_SCREEN_ADAPTER"
	nativeAdapterSmokeOptIn   = "MUNICHBRIEF_NATIVE_ADAPTER_SMOKE_LIVE_TEST"
	nativeAdapterSmokeFilter  = "MUNICHBRIEF_NATIVE_ADAPTER_SMOKE_ADAPTER"
	hyMT2ScreenModel          = "hf.co/mradermacher/Hy-MT2-7B-GGUF:Q5_K_M"
	seedXScreenModel          = "hf.co/mradermacher/Seed-X-Instruct-7B-GGUF:Q5_K_M"
)

// evaluationTranslator keeps live evaluation orchestration independent from a
// model's transport and prompt contract. Production routing does not use it.
type evaluationTranslator interface {
	Translate(context.Context, string) (string, string, error)
}

type nativeAdapterScreenSpec struct {
	Adapter       string         `json:"adapter"`
	Model         string         `json:"model"`
	ModelDigest   string         `json:"model_digest"`
	PromptVersion string         `json:"prompt_version"`
	Settings      map[string]any `json:"settings"`
	Languages     []string       `json:"languages"`
	Repetitions   int            `json:"repetitions"`
	ContextSize   int            `json:"context_size"`
}

type nativeAdapterScreenFixture struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

type nativeAdapterScreenConfig struct {
	Version     int                          `json:"version"`
	Placeholder string                       `json:"placeholder_mode"`
	Adapters    []nativeAdapterScreenSpec    `json:"adapters"`
	Fixtures    []nativeAdapterScreenFixture `json:"fixtures"`
}

type nativeAdapterScreenManifest struct {
	RunID         string                    `json:"run_id"`
	CreatedAt     time.Time                 `json:"created_at"`
	Revision      string                    `json:"revision"`
	ConfigHash    string                    `json:"config_hash"`
	Configuration nativeAdapterScreenConfig `json:"configuration"`
}

type nativeAdapterScreenField struct {
	Key            string         `json:"key"`
	Adapter        string         `json:"adapter"`
	Language       string         `json:"language"`
	Fixture        string         `json:"fixture"`
	FixtureHash    string         `json:"fixture_hash"`
	Repetition     int            `json:"repetition"`
	Field          string         `json:"field"`
	State          string         `json:"state"`
	Attempt        int            `json:"attempt"`
	RequestedModel string         `json:"requested_model"`
	ModelDigest    string         `json:"model_digest"`
	ReturnedModel  string         `json:"returned_model,omitempty"`
	PromptVersion  string         `json:"prompt_version"`
	PromptHash     string         `json:"prompt_hash"`
	Settings       map[string]any `json:"settings"`
	Input          string         `json:"input"`
	RawOutput      string         `json:"raw_output,omitempty"`
	StartedAt      time.Time      `json:"started_at,omitempty"`
	FinishedAt     time.Time      `json:"finished_at,omitempty"`
	DurationMS     int64          `json:"duration_ms,omitempty"`
	Error          string         `json:"error,omitempty"`
}

type nativeAdapterScreenChecks struct {
	Placeholders string `json:"placeholders"`
	Numbers      string `json:"numbers"`
	Markdown     string `json:"markdown"`
	TargetScript string `json:"target_script"`
	Restoration  string `json:"restoration"`
	Final        string `json:"final_validation"`
}

type nativeAdapterManualReview struct {
	Status                  string `json:"status"`
	NegationUncertainty     string `json:"negation_uncertainty"`
	AttributionLegalFraming string `json:"attribution_legal_framing"`
	SemanticRelationships   string `json:"semantic_relationships"`
	TransitTerminology      string `json:"transit_terminology"`
	AdditionsOmissions      string `json:"additions_omissions"`
	GrammarFluency          string `json:"grammar_fluency"`
	Notes                   string `json:"notes,omitempty"`
}

type nativeAdapterScreenResult struct {
	Key             string                    `json:"key"`
	Adapter         string                    `json:"adapter"`
	Language        string                    `json:"language"`
	Fixture         string                    `json:"fixture"`
	Repetition      int                       `json:"repetition"`
	Status          string                    `json:"status"`
	Error           string                    `json:"error,omitempty"`
	Checks          nativeAdapterScreenChecks `json:"checks"`
	ManualReview    nativeAdapterManualReview `json:"manual_review"`
	RestoredTitle   string                    `json:"restored_title,omitempty"`
	RestoredSummary string                    `json:"restored_summary,omitempty"`
}

type nativeAdapterScreenEvent struct {
	RecordedAt time.Time                `json:"recorded_at"`
	Field      nativeAdapterScreenField `json:"field"`
}

type nativeAdapterScreenSnapshot struct {
	ConfigHash string                               `json:"config_hash"`
	UpdatedAt  time.Time                            `json:"updated_at"`
	Fields     map[string]nativeAdapterScreenField  `json:"fields"`
	Results    map[string]nativeAdapterScreenResult `json:"results"`
}

type nativeAdapterProtectedFixture struct {
	Title, Summary string
	Protected      gazetteer.Protected
	Markdown       protectedMarkdown
}

type nativeAdapterFactory func(nativeAdapterScreenSpec, string) (evaluationTranslator, func(string) string, error)

var nativeAdapterTokenPattern = regexp.MustCompile(`__MB_[A-Z_]+_[0-9]{4}__`)

func TestLiveNativeTranslationAdapterSmoke(t *testing.T) {
	if os.Getenv(nativeAdapterSmokeOptIn) != "1" {
		t.Skip("set " + nativeAdapterSmokeOptIn + "=1")
	}
	baseURL := os.Getenv("MUNICHBRIEF_OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434"
	}
	fixture := nativeAdapterScreenFixture{
		Name:    "adapter-contract-smoke",
		Title:   "S-Bahn-Einsatz am Hauptbahnhof",
		Summary: "Nach Angaben der Polizei soll am 29. August 2026 um 03:30 Uhr auf der **Ingolstädter Straße** ein Fahrzeug beschädigt worden sein. Die U-Bahn war nicht betroffen; Hinweise stehen bei [Schwabing-West](https://munichbrief.de/de/incidents/378?page=2).",
	}
	matcher, err := nativeAdapterScreenMatcher()
	if err != nil {
		t.Fatal(err)
	}
	protected, err := protectNativeAdapterFixture(matcher, fixture)
	if err != nil {
		t.Fatal(err)
	}

	for _, baseSpec := range nativeAdapterScreenSpecs() {
		spec := baseSpec
		if filter := strings.TrimSpace(os.Getenv(nativeAdapterSmokeFilter)); filter != "" && filter != spec.Adapter {
			continue
		}
		t.Run(spec.Adapter, func(t *testing.T) {
			digest, err := ollamaModelDigest(context.Background(), baseURL, spec.Model)
			if err != nil {
				t.Fatalf("verify installed model identity: %v", err)
			}
			spec.ModelDigest = digest
			adapter, prompt, err := liveNativeAdapterFactory(baseURL)(spec, "en")
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("REQUEST adapter=%s model=%s digest=%s prompt_version=%s settings=%v\n  title_prompt=%s\n  summary_prompt=%s",
				spec.Adapter, spec.Model, spec.ModelDigest, spec.PromptVersion, spec.Settings, prompt(protected.Title), prompt(protected.Summary))

			started := time.Now()
			title, titleModel, titleErr := adapter.Translate(context.Background(), protected.Title)
			titleDuration := time.Since(started)
			if titleErr != nil {
				t.Fatalf("translate title: %v", titleErr)
			}
			t.Logf("TITLE adapter=%s returned_model=%s duration=%s raw=%s", spec.Adapter, titleModel, titleDuration, title)

			started = time.Now()
			summary, summaryModel, summaryErr := adapter.Translate(context.Background(), protected.Summary)
			summaryDuration := time.Since(started)
			if summaryErr != nil {
				t.Fatalf("translate summary: %v", summaryErr)
			}
			t.Logf("SUMMARY adapter=%s returned_model=%s duration=%s raw=%s", spec.Adapter, summaryModel, summaryDuration, summary)

			result := evaluateNativeAdapterScreenResult(spec.Adapter, "en", fixture, protected, 1,
				nativeAdapterScreenField{State: "completed", RawOutput: title},
				nativeAdapterScreenField{State: "completed", RawOutput: summary})
			t.Logf("RESULT adapter=%s status=%s checks=%+v\n  restored_title=%s\n  restored_summary=%s\n  error=%s",
				spec.Adapter, result.Status, result.Checks, result.RestoredTitle, result.RestoredSummary, result.Error)
			if result.Status != "PASS_MECHANICAL_PENDING_REVIEW" {
				t.Errorf("adapter smoke failed mechanical validation: %s: %s", result.Status, result.Error)
			}
		})
	}
}

func TestLiveNativeTranslationAdapterScreen(t *testing.T) {
	if os.Getenv(nativeAdapterScreenOptIn) != "1" {
		t.Skip("set " + nativeAdapterScreenOptIn + "=1")
	}
	baseURL := os.Getenv("MUNICHBRIEF_OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434"
	}
	runDirectory := os.Getenv("MUNICHBRIEF_NATIVE_ADAPTER_SCREEN_DIR")
	if runDirectory == "" {
		runDirectory = filepath.Join(evaluationRepositoryRoot(t), ".local", "translation-evaluations", "native-adapter-screen-v1")
	}
	if err := os.MkdirAll(runDirectory, 0o700); err != nil {
		t.Fatal(err)
	}

	specs := filterNativeAdapterScreenSpecs(nativeAdapterScreenSpecs(), os.Getenv(nativeAdapterScreenFilter))
	if len(specs) == 0 {
		t.Fatalf("%s did not match a configured adapter", nativeAdapterScreenFilter)
	}
	for index := range specs {
		digest, err := ollamaModelDigest(context.Background(), baseURL, specs[index].Model)
		if err != nil {
			t.Fatalf("verify %s model identity: %v", specs[index].Adapter, err)
		}
		specs[index].ModelDigest = digest
	}
	config := nativeAdapterScreenConfig{
		Version: 1, Placeholder: "typed", Adapters: specs, Fixtures: nativeAdapterScreenFixtures(),
	}
	configHash := hashJSON(t, config)
	ensureNativeAdapterScreenManifest(t, runDirectory, configHash, config)
	state := loadNativeAdapterScreenSnapshot(t, runDirectory, configHash)
	matcher, err := nativeAdapterScreenMatcher()
	if err != nil {
		t.Fatal(err)
	}
	factory := liveNativeAdapterFactory(baseURL)

	for _, spec := range specs {
		for _, language := range spec.Languages {
			adapter, prompt, adapterErr := factory(spec, language)
			if adapterErr != nil {
				t.Fatalf("construct %s/%s adapter: %v", spec.Adapter, language, adapterErr)
			}
			for _, fixture := range config.Fixtures {
				protected, protectErr := protectNativeAdapterFixture(matcher, fixture)
				if protectErr != nil {
					t.Fatal(protectErr)
				}
				fixtureHash := hashJSON(t, fixture)
				for repetition := 1; repetition <= spec.Repetitions; repetition++ {
					resultKey := nativeAdapterResultKey(spec.Adapter, language, fixture.Name, repetition)
					if _, complete := state.Results[resultKey]; complete {
						continue
					}
					for _, item := range []struct{ name, input string }{{"title", protected.Title}, {"summary", protected.Summary}} {
						fieldKey := resultKey + "/" + item.name
						if state.Fields[fieldKey].State == "completed" || state.Fields[fieldKey].State == "model_failed" {
							continue
						}
						previous := state.Fields[fieldKey]
						if previous.State == "running" {
							previous.State, previous.Error, previous.FinishedAt = "interrupted", "request was running when the evaluation resumed", time.Now().UTC()
							recordNativeAdapterScreenField(t, runDirectory, &state, previous)
						}
						promptText := prompt(item.input)
						field := nativeAdapterScreenField{
							Key: fieldKey, Adapter: spec.Adapter, Language: language, Fixture: fixture.Name,
							FixtureHash: fixtureHash, Repetition: repetition, Field: item.name, State: "running", Attempt: previous.Attempt + 1,
							RequestedModel: spec.Model, ModelDigest: spec.ModelDigest, PromptVersion: spec.PromptVersion,
							PromptHash: hashString(promptText), Settings: spec.Settings, Input: item.input, StartedAt: time.Now().UTC(),
						}
						recordNativeAdapterScreenField(t, runDirectory, &state, field)
						raw, returnedModel, generateErr := adapter.Translate(context.Background(), item.input)
						field.FinishedAt = time.Now().UTC()
						field.DurationMS = field.FinishedAt.Sub(field.StartedAt).Milliseconds()
						field.ReturnedModel = returnedModel
						if generateErr != nil {
							field.Error = generateErr.Error()
							if KindOf(generateErr) == ErrorTransient {
								field.State = "transport_failed"
								recordNativeAdapterScreenField(t, runDirectory, &state, field)
								t.Fatalf("transport failure at %s (resume the same run): %v", fieldKey, generateErr)
							}
							field.State = "model_failed"
							recordNativeAdapterScreenField(t, runDirectory, &state, field)
							t.Logf("FIELD state=%s key=%s duration=%s error=%s", field.State, field.Key, time.Duration(field.DurationMS)*time.Millisecond, field.Error)
							continue
						}
						field.State, field.RawOutput = "completed", raw
						recordNativeAdapterScreenField(t, runDirectory, &state, field)
						t.Logf("FIELD state=%s key=%s duration=%s\n  output=%s", field.State, field.Key, time.Duration(field.DurationMS)*time.Millisecond, raw)
					}

					result := evaluateNativeAdapterScreenResult(spec.Adapter, language, fixture, protected, repetition,
						state.Fields[resultKey+"/title"], state.Fields[resultKey+"/summary"])
					state.Results[resultKey] = result
					writeNativeAdapterScreenSnapshot(t, runDirectory, state)
					t.Logf("RESULT status=%s adapter=%s language=%s fixture=%s repetition=%d error=%s\n  title=%s\n  summary=%s",
						result.Status, spec.Adapter, language, fixture.Name, repetition, result.Error, result.RestoredTitle, result.RestoredSummary)
				}
			}
		}
	}
	t.Logf("REPORT directory=%s %s", runDirectory, summarizeNativeAdapterScreenResults(state.Results))
}

func nativeAdapterScreenSpecs() []nativeAdapterScreenSpec {
	return []nativeAdapterScreenSpec{
		{
			Adapter: "hy-mt2", Model: hyMT2ScreenModel, PromptVersion: "hy-mt2-structured-placeholder-v1",
			Settings:  map[string]any{"temperature": 0.7, "top_p": 0.6, "top_k": 20, "repeat_penalty": 1.05, "num_predict": 4096, "num_ctx": 8192},
			Languages: []string{"en", "uk", "hi", "ru"}, Repetitions: 2, ContextSize: 8192,
		},
		{
			Adapter: "seed-x", Model: seedXScreenModel, PromptVersion: "seed-x-official-raw-v1",
			Settings:  map[string]any{"temperature": 0, "num_predict": 512, "num_ctx": 4096},
			Languages: []string{"en", "hr", "ro", "uk"}, Repetitions: 1, ContextSize: 4096,
		},
	}
}

func filterNativeAdapterScreenSpecs(specs []nativeAdapterScreenSpec, filter string) []nativeAdapterScreenSpec {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return specs
	}
	filtered := make([]nativeAdapterScreenSpec, 0, 1)
	for _, spec := range specs {
		if spec.Adapter == filter {
			filtered = append(filtered, spec)
		}
	}
	return filtered
}

func nativeAdapterScreenFixtures() []nativeAdapterScreenFixture {
	return []nativeAdapterScreenFixture{
		{
			Name: "typed-transit-relationships", Title: "Störungen zwischen Hauptbahnhof und Ostbahnhof",
			Summary: "Wegen eines Polizeieinsatzes fuhren S-Bahnen vom Hauptbahnhof zum Ostbahnhof verspätet; am Hauptbahnhof hielt die U-Bahn weiterhin planmäßig.",
		},
		{
			Name: "legal-negation-uncertainty", Title: "Ermittlungen an der Leopoldstraße in Schwabing-West",
			Summary: "Nach Angaben der Polizei soll ein Tatverdächtiger an der Leopoldstraße in Schwabing-West die Scheibe eines geparkten Fahrzeugs beschädigt haben. Ob er beteiligt war, ist noch unklar; eine Verurteilung liegt nicht vor und bis zu einer rechtskräftigen Verurteilung gilt die Unschuldsvermutung.",
		},
		{
			Name: "numbers-causality-markdown", Title: "Einsatz an der Ganghoferstraße am 29. August 2026",
			Summary: "Weil um 03:30 Uhr Rauch aus einem Keller an der **Ganghoferstraße** gemeldet wurde, sperrte die Polizei die Straße für 45 Minuten. Zwei Personen wurden untersucht; niemand wurde verletzt. Hinweise stehen bei [Ramersdorf-Perlach](https://munichbrief.de/de/incidents/717?page=2), in dringenden Fällen gilt die 110.",
		},
	}
}

func nativeAdapterScreenMatcher() (*gazetteer.Matcher, error) {
	return gazetteer.NewMatcher([]gazetteer.Entry{
		{Name: "Hauptbahnhof", Kind: gazetteer.KindTrainStation},
		{Name: "Ostbahnhof", Kind: gazetteer.KindTrainStation},
		{Name: "Ingolstädter Straße", Kind: gazetteer.KindStreet},
		{Name: "Leopoldstraße", Kind: gazetteer.KindStreet},
		{Name: "Schwabing-West", Kind: gazetteer.KindDistrict},
		{Name: "Ganghoferstraße", Kind: gazetteer.KindStreet},
		{Name: "Ramersdorf-Perlach", Kind: gazetteer.KindDistrict},
	})
}

func liveNativeAdapterFactory(baseURL string) nativeAdapterFactory {
	return func(spec nativeAdapterScreenSpec, language string) (evaluationTranslator, func(string) string, error) {
		switch spec.Adapter {
		case "hy-mt2":
			adapter, err := NewHyMT2NativeAdapter(baseURL, spec.Model, language, 15*time.Minute, spec.ContextSize, nil)
			if err != nil {
				return nil, nil, err
			}
			target, ok := hyMT2LanguageNames[language]
			if !ok {
				return nil, nil, fmt.Errorf("Hy-MT2 prompt language %q is unsupported", language)
			}
			return adapter, func(text string) string { return hyMT2NativePrompt(hyMT2LanguageNames["de"], target, text) }, nil
		case "seed-x":
			adapter, err := NewSeedXNativeAdapter(baseURL, spec.Model, language, 15*time.Minute, spec.ContextSize, nil)
			if err != nil {
				return nil, nil, err
			}
			target, ok := seedXLanguages[language]
			if !ok {
				return nil, nil, fmt.Errorf("Seed-X prompt language %q is unsupported", language)
			}
			return adapter, func(text string) string { return seedXNativePrompt(seedXLanguages["de"], target, text) }, nil
		default:
			return nil, nil, fmt.Errorf("unknown evaluation adapter %q", spec.Adapter)
		}
	}
}

func protectNativeAdapterFixture(matcher *gazetteer.Matcher, fixture nativeAdapterScreenFixture) (nativeAdapterProtectedFixture, error) {
	protected, err := matcher.ProtectWithOptions(fixture.Title, fixture.Summary, gazetteer.ProtectionOptions{Mode: gazetteer.ProtectionTyped})
	if err != nil {
		return nativeAdapterProtectedFixture{}, err
	}
	markdown := protectMarkdown(&protected)
	return nativeAdapterProtectedFixture{Title: protected.Title, Summary: protected.Summary, Protected: protected, Markdown: markdown}, nil
}

func evaluateNativeAdapterScreenResult(adapter, language string, fixture nativeAdapterScreenFixture, protected nativeAdapterProtectedFixture, repetition int, titleField, summaryField nativeAdapterScreenField) nativeAdapterScreenResult {
	result := nativeAdapterScreenResult{
		Key: nativeAdapterResultKey(adapter, language, fixture.Name, repetition), Adapter: adapter, Language: language,
		Fixture: fixture.Name, Repetition: repetition, Checks: pendingNativeAdapterChecks(), ManualReview: pendingNativeAdapterManualReview(),
	}
	if titleField.State != "completed" || summaryField.State != "completed" {
		result.Status, result.Error = "FAIL_MODEL_OUTPUT", firstNonEmpty(titleField.Error, summaryField.Error, "model field did not complete")
		return result
	}
	if !sameStringMultiset(nativeAdapterTokenPattern.FindAllString(protected.Title, -1), nativeAdapterTokenPattern.FindAllString(titleField.RawOutput, -1)) ||
		!sameStringMultiset(nativeAdapterTokenPattern.FindAllString(protected.Summary, -1), nativeAdapterTokenPattern.FindAllString(summaryField.RawOutput, -1)) {
		result.Status, result.Error, result.Checks.Placeholders = "FAIL_TOKEN_PRESERVATION", "model changed, removed, added, or moved a typed placeholder between fields", "fail"
		return result
	}
	result.Checks.Placeholders = "pass"
	if !sameTranslationNumbers(protected.Title, titleField.RawOutput) || !sameTranslationNumbers(protected.Summary, summaryField.RawOutput) {
		result.Status, result.Error, result.Checks.Numbers = "FAIL_NUMBERS", "model changed, added, or removed a numeric fact", "fail"
		return result
	}
	result.Checks.Numbers = "pass"
	if !sameMarkdownStructure(protected.Title, titleField.RawOutput) || !sameMarkdownStructure(protected.Summary, summaryField.RawOutput) {
		result.Status, result.Error, result.Checks.Markdown = "FAIL_MARKDOWN", "model changed Markdown or a web address", "fail"
		return result
	}
	result.Checks.Markdown = "pass"
	if err := validateTargetScript(language, nativeAdapterTokenPattern.ReplaceAllString(titleField.RawOutput, ""), nativeAdapterTokenPattern.ReplaceAllString(summaryField.RawOutput, "")); err != nil {
		result.Status, result.Error, result.Checks.TargetScript = "FAIL_TARGET_SCRIPT", err.Error(), "fail"
		return result
	}
	result.Checks.TargetScript = "pass"
	title, summary, err := protected.Markdown.restore(titleField.RawOutput, summaryField.RawOutput)
	if err == nil {
		title, summary, err = gazetteer.Restore(protected.Protected, title, summary)
	}
	if err != nil {
		result.Status, result.Error, result.Checks.Restoration = "FAIL_RESTORATION", err.Error(), "fail"
		return result
	}
	result.Checks.Restoration = "pass"
	result.RestoredTitle, result.RestoredSummary = title, summary
	input := StepInput{Values: map[string]string{"title_de": fixture.Title, "summary_de": fixture.Summary}}
	output := StepOutput{Values: map[string]string{"title": title, "summary": summary}}
	if err := validateTranslation(input, &output); err != nil {
		result.Status, result.Error, result.Checks.Final = "FAIL_FINAL_VALIDATION", err.Error(), "fail"
		return result
	}
	result.Checks.Final = "pass"
	result.RestoredTitle, result.RestoredSummary = output.Values["title"], output.Values["summary"]
	result.Status = "PASS_MECHANICAL_PENDING_REVIEW"
	return result
}

func pendingNativeAdapterChecks() nativeAdapterScreenChecks {
	return nativeAdapterScreenChecks{Placeholders: "pending", Numbers: "pending", Markdown: "pending", TargetScript: "pending", Restoration: "pending", Final: "pending"}
}

func pendingNativeAdapterManualReview() nativeAdapterManualReview {
	return nativeAdapterManualReview{
		Status: "pending", NegationUncertainty: "pending", AttributionLegalFraming: "pending",
		SemanticRelationships: "pending", TransitTerminology: "pending", AdditionsOmissions: "pending", GrammarFluency: "pending",
	}
}

func nativeAdapterResultKey(adapter, language, fixture string, repetition int) string {
	return fmt.Sprintf("%s/%s/%s/%d", adapter, language, fixture, repetition)
}

func ensureNativeAdapterScreenManifest(t *testing.T, directory, configHash string, config nativeAdapterScreenConfig) {
	t.Helper()
	path := filepath.Join(directory, "manifest.json")
	encoded, err := os.ReadFile(path)
	if err == nil {
		var manifest nativeAdapterScreenManifest
		if json.Unmarshal(encoded, &manifest) != nil || manifest.ConfigHash != configHash {
			t.Fatalf("evaluation manifest differs from this run; choose a new directory instead of mixing configurations")
		}
		return
	}
	if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	writeAtomicJSON(t, path, nativeAdapterScreenManifest{
		RunID: filepath.Base(directory), CreatedAt: time.Now().UTC(), Revision: evaluationRevision(), ConfigHash: configHash, Configuration: config,
	})
}

func loadNativeAdapterScreenSnapshot(t *testing.T, directory, configHash string) nativeAdapterScreenSnapshot {
	t.Helper()
	state := nativeAdapterScreenSnapshot{ConfigHash: configHash, Fields: make(map[string]nativeAdapterScreenField), Results: make(map[string]nativeAdapterScreenResult)}
	path := filepath.Join(directory, "snapshot.json")
	if encoded, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(encoded, &state); err != nil {
			t.Fatalf("decode native-adapter snapshot: %v", err)
		}
		if state.ConfigHash != configHash {
			t.Fatal("native-adapter snapshot configuration hash differs from manifest")
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if state.Fields == nil {
		state.Fields = make(map[string]nativeAdapterScreenField)
	}
	if state.Results == nil {
		state.Results = make(map[string]nativeAdapterScreenResult)
	}
	events, err := os.Open(filepath.Join(directory, "events.jsonl"))
	if os.IsNotExist(err) {
		return state
	}
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	scanner := bufio.NewScanner(events)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		var event nativeAdapterScreenEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode native-adapter event: %v", err)
		}
		state.Fields[event.Field.Key] = event.Field
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return state
}

func recordNativeAdapterScreenField(t *testing.T, directory string, state *nativeAdapterScreenSnapshot, field nativeAdapterScreenField) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(directory, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(nativeAdapterScreenEvent{RecordedAt: time.Now().UTC(), Field: field})
	if err == nil {
		_, err = file.Write(append(encoded, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	state.Fields[field.Key] = field
	writeNativeAdapterScreenSnapshot(t, directory, *state)
}

func writeNativeAdapterScreenSnapshot(t *testing.T, directory string, state nativeAdapterScreenSnapshot) {
	t.Helper()
	state.UpdatedAt = time.Now().UTC()
	writeAtomicJSON(t, filepath.Join(directory, "snapshot.json"), state)
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func summarizeNativeAdapterScreenResults(results map[string]nativeAdapterScreenResult) string {
	type count struct{ passed, total int }
	counts := make(map[string]count)
	for _, result := range results {
		key := result.Adapter + "/" + result.Language
		current := counts[key]
		current.total++
		if result.Status == "PASS_MECHANICAL_PENDING_REVIEW" {
			current.passed++
		}
		counts[key] = current
	}
	lines := make([]string, 0, len(counts))
	for key, current := range counts {
		lines = append(lines, fmt.Sprintf("%s=%d/%d", key, current.passed, current.total))
	}
	sort.Strings(lines)
	return strings.Join(lines, ", ")
}

func TestNativeAdapterScreenFixturesUseTypedPlaceholdersAndRestore(t *testing.T) {
	matcher, err := nativeAdapterScreenMatcher()
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range nativeAdapterScreenFixtures() {
		protected, err := protectNativeAdapterFixture(matcher, fixture)
		if err != nil {
			t.Fatal(err)
		}
		if !nativeAdapterTokenPattern.MatchString(protected.Title + protected.Summary) {
			t.Fatalf("fixture %s contains no typed placeholder: %#v", fixture.Name, protected)
		}
		title, summary, err := protected.Markdown.restore(protected.Title, protected.Summary)
		if err == nil {
			title, summary, err = gazetteer.Restore(protected.Protected, title, summary)
		}
		if err != nil || title != fixture.Title || summary != fixture.Summary {
			t.Fatalf("fixture %s restoration = %q / %q / %v", fixture.Name, title, summary, err)
		}
	}
}

func TestNativeAdapterScreenMatrixHasSeventyTwoFieldCalls(t *testing.T) {
	calls := 0
	for _, spec := range nativeAdapterScreenSpecs() {
		calls += len(spec.Languages) * len(nativeAdapterScreenFixtures()) * spec.Repetitions * 2
	}
	if calls != 72 {
		t.Fatalf("native-adapter screen calls = %d, want 72", calls)
	}
}

func TestNativeAdapterScreenFilterSelectsOneAdapter(t *testing.T) {
	specs := nativeAdapterScreenSpecs()
	filtered := filterNativeAdapterScreenSpecs(specs, "hy-mt2")
	if len(filtered) != 1 || filtered[0].Adapter != "hy-mt2" {
		t.Fatalf("filtered screen specs = %#v", filtered)
	}
	if filtered := filterNativeAdapterScreenSpecs(specs, "unknown"); len(filtered) != 0 {
		t.Fatalf("unknown adapter filter = %#v", filtered)
	}
}

func TestNativeAdapterScreenReplayKeepsAdapterCheckpointsSeparate(t *testing.T) {
	directory := t.TempDir()
	state := nativeAdapterScreenSnapshot{ConfigHash: "test", Fields: make(map[string]nativeAdapterScreenField), Results: make(map[string]nativeAdapterScreenResult)}
	for _, adapter := range []string{"hy-mt2", "seed-x"} {
		field := nativeAdapterScreenField{Key: nativeAdapterResultKey(adapter, "en", "fixture", 1) + "/title", Adapter: adapter, State: "completed", RawOutput: adapter}
		recordNativeAdapterScreenField(t, directory, &state, field)
	}
	if err := os.Remove(filepath.Join(directory, "snapshot.json")); err != nil {
		t.Fatal(err)
	}
	replayed := loadNativeAdapterScreenSnapshot(t, directory, "test")
	if len(replayed.Fields) != 2 {
		t.Fatalf("replayed fields = %#v", replayed.Fields)
	}
	for _, adapter := range []string{"hy-mt2", "seed-x"} {
		key := nativeAdapterResultKey(adapter, "en", "fixture", 1) + "/title"
		if replayed.Fields[key].RawOutput != adapter {
			t.Fatalf("adapter checkpoint %s = %#v", adapter, replayed.Fields[key])
		}
	}
}

func TestNativeAdapterScreenResultSeparatesMechanicalAndManualReview(t *testing.T) {
	fixture := nativeAdapterScreenFixtures()[0]
	matcher, err := nativeAdapterScreenMatcher()
	if err != nil {
		t.Fatal(err)
	}
	protected, err := protectNativeAdapterFixture(matcher, fixture)
	if err != nil {
		t.Fatal(err)
	}
	result := evaluateNativeAdapterScreenResult("hy-mt2", "en", fixture, protected, 1,
		nativeAdapterScreenField{State: "completed", RawOutput: protected.Title},
		nativeAdapterScreenField{State: "completed", RawOutput: protected.Summary})
	if result.Status != "PASS_MECHANICAL_PENDING_REVIEW" || result.ManualReview.Status != "pending" || result.Checks.Final != "pass" {
		t.Fatalf("screen result = %#v", result)
	}
}
