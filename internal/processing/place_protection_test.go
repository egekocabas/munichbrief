package processing

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egekocabas/munichbrief/internal/gazetteer"
)

type staticProtector struct {
	ready bool
	value gazetteer.Protected
	err   error
}

func (p staticProtector) Ready() bool { return p.ready }
func (p staticProtector) Protect(string, string) (gazetteer.Protected, error) {
	return p.value, p.err
}

type staticGenerator struct {
	input  StepInput
	output StepOutput
}

func (g *staticGenerator) ModelIdentity() string { return "test-model" }
func (g *staticGenerator) GenerateStep(_ context.Context, _ StepDefinition, input StepInput) (StepOutput, string, error) {
	g.input = input
	return g.output, "test-model", nil
}

func TestProtectedGeneratorMasksAndRestoresTranslationValues(t *testing.T) {
	inner := &staticGenerator{output: StepOutput{Values: map[string]string{"title": "At __MB_PLACE_0001__", "summary": "Near __MB_PLACE_0002__"}}}
	protector := staticProtector{ready: true, value: gazetteer.Protected{
		Title: "An __MB_PLACE_0001__", Summary: "Bei __MB_PLACE_0002__",
		Replacements: []gazetteer.Replacement{{Token: "__MB_PLACE_0001__", Original: "Ingolstädter Straße", Field: "title"}, {Token: "__MB_PLACE_0002__", Original: "Schwabing", Field: "summary"}},
	}}
	generator := &protectedGenerator{inner: inner, protector: protector}
	output, _, err := generator.GenerateStep(context.Background(), TranslationByLanguageMust(t, "en").Step, StepInput{Values: map[string]string{"title_de": "An der Ingolstädter Straße", "summary_de": "Bei Schwabing"}})
	if err != nil {
		t.Fatal(err)
	}
	if inner.input.Value("title_de") != protector.value.Title || output.Values["title"] != "At Ingolstädter Straße" || output.Values["summary"] != "Near Schwabing" {
		t.Fatalf("protected input=%#v output=%#v", inner.input, output)
	}
}

func TestProtectedGeneratorRejectsDamagedTokens(t *testing.T) {
	inner := &staticGenerator{output: StepOutput{Values: map[string]string{"title": "translated", "summary": "translated"}}}
	protector := staticProtector{ready: true, value: gazetteer.Protected{Title: "__MB_PLACE_0001__", Replacements: []gazetteer.Replacement{{Token: "__MB_PLACE_0001__", Original: "Schwabing", Field: "title"}}}}
	generator := &protectedGenerator{inner: inner, protector: protector}
	_, _, err := generator.GenerateStep(context.Background(), TranslationByLanguageMust(t, "en").Step, StepInput{Values: map[string]string{"title_de": "Schwabing", "summary_de": "Text"}})
	if err == nil || KindOf(err) != ErrorOutput {
		t.Fatalf("damaged token error = %v", err)
	}
}

func TestProtectedGeneratorRestoresReorderedProtectedPlaces(t *testing.T) {
	inner := &staticGenerator{output: StepOutput{Values: map[string]string{
		"title":   "Between __MB_PLACE_0002__ and __MB_PLACE_0001__",
		"summary": "Near __MB_PLACE_0003__",
	}}}
	protector := staticProtector{ready: true, value: gazetteer.Protected{
		Title:   "Zwischen __MB_PLACE_0001__ und __MB_PLACE_0002__",
		Summary: "Bei __MB_PLACE_0003__",
		Replacements: []gazetteer.Replacement{
			{Token: "__MB_PLACE_0001__", Original: "Hauptbahnhof", Field: "title"},
			{Token: "__MB_PLACE_0002__", Original: "Ostbahnhof", Field: "title"},
			{Token: "__MB_PLACE_0003__", Original: "Ramersdorf-Perlach", Field: "summary"},
		},
	}}
	generator := &protectedGenerator{inner: inner, protector: protector}
	output, _, err := generator.GenerateStep(context.Background(), TranslationByLanguageMust(t, "en").Step, StepInput{Values: map[string]string{
		"title_de":   "Zwischen Hauptbahnhof und Ostbahnhof",
		"summary_de": "Bei Ramersdorf-Perlach",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if inner.input.Value("title_de") != protector.value.Title || inner.input.Value("summary_de") != protector.value.Summary {
		t.Fatalf("model did not receive protected places: %#v", inner.input.Values)
	}
	if output.Values["title"] != "Between Ostbahnhof and Hauptbahnhof" || output.Values["summary"] != "Near Ramersdorf-Perlach" {
		t.Fatalf("programmatic place restoration = %#v", output.Values)
	}
}

func TestProtectedGeneratorRestoresTypedPlaces(t *testing.T) {
	inner := &staticGenerator{output: StepOutput{Values: map[string]string{
		"title":   "At __MB_STREET_0001__",
		"summary": "See __MB_DISTRICT_0002__",
	}}}
	protector := staticProtector{ready: true, value: gazetteer.Protected{
		Title:   "An der __MB_STREET_0001__",
		Summary: "Bei __MB_DISTRICT_0002__",
		Replacements: []gazetteer.Replacement{
			{Token: "__MB_STREET_0001__", Original: "Ganghoferstraße", Field: "title", Kind: gazetteer.KindStreet},
			{Token: "__MB_DISTRICT_0002__", Original: "Ramersdorf-Perlach", Field: "summary", Kind: gazetteer.KindDistrict},
		},
	}}
	generator := &protectedGenerator{inner: inner, protector: protector}
	output, _, err := generator.GenerateStep(context.Background(), TranslationByLanguageMust(t, "en").Step, StepInput{Values: map[string]string{
		"title_de":   "An der Ganghoferstraße",
		"summary_de": "Bei Ramersdorf-Perlach",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if inner.input.Value("title_de") != protector.value.Title || inner.input.Value("summary_de") != protector.value.Summary {
		t.Fatalf("model did not receive typed places: %#v", inner.input.Values)
	}
	if output.Values["title"] != "At Ganghoferstraße" || output.Values["summary"] != "See Ramersdorf-Perlach" {
		t.Fatalf("typed programmatic place restoration = %#v", output.Values)
	}
}

func TestTranslationProcessorAvailabilityTracksGazetteer(t *testing.T) {
	registry := DefaultPostProcessorRegistry(staticProtector{ready: false})
	if registry.Ready(TranslationModelStep) {
		t.Fatal("translation readiness gate ignored unavailable gazetteer")
	}
	if !registry.Ready(CategoryVerificationStep) {
		t.Fatal("gazetteer readiness incorrectly paused another processor")
	}
}

func TestTranslationProcessorPausesForTypedNilGazetteer(t *testing.T) {
	var manager *gazetteer.Manager
	if DefaultPostProcessorRegistry(manager).Ready(TranslationModelStep) {
		t.Fatal("translation processor treated a disabled gazetteer as ready")
	}
}

func TestTranslationValidationRejectsURLsAndMarkdown(t *testing.T) {
	validInput := StepInput{Values: map[string]string{"title_de": "Titel", "summary_de": "Zusammenfassung"}}
	for _, output := range []StepOutput{
		{Values: map[string]string{"title": "https://munichbrief.de/en/incidents/378?page=2", "summary": "Summary"}},
		{Values: map[string]string{"title": "Title", "summary": "See https://example.com/added"}},
		{Values: map[string]string{"title": "**Title**", "summary": "Summary"}},
		{Values: map[string]string{"title": "Title", "summary": "[Schwabing](https://munichbrief.de/en/incidents/378)"}},
		{Values: map[string]string{"title": "Title", "summary": "`Summary`"}},
	} {
		if err := validateTranslation(validInput, &output); err == nil || KindOf(err) != ErrorOutput {
			t.Fatalf("non-plain translation accepted: %#v, err=%v", output.Values, err)
		}
	}
	for _, input := range []StepInput{
		{Values: map[string]string{"title_de": "Titel", "summary_de": "https://example.com/source"}},
		{Values: map[string]string{"title_de": "**Titel**", "summary_de": "Zusammenfassung"}},
	} {
		output := StepOutput{Values: map[string]string{"title": "Title", "summary": "Summary"}}
		if err := validateTranslation(input, &output); err == nil || KindOf(err) != ErrorOutput {
			t.Fatalf("non-plain German input accepted: %#v, err=%v", input.Values, err)
		}
	}
}

func TestTranslationValidationPreservesNumbersWithPlaceholders(t *testing.T) {
	input := StepInput{Values: map[string]string{
		"title_de": "Einsatz 52", "summary_de": "Zwischen __MB_PLACE_0001__ und __MB_PLACE_0002__: 04:40 Uhr.",
	}}
	valid := StepOutput{Values: map[string]string{
		"title": "Operation 52", "summary": "Between __MB_PLACE_0001__ and __MB_PLACE_0002__ at 04:40.",
	}}
	if err := validateTranslation(input, &valid); err != nil {
		t.Fatalf("valid structure rejected: %v", err)
	}
	invalid := []StepOutput{
		{Values: map[string]string{"title": "Operation 53", "summary": valid.Values["summary"]}},
		{Values: map[string]string{"title": "Operation 52", "summary": "Between __MB_PLACE_0001__ and __MB_PLACE_0002__ at 05:40."}},
		{Values: map[string]string{"title": "Operation 52", "summary": "Between __MB_PLACE_0001__ and __MB_PLACE_0002__."}},
	}
	for _, output := range invalid {
		if err := validateTranslation(input, &output); err == nil {
			t.Fatalf("invalid structure accepted: %#v", output.Values)
		}
	}
}

func TestTranslationValidationDefersProtectedFieldLengthUntilRestoration(t *testing.T) {
	protectedInput := StepInput{Values: map[string]string{
		"title_de":   "Polizeieinsatz an der __MB_STREET_0001__ verzögert __MB_COMMUTER_TRAIN_0002__",
		"summary_de": "Zusammenfassung",
	}}
	protectedOutput := StepOutput{Values: map[string]string{
		"title":   "La intervención policial en la __MB_STREET_0001__ ha retrasado al __MB_COMMUTER_TRAIN_0002__.",
		"summary": "Resumen",
	}}
	if utf8.RuneCountInString(protectedOutput.Values["title"]) <= 90 {
		t.Fatal("regression fixture does not exceed the protected title limit")
	}
	if err := validateTranslation(protectedInput, &protectedOutput); err != nil {
		t.Fatalf("protected title length was not deferred: %v", err)
	}

	restoredInput := StepInput{Values: map[string]string{
		"title_de":   "Polizeieinsatz an der Ingolstädter Straße verzögert S-Bahnen",
		"summary_de": "Zusammenfassung",
	}}
	restoredOutput := StepOutput{Values: map[string]string{
		"title":   "La intervención policial en la Ingolstädter Straße ha retrasado al S-Bahnen.",
		"summary": "Resumen",
	}}
	if err := validateTranslation(restoredInput, &restoredOutput); err != nil {
		t.Fatalf("restored reader-visible title rejected: %v", err)
	}
	restoredOutput.Values["title"] = strings.Repeat("a", 91)
	if err := validateTranslation(restoredInput, &restoredOutput); err == nil {
		t.Fatal("overlong restored reader-visible title was accepted")
	}
}

func TestTranslationSchemaDefersFieldLimitsUntilPlaceRestoration(t *testing.T) {
	translation := TranslationByLanguageMust(t, "el")
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(translation.Step.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"title_el", "summary_el"} {
		property := schema.Properties[field]
		if property["minLength"] != float64(1) {
			t.Fatalf("%s minimum length = %#v", field, property["minLength"])
		}
		if _, found := property["maxLength"]; found {
			t.Fatalf("%s applies its reader limit before restoration: %#v", field, property)
		}
	}
}

func TestTranslationValidationAcceptsEquivalentLocalizedNumberFormatting(t *testing.T) {
	input := StepInput{Values: map[string]string{
		"title_de": "Titel", "summary_de": "Am 30. August 2026 um 04:40 Uhr wurde die 110 gerufen.",
	}}
	for _, summary := range []string{
		"On 30 August 2026 at 4:40, emergency number 110 was called.",
		"On 30 August 2026 at 4:40 a.m., emergency number 110 was called.",
		"2026年8月30日4:40，拨打了110。",
	} {
		output := StepOutput{Values: map[string]string{"title": "Title", "summary": summary}}
		if err := validateTranslation(input, &output); err != nil {
			t.Fatalf("equivalent localized numbers rejected for %q: %v", summary, err)
		}
	}
	changed := StepOutput{Values: map[string]string{"title": "Title", "summary": "On 30 August 2026 at 4:40, emergency number 112 was called."}}
	if err := validateTranslation(input, &changed); err == nil {
		t.Fatal("changed emergency number was accepted")
	}
	nonMonth := StepInput{Values: map[string]string{"title_de": "Titel", "summary_de": "Einsatz an der Augustinerstraße"}}
	addedMonthNumber := StepOutput{Values: map[string]string{"title": "Title", "summary": "Incident at Augustinerstraße 8"}}
	if err := validateTranslation(nonMonth, &addedMonthNumber); err == nil {
		t.Fatal("month substring authorized an added number")
	}

	twelveHourInput := StepInput{Values: map[string]string{
		"title_de": "Titel", "summary_de": "Am Freitag gegen 20:00 Uhr begann der Einsatz.",
	}}
	validTwelveHour := StepOutput{Values: map[string]string{
		"title": "Title", "summary": "The operation began on Friday at around 8 p.m.",
	}}
	if err := validateTranslation(twelveHourInput, &validTwelveHour); err != nil {
		t.Fatalf("equivalent 12-hour time rejected: %v", err)
	}
	if !sameTranslationNumbers(
		"Am Freitagabend gegen 20:00 Uhr in __MB_MUNICIPALITY_0001__ kam es zu einem Streit.",
		"On Friday evening around 8 p.m. in __MB_MUNICIPALITY_0001__, a dispute broke out.",
	) {
		t.Fatal("recorded English fixture D time conversion was rejected")
	}
	for _, summary := range []string{
		"The operation began on Friday at around 8 a.m.",
		"The operation began on Friday at around 9 p.m.",
		"The operation began on Friday at around 8 p.m. and ended at 9 p.m.",
	} {
		output := StepOutput{Values: map[string]string{"title": "Title", "summary": summary}}
		if err := validateTranslation(twelveHourInput, &output); err == nil {
			t.Fatalf("changed or added 12-hour time accepted for %q", summary)
		}
	}
}

func TestTranslationValidationAcceptsEquivalentLocalized24HourNotation(t *testing.T) {
	input := StepInput{Values: map[string]string{
		"title_de": "Titel", "summary_de": "Am Freitag gegen 20:00 Uhr begann der Einsatz.",
	}}
	for _, summary := range []string{
		"L’intervention a commencé vendredi vers 20 h.",
		"L’intervention a commencé vendredi vers 20h.",
		"L’intervention a commencé vendredi vers 20 heures.",
	} {
		output := StepOutput{Values: map[string]string{"title": "Titre", "summary": summary}}
		if err := validateTranslation(input, &output); err != nil {
			t.Fatalf("equivalent localized 24-hour time rejected for %q: %v", summary, err)
		}
	}
	minuteInput := StepInput{Values: map[string]string{
		"title_de": "Titel", "summary_de": "Am Freitag gegen 20:30 Uhr begann der Einsatz.",
	}}
	minuteOutput := StepOutput{Values: map[string]string{
		"title": "Titre", "summary": "L’intervention a commencé vendredi vers 20 h 30.",
	}}
	if err := validateTranslation(minuteInput, &minuteOutput); err != nil {
		t.Fatalf("equivalent localized 24-hour time with minutes rejected: %v", err)
	}
	for _, summary := range []string{
		"L’intervention a commencé vendredi vers 20 h 30.",
		"L’intervention a commencé vendredi vers 21 h.",
		"L’intervention a commencé vendredi vers 20 h et a pris fin à 21 h.",
	} {
		output := StepOutput{Values: map[string]string{"title": "Titre", "summary": summary}}
		if err := validateTranslation(input, &output); err == nil {
			t.Fatalf("changed or added localized 24-hour time accepted for %q", summary)
		}
	}
}

func TestTranslationValidationAcceptsEquivalentChineseTimeNotation(t *testing.T) {
	input := StepInput{Values: map[string]string{
		"title_de": "Titel", "summary_de": "Am Freitagabend gegen 20:00 Uhr begann der Einsatz.",
	}}
	for _, summary := range []string{
		"周五晚上20点左右，警方开始行动。",
		"周五晚上大约20点，警方开始行动。",
	} {
		output := StepOutput{Values: map[string]string{"title": "标题", "summary": summary}}
		if err := validateTranslation(input, &output); err != nil {
			t.Fatalf("equivalent Chinese hour notation rejected for %q: %v", summary, err)
		}
	}
	minuteInput := StepInput{Values: map[string]string{
		"title_de": "Titel", "summary_de": "Am Freitag gegen 20:30 Uhr begann der Einsatz.",
	}}
	minuteOutput := StepOutput{Values: map[string]string{
		"title": "标题", "summary": "周五晚上20点30分左右，警方开始行动。",
	}}
	if err := validateTranslation(minuteInput, &minuteOutput); err != nil {
		t.Fatalf("equivalent Chinese time with minutes rejected: %v", err)
	}
	for _, summary := range []string{
		"周五晚上20点30分左右，警方开始行动。",
		"周五晚上21点左右，警方开始行动。",
		"周五晚上20点左右开始，21点结束。",
	} {
		output := StepOutput{Values: map[string]string{"title": "标题", "summary": summary}}
		if err := validateTranslation(input, &output); err == nil {
			t.Fatalf("changed or added Chinese time accepted for %q", summary)
		}
	}
}

func TestTranslationValidationRejectsControlCharactersAndWrongScript(t *testing.T) {
	input := StepInput{Values: map[string]string{"title_de": "Titel", "summary_de": "Zusammenfassung"}}
	control := StepOutput{Values: map[string]string{"title": "Title", "summary": "Bad\u009ftext"}}
	if err := validateTranslation(input, &control); err == nil {
		t.Fatal("control character was accepted")
	}
	russian := translationValidator("ru")
	for _, output := range []StepOutput{
		{Values: map[string]string{"title": "Russian title", "summary": "据称有人将一件物品遗留在地铁站。"}},
		{Values: map[string]string{"title": "Случай", "summary": "По данным полиции, 据称有人将一件物品遗留在地铁站。"}},
	} {
		if err := russian(input, &output); err == nil {
			t.Fatalf("wrong-script output accepted: %#v", output.Values)
		}
	}
	validRussian := StepOutput{Values: map[string]string{"title": "Неясный случай", "summary": "По данным полиции, расследование продолжается."}}
	if err := russian(input, &validRussian); err != nil {
		t.Fatalf("valid Russian rejected: %v", err)
	}
}

func TranslationByLanguageMust(t *testing.T, language string) TranslationDefinition {
	t.Helper()
	translation, found := TranslationByLanguage(language)
	if !found {
		t.Fatalf("translation %s not registered", language)
	}
	return translation
}
