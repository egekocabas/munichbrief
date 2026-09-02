package processing

import (
	"context"
	"testing"

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

func TestProtectedGeneratorOwnsProtectedPlaceMarkdown(t *testing.T) {
	inner := &staticGenerator{output: StepOutput{Values: map[string]string{
		"title":   "Between __MB_PLACE_0002__ and __MB_PLACE_0001__",
		"summary": "See __MB_PLACE_0003__",
	}}}
	protector := staticProtector{ready: true, value: gazetteer.Protected{
		Title:   "Zwischen **__MB_PLACE_0001__** und __MB_PLACE_0002__",
		Summary: "Siehe [__MB_PLACE_0003__](https://munichbrief.de/de/incidents/717)",
		Replacements: []gazetteer.Replacement{
			{Token: "__MB_PLACE_0001__", Original: "Hauptbahnhof", Field: "title"},
			{Token: "__MB_PLACE_0002__", Original: "Ostbahnhof", Field: "title"},
			{Token: "__MB_PLACE_0003__", Original: "Ramersdorf-Perlach", Field: "summary"},
		},
	}}
	generator := &protectedGenerator{inner: inner, protector: protector}
	output, _, err := generator.GenerateStep(context.Background(), TranslationByLanguageMust(t, "en").Step, StepInput{Values: map[string]string{
		"title_de":   "Zwischen **Hauptbahnhof** und Ostbahnhof",
		"summary_de": "Siehe [Ramersdorf-Perlach](https://munichbrief.de/de/incidents/717)",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if inner.input.Value("title_de") != "Zwischen __MB_PLACE_0001__ und __MB_PLACE_0002__" || inner.input.Value("summary_de") != "Siehe __MB_PLACE_0003__" {
		t.Fatalf("model still received protected Markdown: %#v", inner.input.Values)
	}
	if output.Values["title"] != "Between Ostbahnhof and **Hauptbahnhof**" || output.Values["summary"] != "See [Ramersdorf-Perlach](https://munichbrief.de/de/incidents/717)" {
		t.Fatalf("programmatic Markdown restoration = %#v", output.Values)
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

func TestTranslationValidationAllowsOnlySameFieldSourceURLs(t *testing.T) {
	input := StepInput{Values: map[string]string{
		"title_de": "Titel", "summary_de": "[Schwabing](https://munichbrief.de/en/incidents/378?page=2)",
	}}
	valid := StepOutput{Values: map[string]string{"title": "Title", "summary": "[Schwabing](https://munichbrief.de/en/incidents/378?page=2)"}}
	if err := validateTranslation(input, &valid); err != nil {
		t.Fatalf("unchanged source URL rejected: %v", err)
	}
	reorderedInput := StepInput{Values: map[string]string{"title_de": "Titel", "summary_de": "[A](https://munichbrief.de/de/incidents/1) und [B](https://munichbrief.de/de/incidents/2)"}}
	reorderedOutput := StepOutput{Values: map[string]string{"title": "Title", "summary": "[B](https://munichbrief.de/de/incidents/2) and [A](https://munichbrief.de/de/incidents/1)"}}
	if err := validateTranslation(reorderedInput, &reorderedOutput); err != nil {
		t.Fatalf("safe target-language link reordering rejected: %v", err)
	}
	for _, output := range []StepOutput{
		{Values: map[string]string{"title": "https://munichbrief.de/en/incidents/378?page=2", "summary": "Schwabing"}},
		{Values: map[string]string{"title": "Title", "summary": "https://example.com/added"}},
		{Values: map[string]string{"title": "Title", "summary": "Schwabing"}},
	} {
		if err := validateTranslation(input, &output); err == nil || KindOf(err) != ErrorPrivacy {
			t.Fatalf("invalid translated URL accepted: %#v, err=%v", output.Values, err)
		}
	}
	thirdPartyInput := StepInput{Values: map[string]string{"title_de": "Titel", "summary_de": "https://example.com/source"}}
	thirdPartyOutput := StepOutput{Values: map[string]string{"title": "Title", "summary": "https://example.com/source"}}
	if err := validateTranslation(thirdPartyInput, &thirdPartyOutput); err == nil || KindOf(err) != ErrorPrivacy {
		t.Fatalf("third-party source URL accepted: %v", err)
	}
}

func TestTranslationValidationPreservesNumbersAndMarkdownStructure(t *testing.T) {
	input := StepInput{Values: map[string]string{
		"title_de": "Einsatz 52", "summary_de": "Zwischen **__MB_PLACE_0001__** und __MB_PLACE_0002__: 04:40 Uhr. [__MB_PLACE_0003__](https://munichbrief.de/de/incidents/717)",
	}}
	valid := StepOutput{Values: map[string]string{
		"title": "Operation 52", "summary": "Between **__MB_PLACE_0001__** and __MB_PLACE_0002__ at 04:40. [__MB_PLACE_0003__](https://munichbrief.de/de/incidents/717)",
	}}
	if err := validateTranslation(input, &valid); err != nil {
		t.Fatalf("valid structure rejected: %v", err)
	}
	invalid := []StepOutput{
		{Values: map[string]string{"title": "Operation 53", "summary": valid.Values["summary"]}},
		{Values: map[string]string{"title": "Operation 52", "summary": "Between **__MB_PLACE_0001__** and **__MB_PLACE_0002__** at 04:40. [__MB_PLACE_0003__](https://munichbrief.de/de/incidents/717)"}},
		{Values: map[string]string{"title": "Operation 52", "summary": "Between **__MB_PLACE_0001__** and __MB_PLACE_0002__ at 04:40. __MB_PLACE_0003__ https://munichbrief.de/de/incidents/717"}},
	}
	for _, output := range invalid {
		if err := validateTranslation(input, &output); err == nil {
			t.Fatalf("invalid structure accepted: %#v", output.Values)
		}
	}
}

func TestTranslationValidationAcceptsEquivalentLocalizedNumberFormatting(t *testing.T) {
	input := StepInput{Values: map[string]string{
		"title_de": "Titel", "summary_de": "Am 30. August 2026 um 04:40 Uhr wurde die 110 gerufen.",
	}}
	for _, summary := range []string{
		"On 30 August 2026 at 4:40, emergency number 110 was called.",
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
