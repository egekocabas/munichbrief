package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	langregistry "github.com/egekocabas/munichbrief/internal/languages"
	"github.com/egekocabas/munichbrief/internal/store"
	"golang.org/x/text/unicode/norm"
)

const (
	PipelineVersion                  = store.PipelineVersion
	IncidentMetadataStep             = "incident_metadata"
	GermanPresentationStep           = "german_presentation"
	TranslationModelStep             = "translation"
	PublicAssistanceVerificationStep = "public_assistance_verification"
	CategoryVerificationStep         = "category_verification"
	EnglishLanguage                  = "en"
	EnglishTranslationStep           = "translation/en"
)

type StepDefinition struct {
	Key           string
	DisplayName   string
	Order         int
	PromptVersion string
	UserOnly      bool
	InputKinds    []string
	OutputKinds   []string
	SystemPrompt  string
	Schema        json.RawMessage
	Generator     func(StepInput) (StepInput, string, error)
	OutputDecoder func(string) (StepOutput, error)
	Validator     func(StepInput, *StepOutput) error
	OutputValues  func(StepOutput) ([]store.PipelineValue, error)
}

// TranslationDefinition describes one independently queued target language.
// Translations are deliberately not ordered canonical pipeline stages.
type TranslationDefinition struct {
	Language      string
	DisplayName   string
	PromptVersion string
	Step          StepDefinition
}

// StepInput contains only values declared by a step's InputKinds contract.
type StepInput struct {
	Values        map[string]string
	OriginalTitle string
	IncidentBody  string
	TitleDE       string
	SummaryDE     string
}

func (i StepInput) Value(kind string) string {
	if value, ok := i.Values[kind]; ok {
		return value
	}
	switch kind {
	case "original_title":
		return i.OriginalTitle
	case "incident_body":
		return i.IncidentBody
	case "title_de":
		return i.TitleDE
	case "summary_de":
		return i.SummaryDE
	default:
		return ""
	}
}
func (i StepInput) clone() StepInput {
	result := i
	result.Values = make(map[string]string, len(i.Values))
	for kind, value := range i.Values {
		result.Values[kind] = value
	}
	return result
}

type StepOutput struct {
	Values                 map[string]string `json:"-"`
	TitleDE                string            `json:"title_de,omitempty"`
	SummaryDE              string            `json:"summary_de,omitempty"`
	Category               string            `json:"category,omitempty"`
	AreaName               *string           `json:"area_name"`
	AreaType               *string           `json:"area_type"`
	EventStartDate         *string           `json:"event_start_date"`
	EventStartTime         *string           `json:"event_start_time"`
	EventDayPart           *string           `json:"event_day_part"`
	ReportKind             string            `json:"report_kind,omitempty"`
	PublicAssistanceStatus string            `json:"public_assistance_status,omitempty"`
	PublicAssistanceTypes  []string          `json:"public_assistance_types,omitempty"`
	PrivacyStatus          string            `json:"privacy_status,omitempty"`
	PrivacyFlags           []string          `json:"privacy_flags,omitempty"`
}

type StepGenerator interface {
	GenerateStep(context.Context, StepDefinition, StepInput) (StepOutput, string, error)
	ModelIdentity() string
}

type StepGeneratorProvider interface {
	StepGenerator(model string) (StepGenerator, error)
}

var canonicalCategoryLabels = map[string]string{
	"traffic": "Verkehr", "theft_burglary": "Diebstahl und Einbruch",
	"robbery_extortion": "Raub und Erpressung", "violence": "Gewalt",
	"sexual_offense": "Sexualdelikte", "fraud_cyber": "Betrug und Cyberkriminalität",
	"drugs": "Rauschgift", "fire_hazard": "Brand und Gefahrenlage",
	"property_damage": "Sachbeschädigung", "missing_wanted": "Vermisstensuche und Fahndung",
	"police_operation": "Polizeieinsatz", "other": "Sonstiges",
}

var areaTypes = map[string]bool{"neighbourhood": true, "district": true, "municipality": true, "broad_area": true}
var eventDayParts = map[string]bool{"morning": true, "midday": true, "afternoon": true, "evening": true, "night": true}
var reportKinds = map[string]bool{"incident": true, "follow_up": true, "missing_person": true, "wanted_person": true, "public_warning": true, "other": true}
var assistanceStatuses = map[string]bool{"requested": true, "not_requested": true, "unclear": true}
var assistanceTypes = map[string]bool{
	"witness_observations": true, "identify_person": true, "locate_person": true, "photo_video_material": true,
	"vehicle_information": true, "property_information": true, "other_information": true,
}

var incidentMetadataSchema = json.RawMessage(`{
  "type":"object",
  "properties":{
    "category":{"type":"string","enum":["traffic","theft_burglary","robbery_extortion","violence","sexual_offense","fraud_cyber","drugs","fire_hazard","property_damage","missing_wanted","police_operation","other"]},
    "area_name":{"type":["string","null"],"maxLength":80},"area_type":{"type":["string","null"],"enum":["neighbourhood","district","municipality","broad_area",null]},
    "event_start_date":{"type":["string","null"]},"event_start_time":{"type":["string","null"]},
    "event_day_part":{"type":["string","null"],"enum":["morning","midday","afternoon","evening","night",null]},
    "report_kind":{"type":"string","enum":["incident","follow_up","missing_person","wanted_person","public_warning","other"]},
    "public_assistance_status":{"type":"string","enum":["requested","not_requested","unclear"]},
    "public_assistance_types":{"type":"array","maxItems":7,"uniqueItems":true,"items":{"type":"string","enum":["witness_observations","identify_person","locate_person","photo_video_material","vehicle_information","property_information","other_information"]}}
  },
  "required":["category","area_name","area_type","event_start_date","event_start_time","event_day_part","report_kind","public_assistance_status","public_assistance_types"],
  "additionalProperties":false
}`)

var germanPresentationSchema = json.RawMessage(`{
  "type":"object","properties":{
    "title_de":{"type":"string","minLength":1,"maxLength":90},"summary_de":{"type":"string","minLength":1,"maxLength":600},
    "privacy_status":{"type":"string","enum":["safe","review_required"]},
    "privacy_flags":{"type":"array","maxItems":8,"uniqueItems":true,"items":{"type":"string","enum":["person_name","direct_identifier","precise_location","age","sensitive_attribute","minor","missing_or_wanted_person","uncertain"]}}
  },"required":["title_de","summary_de","privacy_status","privacy_flags"],"additionalProperties":false
}`)

var categoryVerificationSchema = json.RawMessage(`{
  "type":"object","properties":{
    "is_correct":{"type":"boolean"},
    "corrected_category":{"type":"string","enum":["Verkehr","Diebstahl und Einbruch","Raub und Erpressung","Gewalt","Sexualdelikte","Betrug und Cyberkriminalität","Rauschgift","Brand und Gefahrenlage","Sachbeschädigung","Vermisstensuche und Fahndung","Polizeieinsatz","Sonstiges"]}
  },"required":["is_correct","corrected_category"],"additionalProperties":false
}`)

var publicAssistanceVerificationSchema = json.RawMessage(`{
  "type":"object","properties":{
    "is_correct":{"type":"boolean"},
    "corrected_public_assistance_status":{"type":"string","enum":["requested","not_requested","unclear"]},
    "corrected_public_assistance_types":{"type":"array","maxItems":7,"uniqueItems":true,"items":{"type":"string","enum":["witness_observations","identify_person","locate_person","photo_video_material","vehicle_information","property_information","other_information"]}}
  },"required":["is_correct","corrected_public_assistance_status","corrected_public_assistance_types"],"additionalProperties":false
}`)

var metadataOutputKinds = []string{
	"category", "area_name", "area_type", "event_start_date", "event_start_time",
	"event_day_part", "report_kind", "public_assistance_status", "public_assistance_types",
}

var registeredSteps = []StepDefinition{
	{Key: IncidentMetadataStep, DisplayName: "Incident metadata", Order: 0, PromptVersion: IncidentMetadataPromptVersion,
		InputKinds: []string{"original_title", "incident_body", "published_at"}, OutputKinds: metadataOutputKinds,
		SystemPrompt: mustPromptByVersion(IncidentMetadataPromptVersion).SystemPrompt, Schema: incidentMetadataSchema,
		Generator: generateIncidentMetadataInput, Validator: validateIncidentMetadata, OutputValues: incidentMetadataValues},
	{Key: GermanPresentationStep, DisplayName: "German presentation", Order: 1, PromptVersion: GermanPresentationPromptVersion,
		InputKinds: append([]string{"original_title", "incident_body"}, metadataOutputKinds...), OutputKinds: []string{"title_de", "summary_de"},
		SystemPrompt: mustPromptByVersion(GermanPresentationPromptVersion).SystemPrompt, Schema: germanPresentationSchema,
		Generator: generateGermanPresentationInput, Validator: validateGermanPresentation, OutputValues: germanPresentationValues},
}

var registeredTranslations = mustRegisteredTranslations()

func mustRegisteredTranslations() []TranslationDefinition {
	definitions := langregistry.Registered()
	if err := langregistry.Validate(definitions); err != nil {
		panic(err)
	}
	translations := make([]TranslationDefinition, 0, len(definitions)-1)
	for _, target := range langregistry.Translated(definitions) {
		var active []PromptDefinition
		for _, prompt := range promptRegistry {
			if prompt.TranslationLanguage == target.Code && prompt.Status == PromptActive {
				active = append(active, prompt)
			}
		}
		if len(active) != 1 {
			panic(fmt.Sprintf("translation language %s requires exactly one active prompt, got %d", target.Code, len(active)))
		}
		translation, err := newTranslationDefinition(target, active[0])
		if err != nil {
			panic(err)
		}
		translations = append(translations, translation)
	}
	for _, prompt := range promptRegistry {
		if prompt.TranslationLanguage == "" {
			continue
		}
		target, found := langregistry.ByCode(definitions, prompt.TranslationLanguage)
		if !found || target.Canonical {
			panic("translation prompt has no translated language registration: " + prompt.TranslationLanguage)
		}
	}
	return translations
}

func newTranslationDefinition(target langregistry.Definition, prompt PromptDefinition) (TranslationDefinition, error) {
	if target.Canonical || prompt.TranslationLanguage != target.Code || prompt.StepKey != TranslationStepKey(target.Code) {
		return TranslationDefinition{}, fmt.Errorf("translation language %s has inconsistent prompt metadata", target.Code)
	}
	if prompt.Status != PromptActive || strings.TrimSpace(prompt.Version) == "" || !prompt.UserOnly || strings.TrimSpace(prompt.SystemPrompt) != "" || !validTranslationPromptTemplate(prompt.UserPromptTemplate) {
		return TranslationDefinition{}, fmt.Errorf("translation language %s has incomplete active prompt content", target.Code)
	}
	titleField, summaryField := translationFieldNames(target.Code)
	schema, err := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			titleField:   map[string]any{"type": "string", "minLength": 1, "maxLength": 90},
			summaryField: map[string]any{"type": "string", "minLength": 1, "maxLength": 600},
		},
		"required": []string{titleField, summaryField}, "additionalProperties": false,
	})
	if err != nil {
		return TranslationDefinition{}, fmt.Errorf("build %s translation schema: %w", target.Code, err)
	}
	return TranslationDefinition{
		Language: target.Code, DisplayName: target.DisplayName, PromptVersion: prompt.Version,
		Step: StepDefinition{Key: prompt.StepKey, DisplayName: target.DisplayName + " translation", PromptVersion: prompt.Version,
			UserOnly:   prompt.UserOnly,
			InputKinds: []string{"title_de", "summary_de"}, OutputKinds: []string{"title", "summary"},
			SystemPrompt: prompt.SystemPrompt, Schema: schema,
			Generator: translationInputGenerator(prompt.Version), OutputDecoder: translationOutputDecoder(titleField, summaryField),
			Validator: translationValidator(target.Code), OutputValues: postProcessingOutputValues},
	}, nil
}

func validTranslationPromptTemplate(value string) bool {
	if strings.Count(value, "%s") != 1 {
		return false
	}
	const payload = "__MUNICHBRIEF_TRANSLATION_PAYLOAD__"
	rendered := fmt.Sprintf(value, payload)
	return strings.Count(rendered, payload) == 1 && !strings.Contains(rendered, "%!")
}

func TranslationStepKey(languageCode string) string { return TranslationModelStep + "/" + languageCode }

var categoryVerificationDefinition = StepDefinition{
	Key: CategoryVerificationStep, DisplayName: "Category verification", PromptVersion: CategoryVerificationPromptVersion,
	InputKinds: []string{"title_de", "summary_de", "category"}, OutputKinds: []string{"is_correct", "corrected_category"},
	SystemPrompt: mustPromptByVersion(CategoryVerificationPromptVersion).SystemPrompt, Schema: categoryVerificationSchema,
	Generator: categoryVerificationInputGenerator, OutputDecoder: categoryVerificationOutputDecoder,
	Validator: validateCategoryVerification, OutputValues: postProcessingOutputValues,
}

var publicAssistanceVerificationDefinition = StepDefinition{
	Key: PublicAssistanceVerificationStep, DisplayName: "Public assistance verification", PromptVersion: PublicAssistanceVerificationPromptVersion,
	InputKinds:   []string{"original_title", "incident_body", "public_assistance_status", "public_assistance_types"},
	OutputKinds:  []string{"is_correct", "corrected_public_assistance_status", "corrected_public_assistance_types"},
	SystemPrompt: mustPromptByVersion(PublicAssistanceVerificationPromptVersion).SystemPrompt, Schema: publicAssistanceVerificationSchema,
	Generator: publicAssistanceVerificationInputGenerator, OutputDecoder: publicAssistanceVerificationOutputDecoder,
	Validator: validatePublicAssistanceVerification, OutputValues: postProcessingOutputValues,
}

func PublicAssistanceVerificationDefinition() StepDefinition {
	return cloneStepDefinition(publicAssistanceVerificationDefinition)
}

func CategoryVerificationDefinition() StepDefinition {
	return cloneStepDefinition(categoryVerificationDefinition)
}

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

func ModelSettingKeys() []string {
	return append(StepKeys(), DefaultPostProcessorRegistry().ModelSettingKeys()...)
}

func RegisteredTranslations() []TranslationDefinition {
	translations := make([]TranslationDefinition, len(registeredTranslations))
	for index, translation := range registeredTranslations {
		translation.Step = cloneStepDefinition(translation.Step)
		translations[index] = translation
	}
	return translations
}

func TranslationByLanguage(language string) (TranslationDefinition, bool) {
	for _, translation := range registeredTranslations {
		if translation.Language == language {
			translation.Step = cloneStepDefinition(translation.Step)
			return translation, true
		}
	}
	return TranslationDefinition{}, false
}

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

func ValidateStepOutput(step StepDefinition, input StepInput, output *StepOutput) error {
	if step.Validator == nil {
		return errorOf(ErrorConfiguration, "unknown pipeline step %q", step.Key)
	}
	return step.Validator(input, output)
}

func generateIncidentMetadataInput(input StepInput) (StepInput, string, error) {
	requestInput := input.clone()
	title, body := minimizeIncidentSource(input.Value("original_title"), input.Value("incident_body"))
	requestInput.Values["original_title"], requestInput.Values["incident_body"] = title, body
	published, err := time.Parse(time.RFC3339Nano, input.Value("published_at"))
	if err != nil {
		return StepInput{}, "", errorOf(ErrorOutput, "parse publication time: %v", err)
	}
	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return StepInput{}, "", errorOf(ErrorConfiguration, "load publication timezone: %v", err)
	}
	local := published.In(location)
	payload := struct {
		PublicationLocalDateTime string            `json:"publication_local_datetime"`
		PublicationWeekday       string            `json:"publication_weekday"`
		Timezone                 string            `json:"timezone"`
		RelativeDates            map[string]string `json:"relative_dates"`
		WeekdayDates             map[string]string `json:"weekday_dates"`
		OriginalTitle            string            `json:"original_title"`
		IncidentBody             string            `json:"incident_body"`
	}{
		PublicationLocalDateTime: local.Format(time.RFC3339), PublicationWeekday: germanWeekday(local.Weekday()), Timezone: "Europe/Berlin",
		RelativeDates: map[string]string{
			"heute": local.Format("2006-01-02"), "gestern": local.AddDate(0, 0, -1).Format("2006-01-02"),
			"vorgestern": local.AddDate(0, 0, -2).Format("2006-01-02"), "vorabend": local.AddDate(0, 0, -1).Format("2006-01-02"),
		},
		WeekdayDates: make(map[string]string, 7), OriginalTitle: title, IncidentBody: body,
	}
	for daysAgo := 0; daysAgo < 7; daysAgo++ {
		day := local.AddDate(0, 0, -daysAgo)
		payload.WeekdayDates[germanWeekday(day.Weekday())] = day.Format("2006-01-02")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return StepInput{}, "", errorOf(ErrorOutput, "encode incident metadata input: %v", err)
	}
	requestInput.Values["publication_local_datetime"] = payload.PublicationLocalDateTime
	return requestInput, promptUserMessage(IncidentMetadataPromptVersion, string(encoded)), nil
}

func generateGermanPresentationInput(input StepInput) (StepInput, string, error) {
	requestInput := input.clone()
	title, body := minimizeIncidentSource(input.Value("original_title"), input.Value("incident_body"))
	requestInput.Values["original_title"], requestInput.Values["incident_body"] = title, body
	metadata := make(map[string]any, len(metadataOutputKinds))
	for _, kind := range metadataOutputKinds {
		value := input.Value(kind)
		if kind == "public_assistance_types" {
			var values []string
			if value != "" {
				if err := json.Unmarshal([]byte(value), &values); err != nil {
					return StepInput{}, "", errorOf(ErrorOutput, "decode assistance metadata: %v", err)
				}
			}
			metadata[kind] = values
		} else if value == "" {
			metadata[kind] = nil
		} else {
			metadata[kind] = value
		}
	}
	encoded, err := json.Marshal(struct {
		OriginalTitle string         `json:"original_title"`
		IncidentBody  string         `json:"incident_body"`
		Metadata      map[string]any `json:"metadata"`
	}{title, body, metadata})
	if err != nil {
		return StepInput{}, "", errorOf(ErrorOutput, "encode German presentation input: %v", err)
	}
	return requestInput, promptUserMessage(GermanPresentationPromptVersion, string(encoded)), nil
}

func translationInputGenerator(promptVersion string) func(StepInput) (StepInput, string, error) {
	return func(input StepInput) (StepInput, string, error) {
		encoded, err := json.Marshal(struct {
			TitleDE   string `json:"title_de"`
			SummaryDE string `json:"summary_de"`
		}{input.Value("title_de"), input.Value("summary_de")})
		if err != nil {
			return StepInput{}, "", errorOf(ErrorOutput, "encode translation input: %v", err)
		}
		return input, promptUserMessage(promptVersion, string(encoded)), nil
	}
}

func translationOutputDecoder(titleField, summaryField string) func(string) (StepOutput, error) {
	return func(content string) (StepOutput, error) {
		var fields map[string]json.RawMessage
		if err := decodeStrictJSON(content, &fields); err != nil {
			return StepOutput{}, err
		}
		if len(fields) != 2 {
			return StepOutput{}, errors.New("translation output must contain exactly title and summary")
		}
		var result struct{ Title, Summary string }
		title, titleFound := fields[titleField]
		summary, summaryFound := fields[summaryField]
		if !titleFound || !summaryFound {
			return StepOutput{}, errors.New("translation output is missing its registered title or summary field")
		}
		if err := json.Unmarshal(title, &result.Title); err != nil {
			return StepOutput{}, fmt.Errorf("decode translated title: %w", err)
		}
		if err := json.Unmarshal(summary, &result.Summary); err != nil {
			return StepOutput{}, fmt.Errorf("decode translated summary: %w", err)
		}
		return StepOutput{Values: map[string]string{"title": result.Title, "summary": result.Summary}}, nil
	}
}

func publicAssistanceVerificationInputGenerator(input StepInput) (StepInput, string, error) {
	status := strings.TrimSpace(input.Value("public_assistance_status"))
	if !assistanceStatuses[status] {
		return StepInput{}, "", errorOf(ErrorOutput, "invalid public assistance verification input status")
	}
	var types []string
	if err := json.Unmarshal([]byte(input.Value("public_assistance_types")), &types); err != nil {
		return StepInput{}, "", errorOf(ErrorOutput, "decode public assistance verification input types: %v", err)
	}
	types, err := normalizeAssistanceTypes(types)
	if err != nil || (status == "requested") != (len(types) > 0) {
		return StepInput{}, "", errorOf(ErrorOutput, "invalid public assistance verification input types")
	}
	typesJSON, err := json.Marshal(types)
	if err != nil {
		return StepInput{}, "", errorOf(ErrorOutput, "encode public assistance verification input types: %v", err)
	}
	requestInput := StepInput{Values: map[string]string{
		"original_title": input.Value("original_title"), "incident_body": input.Value("incident_body"),
		"public_assistance_status": status, "public_assistance_types": string(typesJSON),
	}}
	encoded, err := json.Marshal(struct {
		OriginalTitle          string   `json:"original_title"`
		IncidentBody           string   `json:"incident_body"`
		PublicAssistanceStatus string   `json:"existing_public_assistance_status"`
		PublicAssistanceTypes  []string `json:"existing_public_assistance_types"`
	}{requestInput.Value("original_title"), requestInput.Value("incident_body"), status, types})
	if err != nil {
		return StepInput{}, "", errorOf(ErrorOutput, "encode public assistance verification input: %v", err)
	}
	return requestInput, promptUserMessage(PublicAssistanceVerificationPromptVersion, string(encoded)), nil
}

func publicAssistanceVerificationOutputDecoder(content string) (StepOutput, error) {
	var fields map[string]json.RawMessage
	if err := decodeStrictJSON(content, &fields); err != nil {
		return StepOutput{}, err
	}
	if len(fields) != 3 {
		return StepOutput{}, errors.New("public assistance verification output must contain exactly is_correct, corrected_public_assistance_status, and corrected_public_assistance_types")
	}
	var result struct {
		IsCorrect bool
		Status    string
		Types     []string
	}
	correct, correctFound := fields["is_correct"]
	status, statusFound := fields["corrected_public_assistance_status"]
	types, typesFound := fields["corrected_public_assistance_types"]
	if !correctFound || !statusFound || !typesFound {
		return StepOutput{}, errors.New("public assistance verification output is missing a required field")
	}
	if err := json.Unmarshal(correct, &result.IsCorrect); err != nil {
		return StepOutput{}, fmt.Errorf("decode public assistance verification verdict: %w", err)
	}
	if err := json.Unmarshal(status, &result.Status); err != nil {
		return StepOutput{}, fmt.Errorf("decode corrected public assistance status: %w", err)
	}
	if err := json.Unmarshal(types, &result.Types); err != nil {
		return StepOutput{}, fmt.Errorf("decode corrected public assistance types: %w", err)
	}
	encodedTypes, err := json.Marshal(result.Types)
	if err != nil {
		return StepOutput{}, fmt.Errorf("encode corrected public assistance types: %w", err)
	}
	return StepOutput{Values: map[string]string{
		"is_correct": fmt.Sprintf("%t", result.IsCorrect), "corrected_public_assistance_status": result.Status,
		"corrected_public_assistance_types": string(encodedTypes),
	}}, nil
}

func categoryVerificationInputGenerator(input StepInput) (StepInput, string, error) {
	category, found := canonicalCategoryLabels[input.Value("category")]
	if !found {
		return StepInput{}, "", errorOf(ErrorOutput, "invalid category verification input category")
	}
	requestInput := StepInput{Values: map[string]string{
		"title_de": input.Value("title_de"), "summary_de": input.Value("summary_de"), "category": category,
	}}
	encoded, err := json.Marshal(struct {
		TitleDE          string `json:"title_de"`
		SummaryDE        string `json:"summary_de"`
		ExistingCategory string `json:"existing_category"`
	}{requestInput.Value("title_de"), requestInput.Value("summary_de"), requestInput.Value("category")})
	if err != nil {
		return StepInput{}, "", errorOf(ErrorOutput, "encode category verification input: %v", err)
	}
	return requestInput, promptUserMessage(CategoryVerificationPromptVersion, string(encoded)), nil
}

func categoryVerificationOutputDecoder(content string) (StepOutput, error) {
	var fields map[string]json.RawMessage
	if err := decodeStrictJSON(content, &fields); err != nil {
		return StepOutput{}, err
	}
	if len(fields) != 2 {
		return StepOutput{}, errors.New("category verification output must contain exactly is_correct and corrected_category")
	}
	var result struct {
		IsCorrect         bool
		CorrectedCategory string
	}
	correct, correctFound := fields["is_correct"]
	category, categoryFound := fields["corrected_category"]
	if !correctFound || !categoryFound {
		return StepOutput{}, errors.New("category verification output is missing a required field")
	}
	if err := json.Unmarshal(correct, &result.IsCorrect); err != nil {
		return StepOutput{}, fmt.Errorf("decode category verification verdict: %w", err)
	}
	if err := json.Unmarshal(category, &result.CorrectedCategory); err != nil {
		return StepOutput{}, fmt.Errorf("decode corrected category: %w", err)
	}
	code, found := categoryCodeForGermanLabel(result.CorrectedCategory)
	if !found {
		return StepOutput{}, fmt.Errorf("decode corrected category: unknown German category %q", result.CorrectedCategory)
	}
	result.CorrectedCategory = code
	return StepOutput{Values: map[string]string{"is_correct": fmt.Sprintf("%t", result.IsCorrect), "corrected_category": result.CorrectedCategory}}, nil
}

func postProcessingOutputValues(output StepOutput) ([]store.PipelineValue, error) {
	if len(output.Values) == 0 {
		return nil, errorOf(ErrorOutput, "post-processing output values are empty")
	}
	kinds := make([]string, 0, len(output.Values))
	for kind := range output.Values {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	values := make([]store.PipelineValue, 0, len(kinds))
	for _, kind := range kinds {
		if strings.TrimSpace(output.Values[kind]) == "" {
			return nil, errorOf(ErrorOutput, "post-processing output %s is empty", kind)
		}
		values = append(values, store.PipelineValue{Kind: kind, Value: output.Values[kind]})
	}
	return values, nil
}

func requireOutputKinds(values []store.PipelineValue, expected []string) error {
	wanted := make(map[string]struct{}, len(expected))
	for _, kind := range expected {
		wanted[kind] = struct{}{}
	}
	if len(values) != len(wanted) {
		return fmt.Errorf("post-processing output has %d values, want %d", len(values), len(wanted))
	}
	for _, value := range values {
		if _, found := wanted[value.Kind]; !found {
			return fmt.Errorf("post-processing output kind %q is not declared", value.Kind)
		}
		delete(wanted, value.Kind)
	}
	if len(wanted) != 0 {
		return errors.New("post-processing output is missing a declared kind")
	}
	return nil
}

func categoryCodeForGermanLabel(label string) (string, bool) {
	label = strings.TrimSpace(label)
	for code, canonicalLabel := range canonicalCategoryLabels {
		if canonicalLabel == label {
			return code, true
		}
	}
	return "", false
}

func decodeStrictJSON(content string, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("structured model output contains trailing content")
	}
	return nil
}

func validateIncidentMetadata(input StepInput, output *StepOutput) error {
	if _, ok := canonicalCategoryLabels[output.Category]; !ok {
		return errorOf(ErrorOutput, "invalid incident category")
	}
	if (output.AreaName == nil) != (output.AreaType == nil) {
		return errorOf(ErrorOutput, "area_name and area_type must both be null or both be set")
	}
	if output.AreaName != nil {
		name := strings.Join(strings.Fields(*output.AreaName), " ")
		if name == "" || utf8.RuneCountInString(name) > 80 || output.AreaType == nil || !areaTypes[*output.AreaType] {
			return errorOf(ErrorOutput, "invalid incident area")
		}
		source := strings.ToLower(strings.Join(strings.Fields(input.Value("original_title")+" "+input.Value("incident_body")), " "))
		if !strings.Contains(source, strings.ToLower(name)) {
			return errorOf(ErrorOutput, "incident area is not explicitly present in the minimized source")
		}
		*output.AreaName = name
	}
	if !reportKinds[output.ReportKind] || !assistanceStatuses[output.PublicAssistanceStatus] {
		return errorOf(ErrorOutput, "invalid incident metadata enum")
	}
	if err := validateTemporalMetadata(input, output); err != nil {
		return err
	}
	normalized, err := normalizeAssistanceTypes(output.PublicAssistanceTypes)
	if err != nil {
		return errorOf(ErrorOutput, "%v", err)
	}
	output.PublicAssistanceTypes = normalized
	if (output.PublicAssistanceStatus == "requested") != (len(normalized) > 0) {
		return errorOf(ErrorOutput, "public assistance status and types are inconsistent")
	}
	return nil
}

func normalizeAssistanceTypes(values []string) ([]string, error) {
	seen := make(map[string]bool, len(values))
	normalized := make([]string, 0, len(values))
	for _, kind := range values {
		if !assistanceTypes[kind] {
			return nil, errors.New("invalid public assistance type")
		}
		if !seen[kind] {
			seen[kind] = true
			normalized = append(normalized, kind)
		}
	}
	sort.Strings(normalized)
	return normalized, nil
}

func validateTemporalMetadata(_ StepInput, output *StepOutput) error {
	for name, value := range map[string]*string{"event_start_date": output.EventStartDate} {
		if value != nil {
			normalized := strings.TrimSpace(*value)
			if _, err := time.Parse("2006-01-02", normalized); err != nil {
				return errorOf(ErrorOutput, "invalid %s", name)
			}
			*value = normalized
		}
	}
	for name, value := range map[string]*string{"event_start_time": output.EventStartTime} {
		if value != nil {
			normalized := strings.TrimSpace(*value)
			if _, err := time.Parse("15:04", normalized); err != nil {
				return errorOf(ErrorOutput, "invalid %s", name)
			}
			*value = normalized
		}
	}
	if output.EventDayPart != nil && !eventDayParts[*output.EventDayPart] {
		return errorOf(ErrorOutput, "invalid event day part")
	}
	if output.EventStartDate == nil {
		if output.EventStartTime != nil || output.EventDayPart != nil {
			return errorOf(ErrorOutput, "unknown event time contains temporal values")
		}
		return nil
	}
	if output.EventStartTime != nil {
		output.EventDayPart = nil
	}
	return nil
}

func validateGermanPresentation(_ StepInput, output *StepOutput) error {
	if err := normalizeLimitedField("title_de", &output.TitleDE, 90); err != nil {
		return err
	}
	if err := normalizeLimitedField("summary_de", &output.SummaryDE, 600); err != nil {
		return err
	}
	if output.PrivacyStatus != "safe" {
		return errorOf(ErrorPrivacy, "model marked German presentation for privacy review")
	}
	if err := normalizePrivacyFlags(&output.PrivacyFlags); err != nil {
		return err
	}
	return validatePublicText(output.TitleDE + "\n" + output.SummaryDE)
}

var (
	translationURLPattern          = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s)]+`)
	translationPlaceTokenPattern   = regexp.MustCompile(`__MB_PLACE_[0-9]{4}__`)
	translationNumberPattern       = regexp.MustCompile(`[0-9]+`)
	translationBoldPattern         = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	translationMarkdownLinkPattern = regexp.MustCompile(`\[([^\]\n]*)\]\((https://[^)\s]+)\)`)
)

func translationValidator(language string) func(StepInput, *StepOutput) error {
	return func(input StepInput, output *StepOutput) error {
		if err := validateTranslation(input, output); err != nil {
			return err
		}
		return validateTargetScript(language, output.Values["title"], output.Values["summary"])
	}
}

func validateTranslation(input StepInput, output *StepOutput) error {
	title, titleFound := output.Values["title"]
	summary, summaryFound := output.Values["summary"]
	if !titleFound || !summaryFound {
		return errorOf(ErrorOutput, "model output contains no translated presentation")
	}
	if err := normalizeLimitedField("translated title", &title, 90); err != nil {
		return err
	}
	if err := normalizeLimitedField("translated summary", &summary, 600); err != nil {
		return err
	}
	output.Values["title"], output.Values["summary"] = title, summary
	for _, field := range []struct {
		name, source, translated string
	}{
		{name: "title", source: input.Value("title_de"), translated: title},
		{name: "summary", source: input.Value("summary_de"), translated: summary},
	} {
		sourceURLs := translationURLPattern.FindAllString(field.source, -1)
		translatedURLs := translationURLPattern.FindAllString(field.translated, -1)
		if !sameStringMultiset(sourceURLs, translatedURLs) {
			return errorOf(ErrorPrivacy, "translated %s changed, added, or removed a web address", field.name)
		}
		for _, rawURL := range sourceURLs {
			parsed, err := url.Parse(rawURL)
			if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "munichbrief.de" || parsed.Port() != "" || parsed.User != nil {
				return errorOf(ErrorPrivacy, "translated %s contains a non-public or third-party web address", field.name)
			}
		}
		if !sameTranslationNumbers(field.source, field.translated) {
			return errorOf(ErrorOutput, "translated %s changed or omitted a number", field.name)
		}
		if !sameMarkdownStructure(field.source, field.translated) {
			return errorOf(ErrorOutput, "translated %s changed Markdown structure", field.name)
		}
	}
	publicText := translationURLPattern.ReplaceAllString(title+"\n"+summary, "")
	return validatePublicText(publicText)
}

func sameTranslationNumbers(source, translated string) bool {
	normalize := func(value string) []string {
		value = translationURLPattern.ReplaceAllString(value, "")
		value = translationPlaceTokenPattern.ReplaceAllString(value, "")
		numbers := translationNumberPattern.FindAllString(value, -1)
		for index, number := range numbers {
			number = strings.TrimLeft(number, "0")
			if number == "" {
				number = "0"
			}
			numbers[index] = number
		}
		sort.Strings(numbers)
		return numbers
	}
	sourceNumbers, translatedNumbers := normalize(source), normalize(translated)
	if strings.Join(sourceNumbers, "\x00") == strings.Join(translatedNumbers, "\x00") {
		return true
	}
	month := germanMonthNumber(source)
	if month == "" {
		return false
	}
	for index, number := range translatedNumbers {
		if number == month {
			translatedNumbers = append(translatedNumbers[:index], translatedNumbers[index+1:]...)
			break
		}
	}
	return strings.Join(sourceNumbers, "\x00") == strings.Join(translatedNumbers, "\x00")
}

func germanMonthNumber(value string) string {
	words := strings.FieldsFunc(strings.ToLower(value), func(character rune) bool {
		return !unicode.IsLetter(character)
	})
	for _, word := range words {
		for index, month := range []string{"januar", "februar", "märz", "april", "mai", "juni", "juli", "august", "september", "oktober", "november", "dezember"} {
			if word == month {
				return strconv.Itoa(index + 1)
			}
		}
	}
	return ""
}

func sameMarkdownStructure(source, translated string) bool {
	if strings.Count(source, "**") != strings.Count(translated, "**") {
		return false
	}
	protectedInBold := func(value string) []string {
		var tokens []string
		for _, match := range translationBoldPattern.FindAllStringSubmatch(value, -1) {
			tokens = append(tokens, translationPlaceTokenPattern.FindAllString(match[1], -1)...)
		}
		return tokens
	}
	if !sameStringMultiset(protectedInBold(source), protectedInBold(translated)) {
		return false
	}
	links := func(value string) []string {
		var signatures []string
		for _, match := range translationMarkdownLinkPattern.FindAllStringSubmatch(value, -1) {
			signatures = append(signatures, match[2]+"\x00"+strings.Join(translationPlaceTokenPattern.FindAllString(match[1], -1), "\x00"))
		}
		return signatures
	}
	return sameStringMultiset(links(source), links(translated))
}

func sameStringMultiset(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	left, right = append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	return strings.Join(left, "\x00") == strings.Join(right, "\x00")
}

func validateTargetScript(language, title, summary string) error {
	target := map[string]*unicode.RangeTable{
		"zh": unicode.Han, "hi": unicode.Devanagari, "el": unicode.Greek,
		"uk": unicode.Cyrillic, "ru": unicode.Cyrillic,
	}[language]
	if target == nil {
		return nil
	}
	for field, value := range map[string]string{"title": title, "summary": summary} {
		value = translationURLPattern.ReplaceAllString(value, "")
		value = translationPlaceTokenPattern.ReplaceAllString(value, "")
		letters, targetLetters := 0, 0
		for _, character := range value {
			if !unicode.IsLetter(character) {
				continue
			}
			letters++
			if unicode.Is(target, character) {
				targetLetters++
			}
		}
		if targetLetters < 2 || letters > 0 && targetLetters*100/letters < 60 {
			return errorOf(ErrorOutput, "translated %s is not predominantly in the target script", field)
		}
	}
	return nil
}

func validatePublicAssistanceVerification(input StepInput, output *StepOutput) error {
	isCorrect, verdictErr := strconv.ParseBool(output.Values["is_correct"])
	correctedStatus := strings.TrimSpace(output.Values["corrected_public_assistance_status"])
	if verdictErr != nil || !assistanceStatuses[correctedStatus] {
		return errorOf(ErrorOutput, "model output contains no valid public assistance verification")
	}
	var correctedTypes []string
	if err := json.Unmarshal([]byte(output.Values["corrected_public_assistance_types"]), &correctedTypes); err != nil {
		return errorOf(ErrorOutput, "decode corrected public assistance types: %v", err)
	}
	var err error
	correctedTypes, err = normalizeAssistanceTypes(correctedTypes)
	if err != nil || (correctedStatus == "requested") != (len(correctedTypes) > 0) {
		return errorOf(ErrorOutput, "corrected public assistance status and types are inconsistent")
	}
	originalStatus := strings.TrimSpace(input.Value("public_assistance_status"))
	if !assistanceStatuses[originalStatus] {
		return errorOf(ErrorOutput, "invalid input public assistance status")
	}
	var originalTypes []string
	if err := json.Unmarshal([]byte(input.Value("public_assistance_types")), &originalTypes); err != nil {
		return errorOf(ErrorOutput, "decode input public assistance types: %v", err)
	}
	originalTypes, err = normalizeAssistanceTypes(originalTypes)
	if err != nil || (originalStatus == "requested") != (len(originalTypes) > 0) {
		return errorOf(ErrorOutput, "input public assistance status and types are inconsistent")
	}
	unchanged := correctedStatus == originalStatus && strings.Join(correctedTypes, "\x00") == strings.Join(originalTypes, "\x00")
	if isCorrect != unchanged {
		return errorOf(ErrorOutput, "public assistance verdict and corrected values are inconsistent")
	}
	encodedTypes, err := json.Marshal(correctedTypes)
	if err != nil {
		return errorOf(ErrorOutput, "encode corrected public assistance types: %v", err)
	}
	output.Values["corrected_public_assistance_status"] = correctedStatus
	output.Values["corrected_public_assistance_types"] = string(encodedTypes)
	return nil
}

func validateCategoryVerification(input StepInput, output *StepOutput) error {
	corrected, categoryFound := output.Values["corrected_category"]
	isCorrect, verdictErr := strconv.ParseBool(output.Values["is_correct"])
	if !categoryFound || verdictErr != nil {
		return errorOf(ErrorOutput, "model output contains no category verification")
	}
	original, ok := categoryCodeForGermanLabel(input.Value("category"))
	corrected = strings.TrimSpace(corrected)
	if !ok {
		return errorOf(ErrorOutput, "invalid input category")
	}
	if _, ok := canonicalCategoryLabels[corrected]; !ok {
		return errorOf(ErrorOutput, "invalid corrected category")
	}
	if isCorrect != (corrected == original) {
		return errorOf(ErrorOutput, "category verdict and corrected category are inconsistent")
	}
	output.Values["corrected_category"] = corrected
	return nil
}

func normalizeLimitedField(name string, value *string, limit int) error {
	if !utf8.ValidString(*value) {
		return errorOf(ErrorOutput, "model output %s is not valid UTF-8", name)
	}
	for _, character := range *value {
		if unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t' {
			return errorOf(ErrorOutput, "model output %s contains a control character", name)
		}
	}
	*value = norm.NFC.String(strings.Join(strings.Fields(*value), " "))
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

func incidentMetadataValues(output StepOutput) ([]store.PipelineValue, error) {
	types, err := json.Marshal(output.PublicAssistanceTypes)
	if err != nil {
		return nil, fmt.Errorf("encode public assistance types: %w", err)
	}
	values := []store.PipelineValue{
		{Kind: "category", Value: output.Category}, {Kind: "report_kind", Value: output.ReportKind}, {Kind: "public_assistance_status", Value: output.PublicAssistanceStatus},
		{Kind: "public_assistance_types", Value: string(types)},
	}
	for kind, value := range map[string]*string{
		"area_name": output.AreaName, "area_type": output.AreaType, "event_start_date": output.EventStartDate,
		"event_start_time": output.EventStartTime,
		"event_day_part":   output.EventDayPart,
	} {
		if value != nil {
			values = append(values, store.PipelineValue{Kind: kind, Value: *value})
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Kind < values[j].Kind })
	return values, nil
}

func germanPresentationValues(output StepOutput) ([]store.PipelineValue, error) {
	flags, err := json.Marshal(output.PrivacyFlags)
	if err != nil {
		return nil, fmt.Errorf("encode privacy flags: %w", err)
	}
	return []store.PipelineValue{{Kind: "title_de", Value: output.TitleDE}, {Kind: "summary_de", Value: output.SummaryDE}, {Kind: "privacy_status", Value: output.PrivacyStatus}, {Kind: "privacy_flags", Value: string(flags)}}, nil
}

func germanWeekday(day time.Weekday) string {
	return []string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"}[day]
}
