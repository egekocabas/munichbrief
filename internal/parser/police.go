package parser

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/egekocabas/munichbrief/internal/domain"
	"golang.org/x/net/html"
)

var (
	ErrPressContentNotFound = errors.New("press-release content container not found")
	ErrNoIncidents          = errors.New("no numbered incidents found")
	numberedHeadingPattern  = regexp.MustCompile(`^([0-9]{1,6})\.\s*(.+)$`)
)

// ParsedRelease contains the normalized incidents and a deterministic hash of
// their content, not of incidental source markup.
type ParsedRelease struct {
	ExtractedText string
	SourceHash    string
	Incidents     []domain.Incident
}

type block struct {
	Kind string
	Text string
}

// ParsePoliceRelease extracts numbered incidents from supported police release
// page shapes. It never retains or renders the original HTML.
func ParsePoliceRelease(contents []byte) (ParsedRelease, error) {
	document, err := html.Parse(bytes.NewReader(contents))
	if err != nil {
		return ParsedRelease{}, fmt.Errorf("parse article HTML: %w", err)
	}
	pressContent := findElement(document, func(node *html.Node) bool {
		return node.Data == "section" && hasClasses(node, "bp-template", "bp-presse")
	})
	if pressContent == nil {
		return ParsedRelease{}, ErrPressContentNotFound
	}
	releaseContent := pressContent
	if mainContent := closestAncestor(pressContent, func(node *html.Node) bool {
		return node.Data == "main" && attributeValue(node, "id") == "readspeaker_lesen"
	}); mainContent != nil {
		releaseContent = mainContent
	}

	var blocks []block
	collectBlocks(releaseContent, &blocks)
	extractedText := extractReleaseText(releaseContent)
	var incidents []domain.Incident
	var current *domain.Incident
	var bodyBlocks []string

	// A new numbered heading closes the preceding incident. Non-numbered headings
	// remain part of that incident's body because real releases use them as
	// subheadings.
	finishCurrent := func() {
		if current == nil {
			return
		}
		current.BodyDE = strings.Join(bodyBlocks, "\n\n")
		current.ContentHash = hash(current.Number, current.TitleDE, current.BodyDE)
		incidents = append(incidents, *current)
		current = nil
		bodyBlocks = nil
	}

	for _, candidate := range blocks {
		if candidate.Text == "" {
			continue
		}
		if candidate.Kind == "h2" || candidate.Kind == "h3" {
			if match := numberedHeadingPattern.FindStringSubmatch(candidate.Text); match != nil {
				finishCurrent()
				current = &domain.Incident{
					Number:   match[1],
					Position: len(incidents),
					TitleDE:  strings.TrimSpace(match[2]),
				}
				continue
			}
		}
		if current != nil {
			bodyBlocks = append(bodyBlocks, candidate.Text)
		}
	}
	finishCurrent()

	if len(incidents) == 0 {
		return ParsedRelease{ExtractedText: extractedText}, ErrNoIncidents
	}
	incidentHashes := make([]string, 0, len(incidents))
	for _, incident := range incidents {
		incidentHashes = append(incidentHashes, incident.ContentHash)
	}
	return ParsedRelease{
		ExtractedText: extractedText,
		SourceHash:    hash(incidentHashes...),
		Incidents:     incidents,
	}, nil
}

func collectBlocks(node *html.Node, blocks *[]block) {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode {
			switch child.Data {
			case "h2", "h3", "h4", "p":
				text := normalizeText(textContent(child))
				if text != "" {
					*blocks = append(*blocks, block{Kind: child.Data, Text: text})
				}
				continue
			}
		}
		collectBlocks(child, blocks)
	}
}

func textContent(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && (current.Data == "script" || current.Data == "style" || current.Data == "noscript") {
			return
		}
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
		}
		if current.Type == html.ElementNode && current.Data == "br" {
			builder.WriteByte('\n')
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return builder.String()
}

func normalizeText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func findElement(node *html.Node, predicate func(*html.Node) bool) *html.Node {
	if node.Type == html.ElementNode && predicate(node) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findElement(child, predicate); found != nil {
			return found
		}
	}
	return nil
}

func closestAncestor(node *html.Node, predicate func(*html.Node) bool) *html.Node {
	for current := node.Parent; current != nil; current = current.Parent {
		if current.Type == html.ElementNode && predicate(current) {
			return current
		}
	}
	return nil
}

func attributeValue(node *html.Node, key string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == key {
			return attribute.Val
		}
	}
	return ""
}

func hasClasses(node *html.Node, required ...string) bool {
	classes := make(map[string]bool)
	for _, attribute := range node.Attr {
		if attribute.Key == "class" {
			for _, class := range strings.Fields(attribute.Val) {
				classes[class] = true
			}
		}
	}
	for _, class := range required {
		if !classes[class] {
			return false
		}
	}
	return true
}

func hash(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%x", digest[:])
}

// extractReleaseText preserves readable source content, including structures the
// incident parser may not recognize, without retaining markup or executable text.
func extractReleaseText(root *html.Node) string {
	var parts []string
	var current strings.Builder
	flush := func() {
		if text := normalizeText(current.String()); text != "" {
			parts = append(parts, text)
		}
		current.Reset()
	}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "noscript") {
			return
		}
		boundary := false
		if node.Type == html.ElementNode {
			switch node.Data {
			case "p", "div", "section", "article", "header", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "li", "ul", "ol", "blockquote", "pre", "tr", "td", "th", "br":
				boundary = true
			}
		}
		if boundary {
			flush()
		}
		if node.Type == html.TextNode {
			current.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if boundary {
			flush()
		}
	}
	walk(root)
	flush()
	return strings.Join(parts, "\n\n")
}
