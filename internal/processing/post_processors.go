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
		definition = clonePostProcessorDefinition(definition)
		definition.Key = strings.TrimSpace(definition.Key)
		definition.DisplayName = strings.TrimSpace(definition.DisplayName)
		definition.ModelSettingKey = strings.TrimSpace(definition.ModelSettingKey)
		definition.Description = strings.TrimSpace(definition.Description)
		if definition.Key == "" || definition.DisplayName == "" || definition.Description == "" || definition.ModelSettingKey == "" || len(definition.Scopes) == 0 || !definition.Automatic && !definition.Manual {
			return nil, errors.New("post-processor key, display name, model setting, and scopes are required")
		}
		if _, duplicate := seenProcessors[definition.Key]; duplicate {
			return nil, errors.New("post-processor keys must be unique")
		}
		seenProcessors[definition.Key] = struct{}{}
		seenScopes := make(map[string]struct{}, len(definition.Scopes))
		outputKindScopes := make(map[string]int)
		for scopeIndex, scope := range definition.Scopes {
			scope.Key = strings.TrimSpace(scope.Key)
			scope.DisplayName = strings.TrimSpace(scope.DisplayName)
			scope.Step.Key = strings.TrimSpace(scope.Step.Key)
			scope.Step.PromptVersion = strings.TrimSpace(scope.Step.PromptVersion)
			if scope.Key == "" || scope.DisplayName == "" || scope.Step.Key == "" || scope.Step.PromptVersion == "" || len(scope.Step.InputKinds) == 0 || len(scope.Step.OutputKinds) == 0 || scope.Step.OutputValues == nil {
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
				outputKindScopes[kind]++
			}
			scope.Step = cloneStepDefinition(scope.Step)
			definition.Scopes[scopeIndex] = scope
		}
		seenCounters := make(map[string]struct{}, len(definition.Counters))
		for counterIndex, counter := range definition.Counters {
			counter.Key = strings.TrimSpace(counter.Key)
			counter.OutputKind = strings.TrimSpace(counter.OutputKind)
			if counter.Key == "" || counter.OutputKind == "" || strings.TrimSpace(counter.EqualsValue) == "" {
				return nil, errors.New("post-processor counter key, output kind, and comparison value are required")
			}
			if _, duplicate := seenCounters[counter.Key]; duplicate {
				return nil, errors.New("post-processor counter keys must be unique")
			}
			if outputKindScopes[counter.OutputKind] != len(definition.Scopes) {
				return nil, errors.New("post-processor counter must reference an output kind declared by every scope")
			}
			seenCounters[counter.Key] = struct{}{}
			definition.Counters[counterIndex] = counter
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
			Key: PublicAssistanceVerificationStep, DisplayName: "Public assistance verification", Description: "Recheck public-assistance status and types while keeping the last successful result effective until a replacement succeeds.", Priority: 10, ModelSettingKey: PublicAssistanceVerificationStep, Automatic: true, Manual: true,
			Scopes:   []PostProcessorScope{{Key: DefaultPostProcessingScope, DisplayName: "Default", Step: PublicAssistanceVerificationDefinition()}},
			Counters: []PostProcessorCounter{{Key: "corrected", OutputKind: "is_correct", EqualsValue: "false"}},
		},
		PostProcessorDefinition{
			Key: CategoryVerificationStep, DisplayName: "Category verification", Description: "Recheck categories while keeping the last successful result effective until a replacement succeeds.", Priority: 20, ModelSettingKey: CategoryVerificationStep, Automatic: true, Manual: true,
			Scopes:   []PostProcessorScope{{Key: DefaultPostProcessingScope, DisplayName: "Default", Step: CategoryVerificationDefinition()}},
			Counters: []PostProcessorCounter{{Key: "corrected", OutputKind: "is_correct", EqualsValue: "false"}},
		},
		PostProcessorDefinition{
			Key: TranslationModelStep, DisplayName: "Translations", Description: "Translate current presentations into one registered language or every registered language.", Priority: 30, ModelSettingKey: TranslationModelStep, Automatic: true, Manual: true,
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

// OrderedQueueStats aligns persisted queue state with the same processor and
// scope order used for execution, model configuration, and focused controls.
// Unknown persisted scopes remain visible after registered scopes.
func (r *PostProcessorRegistry) OrderedQueueStats(stats []store.PostProcessingQueueStats) []store.PostProcessingQueueStats {
	type scopeKey struct {
		processor string
		scope     string
	}
	ranks := make(map[scopeKey]int)
	rank := 0
	for _, definition := range r.Definitions() {
		for _, scope := range definition.Scopes {
			ranks[scopeKey{processor: definition.Key, scope: scope.Key}] = rank
			rank++
		}
	}
	ordered := append([]store.PostProcessingQueueStats(nil), stats...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := ordered[i]
		right := ordered[j]
		leftRank, leftRegistered := ranks[scopeKey{processor: left.ProcessorKey, scope: left.ScopeKey}]
		rightRank, rightRegistered := ranks[scopeKey{processor: right.ProcessorKey, scope: right.ScopeKey}]
		if leftRegistered != rightRegistered {
			return leftRegistered
		}
		if leftRegistered {
			return leftRank < rightRank
		}
		if left.ProcessorKey == right.ProcessorKey {
			return left.ScopeKey < right.ScopeKey
		}
		return left.ProcessorKey < right.ProcessorKey
	})
	return ordered
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
