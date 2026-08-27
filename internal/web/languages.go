package web

import (
	"errors"
	"fmt"
	"time"

	"github.com/egekocabas/munichbrief/internal/processing"
	"golang.org/x/text/language"
)

// readerLanguage is the single registration point for reader-facing language
// behavior. Translation definitions remain in processing because they also
// describe prompts and validators that the web server must not own.
type readerLanguage struct {
	Code            string
	DisplayName     string
	Catalog         string
	Tag             language.Tag
	Canonical       bool
	SwitchMessageID string
	StepMessageID   string
	FormatDate      func(time.Time) string
	FormatDay       func(time.Time) string
	FormatDateTime  func(time.Time) string
}

var readerLanguages = []readerLanguage{
	{
		Code: "de", DisplayName: "Deutsch", Catalog: "locales/active.de.toml", Tag: language.German,
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
		Code: "en", DisplayName: "English", Catalog: "locales/active.en.toml", Tag: language.English,
		SwitchMessageID: "SwitchToEnglish", StepMessageID: "EnglishTranslationStep",
		FormatDate:     func(value time.Time) string { return value.Format("02 January 2006") },
		FormatDay:      func(value time.Time) string { return value.Format("Monday, 02 January 2006") },
		FormatDateTime: func(value time.Time) string { return value.Format("02 January 2006, 15:04 MST") },
	},
}

func validateReaderLanguages() error {
	seen := make(map[string]struct{}, len(readerLanguages))
	canonical := 0
	for _, definition := range readerLanguages {
		if definition.Code == "" || definition.DisplayName == "" || definition.Catalog == "" || definition.SwitchMessageID == "" || definition.StepMessageID == "" {
			return errors.New("reader language registrations require code, name, catalog, switch message, and processing-step message")
		}
		if definition.FormatDate == nil || definition.FormatDay == nil || definition.FormatDateTime == nil {
			return fmt.Errorf("reader language %s requires date formatters", definition.Code)
		}
		if _, exists := seen[definition.Code]; exists {
			return fmt.Errorf("reader language %s is registered more than once", definition.Code)
		}
		seen[definition.Code] = struct{}{}
		if definition.Canonical {
			canonical++
			continue
		}
		translation, exists := processing.TranslationByLanguage(definition.Code)
		if !exists {
			return fmt.Errorf("reader language %s has no translation definition", definition.Code)
		}
		if definition.DisplayName != translation.DisplayName {
			return fmt.Errorf("reader language %s display name does not match its translation definition", definition.Code)
		}
	}
	if canonical != 1 {
		return fmt.Errorf("reader languages require exactly one canonical registration, got %d", canonical)
	}
	for _, translation := range processing.RegisteredTranslations() {
		definition, exists := readerLanguageByCode(translation.Language)
		if !exists || definition.Canonical {
			return fmt.Errorf("translation language %s has no translated reader registration", translation.Language)
		}
	}
	return nil
}

func readerLanguageByCode(code string) (readerLanguage, bool) {
	for _, definition := range readerLanguages {
		if definition.Code == code {
			return definition, true
		}
	}
	return readerLanguage{}, false
}

func canonicalReaderLanguage() readerLanguage {
	for _, definition := range readerLanguages {
		if definition.Canonical {
			return definition
		}
	}
	panic("reader language registry has no canonical language")
}

func translatedReaderLanguages() []readerLanguage {
	definitions := make([]readerLanguage, 0, len(readerLanguages)-1)
	for _, definition := range readerLanguages {
		if !definition.Canonical {
			definitions = append(definitions, definition)
		}
	}
	return definitions
}
