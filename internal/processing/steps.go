package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egekocabas/munichbrief/internal/store"
)

const (
	PipelineVersion        = store.PipelineVersion
	IncidentMetadataStep   = "incident_metadata"
	GermanPresentationStep = "german_presentation"
	// GermanAnalysisStep is retained as a source-compatible alias for callers
	// migrating to the metadata-first pipeline.
	GermanAnalysisStep     = GermanPresentationStep
	EnglishTranslationStep = "english_translation"
)

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
	TitleDE                string   `json:"title_de,omitempty"`
	SummaryDE              string   `json:"summary_de,omitempty"`
	Category               string   `json:"category,omitempty"`
	AreaName               *string  `json:"area_name"`
	AreaType               *string  `json:"area_type"`
	EventStartDate         *string  `json:"event_start_date"`
	EventStartTime         *string  `json:"event_start_time"`
	EventDayPart           *string  `json:"event_day_part"`
	ReportKind             string   `json:"report_kind,omitempty"`
	PublicAssistanceStatus string   `json:"public_assistance_status,omitempty"`
	PublicAssistanceTypes  []string `json:"public_assistance_types,omitempty"`
	TitleEN                string   `json:"title_en,omitempty"`
	SummaryEN              string   `json:"summary_en,omitempty"`
	PrivacyStatus          string   `json:"privacy_status,omitempty"`
	PrivacyFlags           []string `json:"privacy_flags,omitempty"`
}

type StepGenerator interface {
	GenerateStep(context.Context, StepDefinition, StepInput) (StepOutput, string, error)
	ModelIdentity() string
}

type StepGeneratorProvider interface {
	StepGenerator(model string) (StepGenerator, error)
}

var categoryLabels = map[string][2]string{
	"traffic": {"Verkehr", "Traffic"}, "theft_burglary": {"Diebstahl und Einbruch", "Theft and burglary"},
	"robbery_extortion": {"Raub und Erpressung", "Robbery and extortion"}, "violence": {"Gewalt", "Violence"},
	"sexual_offense": {"Sexualdelikte", "Sexual offences"}, "fraud_cyber": {"Betrug und Cyberkriminalität", "Fraud and cybercrime"},
	"drugs": {"Rauschgift", "Drugs"}, "fire_hazard": {"Brand und Gefahrenlage", "Fire and hazards"},
	"property_damage": {"Sachbeschädigung", "Property damage"}, "missing_wanted": {"Vermisstensuche und Fahndung", "Missing and wanted persons"},
	"police_operation": {"Polizeieinsatz", "Police operation"}, "other": {"Sonstiges", "Other"},
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

var englishTranslationSchema = json.RawMessage(`{
  "type":"object","properties":{"title_en":{"type":"string","minLength":1,"maxLength":90},"summary_en":{"type":"string","minLength":1,"maxLength":600}},
  "required":["title_en","summary_en"],"additionalProperties":false
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
	{Key: EnglishTranslationStep, DisplayName: "English translation", Order: 2, PromptVersion: EnglishTranslationPromptVersion,
		InputKinds: []string{"title_de", "summary_de"}, OutputKinds: []string{"title_en", "summary_en"},
		SystemPrompt: mustPromptByVersion(EnglishTranslationPromptVersion).SystemPrompt, Schema: englishTranslationSchema,
		Generator: generateEnglishTranslationInput, Validator: func(_ StepInput, output *StepOutput) error { return validateEnglishTranslation(output) }, OutputValues: englishTranslationValues},
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

func generateEnglishTranslationInput(input StepInput) (StepInput, string, error) {
	encoded, err := json.Marshal(struct {
		TitleDE   string `json:"title_de"`
		SummaryDE string `json:"summary_de"`
	}{input.Value("title_de"), input.Value("summary_de")})
	if err != nil {
		return StepInput{}, "", errorOf(ErrorOutput, "encode translation input: %v", err)
	}
	return input, promptUserMessage(EnglishTranslationPromptVersion, string(encoded)), nil
}

func validateIncidentMetadata(input StepInput, output *StepOutput) error {
	if _, ok := categoryLabels[output.Category]; !ok {
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
	seen := map[string]bool{}
	normalized := make([]string, 0, len(output.PublicAssistanceTypes))
	for _, kind := range output.PublicAssistanceTypes {
		if !assistanceTypes[kind] {
			return errorOf(ErrorOutput, "invalid public assistance type")
		}
		if !seen[kind] {
			seen[kind] = true
			normalized = append(normalized, kind)
		}
	}
	sort.Strings(normalized)
	output.PublicAssistanceTypes = normalized
	if (output.PublicAssistanceStatus == "requested") != (len(normalized) > 0) {
		return errorOf(ErrorOutput, "public assistance status and types are inconsistent")
	}
	return nil
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

func germanWeekday(day time.Weekday) string {
	return []string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"}[day]
}
