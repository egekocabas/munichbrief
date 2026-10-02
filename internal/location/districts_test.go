package location

import "testing"

func TestDistrictGroupsUseOnlyReviewedContainment(t *testing.T) {
	for _, test := range []struct{ name, group string }{
		{"Ramersdorf-Perlach", "munich:district:ramersdorf-perlach"},
		{"Neuperlach", "munich:district:ramersdorf-perlach"},
		{"  NEUPERLACH  ", "munich:district:ramersdorf-perlach"},
		{"Schottenhammel-Festzelt", "munich:district:ludwigsvorstadt-isarvorstadt"},
		{"Untermenzing-Allach", "munich:district:allach-untermenzing"},
		{"Hochbrück", DistrictOutside},
		{"Riemerling", DistrictOutside},
		{"Garching bei München", DistrictOutside},
		{"Schwabing", DistrictUnassigned},
		{"Giesing", DistrictUnassigned},
		{"Innenstadt", DistrictUnassigned},
		{"Am Harthof", DistrictUnassigned},
		{"München", DistrictUnassigned},
		{"Perlacher Forst", DistrictUnassigned},
		{"Forstenrieder Park", DistrictUnassigned},
		{"Neuperlacher Straße", DistrictUnassigned},
		{"Unknown", DistrictUnassigned},
		{"", DistrictUnassigned},
	} {
		if actual := DistrictGroup(test.name); actual != test.group {
			t.Errorf("DistrictGroup(%q)=%q, want %q", test.name, actual, test.group)
		}
	}
	if districts := Districts(); len(districts) != 25 {
		t.Fatalf("got %d official districts", len(districts))
	} else {
		for _, district := range districts {
			resolved, ok := District(district.ID)
			if !ok || resolved != district || DistrictGroup(district.Name) != district.ID {
				t.Errorf("district identity mismatch: %#v", district)
			}
		}
		districts[0].Name = "mutated"
		if Districts()[0].Name == "mutated" {
			t.Fatal("districts exposes mutable catalog")
		}
	}
	for _, invalid := range []string{"", "outside", "unassigned", "Maxvorstadt", "city:munich", "munich:locality:neuperlach"} {
		if _, ok := District(invalid); ok {
			t.Errorf("non-district identity accepted: %q", invalid)
		}
	}
}
