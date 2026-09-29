package location

import "testing"

func TestResolveEvidenceAndRoles(t *testing.T) {
	tests := []struct {
		name, scope, title, body, context, want, outcome string
		mentions                                         []Mention
		withheld                                         bool
	}{
		{name: "heading district", scope: "single", title: "Synthetic report – Altstadt-Lehel", want: "Altstadt-Lehel", outcome: "corrected", mentions: []Mention{{Name: "Altstadt-Lehel", Kind: "area", Role: "primary", Source: "title", Evidence: "Synthetic report – Altstadt-Lehel"}}},
		{name: "officer origin excluded", scope: "single", body: "Beamte aus Frankfurt am Main. Einsatz in Haar.", want: "Haar", outcome: "corrected", mentions: []Mention{{Name: "Frankfurt am Main", Kind: "area", Role: "residence", Source: "body", Evidence: "Beamte aus Frankfurt am Main."}, {Name: "Haar", Kind: "area", Role: "primary", Source: "body", Evidence: "Einsatz in Haar."}}},
		{name: "body locality refines parent", scope: "single", title: "Hohenbrunn", body: "Tatort Riemerling", want: "Riemerling", outcome: "corrected", mentions: []Mention{{Name: "Hohenbrunn", Kind: "area", Role: "primary", Source: "title", Evidence: "Hohenbrunn"}, {Name: "Riemerling", Kind: "area", Role: "primary", Source: "body", Evidence: "Tatort Riemerling"}}},
		{name: "recovery not theft", scope: "unknown", body: "Fund in Schwabing", outcome: "ambiguous", mentions: []Mention{{Name: "Schwabing", Kind: "area", Role: "arrest_recovery", Source: "body", Evidence: "Fund in Schwabing"}}},
		{name: "multiple scenes", scope: "multiple", body: "Hadern und Ramersdorf", outcome: "ambiguous", mentions: []Mention{{Name: "Hadern", Kind: "area", Role: "primary", Source: "body", Evidence: "Hadern und Ramersdorf"}, {Name: "Ramersdorf", Kind: "area", Role: "primary", Source: "body", Evidence: "Hadern und Ramersdorf"}}},
		{name: "related fire excluded", scope: "single", body: "Brand in Hadern. Früher in Sendling-Westpark.", want: "Hadern", outcome: "corrected", mentions: []Mention{{Name: "Hadern", Kind: "area", Role: "primary", Source: "body", Evidence: "Brand in Hadern."}, {Name: "Sendling-Westpark", Kind: "area", Role: "related", Source: "body", Evidence: "Früher in Sendling-Westpark."}}},
		{name: "street is not district", scope: "single", body: "Schwanthalerstraße", outcome: "ambiguous", mentions: []Mention{{Name: "Schwanthalerstraße", Kind: "street", Role: "primary", Source: "body", Evidence: "Schwanthalerstraße"}}},
		{name: "festival with section", scope: "single", body: "Im Festzelt", context: "Wiesnberichte", want: "Ludwigsvorstadt", outcome: "corrected", mentions: []Mention{{Name: "Festzelt", Kind: "venue", Role: "primary", Source: "body", Evidence: "Im Festzelt"}}},
		{name: "festival without section", scope: "single", body: "Im Festzelt", outcome: "ambiguous", mentions: []Mention{{Name: "Festzelt", Kind: "venue", Role: "primary", Source: "body", Evidence: "Im Festzelt"}}},
		{name: "station is scene", scope: "single", body: "Angriff vor Polizeistation in Moosach", want: "Moosach", outcome: "corrected", mentions: []Mention{{Name: "Moosach", Kind: "area", Role: "primary", Source: "body", Evidence: "Angriff vor Polizeistation in Moosach"}}},
		{name: "compound alias", scope: "single", title: "Untermenzing-Allach", want: "Allach-Untermenzing", outcome: "corrected", mentions: []Mention{{Name: "Untermenzing-Allach", Kind: "area", Role: "primary", Source: "title", Evidence: "Untermenzing-Allach"}}},
		{name: "broad Schwabing", scope: "single", title: "Schwabing", want: "Schwabing", outcome: "corrected", mentions: []Mention{{Name: "Schwabing", Kind: "area", Role: "primary", Source: "title", Evidence: "Schwabing"}}},
		{name: "route", scope: "route", title: "Hadern", outcome: "ambiguous", mentions: []Mention{{Name: "Hadern", Kind: "area", Role: "primary", Source: "title", Evidence: "Hadern"}}},
		{name: "caption", scope: "source_problem", outcome: "source_problem"},
		{name: "absent", scope: "unknown", outcome: "no_location"},
		{name: "withheld", scope: "single", withheld: true, outcome: "withheld"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := Resolve(Interpretation{Scope: tt.scope, Mentions: tt.mentions}, Area{Name: "München", Type: "municipality"}, tt.title, tt.body, Source{SectionContext: tt.context}, tt.withheld)
			if err != nil || a.Outcome != tt.outcome {
				t.Fatalf("outcome %q, error %v", a.Outcome, err)
			}
			if tt.want == "" {
				if a.Proposed != nil {
					t.Fatal("unexpected override")
				}
			} else if a.Proposed == nil || a.Proposed.Name != tt.want {
				t.Fatalf("proposed %#v", a.Proposed)
			}
		})
	}
}
func TestRejectFabricatedEvidenceAndSubstring(t *testing.T) {
	for _, text := range []string{"Haarstraße", "Keine Ortsangabe"} {
		_, err := Resolve(Interpretation{Scope: "single", Mentions: []Mention{{Name: "Haar", Kind: "area", Role: "primary", Source: "body", Evidence: text}}}, Area{}, "", text, Source{}, false)
		if err == nil {
			t.Fatal("accepted ungrounded place")
		}
	}
}
func TestCatalogIdentityAndTypes(t *testing.T) {
	ids := map[string]bool{}
	for _, e := range catalog {
		if ids[e.ID] || len(e.Sources) == 0 {
			t.Fatalf("invalid entity %s", e.ID)
		}
		ids[e.ID] = true
		if e.Parent != "" {
			if _, ok := parent(e.Parent); !ok {
				t.Fatalf("missing parent %s", e.Parent)
			}
		}
	}
	for _, tt := range []struct{ name, kind string }{{"Haar", "municipality"}, {"Neuhadern", "neighbourhood"}, {"Allach", "neighbourhood"}, {"Perlacher Forst", "broad_area"}, {"Schwabing", "broad_area"}} {
		if got := Lookup(tt.name); len(got) != 1 || got[0].Type != tt.kind {
			t.Fatalf("%s: %#v", tt.name, got)
		}
	}
	if len(Lookup("Englischer Garten")) != 0 || len(Lookup("Markus")) != 0 {
		t.Fatal("spelling dictionary collisions must not become area identities")
	}
}

func TestWhitespaceLocationEvidenceIsMalformed(t *testing.T) {
	for _, name := range []string{" ", "\t\n"} {
		_, err := Resolve(Interpretation{Scope: "single", Mentions: []Mention{{Name: name, Kind: "area", Role: "primary", Source: "body", Evidence: "Some body"}}}, Area{}, "", "Some body", Source{}, false)
		if err == nil {
			t.Fatal("accepted empty normalized name")
		}
	}
}

func TestTypeOnlyConfirmationAndNormalizedAreas(t *testing.T) {
	for _, tt := range []struct{ name, kind, outcome, want string }{
		{"Haar", "neighbourhood", "corrected", "Haar"},
		{"Haar", "municipality", "confirmed", "Haar"},
		{"Milbertshofen / Am Hart", "district", "corrected", "Milbertshofen-Am Hart"},
		{"Ramersdorf - Perlach", "district", "corrected", "Ramersdorf-Perlach"},
		{"Englschalking", "neighbourhood", "confirmed", "Englschalking"},
		{"Neuaubing", "neighbourhood", "confirmed", "Neuaubing"},
	} {
		t.Run(tt.name+tt.kind, func(t *testing.T) {
			a, err := Resolve(Interpretation{Scope: "single", Mentions: []Mention{{Name: tt.name, Kind: "area", Role: "primary", Source: "title", Evidence: tt.name}}}, Area{Name: tt.name, Type: tt.kind}, tt.name, "", Source{}, false)
			if err != nil || a.Outcome != tt.outcome || a.Proposed == nil || a.Proposed.Name != tt.want {
				t.Fatalf("assessment %#v / %v", a, err)
			}
		})
	}
}
