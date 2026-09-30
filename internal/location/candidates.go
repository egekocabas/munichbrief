package location

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const CandidateVersion = "source-area-candidates-v2"

// CandidateEvidence is copied from the source. The model selects an ID and
// never supplies geographic identity, quotations, offsets, or field names.
type CandidateEvidence struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Text   string `json:"text"`
}
type Candidate struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Kind     string              `json:"kind"`
	Evidence []CandidateEvidence `json:"evidence"`
}
type sourceMatch struct {
	start, end int
	name       string
	entity     Entity
}
type catalogMatcher struct{ pattern *regexp.Regexp }

var sourceMatchers = buildSourceMatchers()

func buildSourceMatchers() []catalogMatcher {
	names := map[string]bool{}
	for _, e := range catalog {
		for _, name := range append([]string{e.Name}, e.Aliases...) {
			names[name] = true
		}
	}
	for _, name := range []string{"Festzelt", "Festzelts", "Festgelände", "Festgeländes", "Wiesnzelt", "Wiesnzelts", "Wiesn-Zelt", "Wiesn-Zelts"} {
		names[name] = true
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	var out []catalogMatcher
	for _, name := range ordered {
		var pattern strings.Builder
		pattern.WriteString("(?i)")
		for _, r := range name {
			switch r {
			case ' ':
				pattern.WriteString(`\s+`)
			case '-', '–', '—', '/':
				pattern.WriteString(`\s*[-–—/]\s*`)
			default:
				pattern.WriteString(regexp.QuoteMeta(string(r)))
			}
		}
		out = append(out, catalogMatcher{regexp.MustCompile(pattern.String())})
	}
	return out
}

var directionSuffix = regexp.MustCompile(`(?i)^\s+(?:west|ost|nord|süd|sued)(?:\s|[.,;:]|$)`)
var countyPrefix = regexp.MustCompile(`(?i)\bLandkreis\s*$`)

func sourceMatches(text string, festivalContext bool) []sourceMatch {
	var matches []sourceMatch
	for _, matcher := range sourceMatchers {
		for _, span := range matcher.pattern.FindAllStringIndex(text, -1) {
			start, end := span[0], span[1]
			before, after := rune(' '), rune(' ')
			if start > 0 {
				before, _ = utf8.DecodeLastRuneInString(text[:start])
			}
			if end < len(text) {
				after, _ = utf8.DecodeRuneInString(text[end:])
			}
			if locationWordRune(before) || locationWordRune(after) {
				continue
			}
			name := text[start:end]
			// A county reference or an unrecognised qualified locality must not be
			// reduced to a same-spelled city/base locality by substring extraction.
			if directionSuffix.MatchString(text[end:]) {
				continue
			}
			entities := Lookup(name)
			if len(entities) == 1 {
				if entities[0].ID == "city:munich" && countyPrefix.MatchString(text[:start]) {
					continue
				}
				matches = append(matches, sourceMatch{start, end, name, entities[0]})
			} else if len(entities) == 0 && festivalContext && IsGenericFestivalVenue(name) {
				matches = append(matches, sourceMatch{start, end, name, Entity{Area: Area{ID: "generic:" + normalize(name), Name: name}, Kind: "venue"}})
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.start != b.start {
			return a.start < b.start
		}
		if a.end != b.end {
			return a.end > b.end
		}
		return a.entity.ID < b.entity.ID
	})
	var selected []sourceMatch
	for _, m := range matches {
		if len(selected) > 0 && m.start < selected[len(selected)-1].end {
			continue
		}
		selected = append(selected, m)
	}
	return selected
}

func sourceExcerpt(text string, start, end int) string {
	left, right := max(0, start-100), min(len(text), end+100)
	for left > 0 && !strings.ContainsRune(" \n\r\t", rune(text[left-1])) {
		left--
	}
	for right < len(text) && !strings.ContainsRune(" \n\r\t", rune(text[right])) {
		right++
	}
	return strings.TrimSpace(text[left:right])
}

// SourceCandidates offers only catalog-supported areas/venues explicitly
// present in the source. It never consults the street district hint or geocodes.
// Evidence is bounded for prompt size; the full report is supplied separately.
func SourceCandidates(title, body string, source Source) []Candidate {
	candidates := []Candidate{}
	byEntity := map[string]int{}
	festival := source.SectionContext == "Wiesnberichte" || source.SectionContext == "Wiesn-Berichte"
	for _, field := range []struct{ name, text string }{{"original_title", title}, {"incident_body", body}, {"section_context", source.SectionContext}} {
		for _, m := range sourceMatches(field.text, festival) {
			if m.entity.ID == "venue:theresienwiese" && !festivalSceneEvidence(field.name, field.text, m.start, m.end) {
				continue
			}
			index, exists := byEntity[m.entity.ID]
			if !exists {
				index = len(candidates)
				byEntity[m.entity.ID] = index
				kind := "area"
				if m.entity.Kind == "venue" {
					kind = "venue"
				}
				candidates = append(candidates, Candidate{ID: fmt.Sprintf("c%d", index+1), Name: m.entity.Name, Kind: kind})
			}
			if len(candidates[index].Evidence) < 3 {
				candidates[index].Evidence = append(candidates[index].Evidence, CandidateEvidence{Source: field.name, Name: m.name, Text: sourceExcerpt(field.text, m.start, m.end)})
			}
		}
	}
	return candidates
}

var headingSeparator = regexp.MustCompile(`\s+[–—-]\s+`)
var sceneSiteBefore = regexp.MustCompile(`(?i)(?:\b(?:mehrfamilienhaus|einfamilienhaus|wohnung|gaststätte|diskothek|baustelle|park|krankenhaus|geschäft|lokal|restaurant|schule|gebäude|tatort|unfallort|einsatzort)\s+(?:lag\s+|liegt\s+|befand sich\s+)?(?:in|im)\s+|\b(?:kam es|geschah|passierte|ereignete sich)\s+(?:in|im)\s+)$`)
var sceneSiteAfter = regexp.MustCompile(`(?i)^\s+(?:kam es|ereignete sich|geschah|passierte)\b`)
var backgroundSceneMarker = regexp.MustCompile(`(?i)(?:wohnsitz|wohnhaft|wohnte|lebt(?:e)?\b|zur behandlung|eingeliefert|überstellt|wohnanschrift)`)
var relatedSceneMarker = regexp.MustCompile(`(?i)(?:bereits zuvor|früher|vorherigen|vorangegangenen|zurückliegenden|zum vergleich|siehe medieninformation)`)

// SourceAreaConflict recognises explicit scene-area contradictions, rather
// than treating every residence, authority or prior event as another scene.
// This is deliberately conservative pattern coverage, not a language parser.
func SourceAreaConflict(title, body string) bool {
	var heading []Entity
	for _, separator := range headingSeparator.FindAllStringIndex(title, -1) {
		matches := Lookup(strings.TrimSpace(title[separator[1]:]))
		if len(matches) == 1 && matches[0].Kind != "venue" {
			heading = matches
			break
		}
	}
	if len(heading) != 1 {
		return false
	}
	for _, m := range sourceMatches(body, false) {
		if m.entity.Kind == "venue" {
			continue
		}
		prefix := body[:m.start]
		// Inspect the current sentence/paragraph only, preserving date punctuation.
		boundary := max(strings.LastIndex(prefix, "\n"), strings.LastIndex(prefix, ". ")+1)
		if boundary >= 0 {
			prefix = prefix[boundary:]
		}
		if relatedSceneMarker.MatchString(prefix) || backgroundSceneMarker.MatchString(prefix) {
			continue
		}
		if !sceneSiteBefore.MatchString(prefix) && !sceneSiteAfter.MatchString(body[m.end:]) {
			continue
		}
		a, b := heading[0].ID, m.entity.ID
		if a != b && !ancestor(a, b) && !ancestor(b, a) {
			return true
		}
	}
	return false
}
