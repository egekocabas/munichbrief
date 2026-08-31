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

// PostProcessorVerificationField maps one canonical presentation value to the
// corrected value emitted by a correction-style verifier.
type PostProcessorVerificationField struct {
	DisplayName   string `json:"display_name"`
	OriginalKind  string `json:"original_kind"`
	CorrectedKind string `json:"corrected_kind"`
}

// PostProcessorVerification describes the review contract for a
// correction-style post-processor. Processors without this metadata are not
// presented as verifiers by focused administration views.
type PostProcessorVerification struct {
	VerdictKind string                           `json:"verdict_kind"`
	Fields      []PostProcessorVerificationField `json:"fields"`
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
	Verification    *PostProcessorVerification
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
		if definition.Verification != nil {
			verification := clonePostProcessorVerification(definition.Verification)
			verification.VerdictKind = strings.TrimSpace(verification.VerdictKind)
			if verification.VerdictKind == "" || len(verification.Fields) == 0 {
				return nil, errors.New("verification verdict and fields are required")
			}
			if outputKindScopes[verification.VerdictKind] != len(definition.Scopes) {
				return nil, errors.New("verification verdict must be emitted by every scope")
			}
			seenOriginals := make(map[string]struct{}, len(verification.Fields))
			seenCorrected := make(map[string]struct{}, len(verification.Fields))
			for fieldIndex, field := range verification.Fields {
				field.DisplayName = strings.TrimSpace(field.DisplayName)
				field.OriginalKind = strings.TrimSpace(field.OriginalKind)
				field.CorrectedKind = strings.TrimSpace(field.CorrectedKind)
				if field.DisplayName == "" || field.OriginalKind == "" || field.CorrectedKind == "" {
					return nil, errors.New("verification field display name, original kind, and corrected kind are required")
				}
				if _, duplicate := seenOriginals[field.OriginalKind]; duplicate {
					return nil, errors.New("verification original kinds must be unique")
				}
				if _, duplicate := seenCorrected[field.CorrectedKind]; duplicate {
					return nil, errors.New("verification corrected kinds must be unique")
				}
				for _, scope := range definition.Scopes {
					if !containsValueKind(scope.Step.InputKinds, field.OriginalKind) {
						return nil, errors.New("verification original kind must be required by every scope")
					}
				}
				if outputKindScopes[field.CorrectedKind] != len(definition.Scopes) {
					return nil, errors.New("verification corrected kind must be emitted by every scope")
				}
				seenOriginals[field.OriginalKind] = struct{}{}
				seenCorrected[field.CorrectedKind] = struct{}{}
				verification.Fields[fieldIndex] = field
			}
			definition.Verification = verification
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
			Verification: &PostProcessorVerification{VerdictKind: "is_correct", Fields: []PostProcessorVerificationField{
				{DisplayName: "Public assistance status", OriginalKind: "public_assistance_status", CorrectedKind: "corrected_public_assistance_status"},
				{DisplayName: "Public assistance types", OriginalKind: "public_assistance_types", CorrectedKind: "corrected_public_assistance_types"},
			}},
		},
		PostProcessorDefinition{
			Key: CategoryVerificationStep, DisplayName: "Category verification", Description: "Recheck categories while keeping the last successful result effective until a replacement succeeds.", Priority: 20, ModelSettingKey: CategoryVerificationStep, Automatic: true, Manual: true,
			Scopes:   []PostProcessorScope{{Key: DefaultPostProcessingScope, DisplayName: "Default", Step: CategoryVerificationDefinition()}},
			Counters: []PostProcessorCounter{{Key: "corrected", OutputKind: "is_correct", EqualsValue: "false"}},
			Verification: &PostProcessorVerification{VerdictKind: "is_correct", Fields: []PostProcessorVerificationField{
				{DisplayName: "Category", OriginalKind: "category", CorrectedKind: "corrected_category"},
			}},
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

func containsValueKind(kinds []string, wanted string) bool {
	for _, kind := range kinds {
		if kind == wanted {
			return true
		}
	}
	return false
}

func clonePostProcessorDefinition(definition PostProcessorDefinition) PostProcessorDefinition {
	definition.Scopes = append([]PostProcessorScope(nil), definition.Scopes...)
	for index := range definition.Scopes {
		definition.Scopes[index].Step = cloneStepDefinition(definition.Scopes[index].Step)
	}
	definition.Counters = append([]PostProcessorCounter(nil), definition.Counters...)
	definition.Verification = clonePostProcessorVerification(definition.Verification)
	return definition
}

func clonePostProcessorVerification(verification *PostProcessorVerification) *PostProcessorVerification {
	if verification == nil {
		return nil
	}
	cloned := *verification
	cloned.Fields = append([]PostProcessorVerificationField(nil), verification.Fields...)
	return &cloned
}
