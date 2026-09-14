package gazetteer

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	ahocorasick "github.com/pgavlin/aho-corasick"
	"golang.org/x/text/unicode/norm"
)

const tokenPrefix = "__MB_"

var placeTokenPattern = regexp.MustCompile(`__MB_[A-Z_]+_[0-9]{4}__`)

type ProtectionMode string

const (
	ProtectionOpaque ProtectionMode = "opaque"
	ProtectionTyped  ProtectionMode = "typed"
)

type ProtectionOptions struct {
	Mode         ProtectionMode
	VisibleKinds []string
}

type Matcher struct {
	automaton ahocorasick.AhoCorasick
	entries   []Entry
}

type Replacement struct {
	Token    string
	Original string
	Field    string
	Kind     string
}

type Protected struct {
	Title        string
	Summary      string
	Replacements []Replacement
}

func NewMatcher(entries []Entry) (*Matcher, error) {
	byName := make(map[string]Entry, len(entries)+4)
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
	for _, name := range []string{"U-Bahn", "U-Bahnen"} {
		byName[name] = Entry{Name: name, Kind: KindSubwaySystem, Priority: 0}
	}
	for _, name := range []string{"S-Bahn", "S-Bahnen"} {
		byName[name] = Entry{Name: name, Kind: KindCommuterTrain, Priority: 0}
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
	return m.ProtectWithOptions(title, summary, ProtectionOptions{Mode: ProtectionOpaque})
}

func (m *Matcher) ProtectWithOptions(title, summary string, options ProtectionOptions) (Protected, error) {
	if m == nil || len(m.entries) == 0 {
		return Protected{}, errors.New("gazetteer matcher is unavailable")
	}
	if options.Mode == "" {
		options.Mode = ProtectionOpaque
	}
	if options.Mode != ProtectionOpaque && options.Mode != ProtectionTyped {
		return Protected{}, fmt.Errorf("unknown gazetteer protection mode %q", options.Mode)
	}
	title = norm.NFC.String(title)
	summary = norm.NFC.String(summary)
	if strings.Contains(title, tokenPrefix) || strings.Contains(summary, tokenPrefix) {
		return Protected{}, errors.New("source text contains reserved gazetteer token prefix")
	}
	visibleKinds := make(map[string]bool, len(options.VisibleKinds))
	for _, kind := range options.VisibleKinds {
		visibleKinds[kind] = true
	}
	result := Protected{}
	var replacements []Replacement
	tokens := make(map[string]string)
	result.Title, replacements = m.protectField(title, "title", replacements, tokens, options.Mode, visibleKinds)
	result.Summary, replacements = m.protectField(summary, "summary", replacements, tokens, options.Mode, visibleKinds)
	result.Replacements = replacements
	return result, nil
}

func (m *Matcher) protectField(value, field string, replacements []Replacement, tokens map[string]string, mode ProtectionMode, visibleKinds map[string]bool) (string, []Replacement) {
	var builder strings.Builder
	position := 0
	iter := m.automaton.Iter(value)
	for match := iter.Next(); match != nil; match = iter.Next() {
		start, end := match.Start(), match.End()
		entry := m.entries[match.Pattern()]
		if start < position || !unicodeBoundary(value, start, end) || letterCount(entry.Name) < 3 && touchesDash(value, start, end) || entry.RequiresContext && !hasLocationContext(value, start) && !haarTitleLocation(value, field, entry, start, end) {
			continue
		}
		if visibleKinds[entry.Kind] {
			continue
		}
		token, found := tokens[entry.Name]
		if !found {
			label := "PLACE"
			if mode == ProtectionTyped {
				label = placeholderLabel(entry.Kind)
			}
			token = fmt.Sprintf("%s%s_%04d__", tokenPrefix, label, len(tokens)+1)
			tokens[entry.Name] = token
		}
		builder.WriteString(value[position:start])
		builder.WriteString(token)
		replacements = append(replacements, Replacement{Token: token, Original: value[start:end], Field: field, Kind: entry.Kind})
		position = end
	}
	if position == 0 {
		return value, replacements
	}
	builder.WriteString(value[position:])
	return builder.String(), replacements
}

func placeholderLabel(kind string) string {
	switch kind {
	case KindStreet:
		return "STREET"
	case KindDistrict:
		return "DISTRICT"
	case KindNeighbourhood:
		return "NEIGHBOURHOOD"
	case KindMunicipality:
		return "MUNICIPALITY"
	case KindTransit:
		return "TRANSIT"
	case KindPark:
		return "PARK"
	case KindSquare:
		return "SQUARE"
	case KindLandmark:
		return "LANDMARK"
	case KindTrainStation:
		return "TRAIN_STATION"
	case KindCommuterTrain:
		return "COMMUTER_TRAIN"
	case KindSubwaySystem:
		return "SUBWAY_SYSTEM"
	default:
		return "PLACE"
	}
}

func Restore(protected Protected, title, summary string) (string, string, error) {
	expected := map[string]map[string]int{"title": {}, "summary": {}}
	for _, replacement := range protected.Replacements {
		if replacement.Field != "title" && replacement.Field != "summary" {
			return "", "", fmt.Errorf("protected place token %s has unknown source field", replacement.Token)
		}
		expected[replacement.Field][replacement.Token]++
	}
	for field, value := range map[string]string{"title": title, "summary": summary} {
		actual := placeTokenPattern.FindAllString(value, -1)
		actualCounts := make(map[string]int, len(actual))
		for _, token := range actual {
			actualCounts[token]++
		}
		if !sameTokenCounts(expected[field], actualCounts) {
			return "", "", fmt.Errorf("protected place tokens in %s were missing, duplicated, moved, or unknown", field)
		}
		for _, token := range actual {
			if !standaloneToken(value, token) {
				return "", "", fmt.Errorf("protected place token %s was modified or inflected", token)
			}
		}
	}
	restored := make(map[string]bool, len(protected.Replacements))
	for _, replacement := range protected.Replacements {
		if restored[replacement.Token] {
			continue
		}
		title = strings.ReplaceAll(title, replacement.Token, replacement.Original)
		summary = strings.ReplaceAll(summary, replacement.Token, replacement.Original)
		restored[replacement.Token] = true
	}
	if strings.Contains(title, tokenPrefix) || strings.Contains(summary, tokenPrefix) {
		return "", "", errors.New("translation output contains an unknown place token")
	}
	return norm.NFC.String(title), norm.NFC.String(summary), nil
}

func sameTokenCounts(expected, actual map[string]int) bool {
	if len(expected) != len(actual) {
		return false
	}
	for token, count := range expected {
		if actual[token] != count {
			return false
		}
	}
	return true
}

func standaloneToken(value, token string) bool {
	for offset := 0; ; {
		index := strings.Index(value[offset:], token)
		if index < 0 {
			return true
		}
		start := offset + index
		end := start + len(token)
		if adjacentWordOrSuffix(value, start, end) {
			return false
		}
		offset = end
	}
}

func adjacentWordOrSuffix(value string, start, end int) bool {
	if start > 0 {
		character, _ := utf8.DecodeLastRuneInString(value[:start])
		if tokenAttachment(character) {
			return true
		}
	}
	if end < len(value) {
		character, _ := utf8.DecodeRuneInString(value[end:])
		return tokenAttachment(character)
	}
	return false
}

func tokenAttachment(character rune) bool {
	// Han prose does not use spaces between words, so an intact token may
	// legitimately touch a Chinese conjunction or particle on either side.
	if unicode.Is(unicode.Han, character) {
		return false
	}
	return unicode.IsLetter(character) || unicode.IsDigit(character) || unicode.IsMark(character) || character == '_'
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

// Police headlines use a separated dash before the final location. Recognize
// that convention only for the ambiguous municipality Haar, never in prose.
func haarTitleLocation(value, field string, entry Entry, start, end int) bool {
	if field != "title" || entry.Name != "Haar" || entry.Kind != KindMunicipality || strings.TrimSpace(value[end:]) != "" {
		return false
	}
	horizontalSpace := func(r rune) bool { return unicode.Is(unicode.Zs, r) || r == '\t' }
	prefix := value[:start]
	beforeSpace := strings.TrimRightFunc(prefix, horizontalSpace)
	if len(beforeSpace) == len(prefix) || beforeSpace == "" {
		return false
	}
	dash, size := utf8.DecodeLastRuneInString(beforeSpace)
	if dash != '–' && dash != '—' && dash != '-' {
		return false
	}
	beforeDash := beforeSpace[:len(beforeSpace)-size]
	if beforeDash == "" {
		return true
	}
	last, _ := utf8.DecodeLastRuneInString(beforeDash)
	return horizontalSpace(last)
}
