package location

import (
	"reflect"
	"strings"
	"testing"
)

func TestSourceCandidatesOnlyExplicitAreas(t *testing.T) {
	for _, tc := range []struct {
		text  string
		names []string
	}{
		{"Leopoldstraße", nil}, {"Neuhausenstraße", nil}, {"Fürstenried West", nil}, {"Landkreis München", nil},
		{"Schwabing", []string{"Schwabing"}}, {"Schwabing-West", []string{"Schwabing-West"}},
		{"Ramersdorf - Perlach", []string{"Ramersdorf-Perlach"}}, {"Milbertshofen/ Am Hart", []string{"Milbertshofen-Am Hart"}},
		{"Garching b. München", []string{"Garching"}},
		{"im Schottenhammel-Festzelts", []string{"Schottenhamel-Festzelt"}},
	} {
		cs := SourceCandidates(tc.text, "", Source{})
		var names []string
		for _, c := range cs {
			names = append(names, c.Name)
			for _, e := range c.Evidence {
				if !strings.Contains(tc.text, e.Name) || !strings.Contains(tc.text, e.Text) {
					t.Fatalf("invented evidence: %+v", e)
				}
			}
		}
		if !reflect.DeepEqual(names, tc.names) {
			t.Errorf("%q: got %v want %v", tc.text, names, tc.names)
		}
	}
}
func TestCandidateEvidenceDeterministicAndGrounded(t *testing.T) {
	title, body := "Vorfall – Haar", "In Haar fand ein Test statt. Eine Person mit Wohnsitz in München rief an."
	a, b := SourceCandidates(title, body, Source{}), SourceCandidates(title, body, Source{})
	if !reflect.DeepEqual(a, b) || len(a) != 2 || a[0].ID != "c1" || a[1].ID != "c2" {
		t.Fatalf("unstable candidates: %+v", a)
	}
	for _, c := range a {
		e := c.Evidence[0]
		field := title
		if e.Source == "incident_body" {
			field = body
		}
		if !containsName(field, e.Text) || !containsName(e.Text, e.Name) {
			t.Fatal("candidate cannot pass literal grounding")
		}
	}
	if cs := SourceCandidates("Vorfall – Festzelt", "", Source{}); len(cs) != 0 {
		t.Fatal("generic tent resolved without event context")
	}
	if cs := SourceCandidates("Vorfall – Festzelt", "", Source{SectionContext: "Wiesnberichte"}); len(cs) != 1 || cs[0].Kind != "venue" {
		t.Fatal("verified event context missing")
	}
}
func TestSourceAreaConflict(t *testing.T) {
	for _, tc := range []struct {
		title, body string
		want        bool
	}{
		{"Raub – Sendling-Westpark", "Am Abend kam es in einem Mehrfamilienhaus in Sendling zu einem Raub.", true},
		{"Vorfall – Hadern", "In Haar kam es zu einem Vorfall.", true},
		{"Vorfall – München", "Der Tatort lag in Neuhausen.", false},
		{"Vorfall – Neuhausen-Nymphenburg", "Auf einer Baustelle in Neuhausen geschah ein Unfall.", false},
		{"Vorfall – Ramersdorf - Perlach", "In Perlach kam es zu einem Vorfall.", false},
		{"Vorfall – Hadern", "Bereits zuvor kam es in Sendling-Westpark zu einem Vorfall.", false},
		{"Vorfall – Haar", "Die Frau wohnte in einer Wohnung in München.", false},
		{"Vorfall – Haar", "Eine Frau mit Wohnsitz in München rief die Polizei.", false},
		{"Vorfall – Hadern", "Der Tatort lag in Hadern. Die Polizei aus München ermittelt.", false},
	} {
		if got := SourceAreaConflict(tc.title, tc.body); got != tc.want {
			t.Errorf("%s / %s: %t want %t", tc.title, tc.body, got, tc.want)
		}
	}
}
