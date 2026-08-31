package processing

import (
	"errors"
	"testing"

	"github.com/egekocabas/munichbrief/internal/store"
)

func TestDefaultPostProcessorRegistryOrdersAndExpandsScopes(t *testing.T) {
	registry := DefaultPostProcessorRegistry()
	definitions := registry.Definitions()
	if len(definitions) != 3 || definitions[0].Key != PublicAssistanceVerificationStep || definitions[1].Key != CategoryVerificationStep || definitions[2].Key != TranslationModelStep {
		t.Fatalf("post-processor priority order = %#v", definitions)
	}
	if definitions[0].Priority != 10 || definitions[0].ModelSettingKey != PublicAssistanceVerificationStep || !definitions[0].Automatic || !definitions[0].Manual || len(definitions[0].Counters) != 1 || definitions[0].Counters[0].Key != "corrected" || definitions[0].Verification == nil || len(definitions[0].Verification.Fields) != 2 {
		t.Fatalf("public assistance processor registration = %#v", definitions[0])
	}
	if definitions[2].Verification != nil {
		t.Fatalf("translation unexpectedly registered as a verifier: %#v", definitions[2].Verification)
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

	definitions[1].Scopes[0].Step.InputKinds[0] = "mutated"
	definitions[1].Verification.Fields[0].DisplayName = "mutated"
	_, scope, found := registry.Scope(CategoryVerificationStep, DefaultPostProcessingScope)
	if !found || scope.Step.InputKinds[0] == "mutated" {
		t.Fatal("registry definitions are not immutable copies")
	}
	definition, found := registry.Definition(CategoryVerificationStep)
	if !found || definition.Verification.Fields[0].DisplayName == "mutated" {
		t.Fatal("registry verification contract is not an immutable copy")
	}
}

func TestPostProcessorRegistryValidatesVerificationContract(t *testing.T) {
	base := PostProcessorDefinition{
		Key: "test", DisplayName: "Test", Description: "Test verifier.", Priority: 1,
		ModelSettingKey: "test", Automatic: true,
		Scopes: []PostProcessorScope{{Key: "default", DisplayName: "Default", Step: StepDefinition{
			Key: "test/default", PromptVersion: "test-v1", InputKinds: []string{"original"},
			OutputKinds: []string{"verdict", "corrected"}, OutputValues: postProcessingOutputValues,
		}}},
		Verification: &PostProcessorVerification{VerdictKind: "verdict", Fields: []PostProcessorVerificationField{{DisplayName: "Value", OriginalKind: "original", CorrectedKind: "corrected"}}},
	}
	if _, err := NewPostProcessorRegistry(base); err != nil {
		t.Fatalf("valid verification contract = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*PostProcessorDefinition)
	}{
		{name: "missing verdict", mutate: func(definition *PostProcessorDefinition) { definition.Verification.VerdictKind = "missing" }},
		{name: "missing original", mutate: func(definition *PostProcessorDefinition) { definition.Verification.Fields[0].OriginalKind = "missing" }},
		{name: "missing corrected", mutate: func(definition *PostProcessorDefinition) { definition.Verification.Fields[0].CorrectedKind = "missing" }},
		{name: "duplicate original", mutate: func(definition *PostProcessorDefinition) {
			definition.Verification.Fields = append(definition.Verification.Fields, PostProcessorVerificationField{DisplayName: "Other", OriginalKind: "original", CorrectedKind: "other"})
			definition.Scopes[0].Step.OutputKinds = append(definition.Scopes[0].Step.OutputKinds, "other")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := clonePostProcessorDefinition(base)
			test.mutate(&definition)
			if _, err := NewPostProcessorRegistry(definition); err == nil {
				t.Fatal("invalid verification contract unexpectedly registered")
			}
		})
	}
}

func TestPostProcessorRegistryOrdersQueueStatsWithDefinitions(t *testing.T) {
	registry := DefaultPostProcessorRegistry()
	stats := []store.PostProcessingQueueStats{
		{ProcessorKey: TranslationModelStep, ScopeKey: EnglishLanguage},
		{ProcessorKey: "retired", ScopeKey: "z"},
		{ProcessorKey: CategoryVerificationStep, ScopeKey: DefaultPostProcessingScope},
		{ProcessorKey: PublicAssistanceVerificationStep, ScopeKey: DefaultPostProcessingScope},
		{ProcessorKey: "retired", ScopeKey: "a"},
	}
	ordered := registry.OrderedQueueStats(stats)
	want := []string{
		PublicAssistanceVerificationStep + "/" + DefaultPostProcessingScope,
		CategoryVerificationStep + "/" + DefaultPostProcessingScope,
		TranslationModelStep + "/" + EnglishLanguage,
		"retired/a",
		"retired/z",
	}
	for index, stat := range ordered {
		if key := stat.ProcessorKey + "/" + stat.ScopeKey; key != want[index] {
			t.Fatalf("ordered queue stat %d = %q, want %q", index, key, want[index])
		}
	}
	if stats[0].ProcessorKey != TranslationModelStep {
		t.Fatal("queue ordering mutated its input")
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

func TestPostProcessorRegistryRejectsDefinitionWideCounterMissingFromOneScope(t *testing.T) {
	_, err := NewPostProcessorRegistry(PostProcessorDefinition{
		Key: "test", DisplayName: "Test", Description: "Test processor.", Priority: 1,
		ModelSettingKey: "test", Automatic: true,
		Scopes: []PostProcessorScope{
			{Key: "one", DisplayName: "One", Step: StepDefinition{Key: "test/one", PromptVersion: "test-one-v1", InputKinds: []string{"title_de"}, OutputKinds: []string{"shared"}, OutputValues: postProcessingOutputValues}},
			{Key: "two", DisplayName: "Two", Step: StepDefinition{Key: "test/two", PromptVersion: "test-two-v1", InputKinds: []string{"title_de"}, OutputKinds: []string{"different"}, OutputValues: postProcessingOutputValues}},
		},
		Counters: []PostProcessorCounter{{Key: "shared", OutputKind: "shared", EqualsValue: "true"}},
	})
	if err == nil {
		t.Fatal("definition-wide counter missing from one scope unexpectedly registered")
	}
}
