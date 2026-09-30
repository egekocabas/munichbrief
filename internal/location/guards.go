package location

import (
	"regexp"
	"strings"
)

var captionStart = regexp.MustCompile(`(?i)^(?:sonderbeilage|anlage|anhang|download|zwischenbilanz|abschlussbilanz|bilanz)\b`)
var attachmentTitle = regexp.MustCompile(`(?i)\b(?:sonderbeilage|anlage|anhang)\b`)
var narrativeVerb = regexp.MustCompile(`(?i)\b(?:kam|kamen|fand|fanden|befand|befanden|wurde|wurden|ereignete|geschah|findet|fuhr|fuhr[et]n|kontrollierten|beobachteten)\b`)

// CaptionOnlySource requires an attachment/briefing label and no narrative;
// short real incident reports are not rejected just because they are short.
func CaptionOnlySource(title, body string) bool {
	body = strings.TrimSpace(body)
	if len([]rune(body)) > 240 || len(strings.Fields(body)) > 24 || strings.Contains(body, "\n\n") || narrativeVerb.MatchString(body) {
		return false
	}
	return captionStart.MatchString(body) && (attachmentTitle.MatchString(title) || strings.HasPrefix(strings.ToLower(body), "sonderbeilage"))
}

var festivalLocative = regexp.MustCompile(`(?i)(?:\bauf (?:dem|der)|\bam|\bvom|\bvon (?:der|da)|\bzur)\s*$`)
var festivalTopic = regexp.MustCompile(`(?i)(?:\b(?:thema|sicherheit|bilanz|pressekonferenz|informiert|informieren|diskutiert|berichtet|berichteten)\b)`)

func festivalSceneEvidence(field, text string, start, end int) bool {
	if field == "original_title" {
		for _, s := range headingSeparator.FindAllStringIndex(text, -1) {
			if strings.TrimSpace(text[s[1]:]) == strings.TrimSpace(text[start:end]) {
				return true
			}
		}
		return false
	}
	prefix := text[:start]
	boundary := max(strings.LastIndex(prefix, "\n"), strings.LastIndex(prefix, ". ")+1)
	if boundary >= 0 {
		prefix = prefix[boundary:]
	}
	return festivalLocative.MatchString(prefix) && !festivalTopic.MatchString(prefix)
}

var streetSceneBefore = regexp.MustCompile(`(?i)\b[\p{L}][\p{L}-]*(?:straße|strasse|ring|platz|allee|weg)\s+(?:in|im)\s+$`)
var movingScene = regexp.MustCompile(`(?i)\b(?:fuhr|befuhr|kollidierte|ereignete|kam|befand|befanden|überquerte)\b`)
var residenceStreet = regexp.MustCompile(`(?i)(?:wohn(?:te|en|haft)|lebt(?:e)?|wohnsitz|wohnanschrift)\s+(?:in|an)\s+(?:der\s+)?[\p{L}][\p{L}-]*(?:straße|strasse|ring|platz|allee|weg)\s+(?:in|im)\s+$`)

// BroadensSupportedScene protects a canonical child area only when the original
// body independently supports it as a scene. It never maps a street to an area.
// Retention is deliberately separate from an automatic type-only correction.
func BroadensSupportedScene(original, proposed Area, body string) bool {
	current := Lookup(original.Name)
	if len(current) != 1 || current[0].ID == proposed.ID || !ancestor(proposed.ID, current[0].ID) {
		return false
	}
	for _, m := range sourceMatches(body, false) {
		if m.entity.ID != current[0].ID {
			continue
		}
		prefix := body[:m.start]
		boundary := max(strings.LastIndex(prefix, "\n"), strings.LastIndex(prefix, ". ")+1)
		if boundary >= 0 {
			prefix = prefix[boundary:]
		}
		if relatedSceneMarker.MatchString(prefix) {
			continue
		}
		explicit := (sceneSiteBefore.MatchString(prefix) || sceneSiteAfter.MatchString(body[m.end:])) && !backgroundSceneMarker.MatchString(prefix)
		street := streetSceneBefore.MatchString(prefix) && movingScene.MatchString(prefix) && !residenceStreet.MatchString(prefix)
		if explicit || street {
			return true
		}
	}
	return false
}
