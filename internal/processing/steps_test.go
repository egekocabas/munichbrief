package processing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	langregistry "github.com/egekocabas/munichbrief/internal/languages"
	"github.com/egekocabas/munichbrief/internal/store"
	"golang.org/x/text/language"
)

func TestRegisteredPipelineStepsAreStableAndOrdered(t *testing.T) {
	steps := RegisteredSteps()
	if len(steps) != 2 ||
		steps[0].Key != IncidentMetadataStep || steps[0].PromptVersion != IncidentMetadataPromptVersion ||
		steps[1].Key != GermanPresentationStep || steps[1].PromptVersion != GermanPresentationPromptVersion {
		t.Fatalf("registered step identities = %v", StepKeys())
	}
	translations := RegisteredTranslations()
	wantTranslations := []struct{ language, prompt string }{
		{EnglishLanguage, EnglishTranslationPromptVersion},
		{"tr", TurkishTranslationPromptVersion},
		{"hr", CroatianTranslationPromptVersion},
		{"it", ItalianTranslationPromptVersion},
		{"uk", UkrainianTranslationPromptVersion},
		{"bs", BosnianTranslationPromptVersion},
		{"zh", ChineseTranslationPromptVersion},
		{"hi", HindiTranslationPromptVersion},
		{"es", SpanishTranslationPromptVersion},
		{"fr", FrenchTranslationPromptVersion},
	}
	if len(translations) != len(wantTranslations) {
		t.Fatalf("registered translations = %#v", translations)
	}
	for index, want := range wantTranslations {
		if translations[index].Language != want.language || translations[index].PromptVersion != want.prompt || translations[index].Step.OutputDecoder == nil {
			t.Errorf("registered translation %d = %#v, want %s/%s", index, translations[index], want.language, want.prompt)
		}
	}
	if PipelineVersion != "incident-pipeline-v2" {
		t.Fatalf("pipeline version = %q", PipelineVersion)
	}
	for _, expected := range []string{"Veröffentlichungszeit", "Wochentag", "relative Angaben", "öffentliche Mithilfe"} {
		if !strings.Contains(steps[0].SystemPrompt, expected) {
			t.Errorf("metadata prompt does not contain %q", expected)
		}
	}
	for _, forbidden := range []string{"Return only", "Write a", "Create a", "German title", "English summary"} {
		if strings.Contains(steps[1].SystemPrompt, forbidden) {
			t.Errorf("German presentation prompt contains English instruction %q", forbidden)
		}
	}
}

func TestGeneratedTextIsNormalizedToUnicodeNFCBeforeValidation(t *testing.T) {
	value := "  Ukrai\u0308nische   Straße  "
	if err := normalizeLimitedField("translated title", &value, 90); err != nil {
		t.Fatal(err)
	}
	if value != "Ukraïnische Straße" {
		t.Fatalf("normalized text = %q", value)
	}
}

func TestTranslationDefinitionFactorySupportsBCP47Target(t *testing.T) {
	source := langregistry.Definition{Code: "de", Tag: language.MustParse("de-DE"), DisplayName: "Deutsch", TranslationName: "German", Canonical: true}
	target := langregistry.Definition{Code: "pt-br", Tag: language.MustParse("pt-BR"), DisplayName: "Português (Brasil)", TranslationName: "Portuguese"}
	prompt := PromptDefinition{
		Version: "incident-translation-pt-br-v1", StepKey: TranslationStepKey(target.Code), TranslationLanguage: target.Code,
		Status: PromptActive, UserOnly: true, UserPromptTemplate: translateGemmaV2UserPromptTemplate(source, target, ""),
	}
	translation, err := newTranslationDefinition(target, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if translation.Language != "pt-br" || translation.Step.Key != "translation/pt-br" || !bytes.Contains(translation.Step.Schema, []byte(`"title_pt_br"`)) || !bytes.Contains(translation.Step.Schema, []byte(`"summary_pt_br"`)) {
		t.Fatalf("synthetic translation definition = %#v / %s", translation, translation.Step.Schema)
	}
	message := fmt.Sprintf(prompt.UserPromptTemplate, `{"title_de":"Titel","summary_de":"Zusammenfassung."}`)
	if !strings.Contains(message, "German (de-DE) to Portuguese (pt-BR)") || !strings.Contains(message, `"title_pt_br"`) || !strings.Contains(message, `"summary_pt_br"`) {
		t.Fatalf("synthetic TranslateGemma prompt = %q", message)
	}
	output, err := translation.Step.OutputDecoder(`{"title_pt_br":"Título","summary_pt_br":"Resumo seguro."}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateStepOutput(translation.Step, StepInput{}, &output); err != nil || output.Values["title"] != "Título" || output.Values["summary"] != "Resumo seguro." {
		t.Fatalf("synthetic translation output = %#v/%v", output, err)
	}
	if _, err := translation.Step.OutputDecoder(`{"title_pt_br":"Título","summary_pt_br":"Resumo.","extra":"no"}`); err == nil {
		t.Fatal("synthetic translation accepted an undeclared field")
	}
	unsafe, err := translation.Step.OutputDecoder(`{"title_pt_br":"Título","summary_pt_br":"Kontakt: person@example.com"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateStepOutput(translation.Step, StepInput{}, &unsafe); KindOf(err) != ErrorPrivacy {
		t.Fatalf("synthetic translation privacy error = %v, kind %q", err, KindOf(err))
	}
}

func TestTranslationDefinitionFactoryRejectsIncompletePrompt(t *testing.T) {
	target := langregistry.Definition{Code: "pt-br", Tag: language.MustParse("pt-BR"), DisplayName: "Português (Brasil)", TranslationName: "Portuguese"}
	valid := PromptDefinition{
		Version: "incident-translation-pt-br-v1", StepKey: TranslationStepKey(target.Code), TranslationLanguage: target.Code,
		Status: PromptActive, UserOnly: true, UserPromptTemplate: "%s",
	}
	for name, mutate := range map[string]func(*PromptDefinition){
		"inactive":          func(prompt *PromptDefinition) { prompt.Status = "retired" },
		"missing version":   func(prompt *PromptDefinition) { prompt.Version = "" },
		"not user-only":     func(prompt *PromptDefinition) { prompt.UserOnly = false },
		"unexpected system": func(prompt *PromptDefinition) { prompt.SystemPrompt = "Translate safely." },
		"missing payload":   func(prompt *PromptDefinition) { prompt.UserPromptTemplate = "Translate this." },
		"repeated payload":  func(prompt *PromptDefinition) { prompt.UserPromptTemplate = "%s %s" },
		"escaped payload":   func(prompt *PromptDefinition) { prompt.UserPromptTemplate = "%%s" },
		"invalid format":    func(prompt *PromptDefinition) { prompt.UserPromptTemplate = "100% safe: %s" },
	} {
		t.Run(name, func(t *testing.T) {
			prompt := valid
			mutate(&prompt)
			if _, err := newTranslationDefinition(target, prompt); err == nil {
				t.Fatal("incomplete translation prompt was accepted")
			}
		})
	}
}

func TestCategoryVerificationUsesOnlySummaryAndEnforcesVerdictInvariant(t *testing.T) {
	step := CategoryVerificationDefinition()
	if step.PromptVersion != "incident-category-verification-v2" {
		t.Fatalf("category verification prompt version = %q", step.PromptVersion)
	}
	input := StepInput{Values: map[string]string{
		"title_de": "Kontrolle in München", "summary_de": "Die Polizei kontrollierte Fahrzeuge.",
		"category": "traffic", "incident_body": "must never be sent", "original_title": "must never be sent",
	}}
	generated, message, err := step.Generator(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(generated.Values) != 3 || generated.Value("category") != "Verkehr" || generated.Value("incident_body") != "" || strings.Contains(message, "must never be sent") {
		t.Fatalf("category verifier leaked undeclared source input: %s", message)
	}
	for _, expected := range []string{`"title_de":"Kontrolle in München"`, `"summary_de":"Die Polizei kontrollierte Fahrzeuge."`, `"existing_category":"Verkehr"`} {
		if !strings.Contains(message, expected) {
			t.Errorf("category verification input omitted %s: %s", expected, message)
		}
	}
	if strings.Contains(step.SystemPrompt, "traffic") || strings.Contains(message, `"category":"traffic"`) {
		t.Fatal("category verifier exposed an internal category code to the German model")
	}
	if strings.Contains(message, `"category":"Verkehr"`) {
		t.Fatal("category verifier used the ambiguous category field name")
	}
	for _, expected := range []string{"vollständig ignoriert", "Trickdiebstahl", "falsche Handwerker", "freiwillig etwas", "Gewalt nur gegen Sachen", "kein Polizeieinsatz", "Amtswechsel", "bloße Bedrohung", "Gleichheitsprüfung"} {
		if !strings.Contains(step.SystemPrompt, expected) {
			t.Errorf("German category prompt omitted %q", expected)
		}
	}
	cases := []struct {
		name    string
		content string
		valid   bool
	}{
		{"confirmed", `{"is_correct":true,"corrected_category":"Verkehr"}`, true},
		{"corrected", `{"is_correct":false,"corrected_category":"Polizeieinsatz"}`, true},
		{"true but changed", `{"is_correct":true,"corrected_category":"Polizeieinsatz"}`, false},
		{"false but unchanged", `{"is_correct":false,"corrected_category":"Verkehr"}`, false},
		{"unknown enum", `{"is_correct":false,"corrected_category":"not-a-category"}`, false},
		{"malformed JSON", `{"is_correct":`, false},
		{"extra explanation", `{"is_correct":true,"corrected_category":"Verkehr","reason":"x"}`, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			output, decodeErr := step.OutputDecoder(test.content)
			if decodeErr == nil {
				decodeErr = ValidateStepOutput(step, generated, &output)
			}
			if (decodeErr == nil) != test.valid {
				t.Fatalf("validation error = %v, valid=%t", decodeErr, test.valid)
			}
		})
	}
	corrected, err := step.OutputDecoder(`{"is_correct":false,"corrected_category":"Polizeieinsatz"}`)
	if err != nil || corrected.Values["corrected_category"] != "police_operation" {
		t.Fatalf("German category mapping = %#v/%v", corrected.Values, err)
	}
}

func TestPublicAssistanceVerificationUsesRawGermanSourceAndEnforcesVerdictInvariant(t *testing.T) {
	step := PublicAssistanceVerificationDefinition()
	if step.PromptVersion != "incident-public-assistance-verification-v2" {
		t.Fatalf("public assistance prompt version = %q", step.PromptVersion)
	}
	input := StepInput{Values: map[string]string{
		"original_title":           "Zeugenaufruf nach Verkehrsunfall",
		"incident_body":            "Die Polizei bittet Zeugen um Beobachtungen und Videos unter 089/123456.",
		"public_assistance_status": "requested", "public_assistance_types": `["witness_observations"]`,
		"title_de": "must never be sent", "summary_de": "must never be sent",
	}}
	generated, message, err := step.Generator(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Zeugenaufruf nach Verkehrsunfall", "089/123456", `"existing_public_assistance_status":"requested"`, `"existing_public_assistance_types":["witness_observations"]`} {
		if !strings.Contains(message, expected) {
			t.Errorf("public assistance verification input omitted %q: %s", expected, message)
		}
	}
	for _, obsolete := range []string{`"public_assistance_status":`, `"public_assistance_types":`} {
		if strings.Contains(message, obsolete) {
			t.Errorf("public assistance verification input used ambiguous field %q: %s", obsolete, message)
		}
	}
	if strings.Contains(message, "must never be sent") || generated.Value("title_de") != "" || generated.Value("summary_de") != "" {
		t.Fatalf("public assistance verifier received undeclared presentation input: %s", message)
	}
	for _, expected := range []string{"vollständig ignoriert", "Fahrzeugbeobachtungen", "is_correct nur dann", "Typenmenge exakt"} {
		if !strings.Contains(step.SystemPrompt, expected) {
			t.Errorf("German public assistance prompt omitted %q", expected)
		}
	}
	cases := []struct {
		name    string
		content string
		valid   bool
	}{
		{"confirmed", `{"is_correct":true,"corrected_public_assistance_status":"requested","corrected_public_assistance_types":["witness_observations"]}`, true},
		{"corrected status and types", `{"is_correct":false,"corrected_public_assistance_status":"requested","corrected_public_assistance_types":["photo_video_material","witness_observations"]}`, true},
		{"corrected to not requested", `{"is_correct":false,"corrected_public_assistance_status":"not_requested","corrected_public_assistance_types":[]}`, true},
		{"corrected to unclear", `{"is_correct":false,"corrected_public_assistance_status":"unclear","corrected_public_assistance_types":[]}`, true},
		{"true but changed", `{"is_correct":true,"corrected_public_assistance_status":"not_requested","corrected_public_assistance_types":[]}`, false},
		{"false but unchanged", `{"is_correct":false,"corrected_public_assistance_status":"requested","corrected_public_assistance_types":["witness_observations"]}`, false},
		{"requested without types", `{"is_correct":false,"corrected_public_assistance_status":"requested","corrected_public_assistance_types":[]}`, false},
		{"not requested with types", `{"is_correct":false,"corrected_public_assistance_status":"not_requested","corrected_public_assistance_types":["witness_observations"]}`, false},
		{"unknown type", `{"is_correct":false,"corrected_public_assistance_status":"requested","corrected_public_assistance_types":["unknown"]}`, false},
		{"missing field", `{"is_correct":false,"corrected_public_assistance_status":"not_requested"}`, false},
		{"extra field", `{"is_correct":false,"corrected_public_assistance_status":"not_requested","corrected_public_assistance_types":[],"reason":"x"}`, false},
		{"malformed", `{"is_correct":`, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			output, decodeErr := step.OutputDecoder(test.content)
			if decodeErr == nil {
				decodeErr = ValidateStepOutput(step, generated, &output)
			}
			if (decodeErr == nil) != test.valid {
				t.Fatalf("validation error = %v, valid=%t", decodeErr, test.valid)
			}
		})
	}
	output, err := step.OutputDecoder(`{"is_correct":false,"corrected_public_assistance_status":"requested","corrected_public_assistance_types":["witness_observations","photo_video_material","witness_observations"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateStepOutput(step, generated, &output); err != nil {
		t.Fatal(err)
	}
	if output.Values["corrected_public_assistance_types"] != `["photo_video_material","witness_observations"]` {
		t.Fatalf("normalized types = %s", output.Values["corrected_public_assistance_types"])
	}
	noRequest, _, err := step.Generator(StepInput{Values: map[string]string{
		"original_title": "Mitteilung", "incident_body": "Text",
		"public_assistance_status": "not_requested", "public_assistance_types": "[]",
	}})
	if err != nil {
		t.Fatal(err)
	}
	for assistanceType := range assistanceTypes {
		content := fmt.Sprintf(`{"is_correct":false,"corrected_public_assistance_status":"requested","corrected_public_assistance_types":[%q]}`, assistanceType)
		candidate, err := step.OutputDecoder(content)
		if err != nil {
			t.Fatalf("decode assistance type %s: %v", assistanceType, err)
		}
		if err := ValidateStepOutput(step, noRequest, &candidate); err != nil {
			t.Errorf("allowed assistance type %s rejected: %v", assistanceType, err)
		}
	}
}

func TestRegisteredStepsReturnsDeepCopy(t *testing.T) {
	steps := RegisteredSteps()
	steps[0].InputKinds[0] = "modified"
	steps[0].Schema[0] = 'x'
	resolved, _ := StepByKey(IncidentMetadataStep)
	if resolved.InputKinds[0] == "modified" || resolved.Schema[0] == 'x' {
		t.Fatal("caller mutated the step registry")
	}
}

func TestStepInputsAndHashesUseOnlyOrderedDeclaredValues(t *testing.T) {
	step, _ := StepByKey(GermanPresentationStep)
	job := store.PipelineJob{ModelIdentity: "qwen3.5:4b", InputValues: map[string]string{
		"original_title": "Titel", "incident_body": "Text", "published_at": "must not be passed",
		"category": "traffic", "report_kind": "incident",
	}}
	input, firstHash := stepInputAndHash(job, step)
	if len(input.Values) != len(step.InputKinds) || input.Value("published_at") != "" {
		t.Fatalf("German step received undeclared inputs: %#v", input.Values)
	}
	job.InputValues["published_at"] = "changed undeclared value"
	_, unchangedHash := stepInputAndHash(job, step)
	if unchangedHash != firstHash {
		t.Fatal("undeclared input changed the German step hash")
	}
	job.InputValues["category"] = "other"
	_, changedHash := stepInputAndHash(job, step)
	if changedHash == firstHash {
		t.Fatal("metadata change did not invalidate the German step hash")
	}
}

func TestMetadataInputIncludesPublicationClockWeekdayAndTimezone(t *testing.T) {
	step, _ := StepByKey(IncidentMetadataStep)
	input := StepInput{Values: map[string]string{
		"original_title": "Mitteilung", "incident_body": "Am Montag geschah etwas in München.",
		"published_at": "2026-08-27T10:15:00Z",
	}}
	generated, message, err := step.Generator(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"publication_local_datetime":"2026-08-27T12:15:00+02:00"`, `"publication_weekday":"Donnerstag"`, `"timezone":"Europe/Berlin"`} {
		if !strings.Contains(message, expected) {
			t.Errorf("metadata request omitted %s: %s", expected, message)
		}
	}
	for _, expected := range []string{`"gestern":"2026-08-26"`, `"vorgestern":"2026-08-25"`, `"Montag":"2026-08-24"`, `"Mittwoch":"2026-08-26"`} {
		if !strings.Contains(message, expected) {
			t.Errorf("metadata request omitted reference day %s: %s", expected, message)
		}
	}
	if generated.Value("publication_local_datetime") != "2026-08-27T12:15:00+02:00" {
		t.Fatalf("generated publication context = %#v", generated.Values)
	}
}

func TestIncidentMetadataAcceptsSupportedTemporalForms(t *testing.T) {
	date, at, evening := "2026-08-24", "22:30", "evening"
	cases := map[string]StepOutput{
		"clock time": metadataOutput(&date, &at, nil),
		"date only":  metadataOutput(&date, nil, nil),
		"day part":   metadataOutput(&date, nil, &evening),
		"unknown":    metadataOutput(nil, nil, nil),
	}
	step, _ := StepByKey(IncidentMetadataStep)
	input := metadataInput("2026-08-25T12:00:00+02:00", "Am Montag, 24.08.2026, gegen 22:30 Uhr ereignete sich in München ein Vorfall.")
	for name, output := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateStepOutput(step, input, &output); err != nil {
				t.Fatalf("valid metadata rejected: %v", err)
			}
		})
	}
}

func TestIncidentMetadataRejectsMalformedOrUngroundedValues(t *testing.T) {
	validDate, badDate, at := "2026-08-24", "2026-02-30", "12:00"
	area, areaType := "Schwabing", "district"
	cases := map[string]StepOutput{
		"invalid date": metadataOutput(&badDate, nil, nil),
		"unsupported enum": func() StepOutput {
			o := metadataOutput(&validDate, nil, nil)
			o.ReportKind = "sometimes"
			return o
		}(),
		"time without date": metadataOutput(nil, &at, nil),
		"ungrounded area": func() StepOutput {
			o := metadataOutput(&validDate, nil, nil)
			o.AreaName, o.AreaType = &area, &areaType
			return o
		}(),
	}
	step, _ := StepByKey(IncidentMetadataStep)
	input := metadataInput("2026-08-25T12:00:00+02:00", "Der Vorfall ereignete sich in München.")
	for name, output := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateStepOutput(step, input, &output); KindOf(err) != ErrorOutput {
				t.Fatalf("error = %v, kind %q", err, KindOf(err))
			}
		})
	}
}

func TestIncidentMetadataValidatesPublicAssistanceConsistency(t *testing.T) {
	step, _ := StepByKey(IncidentMetadataStep)
	input := metadataInput("2026-08-25T12:00:00+02:00", "Die Polizei bittet Zeugen um Videos und Beobachtungen.")
	output := metadataOutput(nil, nil, nil)
	output.PublicAssistanceStatus = "requested"
	output.PublicAssistanceTypes = []string{"witness_observations", "photo_video_material", "witness_observations"}
	if err := ValidateStepOutput(step, input, &output); err != nil {
		t.Fatalf("valid assistance rejected: %v", err)
	}
	if strings.Join(output.PublicAssistanceTypes, ",") != "photo_video_material,witness_observations" {
		t.Fatalf("assistance types = %#v", output.PublicAssistanceTypes)
	}
	output.PublicAssistanceStatus = "not_requested"
	if err := ValidateStepOutput(step, input, &output); KindOf(err) != ErrorOutput {
		t.Fatalf("inconsistent assistance error = %v", err)
	}

	unclear := metadataOutput(nil, nil, nil)
	unclear.PublicAssistanceStatus = "unclear"
	if err := ValidateStepOutput(step, input, &unclear); err != nil {
		t.Fatalf("unclear assistance rejected: %v", err)
	}
}

func TestGermanPresentationValidatesPrivacyOnly(t *testing.T) {
	step, _ := StepByKey(GermanPresentationStep)
	output := StepOutput{TitleDE: "Sachlicher Titel", SummaryDE: "Eine sachliche Zusammenfassung.", PrivacyStatus: "safe", PrivacyFlags: []string{"age", "age"}}
	if err := ValidateStepOutput(step, StepInput{}, &output); err != nil {
		t.Fatalf("valid German presentation rejected: %v", err)
	}
	if len(output.PrivacyFlags) != 1 || canonicalCategoryLabels["traffic"] != "Verkehr" {
		t.Fatalf("normalized German output = %#v", output)
	}
	output.PrivacyFlags = []string{"uncertain"}
	if err := ValidateStepOutput(step, StepInput{}, &output); KindOf(err) != ErrorPrivacy {
		t.Fatalf("privacy uncertainty error = %v, kind %q", err, KindOf(err))
	}
}

func TestEnglishTranslationReceivesOnlyDeclaredGermanPresentation(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Messages) != 1 || payload.Messages[0].Role != "user" {
			t.Fatalf("translation request messages = %#v", payload.Messages)
		}
		for _, expected := range []string{`"title_en"`, `"summary_en"`, `"additionalProperties":false`} {
			if !bytes.Contains(payload.Format, []byte(expected)) {
				t.Fatalf("translation response schema omitted %s: %s", expected, payload.Format)
			}
		}
		user := payload.Messages[0].Content
		for _, forbidden := range []string{"private original body", "original_title", "incident_body"} {
			if strings.Contains(user, forbidden) {
				t.Fatalf("translation request leaked %q: %s", forbidden, user)
			}
		}
		if !strings.Contains(user, "Sicherer Titel") || !strings.Contains(user, "Sichere Zusammenfassung") {
			t.Fatalf("translation request omitted accepted German text: %s", user)
		}
		for _, expected := range []string{"German (de-DE) to English (en-GB)", `"title_en"`, `"summary_en"`} {
			if !strings.Contains(user, expected) {
				t.Fatalf("translation request omitted %q: %s", expected, user)
			}
		}
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(chatResponse{Model: "translategemma:4b", Done: true, Message: chatMessage{Role: "assistant", Content: `{"title_en":"Safe title","summary_en":"Safe summary."}`}})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
	})
	client, err := NewOllamaClient("http://ollama.test:11434", "translategemma:4b", time.Second, 8192, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	translation, _ := TranslationByLanguage(EnglishLanguage)
	step := translation.Step
	output, _, err := client.GenerateStep(context.Background(), step, StepInput{Values: map[string]string{
		"original_title": "private original title", "incident_body": "private original body",
		"title_de": "Sicherer Titel", "summary_de": "Sichere Zusammenfassung.",
	}})
	if err != nil || output.Values["title"] != "Safe title" || output.Values["summary"] != "Safe summary." {
		t.Fatalf("translation output = %#v, err=%v", output, err)
	}
}

func TestEnglishTranslationRejectsUndeclaredOutputFields(t *testing.T) {
	translation, _ := TranslationByLanguage(EnglishLanguage)
	if _, err := translation.Step.OutputDecoder(`{"title_en":"Safe title","summary_en":"Safe summary.","extra":"refuse"}`); err == nil {
		t.Fatal("English translation decoder accepted an undeclared field")
	}
}

func metadataInput(publishedAt, body string) StepInput {
	return StepInput{Values: map[string]string{"original_title": "Mitteilung", "incident_body": body, "published_at": publishedAt}}
}

func metadataOutput(startDate, startTime, dayPart *string) StepOutput {
	return StepOutput{
		Category:       "other",
		EventStartDate: startDate, EventStartTime: startTime,
		EventDayPart: dayPart,
		ReportKind:   "incident", PublicAssistanceStatus: "not_requested", PublicAssistanceTypes: []string{},
	}
}
