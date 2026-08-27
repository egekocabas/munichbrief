package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/egekocabas/munichbrief/internal/store"
)

const (
	PipelineVersion        = store.PipelineVersion
	GermanAnalysisStep     = "german_analysis"
	EnglishTranslationStep = "english_translation"
)

// StepDefinition is an immutable registry entry describing one pipeline stage.
// Callers receive deep copies so persisted behavior cannot be mutated at runtime.
type StepDefinition struct {
	Key           string
	DisplayName   string
	Order         int
	PromptVersion string
	InputKinds    []string
	OutputKinds   []string
	SystemPrompt  string
	Schema        json.RawMessage
	Generator     func(StepInput) (StepInput, string, error)
	Validator     func(StepInput, *StepOutput) error
	OutputValues  func(StepOutput) ([]store.PipelineValue, error)
}

// StepInput contains the minimal source or accepted upstream values needed by a
// step. Later stages must not receive the original incident body.
type StepInput struct {
	OriginalTitle string
	IncidentBody  string
	TitleDE       string
	SummaryDE     string
}

// StepOutput is the strict union decoded from model responses. A step validator
// decides which fields are permitted and publishable for that stage.
type StepOutput struct {
	TitleDE       string   `json:"title_de,omitempty"`
	SummaryDE     string   `json:"summary_de,omitempty"`
	Category      string   `json:"category,omitempty"`
	AreaName      *string  `json:"area_name"`
	AreaType      *string  `json:"area_type"`
	TitleEN       string   `json:"title_en,omitempty"`
	SummaryEN     string   `json:"summary_en,omitempty"`
	PrivacyStatus string   `json:"privacy_status,omitempty"`
	PrivacyFlags  []string `json:"privacy_flags,omitempty"`
}

// StepGenerator executes one registered step with a fixed model identity.
type StepGenerator interface {
	GenerateStep(context.Context, StepDefinition, StepInput) (StepOutput, string, error)
	ModelIdentity() string
}

// StepGeneratorProvider constructs a generator for a selected model.
type StepGeneratorProvider interface {
	StepGenerator(model string) (StepGenerator, error)
}

var categoryLabels = map[string][2]string{
	"traffic":           {"Verkehr", "Traffic"},
	"theft_burglary":    {"Diebstahl und Einbruch", "Theft and burglary"},
	"robbery_extortion": {"Raub und Erpressung", "Robbery and extortion"},
	"violence":          {"Gewalt", "Violence"},
	"sexual_offense":    {"Sexualdelikte", "Sexual offences"},
	"fraud_cyber":       {"Betrug und Cyberkriminalität", "Fraud and cybercrime"},
	"drugs":             {"Rauschgift", "Drugs"},
	"fire_hazard":       {"Brand und Gefahrenlage", "Fire and hazards"},
	"property_damage":   {"Sachbeschädigung", "Property damage"},
	"missing_wanted":    {"Vermisstensuche und Fahndung", "Missing and wanted persons"},
	"police_operation":  {"Polizeieinsatz", "Police operation"},
	"other":             {"Sonstiges", "Other"},
}

var areaTypes = map[string]bool{
	"neighbourhood": true, "district": true, "municipality": true, "broad_area": true,
}

var germanAnalysisSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "title_de": {"type":"string","minLength":1,"maxLength":90},
    "summary_de": {"type":"string","minLength":1,"maxLength":600},
    "category": {"type":"string","enum":["traffic","theft_burglary","robbery_extortion","violence","sexual_offense","fraud_cyber","drugs","fire_hazard","property_damage","missing_wanted","police_operation","other"]},
    "area_name": {"type":["string","null"],"maxLength":80},
    "area_type": {"type":["string","null"],"enum":["neighbourhood","district","municipality","broad_area",null]},
    "privacy_status": {"type":"string","enum":["safe","review_required"]},
    "privacy_flags": {"type":"array","maxItems":8,"uniqueItems":true,"items":{"type":"string","enum":["person_name","direct_identifier","precise_location","age","sensitive_attribute","minor","missing_or_wanted_person","uncertain"]}}
  },
  "required":["title_de","summary_de","category","area_name","area_type","privacy_status","privacy_flags"],
  "additionalProperties":false
}`)

var englishTranslationSchema = json.RawMessage(`{
  "type":"object",
  "properties":{
    "title_en":{"type":"string","minLength":1,"maxLength":90},
    "summary_en":{"type":"string","minLength":1,"maxLength":600}
  },
  "required":["title_en","summary_en"],
  "additionalProperties":false
}`)

var registeredSteps = []StepDefinition{
	{
		Key: GermanAnalysisStep, DisplayName: "German analysis", Order: 0,
		PromptVersion: GermanAnalysisPromptVersion,
		InputKinds:    []string{"original_title", "incident_body"},
		OutputKinds:   []string{"title_de", "summary_de", "category", "area_name", "area_type"},
		SystemPrompt:  mustPromptByVersion(GermanAnalysisPromptVersion).SystemPrompt, Schema: germanAnalysisSchema,
		Generator: generateGermanAnalysisInput, Validator: validateGermanAnalysis, OutputValues: germanAnalysisValues,
	},
	{
		Key: EnglishTranslationStep, DisplayName: "English translation", Order: 1,
		PromptVersion: EnglishTranslationPromptVersion,
		InputKinds:    []string{"title_de", "summary_de"}, OutputKinds: []string{"title_en", "summary_en"},
		SystemPrompt: mustPromptByVersion(EnglishTranslationPromptVersion).SystemPrompt, Schema: englishTranslationSchema,
		Generator: generateEnglishTranslationInput, Validator: func(_ StepInput, output *StepOutput) error { return validateEnglishTranslation(output) }, OutputValues: englishTranslationValues,
	},
}

// RegisteredSteps returns a deep copy of the stable, ordered step registry.
func RegisteredSteps() []StepDefinition {
	steps := make([]StepDefinition, len(registeredSteps))
	for index, step := range registeredSteps {
		steps[index] = cloneStepDefinition(step)
	}
	return steps
}

func StepByKey(key string) (StepDefinition, bool) {
	for _, step := range registeredSteps {
		if step.Key == key {
			return cloneStepDefinition(step), true
		}
	}
	return StepDefinition{}, false
}

func cloneStepDefinition(step StepDefinition) StepDefinition {
	step.InputKinds = append([]string(nil), step.InputKinds...)
	step.OutputKinds = append([]string(nil), step.OutputKinds...)
	step.Schema = append(json.RawMessage(nil), step.Schema...)
	return step
}

func StepKeys() []string {
	keys := make([]string, 0, len(registeredSteps))
	for _, step := range registeredSteps {
		keys = append(keys, step.Key)
	}
	return keys
}

// StepPlans freezes registered prompt versions and selected models into a cycle.
func StepPlans(models map[string]string) ([]store.PipelineStepPlan, error) {
	plans := make([]store.PipelineStepPlan, 0, len(registeredSteps))
	for _, step := range registeredSteps {
		model := strings.TrimSpace(models[step.Key])
		if model == "" {
			return nil, fmt.Errorf("%w: %s", store.ErrPipelineUnconfigured, step.Key)
		}
		plans = append(plans, store.PipelineStepPlan{Key: step.Key, Order: step.Order, PromptVersion: step.PromptVersion, Model: model})
	}
	return plans, nil
}

// ValidateStepOutput applies the stage-specific publication and privacy rules.
func ValidateStepOutput(step StepDefinition, input StepInput, output *StepOutput) error {
	if step.Validator == nil {
		return errorOf(ErrorConfiguration, "unknown pipeline step %q", step.Key)
	}
	return step.Validator(input, output)
}

func generateGermanAnalysisInput(input StepInput) (StepInput, string, error) {
	input.OriginalTitle, input.IncidentBody = minimizeIncidentSource(input.OriginalTitle, input.IncidentBody)
	encoded, err := json.Marshal(struct {
		OriginalTitle string `json:"original_title"`
		IncidentBody  string `json:"incident_body"`
	}{OriginalTitle: input.OriginalTitle, IncidentBody: input.IncidentBody})
	if err != nil {
		return StepInput{}, "", errorOf(ErrorOutput, "encode German analysis input: %v", err)
	}
	return input, promptUserMessage(GermanAnalysisPromptVersion, string(encoded)), nil
}

func generateEnglishTranslationInput(input StepInput) (StepInput, string, error) {
	encoded, err := json.Marshal(struct {
		TitleDE   string `json:"title_de"`
		SummaryDE string `json:"summary_de"`
	}{TitleDE: input.TitleDE, SummaryDE: input.SummaryDE})
	if err != nil {
		return StepInput{}, "", errorOf(ErrorOutput, "encode translation input: %v", err)
	}
	return input, promptUserMessage(EnglishTranslationPromptVersion, string(encoded)), nil
}

func validateGermanAnalysis(input StepInput, output *StepOutput) error {
	if err := normalizeLimitedField("title_de", &output.TitleDE, 90); err != nil {
		return err
	}
	if err := normalizeLimitedField("summary_de", &output.SummaryDE, 600); err != nil {
		return err
	}
	if _, ok := categoryLabels[output.Category]; !ok {
		return errorOf(ErrorOutput, "invalid incident category")
	}
	if output.PrivacyStatus != "safe" {
		return errorOf(ErrorPrivacy, "model marked German analysis for privacy review")
	}
	if err := normalizePrivacyFlags(&output.PrivacyFlags); err != nil {
		return err
	}
	if (output.AreaName == nil) != (output.AreaType == nil) {
		return errorOf(ErrorOutput, "area_name and area_type must both be null or both be set")
	}
	if output.AreaName != nil {
		name := strings.Join(strings.Fields(*output.AreaName), " ")
		if name == "" || utf8.RuneCountInString(name) > 80 || !areaTypes[*output.AreaType] {
			return errorOf(ErrorOutput, "invalid incident area")
		}
		source := strings.ToLower(strings.Join(strings.Fields(input.OriginalTitle+" "+input.IncidentBody), " "))
		if !strings.Contains(source, strings.ToLower(name)) {
			return errorOf(ErrorOutput, "incident area is not explicitly present in the minimized source")
		}
		*output.AreaName = name
	}
	return validatePublicText(output.TitleDE + "\n" + output.SummaryDE)
}

func validateEnglishTranslation(output *StepOutput) error {
	if err := normalizeLimitedField("title_en", &output.TitleEN, 90); err != nil {
		return err
	}
	if err := normalizeLimitedField("summary_en", &output.SummaryEN, 600); err != nil {
		return err
	}
	return validatePublicText(output.TitleEN + "\n" + output.SummaryEN)
}

func normalizeLimitedField(name string, value *string, limit int) error {
	if !utf8.ValidString(*value) {
		return errorOf(ErrorOutput, "model output %s is not valid UTF-8", name)
	}
	*value = strings.Join(strings.Fields(*value), " ")
	if *value == "" {
		return errorOf(ErrorOutput, "model output %s is empty", name)
	}
	if utf8.RuneCountInString(*value) > limit {
		return errorOf(ErrorOutput, "model output %s exceeds %d characters", name, limit)
	}
	return nil
}

func normalizePrivacyFlags(flags *[]string) error {
	allowed := map[string]bool{"person_name": true, "direct_identifier": true, "precise_location": true, "age": true, "sensitive_attribute": true, "minor": true, "missing_or_wanted_person": true, "uncertain": true}
	seen := make(map[string]bool)
	normalized := make([]string, 0, len(*flags))
	for _, flag := range *flags {
		if !allowed[flag] {
			return errorOf(ErrorOutput, "model output contains an invalid privacy flag")
		}
		if flag == "uncertain" {
			return errorOf(ErrorPrivacy, "model output contains unresolved privacy uncertainty")
		}
		if !seen[flag] {
			seen[flag] = true
			normalized = append(normalized, flag)
		}
	}
	*flags = normalized
	return nil
}

func validatePublicText(value string) error {
	for _, detector := range privacyDetectors {
		if detector.expression.MatchString(value) {
			return errorOf(ErrorPrivacy, "model output contains a possible %s", detector.label)
		}
	}
	return nil
}

func PipelineValues(stepKey string, output StepOutput) ([]store.PipelineValue, error) {
	step, found := StepByKey(stepKey)
	if !found || step.OutputValues == nil {
		return nil, errors.New("unknown pipeline step")
	}
	return step.OutputValues(output)
}

func germanAnalysisValues(output StepOutput) ([]store.PipelineValue, error) {
	flags, err := json.Marshal(output.PrivacyFlags)
	if err != nil {
		return nil, fmt.Errorf("encode privacy flags: %w", err)
	}
	values := []store.PipelineValue{
		{Kind: "title_de", Value: output.TitleDE}, {Kind: "summary_de", Value: output.SummaryDE},
		{Kind: "category", Value: output.Category}, {Kind: "privacy_status", Value: output.PrivacyStatus},
		{Kind: "privacy_flags", Value: string(flags)},
	}
	if output.AreaName != nil {
		values = append(values, store.PipelineValue{Kind: "area_name", Value: *output.AreaName}, store.PipelineValue{Kind: "area_type", Value: *output.AreaType})
	}
	return values, nil
}

func englishTranslationValues(output StepOutput) ([]store.PipelineValue, error) {
	return []store.PipelineValue{{Kind: "title_en", Value: output.TitleEN}, {Kind: "summary_en", Value: output.SummaryEN}}, nil
}

func CategoryLabel(code, language string) string {
	labels, ok := categoryLabels[code]
	if !ok {
		return code
	}
	if language == "de" {
		return labels[0]
	}
	return labels[1]
}
