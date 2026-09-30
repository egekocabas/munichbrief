package processing

import (
	"os"
	"strings"
	"testing"

	"github.com/egekocabas/munichbrief/internal/parser"
)

func TestParsedSectionBoundaryPreservesPrivacyClassification(t *testing.T) {
	page, err := os.ReadFile("../parser/testdata/section_boundaries_shape.html")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParsePoliceRelease(page)
	if err != nil || len(parsed.Incidents) != 4 {
		t.Fatalf("parse: %d reports / %v", len(parsed.Incidents), err)
	}
	for i, incident := range parsed.Incidents {
		title, body := minimizeIncidentSource(incident.TitleDE, incident.BodyDE)
		_, request, err := LocationVerificationDefinition().Generator(candidateInput(incident.TitleDE, incident.BodyDE, incident.SectionContext))
		if err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			if title != incident.TitleDE || body != strings.Join(strings.Fields(incident.BodyDE), " ") || !strings.Contains(request, incident.TitleDE) {
				t.Errorf("ordinary report %s was minimized by a neighboring section", incident.Number)
			}
		} else if title != "Fahndungsaufruf" || strings.Contains(body, incident.BodyDE) || !strings.Contains(request, "Fahndungsaufruf") || strings.Contains(request, incident.BodyDE) {
			t.Error("actual wanted-person withdrawal no longer minimized")
		}
	}
}
