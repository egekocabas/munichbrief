package processing

// Opt-in release evaluation. Network recording is deliberately test-only; the
// provider, protector, queued worker and persistence below are production code.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/gazetteer"
	"github.com/egekocabas/munichbrief/internal/store"
)

type readinessFixture struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Source  string   `json:"source"`
	Facts   []string `json:"facts"`
	Places  []string `json:"places"`
}

func readinessSyntheticFixtures() []readinessFixture {
	return []readinessFixture{
		{ID: "A", Title: "Polizeieinsatz an der Ingolstädter Straße verzögert S-Bahnen",
			Summary: "Nach Angaben der Polizei soll ein Mann am 29. August 2026 gegen 03:30 Uhr an der Ingolstädter Straße eine Frau leicht verletzt haben; sie wurde vor Ort medizinisch untersucht. Wegen des anschließenden Polizeieinsatzes fuhren S-Bahnen zwischen Hauptbahnhof und Ostbahnhof 45 Minuten verspätet, während die U-Bahn nicht betroffen war. Die Polizei bat Zeuginnen und Zeugen, sich bei der zuständigen Dienststelle zu melden.",
			Source:  "synthetic", Places: []string{"Ingolstädter Straße", "S-Bahnen", "Hauptbahnhof", "Ostbahnhof", "U-Bahn"},
			Facts: []string{"Police attribution; alleged injury, not established guilt", "29 August 2026, approximately 03:30; woman slightly injured", "Woman medically examined on site, not questioned", "Subsequent police operation caused 45-minute S-Bahn delays between the two stations", "U-Bahn unaffected; police request witnesses contact responsible station"}},
		{ID: "B", Title: "Ermittlungen an der Leopoldstraße in Schwabing-West",
			Summary: "Nach Angaben der Polizei soll ein Tatverdächtiger an der Leopoldstraße in Schwabing-West die Scheibe eines geparkten Fahrzeugs beschädigt haben. Ob er beteiligt war, ist noch unklar; eine Verurteilung liegt nicht vor und bis zu einer rechtskräftigen Verurteilung gilt die Unschuldsvermutung. Die Polizei sucht Zeugen aus Schwabing-West.",
			Source:  "synthetic", Places: []string{"Leopoldstraße", "Schwabing-West"},
			Facts: []string{"Police attribute alleged damage to a parked vehicle's window", "Involvement remains unclear", "No conviction, not no charges", "Presumption of innocence until final conviction", "Witnesses from Schwabing-West requested; repeated district retained"}},
		{ID: "C", Title: "Polizei untersucht Unfall an der Ganghoferstraße",
			Summary: "Am 29. August 2026 um genau 03:30 Uhr kollidierte ein Auto an der Ganghoferstraße mit einer Mauer. Laut Polizei betrug seine Geschwindigkeit 93 km/h. Wegen des Unfalls blieb die Straße 45 Minuten gesperrt. Gegen 04:20 Uhr untersuchte ein Arzt zwei Personen medizinisch; niemand war verletzt. Die Polizei befragte einen Zeugen. In Notfällen ist die 110 zu wählen.",
			Source:  "synthetic", Places: []string{"Ganghoferstraße"},
			Facts: []string{"29 August 2026 at exactly 03:30; car hit a wall", "Police report actual speed 93 km/h, not excess speed", "Accident caused 45-minute road closure", "Around 04:20 doctor medically examined two people; nobody injured", "Police questioned one witness; emergency number 110"}},
	}
}

type readinessManifest struct {
	Revision            string             `json:"revision"`
	Identity            string             `json:"identity"`
	CodeHash            string             `json:"code_hash"`
	Model               string             `json:"model"`
	Digest              string             `json:"digest"`
	Adapter             string             `json:"adapter"`
	Context             int                `json:"context"`
	GazetteerGeneration int64              `json:"gazetteer_generation"`
	GazetteerHash       string             `json:"gazetteer_hash"`
	Fixtures            []readinessFixture `json:"fixtures"`
}

type readinessCall struct {
	Key         string    `json:"key"`
	RequestHash string    `json:"request_hash"`
	Request     string    `json:"request"`
	Response    string    `json:"response,omitempty"`
	StatusCode  int       `json:"status_code,omitempty"`
	State       string    `json:"state"`
	Error       string    `json:"error,omitempty"`
	Started     time.Time `json:"started"`
	Finished    time.Time `json:"finished"`
	Attempt     int       `json:"attempt"`
}

type readinessTransport struct {
	t                *testing.T
	directory        string
	inner            http.RoundTripper
	caseKey          string
	expected         []string
	index            int
	maxNew, newCalls int
	calls            map[string]readinessCall
	interrupted      bool
}

var errReadinessBudget = errors.New("evaluation new-call budget exhausted")

func (r *readinessTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path != "/api/chat" {
		return nil, fmt.Errorf("unexpected evaluation endpoint %s", req.URL.Path)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if r.index >= len(r.expected) || string(body) != r.expected[r.index] {
		return nil, errors.New("rendered request differs from preflight; refusing generation/replay")
	}
	field := "title"
	if len(r.expected) == 1 {
		field = "structured"
	} else if r.index == 1 {
		field = "summary"
	}
	key := r.caseKey + "/" + field
	r.index++
	hash := hashString(string(body))
	previous := r.calls[key]
	if previous.RequestHash != "" && previous.RequestHash != hash {
		return nil, errors.New("checkpoint request identity changed")
	}
	if previous.State == "completed" {
		r.t.Logf("REPLAY %s", key)
		return &http.Response{StatusCode: previous.StatusCode, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(previous.Response)), Request: req}, nil
	}
	if r.newCalls >= r.maxNew {
		r.interrupted = true
		return nil, errReadinessBudget
	}
	if previous.State == "running" {
		previous.State = "interrupted"
		previous.Error = "previous process ended before response was durably saved"
		r.record(previous)
	}
	call := readinessCall{Key: key, RequestHash: hash, Request: string(body), State: "running", Started: time.Now().UTC(), Attempt: previous.Attempt + 1}
	r.record(call)
	r.newCalls++
	req.Body = io.NopCloser(bytes.NewReader(body))
	response, err := r.inner.RoundTrip(req)
	if err == nil {
		var raw []byte
		raw, err = io.ReadAll(io.LimitReader(response.Body, 4<<20))
		response.Body.Close()
		call.Response = string(raw)
		call.StatusCode = response.StatusCode
		if len(raw) >= 4<<20 {
			err = errors.New("evaluation response exceeds capture bound")
		}
	}
	call.Finished = time.Now().UTC()
	if err != nil {
		call.State = "transport_failed"
		call.Error = err.Error()
		r.interrupted = true
		r.record(call)
		return nil, err
	}
	call.State = "completed"
	// A transport/server failure is resumable; a completed invalid model output
	// is evidence and is replayed, never automatically replaced with a lucky run.
	if call.StatusCode >= 500 || call.StatusCode == http.StatusTooManyRequests {
		call.State = "transport_failed"
		r.interrupted = true
	}
	r.record(call)
	r.t.Logf("CALL %s duration=%s status=%d\n%s", key, call.Finished.Sub(call.Started), call.StatusCode, call.Response)
	response.Body = io.NopCloser(strings.NewReader(call.Response))
	return response, nil
}

func (r *readinessTransport) record(call readinessCall) {
	r.t.Helper()
	encoded, err := json.Marshal(call)
	if err != nil {
		r.t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(r.directory, "requests.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		r.t.Fatal(err)
	}
	_, err = file.Write(append(encoded, '\n'))
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		r.t.Fatal(err)
	}
	if closeErr != nil {
		r.t.Fatal(closeErr)
	}
	r.calls[call.Key] = call
}

func readinessLoadCalls(t *testing.T, directory string) map[string]readinessCall {
	t.Helper()
	calls := make(map[string]readinessCall)
	path := filepath.Join(directory, "requests.jsonl")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return calls
	}
	if err != nil {
		t.Fatal(err)
	}
	// Only an incomplete trailing write is recoverable. Corruption in a complete
	// event is fatal. Retain the fragment before repairing the append boundary.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		end := bytes.LastIndexByte(data, '\n') + 1
		writeAtomicJSON(t, filepath.Join(directory, "interrupted-tail.json"), string(data[end:]))
		if err := os.Truncate(path, int64(end)); err != nil {
			t.Fatal(err)
		}
		data = data[:end]
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var call readinessCall
		if err := json.Unmarshal(line, &call); err != nil {
			t.Fatal(err)
		}
		calls[call.Key] = call
	}
	return calls
}

func readinessExpectedRequests(t *testing.T, baseURL, model, adapter, language string, ctxSize int, input StepInput) []string {
	t.Helper()
	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		requests = append(requests, string(body))
		encoded, _ := json.Marshal(chatResponse{Model: model, Done: true, Message: chatMessage{Role: "assistant", Content: "preflight response"}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(encoded)), Header: make(http.Header)}, nil
	})}
	provider, err := NewOllamaGeneratorProvider(baseURL, time.Minute, ctxSize, client)
	if err != nil {
		t.Fatal(err)
	}
	generator, err := provider.StepGeneratorFor(model, adapter)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = generator.GenerateStep(context.Background(), TranslationByLanguageMust(t, language).Step, input)
	want := 2
	if adapter == TranslationAdapterStructured {
		want = 1
	}
	if len(requests) != want {
		t.Fatalf("preflight requests=%d, want %d", len(requests), want)
	}
	return requests
}

type readinessResult struct {
	Case             string                         `json:"case"`
	Database         string                         `json:"database"`
	Status           string                         `json:"status"`
	Translation      store.AdminTranslationIncident `json:"translation"`
	RestoredTitle    string                         `json:"restored_title,omitempty"`
	RestoredSummary  string                         `json:"restored_summary,omitempty"`
	RestorationError string                         `json:"restoration_error,omitempty"`
	ValidationError  string                         `json:"validation_error,omitempty"`
	Checks           nativeAdapterScreenChecks      `json:"checks"`
}

type readinessRepository struct {
	*store.Store
	failure string
}

func (r *readinessRepository) FailPostProcessingJob(ctx context.Context, job store.PostProcessingJob, status, kind string, retry *time.Time, now time.Time, cause error) error {
	r.failure = cause.Error()
	return r.Store.FailPostProcessingJob(ctx, job, status, kind, retry, now, cause)
}

func readinessRunCase(t *testing.T, directory, baseURL, model, adapter, language string, ctxSize int, fixture readinessFixture, protector TranslationProtector, transport *readinessTransport) readinessResult {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Add(2 * time.Second)
	caseDir, err := os.MkdirTemp(directory, "case-")
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(caseDir, "incidents.db")
	database, err := openTestStore(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertWorkerDocument(t, ctx, database, now, "evaluation")
	// Canonical fixtures are frozen inputs, not live German model generations.
	seed := &pipelineTestProvider{generate: func(step StepDefinition, _ StepInput) (StepOutput, bool) {
		if step.Key == GermanPresentationStep {
			return StepOutput{TitleDE: fixture.Title, SummaryDE: fixture.Summary, PrivacyStatus: "safe", PrivacyFlags: []string{}}, true
		}
		return StepOutput{}, false
	}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalog := testModelCatalog{snapshot: ModelCatalogSnapshot{Models: []string{"fixture-seed", model}, CheckedAt: now}}
	worker, err := NewPipelineWorker(database, seed, catalog, DefaultPostProcessorRegistry(), nil, logger, time.Second, func() time.Time { return now }, Schedule{Immediate: true}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.SetAutomaticProcessing(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RequestNow(ctx, "fixture", map[string]string{IncidentMetadataStep: "fixture-seed", GermanPresentationStep: "fixture-seed"}, nil, false); err != nil {
		t.Fatal(err)
	}
	worker.processAvailable(ctx)
	provider, err := NewOllamaGeneratorProvider(baseURL, 15*time.Minute, ctxSize, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	protectedProvider, err := NewProtectedGeneratorProvider(provider, protector)
	if err != nil {
		t.Fatal(err)
	}
	worker.providers = protectedProvider
	repository := &readinessRepository{Store: database}
	worker.repository = repository
	if err := worker.SetTranslationLanguageSetting(ctx, language, model, adapter); err != nil {
		t.Fatal(err)
	}
	plans, err := worker.configuredTranslationPlans(ctx, []string{language})
	if err != nil || len(plans) != 1 {
		t.Fatalf("configured plans=%v/%v", plans, err)
	}
	if queued, err := database.QueuePostProcessingForAll(ctx, "fixture", plans, true, now); err != nil || queued != 1 {
		t.Fatalf("queued=%d/%v", queued, err)
	}
	_, scope, _ := worker.postProcessors.Scope(TranslationModelStep, language)
	contracts := store.PostProcessingContract{language: {PromptVersion: scope.Step.PromptVersion, InputKinds: scope.Step.InputKinds, OutputKinds: scope.Step.OutputKinds}}
	job, found, err := database.ClaimPostProcessingJob(ctx, TranslationModelStep, contracts, true, nil, now)
	if err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
	if job.AdapterKey != adapter || job.ModelIdentity != model {
		t.Fatal("queued route identity changed")
	}
	worker.processPostProcessingJob(ctx, ctx, job)
	items, total, err := database.ListAdminTranslationIncidents(ctx, 10, 0, "fixture", language, store.AdminTranslationsAll)
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("persisted results=%v/%d/%v", items, total, err)
	}
	result := readinessResult{Case: transport.caseKey, Database: dbPath, Status: "REJECTED", Translation: items[0], ValidationError: repository.failure, Checks: pendingNativeAdapterChecks()}
	if transport.interrupted {
		result.Status = "INTERRUPTED"
	} else if items[0].Title != "" && items[0].Summary != "" {
		result.Status = "STRUCTURAL_PASS_REVIEW_PENDING"
	}
	if result.Status == "STRUCTURAL_PASS_REVIEW_PENDING" && (items[0].Model != model || items[0].AdapterKey != adapter) {
		t.Fatal("persisted model/adapter differs from request")
	}
	// Recover candidate fields for inspection even when a production validator
	// rejects them. This diagnostic never participates in persistence/acceptance.
	{
		var title, summary chatResponse
		_ = json.Unmarshal([]byte(transport.calls[transport.caseKey+"/title"].Response), &title)
		_ = json.Unmarshal([]byte(transport.calls[transport.caseKey+"/summary"].Response), &summary)
		if adapter == TranslationAdapterStructured {
			var response chatResponse
			_ = json.Unmarshal([]byte(transport.calls[transport.caseKey+"/structured"].Response), &response)
			output, err := scope.Step.OutputDecoder(response.Message.Content)
			if err == nil {
				title.Message.Content, summary.Message.Content = output.Values["title"], output.Values["summary"]
			}
		}
		masked, e := protector.Protect(fixture.Title, fixture.Summary)
		if e == nil {
			result.RestoredTitle, result.RestoredSummary, e = gazetteer.Restore(masked, title.Message.Content, summary.Message.Content)
			diagnostic := evaluateNativeAdapterScreenResult(adapter, language, nativeAdapterScreenFixture{Name: fixture.ID, Title: fixture.Title, Summary: fixture.Summary}, nativeAdapterProtectedFixture{Title: masked.Title, Summary: masked.Summary, Protected: masked}, 1, nativeAdapterScreenField{State: "completed", RawOutput: title.Message.Content}, nativeAdapterScreenField{State: "completed", RawOutput: summary.Message.Content})
			result.Checks = diagnostic.Checks
		}
		if e != nil {
			result.RestorationError = e.Error()
		}
	}
	writeAtomicJSON(t, filepath.Join(caseDir, "result.json"), result)
	t.Logf("RESULT %s %s database=%s\n  title=%s\n  summary=%s\n  validation=%s", result.Case, result.Status, dbPath, result.RestoredTitle, result.RestoredSummary, result.ValidationError)
	return result
}

func readinessCodeHash(t *testing.T) string {
	t.Helper()
	root := evaluationRepositoryRoot(t)
	var parts []string
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && (strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".sql")) {
				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				relative, _ := filepath.Rel(root, path)
				parts = append(parts, relative, hashString(string(content)))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		content, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, name, hashString(string(content)))
	}
	return hashJSON(t, parts)
}

func TestLiveTranslationReadiness(t *testing.T) {
	if os.Getenv("MUNICHBRIEF_READINESS_LIVE_TEST") != "1" {
		t.Skip("set MUNICHBRIEF_READINESS_LIVE_TEST=1")
	}
	directory := os.Getenv("MUNICHBRIEF_READINESS_DIR")
	baseURL := os.Getenv("MUNICHBRIEF_OLLAMA_BASE_URL")
	if directory == "" || baseURL == "" {
		t.Fatal("explicit readiness directory and Ollama URL required")
	}
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(evaluationRepositoryRoot(t), directory)
	}
	fixtures := readinessSyntheticFixtures()
	var real []readinessFixture
	encoded, err := os.ReadFile(filepath.Join(directory, "real-fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &real); err != nil {
		t.Fatal(err)
	}
	fixtures = append(fixtures, real...)
	if len(fixtures) != 6 {
		t.Fatal("freeze exactly six fixtures before generation")
	}
	model, adapter, contextSize, err := readinessCandidate(
		os.Getenv("MUNICHBRIEF_READINESS_ADAPTER"),
		os.Getenv("MUNICHBRIEF_READINESS_MODEL"),
	)
	if err != nil {
		t.Fatal(err)
	}
	languages := strings.Split(os.Getenv("MUNICHBRIEF_READINESS_LANGUAGES"), ",")
	for _, lang := range languages {
		_, registered := TranslationByLanguage(lang)
		if !registered || !TranslationAdapterSupports(adapter, lang) {
			t.Fatalf("unsupported language %q", lang)
		}
	}
	selected := commaSeparatedSelection(os.Getenv("MUNICHBRIEF_READINESS_FIXTURES"))
	if len(selected) == 0 {
		t.Fatal("explicit fixture selection required")
	}
	for id := range selected {
		found := false
		for _, fixture := range fixtures {
			found = found || fixture.ID == id
		}
		if !found {
			t.Fatalf("unknown fixture %q", id)
		}
	}
	repetitions := strings.Split(os.Getenv("MUNICHBRIEF_READINESS_REPETITIONS"), ",")
	for _, rep := range repetitions {
		if rep != "1" && rep != "2" {
			t.Fatal("explicit repetitions 1 and/or 2 required")
		}
	}
	maxNew, err := strconv.Atoi(os.Getenv("MUNICHBRIEF_READINESS_MAX_NEW_CALLS"))
	if err != nil || maxNew < 0 {
		t.Fatal("explicit nonnegative new-call limit required")
	}
	ctx := context.Background()
	gazStore, err := gazetteer.Open(ctx, filepath.Join(directory, "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer gazStore.Close()
	status, err := gazStore.Status(ctx)
	if err != nil || status.ActiveGeneration == 0 {
		t.Fatalf("freeze a real Gazetteer generation before evaluation: %v", err)
	}
	entries, err := gazStore.ActiveEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fetcher, err := gazetteer.NewFetcher(http.DefaultClient, "MunichBrief/evaluation", gazStore)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := gazetteer.NewManager(ctx, gazStore, fetcher, gazetteer.DefaultSources(), 7*24*time.Hour, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		if len(fixture.Facts) == 0 {
			t.Fatalf("%s has no source-fact checklist", fixture.ID)
		}
		title, summary := fixture.Title, fixture.Summary
		if err := normalizeLimitedField("title", &title, 90); err != nil {
			t.Fatalf("%s: %v", fixture.ID, err)
		}
		if err := normalizeLimitedField("summary", &summary, 600); err != nil {
			t.Fatalf("%s: %v", fixture.ID, err)
		}
		if err := validatePlainPresentation("fixture", title+" "+summary); err != nil {
			t.Fatal(err)
		}
		masked, err := manager.Protect(title, summary)
		if err != nil {
			t.Fatal(err)
		}
		for _, place := range fixture.Places {
			if strings.Contains(masked.Title, place) || strings.Contains(masked.Summary, place) {
				t.Fatalf("Gazetteer misses %s in fixture %s", place, fixture.ID)
			}
		}
		t.Logf("PREFLIGHT %s protected_title=%s protected_summary=%s facts=%v", fixture.ID, masked.Title, masked.Summary, fixture.Facts)
	}
	digest, err := ollamaModelDigest(ctx, baseURL, model)
	if err != nil {
		t.Fatal(err)
	}
	manifest := readinessManifest{Revision: evaluationRevision(), CodeHash: readinessCodeHash(t), Model: model, Digest: digest, Adapter: adapter, Context: contextSize, GazetteerGeneration: status.ActiveGeneration, GazetteerHash: hashJSON(t, entries), Fixtures: fixtures}
	// Revision is provenance; code-content identity permits documentation-only
	// commits without discarding otherwise identical completed calls.
	identity := manifest
	identity.Revision = ""
	manifest.Identity = hashJSON(t, identity)
	runDir := filepath.Join(directory, adapter)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(runDir, "manifest.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var old readinessManifest
		if json.Unmarshal(data, &old) != nil || old.Identity != manifest.Identity {
			t.Fatal("manifest changed: use a new evaluation directory")
		}
	} else if os.IsNotExist(err) {
		writeAtomicJSON(t, manifestPath, manifest)
	} else {
		t.Fatal(err)
	}
	transport := &readinessTransport{t: t, directory: runDir, inner: http.DefaultTransport, maxNew: maxNew, calls: readinessLoadCalls(t, runDir)}
	for _, language := range languages {
		for _, fixture := range fixtures {
			if !selected[fixture.ID] {
				continue
			}
			for _, rep := range repetitions {
				if rep == "2" && fixture.ID != "A" && fixture.ID != "B" {
					continue
				}
				transport.caseKey = adapter + "/" + language + "/" + fixture.ID + "/" + rep
				transport.index = 0
				masked, _ := manager.Protect(fixture.Title, fixture.Summary)
				transport.expected = readinessExpectedRequests(t, baseURL, model, adapter, language, contextSize, StepInput{Values: map[string]string{"title_de": masked.Title, "summary_de": masked.Summary}})
				result := readinessRunCase(t, runDir, baseURL, model, adapter, language, contextSize, fixture, manager, transport)
				writeAtomicJSON(t, filepath.Join(runDir, hashString(transport.caseKey)+"-result.json"), result)
				reviewPath := filepath.Join(runDir, hashString(transport.caseKey)+"-review.json")
				if _, err := os.Stat(reviewPath); os.IsNotExist(err) {
					writeAtomicJSON(t, reviewPath, pendingNativeAdapterManualReview())
				} else if err != nil {
					t.Fatal(err)
				}
				if transport.interrupted {
					t.Fatalf("stopped after %d new calls; resume the same identity", transport.newCalls)
				}
			}
		}
	}
	t.Logf("CHECKPOINT new_calls=%d directory=%s; editorial review remains required", transport.newCalls, runDir)
}

func readinessCandidate(adapterName, modelOverride string) (string, string, int, error) {
	adapterName = strings.TrimSpace(adapterName)
	modelOverride = strings.TrimSpace(modelOverride)
	model := hyMT2ScreenModel
	adapter := TranslationAdapterHyMT2
	contextSize := 8192
	switch adapterName {
	case "", TranslationAdapterHyMT2:
		if modelOverride != "" {
			model = modelOverride
		}
	case TranslationAdapterTranslateGemma:
		adapter = TranslationAdapterTranslateGemma
		model = "hf.co/mradermacher/translategemma-12b-it-GGUF:Q3_K_S"
		if modelOverride != "" {
			model = modelOverride
		}
		contextSize = 2048
	case TranslationAdapterStructured:
		if modelOverride != "" {
			return "", "", 0, errors.New("readiness model override is not supported for structured comparisons")
		}
		adapter = TranslationAdapterStructured
		model = "hf.co/bartowski/Qwen_Qwen3.5-9B-GGUF:Q3_K_M"
	default:
		return "", "", 0, fmt.Errorf("unsupported readiness adapter %q", adapterName)
	}
	return model, adapter, contextSize, nil
}
