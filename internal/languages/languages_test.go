package languages

import (
	"testing"
	"time"

	"golang.org/x/text/language"
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
	if translated := Translated(nil); len(translated) != 0 {
		t.Fatalf("Translated(nil) = %#v", translated)
	}
}

func TestPreferredCodeUsesBCP47Matching(t *testing.T) {
	definitions := Registered()
	for header, want := range map[string]string{
		"en-US,en;q=0.8,de;q=0.5": "en",
		"de-AT,de;q=0.9":          "de",
		"tr-DE,tr;q=0.9":          "tr",
		"hr-BA,hr;q=0.9":          "hr",
		"it-CH,it;q=0.9":          "it",
		"uk-UA,uk;q=0.9":          "uk",
		"bs-BA,bs;q=0.9":          "bs",
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
	tr, _ := ByCode(Registered(), "tr")
	hr, _ := ByCode(Registered(), "hr")
	it, _ := ByCode(Registered(), "it")
	uk, _ := ByCode(Registered(), "uk")
	bs, _ := ByCode(Registered(), "bs")
	if got := de.FormatDay(value); got != "Montag, 31. August 2026" {
		t.Fatalf("German day = %q", got)
	}
	if got := en.FormatDateTime(value); got != "31 August 2026, 12:30 CEST" {
		t.Fatalf("English date-time = %q", got)
	}
	for name, actual := range map[string]string{
		"Turkish": tr.FormatDay(value), "Croatian": hr.FormatDay(value), "Italian": it.FormatDay(value),
		"Ukrainian": uk.FormatDay(value), "Bosnian": bs.FormatDay(value),
	} {
		want := map[string]string{
			"Turkish": "Pazartesi, 31 Ağustos 2026", "Croatian": "ponedjeljak, 31. kolovoza 2026.",
			"Italian": "lunedì 31 agosto 2026", "Ukrainian": "понеділок, 31 серпня 2026 р.",
			"Bosnian": "ponedjeljak, 31. august 2026.",
		}[name]
		if actual != want {
			t.Errorf("%s day = %q, want %q", name, actual, want)
		}
	}
}

func TestTranslationCodesPreserveNormalizedBCP47Tags(t *testing.T) {
	for routeCode, tag := range map[string]language.Tag{
		"pt-br":   language.MustParse("pt-BR"),
		"zh-hans": language.MustParse("zh-Hans"),
	} {
		if err := validateRouteTagCompatibility(routeCode, tag); err != nil {
			t.Errorf("translation code %s rejected normalized tag %s: %v", routeCode, tag, err)
		}
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
	mismatchedTag := append([]Definition(nil), definitions...)
	mismatchedTag[1].Tag = language.MustParse("fr-FR")
	if err := Validate(mismatchedTag); err == nil {
		t.Fatal("route code and BCP-47 tag mismatch was accepted")
	}
	missingTranslationName := append([]Definition(nil), definitions...)
	missingTranslationName[1].TranslationName = ""
	if err := Validate(missingTranslationName); err == nil {
		t.Fatal("missing model-facing translation name was accepted")
	}
	invalidOpenGraphLocale := append([]Definition(nil), definitions...)
	invalidOpenGraphLocale[1].OpenGraphLocale = "en-gb"
	if err := Validate(invalidOpenGraphLocale); err == nil {
		t.Fatal("invalid Open Graph locale was accepted")
	}
	mismatchedOpenGraphLocale := append([]Definition(nil), definitions...)
	mismatchedOpenGraphLocale[1].OpenGraphLocale = "fr_FR"
	if err := Validate(mismatchedOpenGraphLocale); err == nil {
		t.Fatal("Open Graph locale for another language was accepted")
	}
	duplicateMessageID := append([]Definition(nil), definitions...)
	duplicateMessageID[1].StepMessageID = duplicateMessageID[0].SwitchMessageID
	if err := Validate(duplicateMessageID); err == nil {
		t.Fatal("duplicate language message ID was accepted")
	}
	withoutCanonical := append([]Definition(nil), definitions...)
	withoutCanonical[0].Canonical = false
	if err := Validate(withoutCanonical); err == nil {
		t.Fatal("registry without a canonical language was accepted")
	}
	multipleCanonical := append([]Definition(nil), definitions...)
	multipleCanonical[1].Canonical = true
	if err := Validate(multipleCanonical); err == nil {
		t.Fatal("registry with multiple canonical languages was accepted")
	}
}
