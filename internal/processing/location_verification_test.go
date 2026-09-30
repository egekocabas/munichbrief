package processing

import (
	"encoding/json"
	"github.com/egekocabas/munichbrief/internal/location"
	"strings"
	"testing"
)

func candidateInput(title, body, context string) StepInput {
	source, _ := json.Marshal(location.Source{SourceHash: "s", ContextHash: "c", SectionContext: context})
	return StepInput{Values: map[string]string{"original_title": title, "incident_body": body, "location_original": `{"name":"München","type":"municipality"}`, "location_source": string(source)}}
}
func selectCandidate(t *testing.T, input StepInput, name string) StepOutput {
	t.Helper()
	var source location.Source
	if err := json.Unmarshal([]byte(input.Value("location_source")), &source); err != nil {
		t.Fatal(err)
	}
	for _, c := range location.SourceCandidates(input.Value("original_title"), input.Value("incident_body"), source) {
		if c.Name == name {
			raw, _ := json.Marshal(map[string]any{"decision": "located", "candidate_id": c.ID})
			out, err := locationDecode(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
	}
	t.Fatalf("candidate %q unavailable", name)
	return StepOutput{}
}
func TestLocationContractHandlesMissingOriginalAndGroundsCorrections(t *testing.T) {
	step := LocationVerificationDefinition()
	input := candidateInput("Synthetischer Vorfall – Maxvorstadt", "Ein erfundener Testbericht.", "")
	input.Values["location_original"] = `{}`
	input.Values["title_de"] = "Never send accepted title"
	input.Values["summary_de"] = "Never send accepted summary"
	_, message, err := step.Generator(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, blocked := range []string{"Never send", "existing_location", "presentation_de", "summary_de"} {
		if strings.Contains(message, blocked) {
			t.Fatalf("leaked %s", blocked)
		}
	}
	if !strings.Contains(message, `"candidates"`) {
		t.Fatal("candidate input missing")
	}
	out := selectCandidate(t, input, "Maxvorstadt")
	if err = step.Validator(input, &out); err != nil {
		t.Fatal(err)
	}
	var a location.Assessment
	if err = json.Unmarshal([]byte(out.Values["location_assessment"]), &a); err != nil {
		t.Fatal(err)
	}
	if a.Proposed == nil || a.Proposed.Name != "Maxvorstadt" || out.Values["is_correct"] != "false" || a.Mentions[0].Evidence != input.Value("original_title") {
		t.Fatalf("bad assessment: %+v", a)
	}
	if a.CandidateVersion != location.CandidateVersion || a.SummaryConflictStatus != "not_assessed" {
		t.Fatal("missing provenance")
	}
	values, err := step.OutputValues(out)
	if err != nil || len(values) != len(step.OutputKinds) {
		t.Fatal("invalid persistence contract")
	}
}
func TestLocationWithholdingDoesNotExposeIdentity(t *testing.T) {
	input := candidateInput("Öffentlichkeitsfahndung – Maxvorstadt", "Synthetic private identity", "")
	_, message, err := locationInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(message, "Synthetic private identity") || strings.Contains(message, "Maxvorstadt") {
		t.Fatal("withheld source leaked via candidate list")
	}
	out, err := locationDecode(`{"decision":"no_location","candidate_id":null}`)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateLocation(input, &out); err != nil {
		t.Fatal(err)
	}
	if out.Values["is_correct"] != "unresolved" || out.Values["location_proposed"] != "null" || !strings.Contains(out.Values["location_assessment"], `"outcome":"withheld"`) {
		t.Fatal("withheld source supplied override")
	}
}
func TestPrimaryLocationContract(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `{"decision":"located","candidate_id":null}`, `{"decision":"located","candidate_id":"Maxvorstadt"}`, `{"decision":"located","candidate_id":"c0"}`, `{"decision":"located","candidate_id":1}`, `{"decision":"unresolved","candidate_id":"c1"}`, `{"decision":"no_location"}`, `{"decision":null,"candidate_id":null}`, `{"decision":"unknown","candidate_id":null}`, `{"decision":"no_location","candidate_id":null,"evidence":"invented"}`} {
		if _, err := locationDecode(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	input := candidateInput("Vorfall – Haar", "Ein Test.", "")
	out, err := locationDecode(`{"decision":"located","candidate_id":"c999"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateLocation(input, &out); err == nil {
		t.Fatal("invented ID accepted")
	}
}
func TestPrimaryLocationResolution(t *testing.T) {
	for _, tc := range []struct {
		name, title, body, context, choice, decision, outcome, area string
		conflict                                                    bool
	}{
		{name: "parent heading precise body", title: "Vorfall – München", body: "Der Vorfall ereignete sich in Neuhausen.", choice: "Neuhausen", outcome: "corrected", area: "Neuhausen"},
		{name: "municipality", title: "Vorfall – Haar", choice: "Haar", outcome: "corrected", area: "Haar"},
		{name: "venue", title: "Vorfall im Schottenhamel-Festzelt", choice: "Schottenhamel-Festzelt", outcome: "corrected", area: "Ludwigsvorstadt"},
		{name: "street only", title: "Vorfall", body: "Der Vorfall geschah in der Beispielstraße.", decision: "unresolved", outcome: "ambiguous"},
		{name: "multiple scenes", title: "Zwei Vorfälle – Hadern / Ramersdorf", decision: "unresolved", outcome: "ambiguous"},
		{name: "no scene", title: "Hinweis", body: "Der Tatort ist unbekannt.", decision: "no_location", outcome: "no_location"},
		{name: "recovery must not replace theft", title: "Festnahme – Hadern", body: "Ein Fahrrad wurde in München gestohlen und in Hadern sichergestellt.", choice: "München", outcome: "confirmed", area: "München"},
		{name: "source problem guard", title: "Vorfall – Hadern", body: "123. Weiterer Bericht", choice: "Hadern", outcome: "source_problem"},
		{name: "route guard", title: "Pkw-Fahrer entzieht sich Kontrolle – Englschalking", body: "Er missachtete Anhaltesignale und fuhr von der Beispielstraße in die Musterstraße.", choice: "Englschalking", outcome: "ambiguous"},
		{name: "source conflict body choice", title: "Raub – Sendling-Westpark", body: "Es kam in einem Mehrfamilienhaus in Sendling zu einem Raub.", choice: "Sendling", outcome: "ambiguous", conflict: true},
		{name: "source conflict heading choice", title: "Raub – Sendling-Westpark", body: "Es kam in einem Mehrfamilienhaus in Sendling zu einem Raub.", choice: "Sendling-Westpark", outcome: "ambiguous", conflict: true},
		{name: "residence is not conflict", title: "Vorfall – Haar", body: "Eine Frau mit Wohnsitz in München meldete den Vorfall.", choice: "Haar", outcome: "corrected", area: "Haar"},
		{name: "earlier fire not conflict", title: "Brand – Hadern", body: "Bereits zuvor kam es in Sendling-Westpark zu einem Brand.", choice: "Hadern", outcome: "corrected", area: "Hadern"},
		{name: "festival context", title: "Vorfall – Festzelt", body: "Ein Testvorfall.", context: "Wiesnberichte", choice: "Festzelt", outcome: "corrected", area: "Ludwigsvorstadt"},
		{name: "festival topic is not scene", title: "Pressekonferenz zum Oktoberfest", body: "Eine Sonderbeilage.", decision: "source_problem", outcome: "source_problem"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := candidateInput(tc.title, tc.body, tc.context)
			var out StepOutput
			if tc.choice != "" {
				out = selectCandidate(t, input, tc.choice)
			} else {
				raw, _ := json.Marshal(map[string]any{"decision": tc.decision, "candidate_id": nil})
				var err error
				out, err = locationDecode(string(raw))
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := validateLocation(input, &out); err != nil {
				t.Fatal(err)
			}
			var a location.Assessment
			if err := json.Unmarshal([]byte(out.Values["location_assessment"]), &a); err != nil {
				t.Fatal(err)
			}
			if a.Outcome != tc.outcome || a.SourceConflict != tc.conflict {
				t.Fatalf("bad assessment: %+v", a)
			}
			if tc.area != "" {
				if a.Proposed == nil || a.Proposed.Name != tc.area {
					t.Fatalf("bad proposal: %+v", a.Proposed)
				}
			} else if a.Proposed != nil {
				t.Fatal("abstention supplied override")
			}
		})
	}
}
func TestVehicleRouteGuard(t *testing.T) {
	for _, tc := range []struct {
		title, body string
		want        bool
	}{
		{"Kradfahrer entzieht sich Kontrolle", "Trotz aller Signale anzuhalten fuhr er vom Beispielring in die Musterstraße.", true},
		{"Pkw-Fahrer versucht, sich Kontrolle zu entziehen", "Er missachtete die Anhaltesignale und fuhr über die Lange Straße zum Beispielplatz.", true},
		{"Verkehrsunfall – Hadern", "Der Pkw bog aus der Beispielstraße in die Musterstraße und kollidierte dort.", false},
		{"Einbruch – Hadern", "Die Täter flohen durch die Beispielstraße und Musterstraße. Ein Pkw stand in der Nähe.", false},
		{"Pkw-Fahrer entzieht sich Kontrolle", "Er fuhr auf der Beispielstraße. Dort auf der Beispielstraße hielt er an.", false},
	} {
		if got := unresolvedVehicleRoute(tc.title, tc.body); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.title, got, tc.want)
		}
	}
}

func TestLocationParentSelectionRetainsSupportedCanonicalChild(t *testing.T) {
	in := candidateInput("Unfall – Hadern", "Am Abend fuhr eine Person mit Wohnsitz in München die Teststraße in Neuhadern stadtauswärts.", "")
	in.Values["location_original"] = `{"name":"Neuhadern","type":"municipality"}`
	out := selectCandidate(t, in, "Hadern")
	if err := validateLocation(in, &out); err != nil {
		t.Fatal(err)
	}
	if out.Values["location_proposed"] != "null" || !strings.Contains(out.Values["location_assessment"], "more precise scene") {
		t.Fatalf("%+v", out)
	}
	out = selectCandidate(t, in, "Neuhadern")
	if err := validateLocation(in, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Values["location_proposed"], `"type":"neighbourhood"`) {
		t.Fatal("supported direct child selection should still fix type")
	}
}
