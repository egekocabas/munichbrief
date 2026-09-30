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
		wantTitle        string
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
		{
			name:             "unnumbered standalone based on 108358",
			fixture:          "testdata/standalone_unnumbered_108358_shape.html",
			wantNumbers:      []string{""},
			wantTitle:        "Synthetic road-safety event – Example South",
			wantBodyFragment: "Invented closing details for parser testing.",
		},
		{
			name:             "standalone with number only in page headline based on 109114",
			fixture:          "testdata/standalone_numbered_headline_shape.html",
			wantNumbers:      []string{""},
			wantTitle:        "9101. Synthetic follow-up announcement",
			wantBodyFragment: "Invented follow-up details without a numbered body heading.",
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
			if test.wantTitle != "" {
				if result.Incidents[0].TitleDE != test.wantTitle {
					t.Errorf("incident title = %q, want %q", result.Incidents[0].TitleDE, test.wantTitle)
				}
				if strings.Contains(allBodies.String(), test.wantTitle) {
					t.Errorf("parsed body repeats incident title %q", test.wantTitle)
				}
				repeated, err := ParsePoliceRelease(contents)
				if err != nil {
					t.Fatalf("repeated ParsePoliceRelease() error = %v", err)
				}
				if repeated.SourceHash != result.SourceHash || repeated.Incidents[0].ContentHash != result.Incidents[0].ContentHash {
					t.Error("repeated parse produced different hashes")
				}
			}
			if strings.Contains(allBodies.String(), "table-of-contents") || strings.Contains(allBodies.String(), "Footer content") || strings.Contains(allBodies.String(), "never be collected") {
				t.Errorf("parsed bodies include content outside incident bodies: %q", allBodies.String())
			}
			if result.SourceHash == "" {
				t.Error("source hash must not be empty")
			}
			for _, incident := range result.Incidents {
				if incident.ContentHash == "" {
					t.Error("incident content hash must not be empty")
				}
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

func TestParsePoliceReleaseRejectsIncompleteUnnumberedShapes(t *testing.T) {
	tests := []struct {
		name string
		html string
	}{
		{
			name: "headline without body",
			html: `<section class="bp-template bp-presse"><bp-headline title="Synthetic release"></bp-headline></section>`,
		},
		{
			name: "unexpected body container",
			html: `<section class="bp-template bp-presse"><bp-headline title="Synthetic release"></bp-headline><p>Body text.</p></section>`,
		},
		{
			name: "body with repeated title only",
			html: `<section class="bp-template bp-presse"><bp-headline title="Synthetic release"></bp-headline><section class="bp-flex bp-textblock-image"><div class="bp-iwe2"><h3>Synthetic release</h3></div></section></section>`,
		},
		{
			name: "numbered-looking contents without detail headings",
			html: `<section class="bp-template bp-presse"><bp-headline title="Synthetic release"></bp-headline><section class="bp-flex bp-textblock-image"><div class="bp-iwe2"><p>1200. First entry</p><p>1201. Second entry</p></div></section></section>`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParsePoliceRelease([]byte(test.html))
			if !errors.Is(err, ErrNoIncidents) {
				t.Fatalf("error = %v, want ErrNoIncidents", err)
			}
		})
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

func TestBoldParagraphBoundaryAndFestivalContext(t *testing.T) {
	text := `<main id="readspeaker_lesen"><section class="bp-template bp-presse"><p><strong>1. Contents one</strong></p><p><strong>2. Contents two</strong></p></section><section><h3>1. Synthetic first</h3><p>First body.</p><p><strong>2. Synthetic second</strong></p><p>Second body.</p><h3>Wiesnberichte</h3><h3>3. Synthetic tent</h3><p>Festzelt.</p><h4>Fall 1</h4><p>First case.</p><h4>Fall 2</h4><p>Second case.</p></section></main>`
	parsed, err := ParsePoliceRelease([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Incidents) != 3 {
		t.Fatalf("got %d reports", len(parsed.Incidents))
	}
	if strings.Contains(parsed.Incidents[0].BodyDE, "Second") || parsed.Incidents[2].SectionContext != "Wiesnberichte" || !strings.Contains(parsed.Incidents[2].BodyDE, "Fall 2") {
		t.Fatalf("incorrect boundaries or context: %#v", parsed.Incidents)
	}
	if parsed.Incidents[2].ContextHash == "" {
		t.Fatal("missing context provenance")
	}
}

func TestFestivalContextExcludesContentsAndStopsAtSectionBoundary(t *testing.T) {
	for _, heading := range []string{"Wiesnberichte", "Wiesnbericht", "Wiesn-Bericht:", "Wiesnberichte:", "Wiesn-Berichte: "} {
		t.Run(heading, func(t *testing.T) {
			text := `<main id="readspeaker_lesen"><section class="bp-template bp-presse"><p>1. Contents</p><h3>` + heading + `</h3><p>2. Festival contents</p></section><section><h3>1. Ordinary report</h3><p>Ordinary body.</p></section><section><h3>` + heading + `</h3><p><strong>2. Festival report</strong></p><p>Im Festzelt.</p></section><section><h3>3. Another report</h3><p>Ein anderes Festzelt.</p></section></main>`
			parsed, err := ParsePoliceRelease([]byte(text))
			if err != nil || len(parsed.Incidents) != 3 {
				t.Fatalf("parse %d/%v", len(parsed.Incidents), err)
			}
			if parsed.Incidents[0].SectionContext != "" || parsed.Incidents[1].SectionContext == "" || parsed.Incidents[2].SectionContext != "" {
				t.Fatalf("contexts: %q/%q/%q", parsed.Incidents[0].SectionContext, parsed.Incidents[1].SectionContext, parsed.Incidents[2].SectionContext)
			}
		})
	}
}

func TestFirstDetailCanBeBoldParagraph(t *testing.T) {
	parsed, err := ParsePoliceRelease([]byte(`<main id="readspeaker_lesen"><section class="bp-template bp-presse"><p><strong>1. Contents</strong></p></section><section><p><strong>1. Actual report</strong></p><p>Actual body.</p></section></main>`))
	if err != nil || len(parsed.Incidents) != 1 || parsed.Incidents[0].TitleDE != "Actual report" {
		t.Fatalf("parse: %#v/%v", parsed.Incidents, err)
	}
}

func TestDedicatedFestivalReleaseContextAndUnchangedBody(t *testing.T) {
	for _, tc := range []struct {
		title string
		want  bool
	}{
		{"Wiesnbericht der Polizei München vom 24.09.2026 auf Boarisch", true},
		{"Medieninformation der Polizei München vom 24.09.2026", false},
		{"Pressekonferenz über das Oktoberfest", false},
	} {
		page := `<main id="readspeaker_lesen"><section class="bp-template bp-presse"><bp-headline title="` + tc.title + `"></bp-headline><h2>Wiesnbericht auf Boarisch</h2><p>1. Contents</p></section><section><h3>1. Synthetischer Vorfall</h3><p>Unveränderter Testtext.</p><h3>2. Noch ein Vorfall</h3><p>Zweiter Text.</p></section></main>`
		parsed, err := ParsePoliceRelease([]byte(page))
		if err != nil || len(parsed.Incidents) != 2 {
			t.Fatalf("%+v %v", parsed, err)
		}
		for _, i := range parsed.Incidents {
			if (i.SectionContext != "") != tc.want {
				t.Errorf("%s: %+v", tc.title, i)
			}
		}
		if parsed.Incidents[0].BodyDE != "Unveränderter Testtext." {
			t.Fatal("context injected into source body")
		}
	}
}

func TestReleaseSectionLabelsDoNotContaminateIncidentBodies(t *testing.T) {
	contents, err := os.ReadFile("testdata/section_boundaries_shape.html")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePoliceRelease(contents)
	if err != nil || len(parsed.Incidents) != 4 {
		t.Fatalf("parse: %d reports / %v", len(parsed.Incidents), err)
	}
	want := []struct{ number, body, context string }{
		{"8101", "Erfundener Unfallbericht.\n\nZeugenaufruf\n\nErfundener Hinweistext.", ""},
		{"8102", "Fall 1\n\nErfundener erster Fall im Festzelt.\n\nFall 2\n\nErfundener zweiter Fall im Festzelt.", "Wiesnberichte"},
		{"8103", "Erfundener Körperverletzungsbericht im Festzelt.", "Wiesnberichte"},
		{"8104", "Erfundener Text zum Widerruf.", ""},
	}
	for i, expected := range want {
		incident := parsed.Incidents[i]
		if incident.Number != expected.number || incident.Position != i || incident.BodyDE != expected.body || incident.SectionContext != expected.context {
			t.Errorf("report %d: %#v", i, incident)
		}
		if incident.ContentHash != hash(incident.Number, incident.TitleDE, expected.body) {
			t.Errorf("report %d: content hash does not reflect clean source body", i)
		}
		if incident.ContextHash != hash("section-context-v2", expected.context) {
			t.Errorf("report %d: context provenance changed", i)
		}
	}
	if !strings.Contains(parsed.ExtractedText, "Widerruf einer Öffentlichkeitsfahndung:") || !strings.Contains(parsed.ExtractedText, "Wiesnberichte:") {
		t.Fatal("full protected source snapshot lost release headings")
	}
}

func TestReleaseSectionBoundaryIsStructural(t *testing.T) {
	for _, tc := range []struct{ name, between, next, want string }{
		{"h2 label", "<h2>New section</h2>", "<h3>2. Second</h3><p>Second body.</p>", "Body."},
		{"h3 label", "<h3>New section</h3>", "<h2>2. Second</h2><p>Second body.</p>", "Body."},
		{"heading cluster", "<h2>New section</h2><h3>New subsection</h3>", "<h3>2. Second</h3><p>Second body.</p>", "Body."},
		{"empty layout nodes", "<div><h2>New section</h2></div><hr><p>&nbsp;</p><!-- spacer -->", "<div><h3>2. Second</h3><p>Second body.</p></div>", "Body."},
		{"bold report boundary", "<h2>New section</h2>", "<p><strong>2. Second</strong></p><p>Second body.</p>", "Body."},
		{"h2 internal heading", "<h2>Witness appeal</h2><p>Keep appeal.</p>", "<h3>2. Second</h3><p>Second body.</p>", "Body.\n\nWitness appeal\n\nKeep appeal."},
		{"h3 internal heading", "<h3>Case details</h3><p>Keep details.</p>", "<h3>2. Second</h3><p>Second body.</p>", "Body.\n\nCase details\n\nKeep details."},
		{"list after internal heading", "<h3>Person description</h3><ul><li>Synthetic details.</li></ul>", "<h3>2. Second</h3><p>Second body.</p>", "Body.\n\nPerson description"},
		{"uncollected text after heading", "<h3>Details</h3><div>Synthetic details.</div>", "<h3>2. Second</h3><p>Second body.</p>", "Body.\n\nDetails"},
		{"image after heading", "<h3>Image caption</h3><img src='/synthetic.png' alt='Synthetic scene'>", "<h3>2. Second</h3><p>Second body.</p>", "Body.\n\nImage caption"},
		{"h4 retained", "<h4>Closing note</h4>", "<h3>2. Second</h3><p>Second body.</p>", "Body.\n\nClosing note"},
		{"bold prose retained", "<p><strong>Closing note</strong></p>", "<h3>2. Second</h3><p>Second body.</p>", "Body.\n\nClosing note"},
		{"trailing heading retained", "<h2>Closing note</h2>", "", "Body.\n\nClosing note"},
		{"container continuation", "</section><section><h2>Witness appeal</h2><p>Keep continuation.</p>", "<h3>2. Second</h3><p>Second body.</p>", "Body.\n\nWitness appeal\n\nKeep continuation."},
		{"no inference across sections", "<h2>Closing note</h2></section><section>", "<h3>2. Second</h3><p>Second body.</p>", "Body.\n\nClosing note"},
		{"nonreport numbered prose", "<h2>Internal heading</h2><p>2. Numbered prose</p>", "<h3>3. Third</h3><p>Third body.</p>", "Body.\n\nInternal heading\n\n2. Numbered prose"},
		{"bold contents entries", "<h2>Internal heading</h2><p><b>2. Contents A</b></p><p><b>3. Contents B</b></p>", "<h3>4. Fourth</h3><p>Fourth body.</p>", "Body.\n\nInternal heading\n\n2. Contents A\n\n3. Contents B"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := `<main id="readspeaker_lesen"><section class="bp-template bp-presse"><p>1. Contents</p></section><section><h3>1. First</h3><p>Body.</p>` + tc.between + tc.next + `</section></main>`
			parsed, err := ParsePoliceRelease([]byte(page))
			if err != nil || len(parsed.Incidents) == 0 {
				t.Fatalf("parse: %+v / %v", parsed, err)
			}
			if parsed.Incidents[0].BodyDE != tc.want {
				t.Fatalf("body = %q, want %q", parsed.Incidents[0].BodyDE, tc.want)
			}
		})
	}
}

func TestContentsFormattingDoesNotCreateOrHideReports(t *testing.T) {
	for _, tc := range []struct{ name, contents string }{
		{"line-break contents", `<p>71. Contents first<br>72. Contents second</p>`},
		{"attachment only in contents", `<p>71. Contents first</p><p>99. Interim briefing – see <a href="/synthetic.pdf">attachment</a></p><p>72. Contents second</p>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := `<main id="readspeaker_lesen"><section class="bp-template bp-presse">` + tc.contents + `</section><section><h3>71. Actual first</h3><p>First body.</p><h3>72. Actual second</h3><p>Second body.</p></section></main>`
			parsed, err := ParsePoliceRelease([]byte(page))
			if err != nil || len(parsed.Incidents) != 2 {
				t.Fatalf("parse: %d reports / %v", len(parsed.Incidents), err)
			}
			if parsed.Incidents[0].Number != "71" || parsed.Incidents[1].Number != "72" || parsed.Incidents[0].BodyDE != "First body." || parsed.Incidents[1].BodyDE != "Second body." {
				t.Fatalf("contents changed detail reports: %#v", parsed.Incidents)
			}
			if !strings.Contains(parsed.ExtractedText, "Contents first") {
				t.Fatal("contents lost from protected full-source snapshot")
			}
		})
	}
}
