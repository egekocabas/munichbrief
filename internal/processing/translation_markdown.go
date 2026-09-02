package processing

import (
	"errors"
	"regexp"
	"strings"

	"github.com/egekocabas/munichbrief/internal/gazetteer"
)

type protectedMarkdown struct {
	decorations []markdownDecoration
}

type markdownDecoration struct {
	field, token string
	occurrence   int
	prefix       string
	suffix       string
}

var (
	protectedMarkdownLinkPattern = regexp.MustCompile(`\[(__MB_PLACE_[0-9]{4}__)\]\((https://[^)\s]+)\)`)
	protectedMarkdownBoldPattern = regexp.MustCompile(`\*\*(__MB_PLACE_[0-9]{4}__)\*\*`)
)

func stripProtectedMarkdown(value, field string) (string, []markdownDecoration) {
	value, links := stripMarkdownPattern(value, field, protectedMarkdownLinkPattern, func(match []string) (string, string, string) {
		return match[1], "[", "](" + match[2] + ")"
	})
	value, bold := stripMarkdownPattern(value, field, protectedMarkdownBoldPattern, func(match []string) (string, string, string) {
		return match[1], "**", "**"
	})
	return value, append(links, bold...)
}

func stripMarkdownPattern(value, field string, pattern *regexp.Regexp, decoration func([]string) (string, string, string)) (string, []markdownDecoration) {
	matches := pattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value, nil
	}
	var builder strings.Builder
	position := 0
	records := make([]markdownDecoration, 0, len(matches))
	for _, indices := range matches {
		parts := make([]string, len(indices)/2)
		for index := range parts {
			if indices[index*2] >= 0 {
				parts[index] = value[indices[index*2]:indices[index*2+1]]
			}
		}
		token, prefix, suffix := decoration(parts)
		occurrence := strings.Count(value[:indices[0]], token) + 1
		builder.WriteString(value[position:indices[0]])
		builder.WriteString(token)
		records = append(records, markdownDecoration{field: field, token: token, occurrence: occurrence, prefix: prefix, suffix: suffix})
		position = indices[1]
	}
	builder.WriteString(value[position:])
	return builder.String(), records
}

func protectMarkdown(protected *gazetteer.Protected) protectedMarkdown {
	var result protectedMarkdown
	protected.Title, result.decorations = stripProtectedMarkdown(protected.Title, "title")
	summary, decorations := stripProtectedMarkdown(protected.Summary, "summary")
	protected.Summary = summary
	result.decorations = append(result.decorations, decorations...)
	return result
}

func (protected protectedMarkdown) restore(title, summary string) (string, string, error) {
	values := map[string]*string{"title": &title, "summary": &summary}
	for _, decoration := range protected.decorations {
		value, found := values[decoration.field]
		if !found {
			return "", "", errors.New("protected Markdown has an unknown field")
		}
		restored, found := decorateTokenOccurrence(*value, decoration)
		if !found {
			return "", "", errors.New("protected Markdown token occurrence is missing")
		}
		*value = restored
	}
	return title, summary, nil
}

func decorateTokenOccurrence(value string, decoration markdownDecoration) (string, bool) {
	position := 0
	for occurrence := 1; ; occurrence++ {
		index := strings.Index(value[position:], decoration.token)
		if index < 0 {
			return value, false
		}
		index += position
		if occurrence == decoration.occurrence {
			return value[:index] + decoration.prefix + decoration.token + decoration.suffix + value[index+len(decoration.token):], true
		}
		position = index + len(decoration.token)
	}
}
