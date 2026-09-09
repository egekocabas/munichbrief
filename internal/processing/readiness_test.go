package processing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/gazetteer"
)

func readinessTestManager(t *testing.T) *gazetteer.Manager {
	t.Helper()
	ctx := context.Background()
	database, err := gazetteer.Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	entries := []gazetteer.Entry{{Name: "Ingolstädter Straße", Kind: gazetteer.KindStreet}, {Name: "Hauptbahnhof", Kind: gazetteer.KindTrainStation}, {Name: "Ostbahnhof", Kind: gazetteer.KindTrainStation}, {Name: "Leopoldstraße", Kind: gazetteer.KindStreet}, {Name: "Schwabing-West", Kind: gazetteer.KindDistrict}, {Name: "Ganghoferstraße", Kind: gazetteer.KindStreet}}
	if _, _, err := database.Activate(ctx, nil, entries, "synthetic", time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	fetcher, err := gazetteer.NewFetcher(http.DefaultClient, "test", database)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := gazetteer.NewManager(ctx, database, fetcher, gazetteer.DefaultSources(), time.Hour, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestReadinessProductionWorkerRecordsAndResumesFields(t *testing.T) {
	directory := t.TempDir()
	manager := readinessTestManager(t)
	fixture := readinessFixture{ID: "test", Title: "Einsatz an der Ingolstädter Straße", Summary: "Nach Angaben der Polizei blieb die Ingolstädter Straße gesperrt."}
	masked, err := manager.Protect(fixture.Title, fixture.Summary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(masked.Title, "__MB_STREET_") {
		t.Fatal("production manager did not use typed tokens")
	}
	model := "hy-mt2:test"
	input := StepInput{Values: map[string]string{"title_de": masked.Title, "summary_de": masked.Summary}}
	expected := readinessExpectedRequests(t, "http://ollama.test", model, "hy-mt2", "en", 8192, input)
	requests := 0
	failSummary := true
	inner := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		body, _ := io.ReadAll(req.Body)
		content := "Operation near " + nativeAdapterTokenPattern.FindString(masked.Title)
		if string(body) == expected[1] {
			if failSummary {
				return nil, io.ErrUnexpectedEOF
			}
			content = "According to police, " + nativeAdapterTokenPattern.FindString(masked.Summary) + " remained closed."
		}
		data, _ := json.Marshal(chatResponse{Model: model, Done: true, Message: chatMessage{Role: "assistant", Content: content}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
	})
	transport := &readinessTransport{t: t, directory: directory, inner: inner, caseKey: "hy-mt2/en/test/1", expected: expected, maxNew: 2, calls: map[string]readinessCall{}}
	first := readinessRunCase(t, directory, "http://ollama.test", model, "hy-mt2", "en", 8192, fixture, manager, transport)
	if first.Status != "INTERRUPTED" || first.Translation.Title != "" || first.Translation.FailureKind != "transient" {
		t.Fatalf("interruption persisted incorrectly: %#v", first)
	}
	if requests != 2 {
		t.Fatalf("initial calls=%d", requests)
	}
	failSummary = false
	transport = &readinessTransport{t: t, directory: directory, inner: inner, caseKey: "hy-mt2/en/test/1", expected: expected, maxNew: 1, calls: readinessLoadCalls(t, directory)}
	second := readinessRunCase(t, directory, "http://ollama.test", model, "hy-mt2", "en", 8192, fixture, manager, transport)
	if second.Status != "STRUCTURAL_PASS_REVIEW_PENDING" || requests != 3 || transport.newCalls != 1 {
		t.Fatalf("resume did not reuse title: %#v calls=%d", second, requests)
	}
	if second.Translation.AdapterKey != "hy-mt2" || second.Translation.Model != model || second.Translation.AttemptPromptVersion == "" {
		t.Fatalf("route provenance missing: %#v", second.Translation)
	}
	// Revalidate using saved HTTP responses and the same worker with zero live calls.
	transport = &readinessTransport{t: t, directory: directory, inner: inner, caseKey: "hy-mt2/en/test/1", expected: expected, maxNew: 0, calls: readinessLoadCalls(t, directory)}
	third := readinessRunCase(t, directory, "http://ollama.test", model, "hy-mt2", "en", 8192, fixture, manager, transport)
	if third.Status != "STRUCTURAL_PASS_REVIEW_PENDING" || requests != 3 {
		t.Fatalf("offline replay=%#v requests=%d", third, requests)
	}
}

func TestReadinessCapturesRejectedOutputBeforeValidation(t *testing.T) {
	directory := t.TempDir()
	manager := readinessTestManager(t)
	fixture := readinessFixture{ID: "test", Title: "Einsatz an der Ingolstädter Straße", Summary: "Die Ingolstädter Straße blieb gesperrt."}
	masked, _ := manager.Protect(fixture.Title, fixture.Summary)
	expected := readinessExpectedRequests(t, "http://ollama.test", "hy-mt2:test", "hy-mt2", "en", 8192, StepInput{Values: map[string]string{"title_de": masked.Title, "summary_de": masked.Summary}})
	transport := &readinessTransport{t: t, directory: directory, caseKey: "hy-mt2/en/test/1", expected: expected, maxNew: 2, calls: map[string]readinessCall{}, inner: roundTripFunc(func(*http.Request) (*http.Response, error) {
		data, _ := json.Marshal(chatResponse{Model: "hy-mt2:test", Done: true, Message: chatMessage{Role: "assistant", Content: "**Invented output without required places**"}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
	})}
	result := readinessRunCase(t, directory, "http://ollama.test", "hy-mt2:test", "hy-mt2", "en", 8192, fixture, manager, transport)
	if result.Status != "REJECTED" || result.Translation.Title != "" || result.Translation.FailureKind != "output" {
		t.Fatalf("bad output persisted: %#v", result)
	}
	saved := readinessLoadCalls(t, directory)
	if len(saved) != 2 || !strings.Contains(saved[transport.caseKey+"/summary"].Response, "Invented output") {
		t.Fatal("validator rejected response was lost")
	}
}

func TestReadinessTransportRejectsChangedPromptAndHonorsBudget(t *testing.T) {
	directory := t.TempDir()
	calls := 0
	r := &readinessTransport{t: t, directory: directory, caseKey: "case", expected: []string{"new prompt"}, maxNew: 1, calls: map[string]readinessCall{"case/structured": {State: "completed", RequestHash: hashString("old prompt"), Response: "old output"}}, inner: roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("must not call") })}
	req, _ := http.NewRequest(http.MethodPost, "http://ollama.test/api/chat", strings.NewReader("new prompt"))
	if _, err := r.RoundTrip(req); err == nil || calls != 0 {
		t.Fatal("changed prompt reused or generated output")
	}
	r.index = 0
	r.calls = map[string]readinessCall{}
	r.maxNew = 0
	req, _ = http.NewRequest(http.MethodPost, "http://ollama.test/api/chat", strings.NewReader("new prompt"))
	if _, err := r.RoundTrip(req); !errors.Is(err, errReadinessBudget) || calls != 0 {
		t.Fatalf("budget=%v calls=%d", err, calls)
	}
}

func TestReadinessSyntheticFixturesHaveValidPlainTextAndFacts(t *testing.T) {
	manager := readinessTestManager(t)
	for _, fixture := range readinessSyntheticFixtures() {
		title, summary := fixture.Title, fixture.Summary
		if err := normalizeLimitedField("title", &title, 90); err != nil {
			t.Fatal(err)
		}
		if err := normalizeLimitedField("summary", &summary, 600); err != nil {
			t.Fatal(err)
		}
		if err := validatePlainPresentation("fixture", title+" "+summary); err != nil {
			t.Fatal(err)
		}
		masked, err := manager.Protect(title, summary)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range fixture.Places {
			if strings.Contains(masked.Title, name) || strings.Contains(masked.Summary, name) {
				t.Fatalf("unprotected %s in %s", name, fixture.ID)
			}
		}
		if len(fixture.Facts) < 4 {
			t.Fatal("missing source facts")
		}
	}
}

func TestReadinessRecoversIncompleteTrailingEvent(t *testing.T) {
	dir := t.TempDir()
	r := &readinessTransport{t: t, directory: dir, calls: map[string]readinessCall{}}
	r.record(readinessCall{Key: "completed", State: "completed", Response: "kept"})
	f, err := os.OpenFile(filepath.Join(dir, "requests.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(`{"key":"partial`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	loaded := readinessLoadCalls(t, dir)
	if loaded["completed"].Response != "kept" || len(loaded) != 1 {
		t.Fatal("lost completed event")
	}
	if _, err := os.Stat(filepath.Join(dir, "interrupted-tail.json")); err != nil {
		t.Fatal("partial evidence not retained")
	}
	r.record(readinessCall{Key: "next", State: "completed"})
	if len(readinessLoadCalls(t, dir)) != 2 {
		t.Fatal("event log not appendable after recovery")
	}
}

func TestReadinessCandidateAllowsExplicitNativeAdapterComparisonModels(t *testing.T) {
	model, adapter, contextSize, err := readinessCandidate(TranslationAdapterHyMT2, " hf.co/example/Hy-MT2:Q6_K ")
	if err != nil || model != "hf.co/example/Hy-MT2:Q6_K" || adapter != TranslationAdapterHyMT2 || contextSize != 8192 {
		t.Fatalf("candidate=%q/%q/%d err=%v", model, adapter, contextSize, err)
	}
	model, adapter, contextSize, err = readinessCandidate(TranslationAdapterTranslateGemma, " translategemma:test ")
	if err != nil || model != "translategemma:test" || adapter != TranslationAdapterTranslateGemma || contextSize != 2048 {
		t.Fatalf("candidate=%q/%q/%d err=%v", model, adapter, contextSize, err)
	}
	for _, test := range []struct {
		name, adapter, model string
		context              int
	}{
		{name: "default HY-MT2", model: hyMT2ScreenModel, adapter: TranslationAdapterHyMT2, context: 8192},
		{name: "TranslateGemma", adapter: TranslationAdapterTranslateGemma, model: "hf.co/mradermacher/translategemma-12b-it-GGUF:Q3_K_S", context: 2048},
		{name: "structured", adapter: TranslationAdapterStructured, model: "hf.co/bartowski/Qwen_Qwen3.5-9B-GGUF:Q3_K_M", context: 8192},
	} {
		t.Run(test.name, func(t *testing.T) {
			gotModel, gotAdapter, gotContext, err := readinessCandidate(test.adapter, "")
			if err != nil || gotModel != test.model || gotAdapter != test.adapter || gotContext != test.context {
				t.Fatalf("candidate=%q/%q/%d err=%v", gotModel, gotAdapter, gotContext, err)
			}
		})
	}
	if _, _, _, err := readinessCandidate(TranslationAdapterStructured, "unexpected"); err == nil {
		t.Fatal("structured model override accepted")
	}
}
