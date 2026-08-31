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
		Code: "de", Tag: language.MustParse("de-DE"), DisplayName: "Deutsch", Catalog: "locales/active.de.toml", OpenGraphLocale: "de_DE",
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
		Code: "en", Tag: language.MustParse("en-GB"), DisplayName: "English", Catalog: "locales/active.en.toml", OpenGraphLocale: "en_GB",
		SwitchMessageID: "SwitchToEnglish", StepMessageID: "EnglishTranslationStep",
		FormatDate:     func(value time.Time) string { return value.Format("02 January 2006") },
		FormatDay:      func(value time.Time) string { return value.Format("Monday, 02 January 2006") },
		FormatDateTime: func(value time.Time) string { return value.Format("02 January 2006, 15:04 MST") },
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
		if strings.TrimSpace(definition.DisplayName) == "" || strings.TrimSpace(definition.Catalog) == "" || strings.TrimSpace(definition.OpenGraphLocale) == "" || strings.TrimSpace(definition.SwitchMessageID) == "" || strings.TrimSpace(definition.StepMessageID) == "" {
			return fmt.Errorf("reader language %s has incomplete display or catalog metadata", definition.Code)
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
