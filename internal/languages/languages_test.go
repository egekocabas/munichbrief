package languages

import (
	"testing"
	"time"
)

func TestRegisteredLanguagesAreValidAndDefensive(t *testing.T) {
	definitions := Registered()
	if err := Validate(definitions); err != nil {
		t.Fatal(err)
	}
	definitions[0].Code = "changed"
	if Registered()[0].Code != "de" {
		t.Fatal("Registered returned shared mutable state")
	}
}

func TestPreferredCodeUsesBCP47Matching(t *testing.T) {
	definitions := Registered()
	for header, want := range map[string]string{
		"en-US,en;q=0.8,de;q=0.5": "en",
		"de-AT,de;q=0.9":          "de",
		"fr-FR,*;q=0.1":           "de",
		"":                        "de",
	} {
		if got := PreferredCode(definitions, header); got != want {
			t.Errorf("PreferredCode(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestRegisteredDateFormatting(t *testing.T) {
	value := time.Date(2026, time.August, 31, 12, 30, 0, 0, time.FixedZone("CEST", 2*60*60))
	de, _ := ByCode(Registered(), "de")
	en, _ := ByCode(Registered(), "en")
	if got := de.FormatDay(value); got != "Montag, 31. August 2026" {
		t.Fatalf("German day = %q", got)
	}
	if got := en.FormatDateTime(value); got != "31 August 2026, 12:30 CEST" {
		t.Fatalf("English date-time = %q", got)
	}
}

func TestValidateRejectsUnsafeAndDuplicateRegistrations(t *testing.T) {
	definitions := Registered()
	unsafe := append([]Definition(nil), definitions...)
	unsafe[1].Code = "api"
	if err := Validate(unsafe); err == nil {
		t.Fatal("unsafe route code was accepted")
	}
	duplicate := append(definitions, definitions[1])
	if err := Validate(duplicate); err == nil {
		t.Fatal("duplicate language was accepted")
	}
}
