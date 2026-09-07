package parser

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestParsePoliceReleaseAnonymizedProductionShapes(t *testing.T) {
	tests := []struct {
		name             string
		fixture          string
		wantNumbers      []string
		wantBodyFragment string
	}{
		{
			name:             "bundle based on 107252",
			fixture:          "testdata/bundle_107252_shape.html",
			wantNumbers:      []string{"2101", "2102", "2103"},
			wantBodyFragment: "Person description: Invented description for parser testing.",
		},
		{
			name:             "bundle based on 107292",
			fixture:          "testdata/bundle_107292_shape.html",
			wantNumbers:      []string{"3101", "3102", "3103", "3104"},
			wantBodyFragment: "Police note: Invented safety note.",
		},
		{
			name:             "standalone based on 107230",
			fixture:          "testdata/standalone_107230_shape.html",
			wantNumbers:      []string{"4101"},
			wantBodyFragment: "Witness appeal Invented standalone appeal.",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contents, err := os.ReadFile(test.fixture)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			result, err := ParsePoliceRelease(contents)
			if err != nil {
				t.Fatalf("ParsePoliceRelease() error = %v", err)
			}
			if len(result.Incidents) != len(test.wantNumbers) {
				t.Fatalf("incident count = %d, want %d", len(result.Incidents), len(test.wantNumbers))
			}
			var allBodies strings.Builder
			for position, wantNumber := range test.wantNumbers {
				incident := result.Incidents[position]
				if incident.Number != wantNumber || incident.Position != position {
					t.Errorf("incident %d identity = %q/%d, want %q/%d", position, incident.Number, incident.Position, wantNumber, position)
				}
				allBodies.WriteString(incident.BodyDE)
			}
			if !strings.Contains(allBodies.String(), test.wantBodyFragment) {
				t.Errorf("parsed bodies do not contain %q", test.wantBodyFragment)
			}
			if strings.Contains(allBodies.String(), "table-of-contents") || strings.Contains(allBodies.String(), "Footer content") || strings.Contains(allBodies.String(), "never be collected") {
				t.Errorf("parsed bodies include content outside incident bodies: %q", allBodies.String())
			}
		})
	}
}

func TestParsePoliceReleaseBundle(t *testing.T) {
	html := []byte(`<!doctype html><html><body>
		<main id="readspeaker_lesen">
			<section class="bp-template bp-presse">
				<div class="bp-iwe2"><p>1244. First incident</p><p>1245. Second incident</p></div>
			</section>
			<section class="bp-flex bp-textblock-image"><div class="bp-iwe2">
				<h3><strong>1244.&nbsp; First incident – Maxvorstadt</strong></h3>
				<p>First paragraph.</p><p>Second paragraph.</p>
				<hr><h3>1245. Second incident – Schwabing</h3>
				<p>Incident body.</p><h4>Zeugenaufruf</h4><p>Witness information.</p>
			</div></section>
		</main>
	</body></html>`)

	result, err := ParsePoliceRelease(html)
	if err != nil {
		t.Fatalf("ParsePoliceRelease() error = %v", err)
	}
	if len(result.Incidents) != 2 {
		t.Fatalf("incident count = %d, want 2", len(result.Incidents))
	}
	if result.Incidents[0].Number != "1244" || result.Incidents[0].Position != 0 {
		t.Errorf("first incident identity = %q/%d, want 1244/0", result.Incidents[0].Number, result.Incidents[0].Position)
	}
	if result.Incidents[1].BodyDE != "Incident body.\n\nZeugenaufruf\n\nWitness information." {
		t.Errorf("second incident body = %q", result.Incidents[1].BodyDE)
	}
	if result.SourceHash == "" || result.Incidents[0].ContentHash == "" {
		t.Error("parsed hashes must not be empty")
	}
}

func TestParsePoliceReleaseStandalone(t *testing.T) {
	html := []byte(`<section class="bp-template bp-presse"><div class="bp-iwe2">
		<h2>1210. Standalone incident – Kirchheim</h2><p>Standalone body.</p>
	</div></section>`)

	result, err := ParsePoliceRelease(html)
	if err != nil {
		t.Fatalf("ParsePoliceRelease() error = %v", err)
	}
	if len(result.Incidents) != 1 || result.Incidents[0].Number != "1210" {
		t.Fatalf("standalone result = %#v", result.Incidents)
	}
}

func TestParsePoliceReleaseRejectsMissingContent(t *testing.T) {
	_, err := ParsePoliceRelease([]byte(`<html><body><main>not a release</main></body></html>`))
	if !errors.Is(err, ErrPressContentNotFound) {
		t.Fatalf("error = %v, want ErrPressContentNotFound", err)
	}
}

func TestParsePoliceReleaseRejectsUnnumberedContent(t *testing.T) {
	_, err := ParsePoliceRelease([]byte(`<section class="bp-template bp-presse"><p>No numbered heading.</p></section>`))
	if !errors.Is(err, ErrNoIncidents) {
		t.Fatalf("error = %v, want ErrNoIncidents", err)
	}
}

func TestParsePoliceReleaseDropsScriptText(t *testing.T) {
	result, err := ParsePoliceRelease([]byte(`<section class="bp-template bp-presse"><div class="bp-iwe2">
		<h2>1. Safe title</h2><p>Visible text<script>secret()</script></p>
	</div></section>`))
	if err != nil {
		t.Fatalf("ParsePoliceRelease() error = %v", err)
	}
	if result.Incidents[0].BodyDE != "Visible text" {
		t.Fatalf("body = %q, want script text removed", result.Incidents[0].BodyDE)
	}
}

func TestExtractedTextSurvivesUnsupportedIncidentStructure(t *testing.T) {
	parsed, err := ParsePoliceRelease([]byte(`<section class="bp-template bp-presse"><h1>Synthetic release</h1><div>Unnumbered <strong>source</strong> text.</div><ul><li>First detail</li><li>Second detail</li></ul><script>secretScript()</script><style>secretStyle</style></section>`))
	if !errors.Is(err, ErrNoIncidents) {
		t.Fatalf("error=%v", err)
	}
	want := "Synthetic release\n\nUnnumbered source text.\n\nFirst detail\n\nSecond detail"
	if parsed.ExtractedText != want {
		t.Fatalf("extracted=%q want %q", parsed.ExtractedText, want)
	}
}
