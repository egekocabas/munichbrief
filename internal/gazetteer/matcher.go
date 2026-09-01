package gazetteer

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	ahocorasick "github.com/pgavlin/aho-corasick"
	"golang.org/x/text/unicode/norm"
)

const tokenPrefix = "__MB_PLACE_"

type Matcher struct {
	automaton ahocorasick.AhoCorasick
	entries   []Entry
}

type Replacement struct {
	Token    string
	Original string
	Field    string
}

type Protected struct {
	Title        string
	Summary      string
	Replacements []Replacement
}

func NewMatcher(entries []Entry) (*Matcher, error) {
	byName := make(map[string]Entry, len(entries)+2)
	for _, entry := range entries {
		entry.Name = norm.NFC.String(strings.TrimSpace(entry.Name))
		if !validName(entry.Name) {
			continue
		}
		if letterCount(entry.Name) < 3 {
			entry.RequiresContext = true
		}
		if current, exists := byName[entry.Name]; !exists || entry.Priority < current.Priority {
			byName[entry.Name] = entry
		}
	}
	for _, name := range []string{"U-Bahn", "S-Bahn"} {
		byName[name] = Entry{Name: name, Kind: KindTransit, Priority: 0}
	}
	patterns := make([]string, 0, len(byName))
	for name := range byName {
		patterns = append(patterns, name)
	}
	sort.Slice(patterns, func(i, j int) bool {
		if len(patterns[i]) == len(patterns[j]) {
			return patterns[i] < patterns[j]
		}
		return len(patterns[i]) > len(patterns[j])
	})
	ordered := make([]Entry, len(patterns))
	for i, pattern := range patterns {
		ordered[i] = byName[pattern]
	}
	builder := ahocorasick.NewAhoCorasickBuilder(ahocorasick.Opts{MatchKind: ahocorasick.LeftMostLongestMatch, DFA: false})
	return &Matcher{automaton: builder.Build(patterns), entries: ordered}, nil
}

func (m *Matcher) Count() int {
	if m == nil {
		return 0
	}
	return len(m.entries)
}

func (m *Matcher) Protect(title, summary string) (Protected, error) {
	if m == nil || len(m.entries) == 0 {
		return Protected{}, errors.New("gazetteer matcher is unavailable")
	}
	title = norm.NFC.String(title)
	summary = norm.NFC.String(summary)
	if strings.Contains(title, tokenPrefix) || strings.Contains(summary, tokenPrefix) {
		return Protected{}, errors.New("source text contains reserved gazetteer token prefix")
	}
	result := Protected{}
	var replacements []Replacement
	result.Title, replacements = m.protectField(title, "title", replacements)
	result.Summary, replacements = m.protectField(summary, "summary", replacements)
	result.Replacements = replacements
	return result, nil
}

func (m *Matcher) protectField(value, field string, replacements []Replacement) (string, []Replacement) {
	var builder strings.Builder
	position := 0
	iter := m.automaton.Iter(value)
	for match := iter.Next(); match != nil; match = iter.Next() {
		start, end := match.Start(), match.End()
		entry := m.entries[match.Pattern()]
		if start < position || !unicodeBoundary(value, start, end) || letterCount(entry.Name) < 3 && touchesDash(value, start, end) || entry.RequiresContext && !hasLocationContext(value, start) {
			continue
		}
		token := fmt.Sprintf("%s%04d__", tokenPrefix, len(replacements)+1)
		builder.WriteString(value[position:start])
		builder.WriteString(token)
		replacements = append(replacements, Replacement{Token: token, Original: value[start:end], Field: field})
		position = end
	}
	if position == 0 {
		return value, replacements
	}
	builder.WriteString(value[position:])
	return builder.String(), replacements
}

func Restore(protected Protected, title, summary string) (string, string, error) {
	for _, replacement := range protected.Replacements {
		if strings.Count(title, replacement.Token)+strings.Count(summary, replacement.Token) != 1 {
			return "", "", fmt.Errorf("protected place token %s must occur exactly once", replacement.Token)
		}
		if replacement.Field == "title" && !strings.Contains(title, replacement.Token) || replacement.Field == "summary" && !strings.Contains(summary, replacement.Token) {
			return "", "", fmt.Errorf("protected place token %s moved between fields", replacement.Token)
		}
		title = strings.ReplaceAll(title, replacement.Token, replacement.Original)
		summary = strings.ReplaceAll(summary, replacement.Token, replacement.Original)
	}
	if strings.Contains(title, tokenPrefix) || strings.Contains(summary, tokenPrefix) {
		return "", "", errors.New("translation output contains an unknown place token")
	}
	return norm.NFC.String(title), norm.NFC.String(summary), nil
}

func validName(name string) bool {
	if len(name) < 2 || len(name) > 256 || !utf8.ValidString(name) {
		return false
	}
	return letterCount(name) >= 2
}

func letterCount(name string) int {
	letters := 0
	for _, r := range name {
		if unicode.IsControl(r) {
			return 0
		}
		if unicode.IsLetter(r) {
			letters++
		}
	}
	return letters
}

func unicodeBoundary(value string, start, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(value[:start])
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
			return false
		}
	}
	if end < len(value) {
		r, _ := utf8.DecodeRuneInString(value[end:])
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
			return false
		}
	}
	return true
}

func touchesDash(value string, start, end int) bool {
	if start > 0 {
		character, _ := utf8.DecodeLastRuneInString(value[:start])
		if unicode.Is(unicode.Dash, character) {
			return true
		}
	}
	if end < len(value) {
		character, _ := utf8.DecodeRuneInString(value[end:])
		return unicode.Is(unicode.Dash, character)
	}
	return false
}

func hasLocationContext(value string, start int) bool {
	prefix := strings.TrimRightFunc(value[:start], func(character rune) bool {
		return unicode.IsSpace(character) || unicode.IsPunct(character) || unicode.IsSymbol(character)
	})
	prefix = strings.ToLower(prefix)
	for _, word := range []string{"in", "bei", "nach", "aus", "von", "nahe", "zwischen", "gemeinde", "ortsteil", "stadtteil"} {
		if prefix == word || strings.HasSuffix(prefix, " "+word) {
			return true
		}
	}
	return false
}
