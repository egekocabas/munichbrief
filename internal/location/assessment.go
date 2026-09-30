package location

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

type Mention struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Role       string   `json:"role"`
	Source     string   `json:"source"`
	Evidence   string   `json:"evidence"`
	Candidates []Entity `json:"candidates,omitempty"`
}
type Interpretation struct {
	Scope           string    `json:"scope"`
	Mentions        []Mention `json:"mentions"`
	SummaryConflict bool      `json:"summary_conflict"`
	// Empty denotes the legacy v1 wording check; v2 explicitly records deferral.
	SummaryConflictStatus string `json:"summary_conflict_status,omitempty"`
	Reason                string `json:"reason"`
	CandidateVersion      string `json:"candidate_version,omitempty"`
	SourceConflict        bool   `json:"source_conflict,omitempty"`
}
type Source struct {
	SourceHash     string `json:"source_hash"`
	ContextHash    string `json:"context_hash"`
	SectionContext string `json:"section_context"`
	ReportNumber   string `json:"report_number"`
}
type Assessment struct {
	Outcome  string `json:"outcome"`
	Proposed *Area  `json:"proposed"`
	Interpretation
	Source          Source `json:"source"`
	ResolverVersion string `json:"resolver_version"`
}

func (a Assessment) Applicable() bool {
	return a.Proposed != nil && (a.Outcome == "confirmed" || a.Outcome == "corrected")
}

// Resolve treats model output as evidence to validate, not as a geographic
// authority. Only the bundled catalog can supply public names and area types.
func Resolve(input Interpretation, original Area, title, body string, source Source, withheld bool) (Assessment, error) {
	input.Mentions = append([]Mention(nil), input.Mentions...)
	a := Assessment{Interpretation: input, Source: source, ResolverVersion: CatalogVersion, Outcome: "ambiguous"}
	if withheld {
		a.Outcome = "withheld"
		a.Mentions = nil
		a.Reason = "Source intentionally minimized; retain canonical area."
		a.SummaryConflict = false
		return a, nil
	}
	if len(input.Mentions) > 30 || len(input.Reason) > 1000 {
		return a, fmt.Errorf("location assessment exceeds bounds")
	}
	switch input.Scope {
	case "single", "multiple", "route", "unknown", "source_problem":
	default:
		return a, fmt.Errorf("invalid location scope")
	}
	var primary []Entity
	unknownArea := false
	for index, m := range input.Mentions {
		if normalize(m.Name) == "" || len(m.Name) > 200 || normalize(m.Evidence) == "" || len(m.Evidence) > 1600 {
			return a, fmt.Errorf("invalid location evidence bounds")
		}
		switch m.Role {
		case "primary", "related", "arrest_recovery", "route", "residence", "police_hospital", "topic":
		default:
			return a, fmt.Errorf("invalid location role")
		}
		switch m.Kind {
		case "area", "street", "venue", "other":
		default:
			return a, fmt.Errorf("invalid location kind")
		}
		var text string
		switch m.Source {
		case "title":
			text = title
		case "body":
			text = body
		case "context":
			text = source.SectionContext
		default:
			return a, fmt.Errorf("invalid evidence source")
		}
		if !containsName(text, m.Evidence) || !containsName(m.Evidence, m.Name) {
			return a, fmt.Errorf("ungrounded location evidence")
		}
		candidates := Lookup(m.Name)
		// Context is evidence for the festival, never for a guest's home or an
		// unrelated tent. The model must still identify it as the primary scene.
		if len(candidates) == 0 && IsGenericFestivalVenue(m.Name) && m.Kind == "venue" && (source.SectionContext == "Wiesnberichte" || source.SectionContext == "Wiesn-Berichte") {
			candidates = Lookup("Oktoberfest")
		}
		a.Mentions[index].Candidates = candidates
		if m.Role != "primary" || (m.Kind != "area" && m.Kind != "venue") {
			continue
		}
		if len(candidates) != 1 {
			if m.Kind == "area" || len(candidates) > 1 {
				unknownArea = true
			}
			continue
		}
		e := candidates[0]
		if e.Kind == "venue" {
			var ok bool
			e, ok = parent(e.Parent)
			if !ok {
				unknownArea = true
				continue
			}
		}
		primary = append(primary, e)
	}
	if input.Scope == "source_problem" {
		a.Outcome = "source_problem"
		return a, nil
	}
	if input.Scope == "multiple" || input.Scope == "route" || unknownArea {
		return a, nil
	}
	if len(primary) == 0 {
		if len(input.Mentions) == 0 {
			a.Outcome = "no_location"
		}
		return a, nil
	}
	selected := primary[0]
	for _, e := range primary[1:] {
		if e.ID == selected.ID || ancestor(e.ID, selected.ID) {
			continue
		}
		if ancestor(selected.ID, e.ID) {
			selected = e
			continue
		}
		return a, nil
	}
	// "unknown" is an explicit abstention; catalog lookup must not overrule it.
	if input.Scope != "single" {
		return a, nil
	}
	a.Proposed = &selected.Area
	a.Outcome = "corrected"
	if original.Name == selected.Name && original.Type == selected.Type {
		a.Outcome = "confirmed"
	}
	return a, nil
}

func containsName(text, name string) bool {
	text, name = normalizeEvidence(text), normalizeEvidence(name)
	for offset := 0; offset <= len(text); {
		index := strings.Index(text[offset:], name)
		if index < 0 {
			return false
		}
		index += offset
		before, after := rune(' '), rune(' ')
		if index > 0 {
			before, _ = utf8.DecodeLastRuneInString(text[:index])
		}
		if end := index + len(name); end < len(text) {
			after, _ = utf8.DecodeRuneInString(text[end:])
		}
		if !locationWordRune(before) && !locationWordRune(after) {
			return true
		}
		offset = index + len(name)
	}
	return false
}

// IsGenericFestivalVenue recognizes a venue label, not its geographic identity.
// Resolve still requires verified section context before assigning an area.
func IsGenericFestivalVenue(name string) bool {
	switch normalize(name) {
	case "festzelt", "festzelts", "festgelände", "festgeländes", "wiesnzelt", "wiesnzelts", "wiesn-zelt", "wiesn-zelts":
		return true
	}
	return false
}

// A hyphen joins compound place/street names, not two independent mentions.
func locationWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' }

// Preserve spaces around title separators; catalog alias normalization removes
// those spaces and must not be used to determine source word boundaries.
func normalizeEvidence(s string) string {
	s = strings.ToLower(norm.NFC.String(s))
	s = strings.NewReplacer("–", "-", "—", "-").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}
