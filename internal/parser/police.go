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
	Kind    string
	Text    string
	Bold    bool
	Node    *html.Node
	Section *html.Node
	InPress bool
}

// ParsePoliceRelease extracts incidents from supported police release page
// shapes. It never retains or renders the original HTML.
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
	releaseContext := dedicatedFestivalRelease(pressContent)
	sectionContext := releaseContext
	var contextSection *html.Node
	// Daily bundles keep the contents inside bp-presse and details in sibling
	// sections. Context and numbered entries in that contents block are not
	// authoritative report data. Standalone releases keep their details inside.
	externalDetails := false
	for _, candidate := range blocks {
		if !candidate.InPress && numberedHeadingPattern.MatchString(candidate.Text) && (candidate.Kind == "h2" || candidate.Kind == "h3" || candidate.Bold) {
			externalDetails = true
		}
	}

	// Numbered report headings and release section labels close the preceding
	// incident. Internal subheadings remain part of its body.
	finishCurrent := func() {
		if current == nil {
			return
		}
		current.BodyDE = strings.Join(bodyBlocks, "\n\n")
		current.ContentHash = hash(current.Number, current.TitleDE, current.BodyDE)
		current.ContextHash = hash("section-context-v2", current.SectionContext)
		incidents = append(incidents, *current)
		current = nil
		bodyBlocks = nil
	}

	for index, candidate := range blocks {
		if candidate.Text == "" || (externalDetails && candidate.InPress) {
			continue
		}
		if candidate.Section != contextSection {
			sectionContext = releaseContext
			contextSection = candidate.Section
		}
		if context := festivalContext(candidate.Text); context != "" {
			sectionContext = context
		} else if (candidate.Kind == "h2" || candidate.Kind == "h3") && !numberedHeadingPattern.MatchString(candidate.Text) {
			sectionContext = ""
		}
		if releaseSectionHeading(blocks, index) {
			finishCurrent()
			continue
		}
		if match := reportHeading(blocks, index); match != nil {
			finishCurrent()
			current = &domain.Incident{
				Number:         match[1],
				Position:       len(incidents),
				TitleDE:        strings.TrimSpace(match[2]),
				SectionContext: sectionContext,
			}
			continue
		}
		if current != nil {
			bodyBlocks = append(bodyBlocks, candidate.Text)
		}
	}
	finishCurrent()

	if len(incidents) == 0 {
		incident, ok := parseUnnumberedStandalone(pressContent)
		if !ok {
			return ParsedRelease{ExtractedText: extractedText}, ErrNoIncidents
		}
		incidents = append(incidents, incident)
	}
	incidentHashes := make([]string, 0, len(incidents))
	for _, incident := range incidents {
		incidentHashes = append(incidentHashes, incident.ContentHash, incident.ContextHash)
	}
	return ParsedRelease{
		ExtractedText: extractedText,
		SourceHash:    hash(incidentHashes...),
		Incidents:     incidents,
	}, nil
}

// reportHeading retains the detail-heading contract: bold paragraphs need a
// following prose paragraph in the same section. Contents entries are filtered
// by the caller before they can become reports.
func reportHeading(blocks []block, index int) []string {
	candidate := blocks[index]
	paragraphHeading := candidate.Kind == "p" && candidate.Bold && index+1 < len(blocks) && blocks[index+1].Kind == "p" && blocks[index+1].Section == candidate.Section && !numberedHeadingPattern.MatchString(blocks[index+1].Text)
	if candidate.Kind == "h2" || candidate.Kind == "h3" || paragraphHeading {
		return numberedHeadingPattern.FindStringSubmatch(candidate.Text)
	}
	return nil
}

// releaseSectionHeading recognizes a heading-only prefix to the next report,
// such as Wiesnberichte or a wanted-person withdrawal section. Prose between a
// heading and the next report makes it an internal subheading instead. Requiring
// a report in the same HTML section avoids dropping trailing headings or body
// continuations across containers. h4 and bold prose retain their old behavior.
func releaseSectionHeading(blocks []block, index int) bool {
	first := blocks[index]
	if (first.Kind != "h2" && first.Kind != "h3") || numberedHeadingPattern.MatchString(first.Text) {
		return false
	}
	for next := index + 1; next < len(blocks); next++ {
		candidate := blocks[next]
		if candidate.Section != first.Section || candidate.InPress != first.InPress || !headingOnlyGap(blocks[next-1].Node, candidate.Node) {
			return false
		}
		if reportHeading(blocks, next) != nil {
			return true
		}
		if (candidate.Kind != "h2" && candidate.Kind != "h3") || numberedHeadingPattern.MatchString(candidate.Text) {
			return false
		}
	}
	return false
}

// Block collection intentionally supports only a few source elements. Do not
// mistake an internal heading before a list, image, or uncollected text for a
// release label merely because the next collected block is a report heading.
func headingOnlyGap(previous, next *html.Node) bool {
	after := func(node *html.Node) *html.Node {
		for node != nil {
			if node.NextSibling != nil {
				return node.NextSibling
			}
			node = node.Parent
		}
		return nil
	}
	for node := after(previous); node != nil; {
		if node == next {
			return true
		}
		if node.Type == html.TextNode && strings.TrimSpace(node.Data) != "" {
			return false
		}
		if node.Type == html.ElementNode {
			switch node.Data {
			case "div", "section", "span", "strong", "b", "p", "br", "hr":
				// Empty layout/formatting nodes can separate adjacent headings.
			default:
				return false
			}
		}
		if node.FirstChild != nil {
			node = node.FirstChild
		} else {
			node = after(node)
		}
	}
	return false
}

// parseUnnumberedStandalone accepts only the official single-release shape. A
// numbered-looking block is rejected so a damaged bundle cannot be collapsed
// into one incident when its detail headings stop matching the primary parser.
func parseUnnumberedStandalone(pressContent *html.Node) (domain.Incident, bool) {
	headline := findElement(pressContent, func(node *html.Node) bool {
		return node.Data == "bp-headline"
	})
	if headline == nil {
		return domain.Incident{}, false
	}
	title := normalizeText(attributeValue(headline, "title"))
	if title == "" {
		return domain.Incident{}, false
	}
	bodySection := findElement(pressContent, func(node *html.Node) bool {
		return node.Data == "section" && hasClasses(node, "bp-flex", "bp-textblock-image")
	})
	if bodySection == nil {
		return domain.Incident{}, false
	}
	bodyContent := findElement(bodySection, func(node *html.Node) bool {
		return node.Data == "div" && hasClasses(node, "bp-iwe2")
	})
	if bodyContent == nil {
		return domain.Incident{}, false
	}

	var blocks []block
	collectBlocks(bodyContent, &blocks)
	bodyBlocks := make([]string, 0, len(blocks))
	hasParagraph := false
	for _, candidate := range blocks {
		if numberedHeadingPattern.MatchString(candidate.Text) {
			return domain.Incident{}, false
		}
		if (candidate.Kind == "h2" || candidate.Kind == "h3") && candidate.Text == title {
			continue
		}
		bodyBlocks = append(bodyBlocks, candidate.Text)
		if candidate.Kind == "p" {
			hasParagraph = true
		}
	}
	if !hasParagraph || len(bodyBlocks) == 0 {
		return domain.Incident{}, false
	}
	body := strings.Join(bodyBlocks, "\n\n")
	return domain.Incident{
		Position:    0,
		TitleDE:     title,
		BodyDE:      body,
		ContentHash: hash("", title, body),
		ContextHash: hash("section-context-v2", ""),
	}, true
}

func collectBlocks(node *html.Node, blocks *[]block) {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode {
			switch child.Data {
			case "h2", "h3", "h4", "p":
				text := normalizeText(textContent(child))
				if text != "" {
					*blocks = append(*blocks, block{Kind: child.Data, Text: text, Bold: whollyBold(child), Node: child,
						Section: closestAncestor(child, func(n *html.Node) bool { return n.Data == "section" }),
						InPress: closestAncestor(child, func(n *html.Node) bool { return n.Data == "section" && hasClasses(n, "bp-template", "bp-presse") }) != nil,
					})
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

// whollyBold distinguishes a report heading from prose containing emphasis.
func whollyBold(node *html.Node) bool {
	bold := findElement(node, func(n *html.Node) bool { return n.Data == "strong" || n.Data == "b" })
	return bold != nil && normalizeText(textContent(bold)) == normalizeText(textContent(node))
}

func festivalContext(text string) string {
	switch strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), ":")) {
	case "Wiesnbericht", "Wiesn-Bericht", "Wiesnberichte", "Wiesn-Berichte":
		return "Wiesnberichte"
	default:
		return ""
	}
}

var dedicatedFestivalTitle = regexp.MustCompile(`^Wiesnberichte? der Polizei München vom [0-9]{2}\.[0-9]{2}\.[0-9]{4}(?: auf Boarisch)?$`)

// A dedicated release title establishes event context, never scene location.
// Contents headings in mixed daily releases remain excluded.
func dedicatedFestivalRelease(press *html.Node) string {
	headline := findElement(press, func(n *html.Node) bool { return n.Data == "bp-headline" })
	if headline != nil && dedicatedFestivalTitle.MatchString(normalizeText(attributeValue(headline, "title"))) {
		return "Wiesnberichte"
	}
	return ""
}
