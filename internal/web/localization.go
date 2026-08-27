package web

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/BurntSushi/toml"
	"github.com/nicksnyder/go-i18n/v2/i18n"
)

//go:embed locales/*.toml
var localeFiles embed.FS

type localization struct {
	bundle *i18n.Bundle
}

func newLocalization(languages []readerLanguage) (*localization, error) {
	if len(languages) == 0 {
		return nil, fmt.Errorf("at least one reader language is required")
	}
	catalogs := make([]string, 0, len(languages))
	for _, definition := range languages {
		catalogs = append(catalogs, definition.Catalog)
	}
	if err := validateCatalogParity(localeFiles, catalogs...); err != nil {
		return nil, err
	}
	bundle := i18n.NewBundle(canonicalReaderLanguage().Tag)
	bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	for _, definition := range languages {
		if _, err := bundle.LoadMessageFileFS(localeFiles, definition.Catalog); err != nil {
			return nil, fmt.Errorf("load translation catalog %s: %w", definition.Catalog, err)
		}
	}
	return &localization{bundle: bundle}, nil
}

func (l *localization) Text(locale, messageID string) string {
	value, err := i18n.NewLocalizer(l.bundle, locale).Localize(&i18n.LocalizeConfig{MessageID: messageID})
	if err != nil {
		return "[" + messageID + "]"
	}
	return value
}

func (l *localization) Count(locale, messageID string, count int) string {
	value, err := i18n.NewLocalizer(l.bundle, locale).Localize(&i18n.LocalizeConfig{
		MessageID: messageID,
		TemplateData: map[string]any{
			"Count": count,
		},
		PluralCount: count,
	})
	if err != nil {
		return "[" + messageID + "]"
	}
	return value
}

func (l *localization) ShownTotal(locale string, shown, total int) string {
	value, err := i18n.NewLocalizer(l.bundle, locale).Localize(&i18n.LocalizeConfig{
		MessageID: "ShownTotal",
		TemplateData: map[string]any{
			"Shown": shown,
			"Total": total,
		},
	})
	if err != nil {
		return "[ShownTotal]"
	}
	return value
}

func validateCatalogParity(files fs.FS, names ...string) error {
	if len(names) < 2 {
		return fmt.Errorf("at least two translation catalogs are required")
	}
	base, err := catalogMessageIDs(files, names[0])
	if err != nil {
		return err
	}
	for _, name := range names[1:] {
		candidate, err := catalogMessageIDs(files, name)
		if err != nil {
			return err
		}
		if missing := messageIDDifference(base, candidate); len(missing) > 0 {
			return fmt.Errorf("translation catalog %s is missing messages: %v", name, missing)
		}
		if extra := messageIDDifference(candidate, base); len(extra) > 0 {
			return fmt.Errorf("translation catalog %s has unexpected messages: %v", name, extra)
		}
	}
	return nil
}

func catalogMessageIDs(files fs.FS, name string) (map[string]struct{}, error) {
	contents, err := fs.ReadFile(files, name)
	if err != nil {
		return nil, fmt.Errorf("read translation catalog %s: %w", name, err)
	}
	var messages map[string]map[string]any
	if err := toml.Unmarshal(contents, &messages); err != nil {
		return nil, fmt.Errorf("parse translation catalog %s: %w", name, err)
	}
	ids := make(map[string]struct{}, len(messages))
	for id, message := range messages {
		if len(message) == 0 {
			return nil, fmt.Errorf("translation catalog %s has empty message %s", name, id)
		}
		ids[id] = struct{}{}
	}
	return ids, nil
}

func messageIDDifference(left, right map[string]struct{}) []string {
	difference := make([]string, 0)
	for id := range left {
		if _, ok := right[id]; !ok {
			difference = append(difference, id)
		}
	}
	sort.Strings(difference)
	return difference
}
