// Package languages owns MunichBrief's compile-time reader language registry.
package languages

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/language"
)

var (
	routeCodePattern       = regexp.MustCompile(`^[a-z]{2,3}(?:-[a-z0-9]{2,8})*$`)
	openGraphLocalePattern = regexp.MustCompile(`^[a-z]{2,3}_[A-Z]{2}$`)
)

// Definition describes one reader language and its stable processing identity.
// Code is used in URLs, cookies, and translation scope keys; Tag is the exact
// BCP-47 identity used for negotiation and document metadata.
type Definition struct {
	Code            string
	Tag             language.Tag
	DisplayName     string
	TranslationName string
	Catalog         string
	OpenGraphLocale string
	Canonical       bool
	SwitchMessageID string
	StepMessageID   string
	FormatDate      func(time.Time) string
	FormatDay       func(time.Time) string
	FormatDateTime  func(time.Time) string
}

var registered = []Definition{
	{
		Code: "de", Tag: language.MustParse("de-DE"), DisplayName: "Deutsch", TranslationName: "German", Catalog: "locales/active.de.toml", OpenGraphLocale: "de_DE",
		Canonical: true, SwitchMessageID: "SwitchToGerman", StepMessageID: "GermanPresentationStep",
		FormatDate: func(value time.Time) string {
			return value.Format("02.") + " " + germanMonths[value.Month()] + " " + value.Format("2006")
		},
		FormatDay: func(value time.Time) string {
			return germanWeekdays[value.Weekday()] + ", " + value.Format("02.") + " " + germanMonths[value.Month()] + " " + value.Format("2006")
		},
		FormatDateTime: func(value time.Time) string {
			return value.Format("02.") + " " + germanMonths[value.Month()] + value.Format(" 2006, 15:04 MST")
		},
	},
	{
		Code: "en", Tag: language.MustParse("en-GB"), DisplayName: "English", TranslationName: "English", Catalog: "locales/active.en.toml", OpenGraphLocale: "en_GB",
		SwitchMessageID: "SwitchToEnglish", StepMessageID: "EnglishTranslationStep",
		FormatDate:     func(value time.Time) string { return value.Format("02 January 2006") },
		FormatDay:      func(value time.Time) string { return value.Format("Monday, 02 January 2006") },
		FormatDateTime: func(value time.Time) string { return value.Format("02 January 2006, 15:04 MST") },
	},
	{
		Code: "tr", Tag: language.MustParse("tr-TR"), DisplayName: "Türkçe", TranslationName: "Turkish", Catalog: "locales/active.tr.toml", OpenGraphLocale: "tr_TR",
		SwitchMessageID: "SwitchToTurkish", StepMessageID: "TurkishTranslationStep",
		FormatDate: func(value time.Time) string {
			return value.Format("02") + " " + turkishMonths[value.Month()] + " " + value.Format("2006")
		},
		FormatDay: func(value time.Time) string {
			return turkishWeekdays[value.Weekday()] + ", " + value.Format("02") + " " + turkishMonths[value.Month()] + " " + value.Format("2006")
		},
		FormatDateTime: func(value time.Time) string {
			return value.Format("02") + " " + turkishMonths[value.Month()] + value.Format(" 2006, 15:04 MST")
		},
	},
	{
		Code: "hr", Tag: language.MustParse("hr-HR"), DisplayName: "Hrvatski", TranslationName: "Croatian", Catalog: "locales/active.hr.toml", OpenGraphLocale: "hr_HR",
		SwitchMessageID: "SwitchToCroatian", StepMessageID: "CroatianTranslationStep",
		FormatDate: func(value time.Time) string {
			return value.Format("02.") + " " + croatianMonths[value.Month()] + " " + value.Format("2006.")
		},
		FormatDay: func(value time.Time) string {
			return croatianWeekdays[value.Weekday()] + ", " + value.Format("02.") + " " + croatianMonths[value.Month()] + " " + value.Format("2006.")
		},
		FormatDateTime: func(value time.Time) string {
			return value.Format("02.") + " " + croatianMonths[value.Month()] + value.Format(" 2006., 15:04 MST")
		},
	},
	{
		Code: "it", Tag: language.MustParse("it-IT"), DisplayName: "Italiano", TranslationName: "Italian", Catalog: "locales/active.it.toml", OpenGraphLocale: "it_IT",
		SwitchMessageID: "SwitchToItalian", StepMessageID: "ItalianTranslationStep",
		FormatDate: func(value time.Time) string {
			return value.Format("02") + " " + italianMonths[value.Month()] + " " + value.Format("2006")
		},
		FormatDay: func(value time.Time) string {
			return italianWeekdays[value.Weekday()] + " " + value.Format("02") + " " + italianMonths[value.Month()] + " " + value.Format("2006")
		},
		FormatDateTime: func(value time.Time) string {
			return value.Format("02") + " " + italianMonths[value.Month()] + value.Format(" 2006, 15:04 MST")
		},
	},
	{
		Code: "uk", Tag: language.MustParse("uk-UA"), DisplayName: "Українська", TranslationName: "Ukrainian", Catalog: "locales/active.uk.toml", OpenGraphLocale: "uk_UA",
		SwitchMessageID: "SwitchToUkrainian", StepMessageID: "UkrainianTranslationStep",
		FormatDate: func(value time.Time) string {
			return value.Format("02") + " " + ukrainianMonths[value.Month()] + " " + value.Format("2006") + " р."
		},
		FormatDay: func(value time.Time) string {
			return ukrainianWeekdays[value.Weekday()] + ", " + value.Format("02") + " " + ukrainianMonths[value.Month()] + " " + value.Format("2006") + " р."
		},
		FormatDateTime: func(value time.Time) string {
			return value.Format("02") + " " + ukrainianMonths[value.Month()] + value.Format(" 2006 р., 15:04 MST")
		},
	},
	{
		Code: "bs", Tag: language.MustParse("bs-BA"), DisplayName: "Bosanski", TranslationName: "Bosnian", Catalog: "locales/active.bs.toml", OpenGraphLocale: "bs_BA",
		SwitchMessageID: "SwitchToBosnian", StepMessageID: "BosnianTranslationStep",
		FormatDate: func(value time.Time) string {
			return value.Format("02.") + " " + bosnianMonths[value.Month()] + " " + value.Format("2006.")
		},
		FormatDay: func(value time.Time) string {
			return bosnianWeekdays[value.Weekday()] + ", " + value.Format("02.") + " " + bosnianMonths[value.Month()] + " " + value.Format("2006.")
		},
		FormatDateTime: func(value time.Time) string {
			return value.Format("02.") + " " + bosnianMonths[value.Month()] + value.Format(" 2006., 15:04 MST")
		},
	},
	{
		Code: "zh", Tag: language.MustParse("zh-CN"), DisplayName: "简体中文", TranslationName: "Simplified Chinese", Catalog: "locales/active.zh.toml", OpenGraphLocale: "zh_CN",
		SwitchMessageID: "SwitchToChinese", StepMessageID: "ChineseTranslationStep",
		FormatDate: func(value time.Time) string {
			return value.Format("2006年1月2日")
		},
		FormatDay: func(value time.Time) string {
			return value.Format("2006年1月2日") + chineseWeekdays[value.Weekday()]
		},
		FormatDateTime: func(value time.Time) string {
			return value.Format("2006年1月2日 15:04 MST")
		},
	},
	{
		Code: "hi", Tag: language.MustParse("hi-IN"), DisplayName: "हिन्दी", TranslationName: "Hindi", Catalog: "locales/active.hi.toml", OpenGraphLocale: "hi_IN",
		SwitchMessageID: "SwitchToHindi", StepMessageID: "HindiTranslationStep",
		FormatDate: func(value time.Time) string {
			return value.Format("02") + " " + hindiMonths[value.Month()] + " " + value.Format("2006")
		},
		FormatDay: func(value time.Time) string {
			return hindiWeekdays[value.Weekday()] + ", " + value.Format("02") + " " + hindiMonths[value.Month()] + " " + value.Format("2006")
		},
		FormatDateTime: func(value time.Time) string {
			return value.Format("02") + " " + hindiMonths[value.Month()] + value.Format(" 2006, 15:04 MST")
		},
	},
	{
		Code: "es", Tag: language.MustParse("es-ES"), DisplayName: "Español", TranslationName: "Spanish", Catalog: "locales/active.es.toml", OpenGraphLocale: "es_ES",
		SwitchMessageID: "SwitchToSpanish", StepMessageID: "SpanishTranslationStep",
		FormatDate: func(value time.Time) string {
			return value.Format("02") + " de " + spanishMonths[value.Month()] + " de " + value.Format("2006")
		},
		FormatDay: func(value time.Time) string {
			return spanishWeekdays[value.Weekday()] + ", " + value.Format("02") + " de " + spanishMonths[value.Month()] + " de " + value.Format("2006")
		},
		FormatDateTime: func(value time.Time) string {
			return value.Format("02") + " de " + spanishMonths[value.Month()] + value.Format(" de 2006, 15:04 MST")
		},
	},
	{
		Code: "fr", Tag: language.MustParse("fr-FR"), DisplayName: "Français", TranslationName: "French", Catalog: "locales/active.fr.toml", OpenGraphLocale: "fr_FR",
		SwitchMessageID: "SwitchToFrench", StepMessageID: "FrenchTranslationStep",
		FormatDate: func(value time.Time) string {
			return value.Format("02") + " " + frenchMonths[value.Month()] + " " + value.Format("2006")
		},
		FormatDay: func(value time.Time) string {
			return frenchWeekdays[value.Weekday()] + " " + value.Format("02") + " " + frenchMonths[value.Month()] + " " + value.Format("2006")
		},
		FormatDateTime: func(value time.Time) string {
			return value.Format("02") + " " + frenchMonths[value.Month()] + value.Format(" 2006, 15:04 MST")
		},
	},
}

var germanMonths = map[time.Month]string{
	time.January: "Januar", time.February: "Februar", time.March: "März", time.April: "April",
	time.May: "Mai", time.June: "Juni", time.July: "Juli", time.August: "August",
	time.September: "September", time.October: "Oktober", time.November: "November", time.December: "Dezember",
}

var germanWeekdays = map[time.Weekday]string{
	time.Sunday: "Sonntag", time.Monday: "Montag", time.Tuesday: "Dienstag", time.Wednesday: "Mittwoch",
	time.Thursday: "Donnerstag", time.Friday: "Freitag", time.Saturday: "Samstag",
}

var turkishMonths = map[time.Month]string{
	time.January: "Ocak", time.February: "Şubat", time.March: "Mart", time.April: "Nisan",
	time.May: "Mayıs", time.June: "Haziran", time.July: "Temmuz", time.August: "Ağustos",
	time.September: "Eylül", time.October: "Ekim", time.November: "Kasım", time.December: "Aralık",
}

var turkishWeekdays = map[time.Weekday]string{
	time.Sunday: "Pazar", time.Monday: "Pazartesi", time.Tuesday: "Salı", time.Wednesday: "Çarşamba",
	time.Thursday: "Perşembe", time.Friday: "Cuma", time.Saturday: "Cumartesi",
}

var croatianMonths = map[time.Month]string{
	time.January: "siječnja", time.February: "veljače", time.March: "ožujka", time.April: "travnja",
	time.May: "svibnja", time.June: "lipnja", time.July: "srpnja", time.August: "kolovoza",
	time.September: "rujna", time.October: "listopada", time.November: "studenoga", time.December: "prosinca",
}

var croatianWeekdays = map[time.Weekday]string{
	time.Sunday: "nedjelja", time.Monday: "ponedjeljak", time.Tuesday: "utorak", time.Wednesday: "srijeda",
	time.Thursday: "četvrtak", time.Friday: "petak", time.Saturday: "subota",
}

var italianMonths = map[time.Month]string{
	time.January: "gennaio", time.February: "febbraio", time.March: "marzo", time.April: "aprile",
	time.May: "maggio", time.June: "giugno", time.July: "luglio", time.August: "agosto",
	time.September: "settembre", time.October: "ottobre", time.November: "novembre", time.December: "dicembre",
}

var italianWeekdays = map[time.Weekday]string{
	time.Sunday: "domenica", time.Monday: "lunedì", time.Tuesday: "martedì", time.Wednesday: "mercoledì",
	time.Thursday: "giovedì", time.Friday: "venerdì", time.Saturday: "sabato",
}

var ukrainianMonths = map[time.Month]string{
	time.January: "січня", time.February: "лютого", time.March: "березня", time.April: "квітня",
	time.May: "травня", time.June: "червня", time.July: "липня", time.August: "серпня",
	time.September: "вересня", time.October: "жовтня", time.November: "листопада", time.December: "грудня",
}

var ukrainianWeekdays = map[time.Weekday]string{
	time.Sunday: "неділя", time.Monday: "понеділок", time.Tuesday: "вівторок", time.Wednesday: "середа",
	time.Thursday: "четвер", time.Friday: "п’ятниця", time.Saturday: "субота",
}

var bosnianMonths = map[time.Month]string{
	time.January: "januar", time.February: "februar", time.March: "mart", time.April: "april",
	time.May: "maj", time.June: "juni", time.July: "juli", time.August: "august",
	time.September: "septembar", time.October: "oktobar", time.November: "novembar", time.December: "decembar",
}

var bosnianWeekdays = map[time.Weekday]string{
	time.Sunday: "nedjelja", time.Monday: "ponedjeljak", time.Tuesday: "utorak", time.Wednesday: "srijeda",
	time.Thursday: "četvrtak", time.Friday: "petak", time.Saturday: "subota",
}

var chineseWeekdays = map[time.Weekday]string{
	time.Sunday: "星期日", time.Monday: "星期一", time.Tuesday: "星期二", time.Wednesday: "星期三",
	time.Thursday: "星期四", time.Friday: "星期五", time.Saturday: "星期六",
}

var hindiMonths = map[time.Month]string{
	time.January: "जनवरी", time.February: "फ़रवरी", time.March: "मार्च", time.April: "अप्रैल",
	time.May: "मई", time.June: "जून", time.July: "जुलाई", time.August: "अगस्त",
	time.September: "सितंबर", time.October: "अक्टूबर", time.November: "नवंबर", time.December: "दिसंबर",
}

var hindiWeekdays = map[time.Weekday]string{
	time.Sunday: "रविवार", time.Monday: "सोमवार", time.Tuesday: "मंगलवार", time.Wednesday: "बुधवार",
	time.Thursday: "गुरुवार", time.Friday: "शुक्रवार", time.Saturday: "शनिवार",
}

var spanishMonths = map[time.Month]string{
	time.January: "enero", time.February: "febrero", time.March: "marzo", time.April: "abril",
	time.May: "mayo", time.June: "junio", time.July: "julio", time.August: "agosto",
	time.September: "septiembre", time.October: "octubre", time.November: "noviembre", time.December: "diciembre",
}

var spanishWeekdays = map[time.Weekday]string{
	time.Sunday: "domingo", time.Monday: "lunes", time.Tuesday: "martes", time.Wednesday: "miércoles",
	time.Thursday: "jueves", time.Friday: "viernes", time.Saturday: "sábado",
}

var frenchMonths = map[time.Month]string{
	time.January: "janvier", time.February: "février", time.March: "mars", time.April: "avril",
	time.May: "mai", time.June: "juin", time.July: "juillet", time.August: "août",
	time.September: "septembre", time.October: "octobre", time.November: "novembre", time.December: "décembre",
}

var frenchWeekdays = map[time.Weekday]string{
	time.Sunday: "dimanche", time.Monday: "lundi", time.Tuesday: "mardi", time.Wednesday: "mercredi",
	time.Thursday: "jeudi", time.Friday: "vendredi", time.Saturday: "samedi",
}

// Registered returns a defensive copy in navigation and processing order.
func Registered() []Definition { return append([]Definition(nil), registered...) }

// Validate checks the complete cross-package registration contract.
func Validate(definitions []Definition) error {
	if len(definitions) < 2 {
		return errors.New("at least two reader languages are required")
	}
	seenCodes := make(map[string]struct{}, len(definitions))
	seenTags := make(map[string]struct{}, len(definitions))
	seenCatalogs := make(map[string]struct{}, len(definitions))
	seenMessageIDs := make(map[string]string, len(definitions)*2)
	canonical := 0
	for _, definition := range definitions {
		if !routeCodePattern.MatchString(definition.Code) || definition.Code == "api" {
			return fmt.Errorf("reader language code %q is not a safe normalized BCP-47 route code", definition.Code)
		}
		if strings.TrimSpace(definition.DisplayName) == "" || strings.TrimSpace(definition.TranslationName) == "" || strings.TrimSpace(definition.Catalog) == "" || strings.TrimSpace(definition.OpenGraphLocale) == "" || strings.TrimSpace(definition.SwitchMessageID) == "" || strings.TrimSpace(definition.StepMessageID) == "" {
			return fmt.Errorf("reader language %s has incomplete display, translation, or catalog metadata", definition.Code)
		}
		if !strings.HasSuffix(definition.Catalog, "."+definition.Code+".toml") {
			return fmt.Errorf("reader language %s catalog must end in .%s.toml", definition.Code, definition.Code)
		}
		if definition.Tag == language.Und {
			return fmt.Errorf("reader language %s requires an exact BCP-47 tag", definition.Code)
		}
		if err := validateRouteTagCompatibility(definition.Code, definition.Tag); err != nil {
			return err
		}
		if !openGraphLocalePattern.MatchString(definition.OpenGraphLocale) {
			return fmt.Errorf("reader language %s has invalid Open Graph locale %q", definition.Code, definition.OpenGraphLocale)
		}
		if err := validateOpenGraphLocaleCompatibility(definition); err != nil {
			return err
		}
		if definition.FormatDate == nil || definition.FormatDay == nil || definition.FormatDateTime == nil {
			return fmt.Errorf("reader language %s requires date formatters", definition.Code)
		}
		if _, duplicate := seenCodes[definition.Code]; duplicate {
			return fmt.Errorf("reader language code %s is registered more than once", definition.Code)
		}
		tag := definition.Tag.String()
		if _, duplicate := seenTags[tag]; duplicate {
			return fmt.Errorf("reader language tag %s is registered more than once", tag)
		}
		if _, duplicate := seenCatalogs[definition.Catalog]; duplicate {
			return fmt.Errorf("reader language catalog %s is registered more than once", definition.Catalog)
		}
		for _, message := range []struct{ kind, id string }{
			{kind: "switch", id: definition.SwitchMessageID},
			{kind: "processing step", id: definition.StepMessageID},
		} {
			if owner, duplicate := seenMessageIDs[message.id]; duplicate {
				return fmt.Errorf("reader language %s message ID %s is already used by %s", message.kind, message.id, owner)
			}
			seenMessageIDs[message.id] = definition.Code + " " + message.kind
		}
		seenCodes[definition.Code] = struct{}{}
		seenTags[tag] = struct{}{}
		seenCatalogs[definition.Catalog] = struct{}{}
		if definition.Canonical {
			canonical++
		}
	}
	if canonical != 1 {
		return fmt.Errorf("reader languages require exactly one canonical registration, got %d", canonical)
	}
	return nil
}

func validateOpenGraphLocaleCompatibility(definition Definition) error {
	parts := strings.Split(definition.OpenGraphLocale, "_")
	tagBase, _, tagRegion := definition.Tag.Raw()
	if parts[0] != tagBase.String() || (tagRegion.String() != "ZZ" && parts[1] != tagRegion.String()) {
		return fmt.Errorf("reader language %s Open Graph locale %s does not match BCP-47 tag %s", definition.Code, definition.OpenGraphLocale, definition.Tag)
	}
	return nil
}

func validateRouteTagCompatibility(code string, tag language.Tag) error {
	routeBase, routeScript, routeRegion := language.Make(code).Raw()
	tagBase, tagScript, tagRegion := tag.Raw()
	baseMismatch := routeBase != tagBase
	scriptMismatch := routeScript.String() != "Zzzz" && routeScript != tagScript
	regionMismatch := routeRegion.String() != "ZZ" && routeRegion != tagRegion
	if baseMismatch || scriptMismatch || regionMismatch {
		return fmt.Errorf("reader language code %s does not match BCP-47 tag %s", code, tag)
	}
	return nil
}

// ByCode resolves one stable route and processing code.
func ByCode(definitions []Definition, code string) (Definition, bool) {
	for _, definition := range definitions {
		if definition.Code == code {
			return definition, true
		}
	}
	return Definition{}, false
}

// Canonical returns the one validated canonical language.
func Canonical(definitions []Definition) Definition {
	for _, definition := range definitions {
		if definition.Canonical {
			return definition
		}
	}
	panic("language registry has no canonical language")
}

// Translated returns all independently processed target languages.
func Translated(definitions []Definition) []Definition {
	result := make([]Definition, 0, len(definitions))
	for _, definition := range definitions {
		if !definition.Canonical {
			result = append(result, definition)
		}
	}
	return result
}

// PreferredCode selects the best registered language from an Accept-Language
// value. Invalid, wildcard-only, and unmatched values use the canonical code.
func PreferredCode(definitions []Definition, header string) string {
	fallback := Canonical(definitions).Code
	tags, _, err := language.ParseAcceptLanguage(header)
	if err != nil || len(tags) == 0 {
		return fallback
	}
	registeredTags := make([]language.Tag, 0, len(definitions))
	for _, definition := range definitions {
		registeredTags = append(registeredTags, definition.Tag)
	}
	_, index, confidence := language.NewMatcher(registeredTags).Match(tags...)
	if confidence == language.No || index < 0 || index >= len(definitions) {
		return fallback
	}
	return definitions[index].Code
}
