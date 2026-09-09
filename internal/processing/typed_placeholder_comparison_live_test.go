package processing

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/gazetteer"
	langregistry "github.com/egekocabas/munichbrief/internal/languages"
)

const (
	typedPlaceholderComparisonOptIn = "MUNICHBRIEF_TYPED_PLACEHOLDER_LIVE_TEST"
	typedPlaceholderModel           = "hf.co/mradermacher/translategemma-12b-it-i1-GGUF:IQ3_M"
)

type typedPlaceholderConfig struct {
	Version       int                       `json:"version"`
	Model         string                    `json:"model"`
	ModelDigest   string                    `json:"model_digest"`
	PromptVersion string                    `json:"prompt_version"`
	Languages     []string                  `json:"languages"`
	Strategies    []string                  `json:"strategies"`
	Fixtures      []typedPlaceholderFixture `json:"fixtures"`
	Repetitions   int                       `json:"repetitions"`
	ContextSize   int                       `json:"context_size"`
	Temperature   float64                   `json:"temperature"`
}

type typedPlaceholderManifest struct {
	RunID         string                 `json:"run_id"`
	CreatedAt     time.Time              `json:"created_at"`
	Revision      string                 `json:"revision"`
	ConfigHash    string                 `json:"config_hash"`
	Configuration typedPlaceholderConfig `json:"configuration"`
}

type typedPlaceholderField struct {
	Key           string    `json:"key"`
	Strategy      string    `json:"strategy"`
	Language      string    `json:"language"`
	Fixture       string    `json:"fixture"`
	Repetition    int       `json:"repetition"`
	Field         string    `json:"field"`
	State         string    `json:"state"`
	Attempt       int       `json:"attempt"`
	Input         string    `json:"input"`
	Prompt        string    `json:"prompt"`
	RawOutput     string    `json:"raw_output,omitempty"`
	ReturnedModel string    `json:"returned_model,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	FinishedAt    time.Time `json:"finished_at,omitempty"`
	DurationMS    int64     `json:"duration_ms,omitempty"`
	Error         string    `json:"error,omitempty"`
}

type typedPlaceholderEvent struct {
	RecordedAt time.Time             `json:"recorded_at"`
	Field      typedPlaceholderField `json:"field"`
}

type typedPlaceholderResult struct {
	Key             string `json:"key"`
	Strategy        string `json:"strategy"`
	Language        string `json:"language"`
	Fixture         string `json:"fixture"`
	Repetition      int    `json:"repetition"`
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
	RestoredTitle   string `json:"restored_title,omitempty"`
	RestoredSummary string `json:"restored_summary,omitempty"`
}

type typedPlaceholderSnapshot struct {
	ConfigHash string                            `json:"config_hash"`
	UpdatedAt  time.Time                         `json:"updated_at"`
	Fields     map[string]typedPlaceholderField  `json:"fields"`
	Results    map[string]typedPlaceholderResult `json:"results"`
}

type typedPlaceholderFixture struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

type typedProtectedFixture struct {
	Title, Summary string
	Protected      gazetteer.Protected
}

var typedPlaceholderPattern = regexp.MustCompile(`__MB_[A-Z_]+_[0-9]{4}__`)

func TestLiveTranslateGemmaTypedPlaceholderComparison(t *testing.T) {
	if os.Getenv(typedPlaceholderComparisonOptIn) != "1" {
		t.Skip("set " + typedPlaceholderComparisonOptIn + "=1")
	}
	baseURL := os.Getenv("MUNICHBRIEF_OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434"
	}
	runDirectory := os.Getenv("MUNICHBRIEF_TYPED_PLACEHOLDER_EVAL_DIR")
	if runDirectory == "" {
		runDirectory = filepath.Join(evaluationRepositoryRoot(t), ".local", "translation-evaluations", "typed-placeholders-plain-text-v1")
	}
	if err := os.MkdirAll(runDirectory, 0o700); err != nil {
		t.Fatal(err)
	}

	digest, err := ollamaModelDigest(context.Background(), baseURL, typedPlaceholderModel)
	if err != nil {
		t.Fatalf("resolve model digest: %v", err)
	}
	fixtures := typedPlaceholderFixtures()
	config := typedPlaceholderConfig{
		Version: 1, Model: typedPlaceholderModel, ModelDigest: digest,
		PromptVersion: "translategemma-native-language-code-v1",
		Languages:     []string{"en", "tr", "zh", "uk", "pl"},
		Strategies:    []string{"opaque", "typed", "visible-transit"},
		Fixtures:      fixtures,
		Repetitions:   2, ContextSize: 2048, Temperature: 0,
	}
	configHash := hashJSON(t, config)
	ensureTypedPlaceholderManifest(t, runDirectory, configHash, config)
	state := loadTypedPlaceholderSnapshot(t, runDirectory, configHash)
	matcher, err := typedPlaceholderMatcher()
	if err != nil {
		t.Fatal(err)
	}

	definitions := langregistry.Registered()
	source := langregistry.Canonical(definitions)
	for _, language := range config.Languages {
		target, found := langregistry.ByCode(definitions, language)
		if !found || target.Canonical {
			t.Fatalf("unknown translated target %q", language)
		}
		adapter, adapterErr := NewTranslateGemmaNativeAdapter(baseURL, config.Model, language, 10*time.Minute, config.ContextSize, nil)
		if adapterErr != nil {
			t.Fatal(adapterErr)
		}
		for _, fixture := range fixtures {
			for repetition := 1; repetition <= config.Repetitions; repetition++ {
				for _, strategy := range config.Strategies {
					protected, protectErr := protectTypedFixture(matcher, fixture, strategy)
					if protectErr != nil {
						t.Fatal(protectErr)
					}
					resultKey := typedResultKey(strategy, language, fixture.Name, repetition)
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
							recordTypedPlaceholderField(t, runDirectory, &state, previous)
						}
						field := typedPlaceholderField{
							Key: fieldKey, Strategy: strategy, Language: language, Fixture: fixture.Name,
							Repetition: repetition, Field: item.name, State: "running", Attempt: previous.Attempt + 1,
							Input: item.input, Prompt: translateGemmaNativePrompt(source, target, "", item.input), StartedAt: time.Now().UTC(),
						}
						recordTypedPlaceholderField(t, runDirectory, &state, field)
						raw, returnedModel, generateErr := adapter.Translate(context.Background(), item.input)
						field.FinishedAt = time.Now().UTC()
						field.DurationMS = field.FinishedAt.Sub(field.StartedAt).Milliseconds()
						field.ReturnedModel = returnedModel
						if generateErr != nil {
							field.Error = generateErr.Error()
							if KindOf(generateErr) == ErrorTransient {
								field.State = "transport_failed"
								recordTypedPlaceholderField(t, runDirectory, &state, field)
								t.Logf("FIELD state=%s key=%s attempt=%d duration=%s error=%s", field.State, field.Key, field.Attempt, time.Duration(field.DurationMS)*time.Millisecond, field.Error)
								t.Fatalf("transport failure at %s (resume the same run): %v", fieldKey, generateErr)
							}
							field.State = "model_failed"
							recordTypedPlaceholderField(t, runDirectory, &state, field)
							t.Logf("FIELD state=%s key=%s attempt=%d duration=%s error=%s", field.State, field.Key, field.Attempt, time.Duration(field.DurationMS)*time.Millisecond, field.Error)
							continue
						}
						field.State, field.RawOutput = "completed", raw
						recordTypedPlaceholderField(t, runDirectory, &state, field)
						t.Logf("FIELD state=%s key=%s attempt=%d duration=%s\n  output=%s", field.State, field.Key, field.Attempt, time.Duration(field.DurationMS)*time.Millisecond, field.RawOutput)
					}

					result := evaluateTypedPlaceholderResult(strategy, language, fixture, protected, repetition,
						state.Fields[resultKey+"/title"], state.Fields[resultKey+"/summary"])
					state.Results[resultKey] = result
					writeTypedPlaceholderSnapshot(t, runDirectory, state)
					t.Logf("RESULT status=%s strategy=%s language=%s fixture=%s repetition=%d error=%s\n  title=%s\n  summary=%s",
						result.Status, strategy, language, fixture.Name, repetition, result.Error, result.RestoredTitle, result.RestoredSummary)
				}
			}
		}
	}

	t.Logf("REPORT directory=%s %s", runDirectory, summarizeTypedPlaceholderResults(state.Results))
}

func typedPlaceholderFixtures() []typedPlaceholderFixture {
	return []typedPlaceholderFixture{
		{Name: "transit-and-stations", Title: "Störungen zwischen Hauptbahnhof und Ostbahnhof", Summary: "Wegen eines Polizeieinsatzes fuhren mehrere S-Bahnen zwischen Hauptbahnhof und Ostbahnhof verspätet. Die U-Bahn war nicht betroffen."},
		{Name: "street-and-district", Title: "Kontrolle in Milbertshofen", Summary: "Ein Audi wurde auf der Ingolstädter Straße in Milbertshofen kontrolliert. Die Ermittlungen in Ramersdorf-Perlach dauern an."},
	}
}

func typedPlaceholderMatcher() (*gazetteer.Matcher, error) {
	return gazetteer.NewMatcher([]gazetteer.Entry{
		{Name: "Hauptbahnhof", Kind: gazetteer.KindTrainStation},
		{Name: "Ostbahnhof", Kind: gazetteer.KindTrainStation},
		{Name: "Milbertshofen", Kind: gazetteer.KindDistrict},
		{Name: "Ingolstädter Straße", Kind: gazetteer.KindStreet},
		{Name: "Ramersdorf-Perlach", Kind: gazetteer.KindDistrict},
	})
}

func protectTypedFixture(matcher *gazetteer.Matcher, fixture typedPlaceholderFixture, strategy string) (typedProtectedFixture, error) {
	options := gazetteer.ProtectionOptions{Mode: gazetteer.ProtectionOpaque}
	switch strategy {
	case "opaque":
	case "typed":
		options.Mode = gazetteer.ProtectionTyped
	case "visible-transit":
		options.Mode = gazetteer.ProtectionTyped
		options.VisibleKinds = []string{gazetteer.KindCommuterTrain, gazetteer.KindSubwaySystem}
	default:
		return typedProtectedFixture{}, fmt.Errorf("unknown typed-placeholder strategy %q", strategy)
	}
	protected, err := matcher.ProtectWithOptions(fixture.Title, fixture.Summary, options)
	if err != nil {
		return typedProtectedFixture{}, err
	}
	return typedProtectedFixture{Title: protected.Title, Summary: protected.Summary, Protected: protected}, nil
}

func evaluateTypedPlaceholderResult(strategy, language string, fixture typedPlaceholderFixture, protected typedProtectedFixture, repetition int, titleField, summaryField typedPlaceholderField) typedPlaceholderResult {
	result := typedPlaceholderResult{Key: typedResultKey(strategy, language, fixture.Name, repetition), Strategy: strategy, Language: language, Fixture: fixture.Name, Repetition: repetition}
	if titleField.State != "completed" || summaryField.State != "completed" {
		result.Status, result.Error = "FAIL_MODEL_OUTPUT", firstNonEmpty(titleField.Error, summaryField.Error, "model field did not complete")
		return result
	}
	if scriptErr := validateTargetScript(language, typedPlaceholderPattern.ReplaceAllString(titleField.RawOutput, ""), typedPlaceholderPattern.ReplaceAllString(summaryField.RawOutput, "")); scriptErr != nil {
		result.Status, result.Error = "FAIL_TARGET_SCRIPT", scriptErr.Error()
		return result
	}
	var err error
	result.RestoredTitle, result.RestoredSummary, err = gazetteer.Restore(protected.Protected, titleField.RawOutput, summaryField.RawOutput)
	if err != nil {
		result.Status, result.Error = "FAIL_TOKEN_PRESERVATION", err.Error()
		return result
	}
	input := StepInput{Values: map[string]string{"title_de": fixture.Title, "summary_de": fixture.Summary}}
	output := StepOutput{Values: map[string]string{"title": result.RestoredTitle, "summary": result.RestoredSummary}}
	if err := validateTranslation(input, &output); err != nil {
		result.Status, result.Error = "FAIL_FINAL_VALIDATION", err.Error()
		return result
	}
	result.Status = "PASS_MECHANICAL"
	result.RestoredTitle, result.RestoredSummary = output.Values["title"], output.Values["summary"]
	return result
}

func typedResultKey(strategy, language, fixture string, repetition int) string {
	return fmt.Sprintf("%s/%s/%s/%d", strategy, language, fixture, repetition)
}

func evaluationRepositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("could not locate repository root for evaluation output")
		}
		directory = parent
	}
}

func ensureTypedPlaceholderManifest(t *testing.T, directory, configHash string, config typedPlaceholderConfig) {
	t.Helper()
	path := filepath.Join(directory, "manifest.json")
	encoded, err := os.ReadFile(path)
	if err == nil {
		var manifest typedPlaceholderManifest
		if json.Unmarshal(encoded, &manifest) != nil || manifest.ConfigHash != configHash {
			t.Fatalf("evaluation manifest differs from this run; choose a new directory instead of mixing configurations")
		}
		return
	}
	if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	manifest := typedPlaceholderManifest{
		RunID: filepath.Base(directory), CreatedAt: time.Now().UTC(), Revision: evaluationRevision(),
		ConfigHash: configHash, Configuration: config,
	}
	writeAtomicJSON(t, path, manifest)
}

func loadTypedPlaceholderSnapshot(t *testing.T, directory, configHash string) typedPlaceholderSnapshot {
	t.Helper()
	state := typedPlaceholderSnapshot{ConfigHash: configHash, Fields: make(map[string]typedPlaceholderField), Results: make(map[string]typedPlaceholderResult)}
	path := filepath.Join(directory, "snapshot.json")
	if encoded, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(encoded, &state); err != nil {
			t.Fatalf("decode evaluation snapshot: %v", err)
		}
		if state.ConfigHash != configHash {
			t.Fatal("evaluation snapshot configuration hash differs from manifest")
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if state.Fields == nil {
		state.Fields = make(map[string]typedPlaceholderField)
	}
	if state.Results == nil {
		state.Results = make(map[string]typedPlaceholderResult)
	}
	eventsPath := filepath.Join(directory, "events.jsonl")
	file, err := os.Open(eventsPath)
	if os.IsNotExist(err) {
		return state
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		var event typedPlaceholderEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode evaluation event: %v", err)
		}
		state.Fields[event.Field.Key] = event.Field
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return state
}

func recordTypedPlaceholderField(t *testing.T, directory string, state *typedPlaceholderSnapshot, field typedPlaceholderField) {
	t.Helper()
	eventPath := filepath.Join(directory, "events.jsonl")
	file, err := os.OpenFile(eventPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	event := typedPlaceholderEvent{RecordedAt: time.Now().UTC(), Field: field}
	encoded, err := json.Marshal(event)
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
	writeTypedPlaceholderSnapshot(t, directory, *state)
}

func writeTypedPlaceholderSnapshot(t *testing.T, directory string, state typedPlaceholderSnapshot) {
	t.Helper()
	state.UpdatedAt = time.Now().UTC()
	writeAtomicJSON(t, filepath.Join(directory, "snapshot.json"), state)
}

func writeAtomicJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(append(encoded, '\n'))
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temporaryPath, path)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func ollamaModelDigest(ctx context.Context, baseURL, model string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/tags"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("ollama tags status %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Models []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", err
	}
	for _, candidate := range payload.Models {
		if candidate.Name == model {
			return candidate.Digest, nil
		}
	}
	return "", fmt.Errorf("model %q is not installed", model)
}

func hashJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func evaluationRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return "unknown"
}

func summarizeTypedPlaceholderResults(results map[string]typedPlaceholderResult) string {
	type count struct{ passed, total int }
	counts := make(map[string]count)
	for _, result := range results {
		key := result.Strategy + "/" + result.Language
		current := counts[key]
		current.total++
		if result.Status == "PASS_MECHANICAL" {
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func TestTypedPlaceholderStrategiesAndRestoration(t *testing.T) {
	fixtures := typedPlaceholderFixtures()
	matcher, err := typedPlaceholderMatcher()
	if err != nil {
		t.Fatal(err)
	}
	for _, strategy := range []string{"opaque", "typed", "visible-transit"} {
		for _, fixture := range fixtures {
			protected, protectErr := protectTypedFixture(matcher, fixture, strategy)
			if protectErr != nil {
				t.Fatal(protectErr)
			}
			title, summary, restoreErr := gazetteer.Restore(protected.Protected, protected.Title, protected.Summary)
			if restoreErr != nil {
				t.Fatalf("strategy %s fixture %s did not restore: %v", strategy, fixture.Name, restoreErr)
			}
			if title != fixture.Title || summary != fixture.Summary {
				t.Fatalf("strategy %s fixture %s restored to %q / %q", strategy, fixture.Name, title, summary)
			}
		}
	}
}

func TestTypedPlaceholderEventReplayRecoversCompletedField(t *testing.T) {
	directory := t.TempDir()
	state := typedPlaceholderSnapshot{ConfigHash: "test", Fields: make(map[string]typedPlaceholderField), Results: make(map[string]typedPlaceholderResult)}
	field := typedPlaceholderField{Key: "typed/en/transit/1/title", State: "completed", RawOutput: "output"}
	recordTypedPlaceholderField(t, directory, &state, field)
	if err := os.Remove(filepath.Join(directory, "snapshot.json")); err != nil {
		t.Fatal(err)
	}
	replayed := loadTypedPlaceholderSnapshot(t, directory, "test")
	if replayed.Fields[field.Key].RawOutput != "output" || replayed.Fields[field.Key].State != "completed" {
		t.Fatalf("event replay did not recover completed field: %#v", replayed.Fields[field.Key])
	}
}

func TestTypedPlaceholderEvaluationRejectsMissingTokenAndRestoresTrustedMarkup(t *testing.T) {
	fixture := typedPlaceholderFixtures()[1]
	matcher, err := typedPlaceholderMatcher()
	if err != nil {
		t.Fatal(err)
	}
	protected, err := protectTypedFixture(matcher, fixture, "typed")
	if err != nil {
		t.Fatal(err)
	}
	title := typedPlaceholderField{State: "completed", RawOutput: protected.Title}
	summary := typedPlaceholderField{State: "completed", RawOutput: protected.Summary}
	result := evaluateTypedPlaceholderResult("typed", "en", fixture, protected, 1, title, summary)
	if result.Status != "PASS_MECHANICAL" || result.RestoredSummary != fixture.Summary {
		t.Fatalf("trusted restoration result = %#v", result)
	}
	summary.RawOutput = strings.Replace(summary.RawOutput, "__MB_STREET_0002__", "", 1)
	result = evaluateTypedPlaceholderResult("typed", "en", fixture, protected, 1, title, summary)
	if result.Status != "FAIL_TOKEN_PRESERVATION" {
		t.Fatalf("missing typed token result = %#v", result)
	}
}
