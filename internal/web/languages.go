package web

import (
	"fmt"

	langregistry "github.com/egekocabas/munichbrief/internal/languages"
	"github.com/egekocabas/munichbrief/internal/processing"
)

type readerLanguage = langregistry.Definition

func validateReaderLanguageDefinitions(definitions []readerLanguage) error {
	if err := langregistry.Validate(definitions); err != nil {
		return err
	}
	for _, definition := range langregistry.Translated(definitions) {
		translation, exists := processing.TranslationByLanguage(definition.Code)
		if !exists {
			return fmt.Errorf("reader language %s has no translation definition", definition.Code)
		}
		if definition.DisplayName != translation.DisplayName {
			return fmt.Errorf("reader language %s display name does not match its translation definition", definition.Code)
		}
	}
	for _, translation := range processing.RegisteredTranslations() {
		definition, exists := langregistry.ByCode(definitions, translation.Language)
		if !exists || definition.Canonical {
			return fmt.Errorf("translation language %s has no translated reader registration", translation.Language)
		}
	}
	return nil
}

func (s *Server) languageByCode(code string) (readerLanguage, bool) {
	return langregistry.ByCode(s.languages, code)
}

func (s *Server) canonicalLanguage() readerLanguage {
	return langregistry.Canonical(s.languages)
}

func (s *Server) translatedLanguages() []readerLanguage {
	return langregistry.Translated(s.languages)
}
