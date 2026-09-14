package web

import (
	"sync"
	"testing"

	langregistry "github.com/egekocabas/munichbrief/internal/languages"
)

func TestStandardLocalizationSharedAndCustomRegistryValidated(t *testing.T) {
	expected, err := newLocalization(langregistry.Registered())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			actual, err := newLocalization(langregistry.Registered())
			if err != nil || actual != expected {
				t.Errorf("standard catalogue not reused: %v", err)
				return
			}
			if actual.Text("tr", "ContactHeading") == "[ContactHeading]" {
				t.Error("shared translation missing")
			}
		})
	}
	wg.Wait()
	custom := langregistry.Registered()
	custom[0].SwitchMessageID = "MissingSyntheticSwitch"
	if _, err = newLocalization(custom); err == nil {
		t.Fatal("cached standard registry bypassed custom validation")
	}
	custom = langregistry.Registered()
	custom[0].Catalog = "locales/missing.toml"
	if _, err = newLocalization(custom); err == nil {
		t.Fatal("cached catalogue bypassed missing-file validation")
	}
}
