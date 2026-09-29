package processing

import (
	"encoding/json"
	"github.com/egekocabas/munichbrief/internal/location"
	"strings"
	"testing"
)

func TestLocationContractHandlesMissingOriginalAndGroundsCorrections(t *testing.T) {
	step := LocationVerificationDefinition()
	input := StepInput{Values: map[string]string{"original_title": "Synthetischer Vorfall – Maxvorstadt", "incident_body": "Ein erfundener Testbericht.", "location_original": `{"name":"","type":""}`, "location_source": `{"source_hash":"source","context_hash":"context","section_context":"","report_number":"1"}`, "title_de": "Ein Vorfall", "summary_de": "Ein synthetischer Vorfall in München."}}
	_, message, err := step.Generator(input)
	if err != nil || !strings.Contains(message, "Maxvorstadt") || !strings.Contains(message, "existing_location") {
		t.Fatalf("input %v", err)
	}
	output, err := step.OutputDecoder(`{"scope":"single","mentions":[{"name":"Maxvorstadt","kind":"area","role":"primary","source":"title","evidence":"Synthetischer Vorfall – Maxvorstadt"}],"summary_conflict":false,"reason":"Expliziter Ort im Titel."}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := step.Validator(input, &output); err != nil {
		t.Fatal(err)
	}
	if output.Values["is_correct"] != "false" {
		t.Fatal("did not correct absent area")
	}
	var a location.Assessment
	if err := json.Unmarshal([]byte(output.Values["location_assessment"]), &a); err != nil || a.Proposed == nil || a.Proposed.Name != "Maxvorstadt" {
		t.Fatal("missing typed assessment")
	}
	values, err := step.OutputValues(output)
	if err != nil || len(values) != len(step.OutputKinds) {
		t.Fatal("invalid persistence contract")
	}
}
func TestLocationWithholdingDoesNotExposeIdentity(t *testing.T) {
	input := StepInput{Values: map[string]string{"original_title": "Öffentlichkeitsfahndung – Maxvorstadt", "incident_body": "Synthetic private identity must not be included", "location_original": `{"name":"München","type":"municipality"}`, "location_source": `{"source_hash":"s","context_hash":"c"}`, "title_de": "Hinweis", "summary_de": "Allgemeiner Hinweis."}}
	_, message, err := locationInput(input)
	if err != nil || strings.Contains(message, "Synthetic private identity") {
		t.Fatal("identity sent despite withholding")
	}
	output, err := locationDecode(`{"scope":"unknown","mentions":[],"summary_conflict":false,"reason":"No location"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLocation(input, &output); err != nil {
		t.Fatal(err)
	}
	if output.Values["is_correct"] != "unresolved" || output.Values["location_proposed"] != "null" || !strings.Contains(output.Values["location_assessment"], `"outcome":"withheld"`) {
		t.Fatal("withheld source supplied override")
	}
}
func TestLocationRejectsInvalidStructuredOutput(t *testing.T) {
	for _, value := range []string{`{}`, `{"scope":"single","mentions":[],"summary_conflict":false}`, `{"scope":"single","mentions":[],"summary_conflict":false,"reason":"ok","extra":true}`} {
		if _, err := locationDecode(value); err == nil {
			t.Fatal("invalid output accepted")
		}
	}
}
