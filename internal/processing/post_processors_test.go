package processing

import (
	"errors"
	"testing"

	"github.com/egekocabas/munichbrief/internal/store"
)

func TestDefaultPostProcessorRegistryOrdersAndExpandsScopes(t *testing.T) {
	registry := DefaultPostProcessorRegistry()
	definitions := registry.Definitions()
	if len(definitions) != 2 || definitions[0].Key != CategoryVerificationStep || definitions[1].Key != TranslationModelStep {
		t.Fatalf("post-processor priority order = %#v", definitions)
	}
	plans, err := registry.Plans(TranslationModelStep, nil, "translate:4b")
	if err != nil || len(plans) != len(RegisteredTranslations()) || plans[0].ScopeKey != EnglishLanguage || plans[0].Model != "translate:4b" {
		t.Fatalf("all-scope plans = %#v/%v", plans, err)
	}
	if _, err := registry.Plans(TranslationModelStep, []string{EnglishLanguage, EnglishLanguage}, "translate:4b"); err == nil {
		t.Fatal("duplicate scope selection unexpectedly succeeded")
	}
	if _, err := registry.Plans("missing", nil, "translate:4b"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown processor error = %v", err)
	}

	definitions[0].Scopes[0].Step.InputKinds[0] = "mutated"
	_, scope, found := registry.Scope(CategoryVerificationStep, DefaultPostProcessingScope)
	if !found || scope.Step.InputKinds[0] == "mutated" {
		t.Fatal("registry definitions are not immutable copies")
	}
}

func TestPostProcessorRegistryRejectsCounterOutsideDeclaredOutputs(t *testing.T) {
	_, err := NewPostProcessorRegistry(PostProcessorDefinition{
		Key: "test", DisplayName: "Test", Description: "Test processor.", Priority: 1,
		ModelSettingKey: "test", Automatic: true,
		Scopes: []PostProcessorScope{{Key: "default", DisplayName: "Default", Step: StepDefinition{
			Key: "test/default", PromptVersion: "test-v1", InputKinds: []string{"title_de"},
			OutputKinds: []string{"note"}, OutputValues: postProcessingOutputValues,
		}}},
		Counters: []PostProcessorCounter{{Key: "invalid", OutputKind: "missing", EqualsValue: "true"}},
	})
	if err == nil {
		t.Fatal("counter for undeclared output unexpectedly registered")
	}
}
