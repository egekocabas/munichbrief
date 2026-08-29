package processing

import (
	"errors"
	"sort"
	"strings"

	"github.com/egekocabas/munichbrief/internal/store"
)

const DefaultPostProcessingScope = "default"

type PostProcessorScope struct {
	Key         string
	DisplayName string
	Step        StepDefinition
}

type PostProcessorCounter struct {
	Key         string
	OutputKind  string
	EqualsValue string
}

// PostProcessorDefinition is the complete registration contract for one
// independent post-canonical LLM feature.
type PostProcessorDefinition struct {
	Key             string
	DisplayName     string
	Description     string
	Priority        int
	ModelSettingKey string
	Automatic       bool
	Manual          bool
	Scopes          []PostProcessorScope
	Counters        []PostProcessorCounter
}

// PostProcessorRegistry provides validated deterministic lookup and ordering.
type PostProcessorRegistry struct {
	definitions []PostProcessorDefinition
}

func NewPostProcessorRegistry(definitions ...PostProcessorDefinition) (*PostProcessorRegistry, error) {
	registry := &PostProcessorRegistry{definitions: make([]PostProcessorDefinition, len(definitions))}
	seenProcessors := make(map[string]struct{}, len(definitions))
	for index, definition := range definitions {
		definition.Key = strings.TrimSpace(definition.Key)
		definition.ModelSettingKey = strings.TrimSpace(definition.ModelSettingKey)
		definition.Description = strings.TrimSpace(definition.Description)
		if definition.Key == "" || strings.TrimSpace(definition.DisplayName) == "" || definition.Description == "" || definition.ModelSettingKey == "" || len(definition.Scopes) == 0 || !definition.Automatic && !definition.Manual {
			return nil, errors.New("post-processor key, display name, model setting, and scopes are required")
		}
		if _, duplicate := seenProcessors[definition.Key]; duplicate {
			return nil, errors.New("post-processor keys must be unique")
		}
		seenProcessors[definition.Key] = struct{}{}
		seenScopes := make(map[string]struct{}, len(definition.Scopes))
		outputKinds := make(map[string]struct{})
		for scopeIndex, scope := range definition.Scopes {
			scope.Key = strings.TrimSpace(scope.Key)
			if scope.Key == "" || strings.TrimSpace(scope.DisplayName) == "" || scope.Step.Key == "" || scope.Step.PromptVersion == "" || len(scope.Step.InputKinds) == 0 || len(scope.Step.OutputKinds) == 0 || scope.Step.OutputValues == nil {
				return nil, errors.New("post-processor scope metadata and executable step are required")
			}
			if _, duplicate := seenScopes[scope.Key]; duplicate {
				return nil, errors.New("post-processor scope keys must be unique")
			}
			seenScopes[scope.Key] = struct{}{}
			if err := validateValueKinds(scope.Step.InputKinds, "input"); err != nil {
				return nil, err
			}
			if err := validateValueKinds(scope.Step.OutputKinds, "output"); err != nil {
				return nil, err
			}
			for _, kind := range scope.Step.OutputKinds {
				outputKinds[kind] = struct{}{}
			}
			scope.Step = cloneStepDefinition(scope.Step)
			definition.Scopes[scopeIndex] = scope
		}
		seenCounters := make(map[string]struct{}, len(definition.Counters))
		for _, counter := range definition.Counters {
			if strings.TrimSpace(counter.Key) == "" || strings.TrimSpace(counter.OutputKind) == "" {
				return nil, errors.New("post-processor counter key and output kind are required")
			}
			if _, duplicate := seenCounters[counter.Key]; duplicate {
				return nil, errors.New("post-processor counter keys must be unique")
			}
			if _, found := outputKinds[counter.OutputKind]; !found {
				return nil, errors.New("post-processor counter must reference a declared output kind")
			}
			seenCounters[counter.Key] = struct{}{}
		}
		registry.definitions[index] = clonePostProcessorDefinition(definition)
	}
	sort.SliceStable(registry.definitions, func(i, j int) bool {
		if registry.definitions[i].Priority == registry.definitions[j].Priority {
			return registry.definitions[i].Key < registry.definitions[j].Key
		}
		return registry.definitions[i].Priority < registry.definitions[j].Priority
	})
	return registry, nil
}

func DefaultPostProcessorRegistry() *PostProcessorRegistry {
	translationScopes := make([]PostProcessorScope, 0, len(registeredTranslations))
	for _, translation := range RegisteredTranslations() {
		translationScopes = append(translationScopes, PostProcessorScope{Key: translation.Language, DisplayName: translation.DisplayName, Step: translation.Step})
	}
	registry, err := NewPostProcessorRegistry(
		PostProcessorDefinition{
			Key: CategoryVerificationStep, DisplayName: "Category verification", Description: "Recheck categories while keeping the last successful result effective until a replacement succeeds.", Priority: 10, ModelSettingKey: CategoryVerificationStep, Automatic: true, Manual: true,
			Scopes:   []PostProcessorScope{{Key: DefaultPostProcessingScope, DisplayName: "Default", Step: CategoryVerificationDefinition()}},
			Counters: []PostProcessorCounter{{Key: "corrected", OutputKind: "is_correct", EqualsValue: "false"}},
		},
		PostProcessorDefinition{
			Key: TranslationModelStep, DisplayName: "Translations", Description: "Translate current presentations into one registered language or every registered language.", Priority: 20, ModelSettingKey: TranslationModelStep, Automatic: true, Manual: true,
			Scopes: translationScopes,
		},
	)
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *PostProcessorRegistry) Definitions() []PostProcessorDefinition {
	if r == nil {
		return nil
	}
	definitions := make([]PostProcessorDefinition, len(r.definitions))
	for index, definition := range r.definitions {
		definitions[index] = clonePostProcessorDefinition(definition)
	}
	return definitions
}

func (r *PostProcessorRegistry) Definition(key string) (PostProcessorDefinition, bool) {
	if r == nil {
		return PostProcessorDefinition{}, false
	}
	for _, definition := range r.definitions {
		if definition.Key == key {
			return clonePostProcessorDefinition(definition), true
		}
	}
	return PostProcessorDefinition{}, false
}

func (r *PostProcessorRegistry) Scope(processorKey, scopeKey string) (PostProcessorDefinition, PostProcessorScope, bool) {
	definition, found := r.Definition(processorKey)
	if !found {
		return PostProcessorDefinition{}, PostProcessorScope{}, false
	}
	for _, scope := range definition.Scopes {
		if scope.Key == scopeKey {
			return definition, scope, true
		}
	}
	return PostProcessorDefinition{}, PostProcessorScope{}, false
}

func (r *PostProcessorRegistry) ModelSettingKeys() []string {
	definitions := r.Definitions()
	keys := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		keys = append(keys, definition.ModelSettingKey)
	}
	return keys
}

func (r *PostProcessorRegistry) StoreScopes() []store.PostProcessingScope {
	var scopes []store.PostProcessingScope
	for _, definition := range r.Definitions() {
		for _, scope := range definition.Scopes {
			scopes = append(scopes, store.PostProcessingScope{ProcessorKey: definition.Key, ScopeKey: scope.Key})
		}
	}
	return scopes
}

func (r *PostProcessorRegistry) Plans(processorKey string, scopeKeys []string, model string) ([]store.PostProcessingPlan, error) {
	definition, found := r.Definition(processorKey)
	if !found {
		return nil, store.ErrNotFound
	}
	wanted := make(map[string]struct{}, len(scopeKeys))
	for _, scopeKey := range scopeKeys {
		scopeKey = strings.TrimSpace(scopeKey)
		if scopeKey == "" {
			return nil, store.ErrNotFound
		}
		if _, duplicate := wanted[scopeKey]; duplicate {
			return nil, errors.New("post-processing scopes must be unique")
		}
		wanted[scopeKey] = struct{}{}
	}
	plans := make([]store.PostProcessingPlan, 0, len(definition.Scopes))
	for _, scope := range definition.Scopes {
		if len(wanted) > 0 {
			if _, selected := wanted[scope.Key]; !selected {
				continue
			}
		}
		plans = append(plans, store.PostProcessingPlan{
			ProcessorKey: definition.Key, ScopeKey: scope.Key, PromptVersion: scope.Step.PromptVersion,
			Model: strings.TrimSpace(model), InputKinds: append([]string(nil), scope.Step.InputKinds...),
		})
	}
	if len(plans) == 0 || len(wanted) > 0 && len(plans) != len(wanted) {
		return nil, store.ErrNotFound
	}
	return plans, nil
}

func validateValueKinds(kinds []string, label string) error {
	seen := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		if kind == "" || strings.TrimSpace(kind) != kind {
			return errors.New("post-processor " + label + " kinds must be non-empty and normalized")
		}
		if _, duplicate := seen[kind]; duplicate {
			return errors.New("post-processor " + label + " kinds must be unique")
		}
		seen[kind] = struct{}{}
	}
	return nil
}

func clonePostProcessorDefinition(definition PostProcessorDefinition) PostProcessorDefinition {
	definition.Scopes = append([]PostProcessorScope(nil), definition.Scopes...)
	for index := range definition.Scopes {
		definition.Scopes[index].Step = cloneStepDefinition(definition.Scopes[index].Step)
	}
	definition.Counters = append([]PostProcessorCounter(nil), definition.Counters...)
	return definition
}
