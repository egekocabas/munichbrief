// Package translationcheck validates UI catalogs against production source.
// It is developer tooling and is not imported by the application.
package translationcheck

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"text/template"
	"text/template/parse"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/egekocabas/munichbrief/internal/languages"
	"golang.org/x/text/unicode/norm"
)

type Issue struct {
	Path    string
	Line    int
	Message string
}

type Summary struct{ Languages, Messages int }

type catalog map[string]map[string][]string // message -> plural form -> placeholders

var pluralForms = []string{"zero", "one", "two", "few", "many", "other"}

// Check reads only source and catalogs: it needs no server, database, or network.
// Structural errors are reported first so malformed files cannot cause a flood
// of misleading unused/missing-key diagnostics.
func Check(root string) (Summary, []Issue) {
	definitions := languages.Registered()
	var summary Summary
	if err := languages.Validate(definitions); err != nil {
		return summary, []Issue{{Path: "internal/languages/languages.go", Message: err.Error()}}
	}
	files := os.DirFS(root)
	catalogs := make(map[string]catalog)
	var issues []Issue
	for _, definition := range definitions {
		name := path.Join("internal/web", definition.Catalog)
		messages, failures := readCatalog(files, name)
		catalogs[name] = messages
		issues = append(issues, failures...)
	}
	names, err := fs.Glob(files, "internal/web/locales/*.toml")
	if err != nil {
		issues = append(issues, Issue{Path: "internal/web/locales", Message: err.Error()})
	}
	for _, name := range names {
		if _, registered := catalogs[name]; !registered {
			issues = append(issues, Issue{Path: name, Message: "catalog is not registered in internal/languages/languages.go"})
		}
	}
	summary.Languages = len(definitions)
	canonical := path.Join("internal/web", languages.Canonical(definitions).Catalog)
	summary.Messages = len(catalogs[canonical])
	if len(issues) == 0 {
		used, failures := SourceUsage(root)
		issues = append(issues, failures...)
		// Unknown dynamic lookups make unused-key conclusions unreliable.
		if len(failures) == 0 {
			issues = append(issues, compareCatalogs(catalogs, canonical, used)...)
		}
	}
	sortIssues(issues)
	return summary, issues
}

func sortIssues(issues []Issue) {
	slices.SortFunc(issues, func(a, b Issue) int {
		if n := strings.Compare(a.Path, b.Path); n != 0 {
			return n
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return strings.Compare(a.Message, b.Message)
	})
}

func readCatalog(files fs.FS, name string) (catalog, []Issue) {
	result := make(catalog)
	var issues []Issue
	add := func(message string) { issues = append(issues, Issue{Path: name, Message: message}) }
	contents, err := fs.ReadFile(files, name)
	if err != nil {
		add("cannot read registered catalog: " + err.Error())
		return result, issues
	}
	if !utf8.Valid(contents) || !norm.NFC.IsNormal(contents) {
		add("catalog must contain valid UTF-8 in NFC normalization")
		return result, issues
	}
	var raw map[string]any
	if err := toml.Unmarshal(contents, &raw); err != nil {
		add("invalid TOML: " + err.Error())
		return result, issues
	}
	if len(raw) == 0 {
		add("catalog contains no messages")
	}
	for id, value := range raw {
		if !norm.NFC.IsNormalString(id) {
			add(fmt.Sprintf("%s: decoded message ID must use NFC normalization", id))
		}
		message, ok := value.(map[string]any)
		if !ok || id == "" {
			add(fmt.Sprintf("%s: expected a message table with a nonempty ID", id))
			continue
		}
		result[id] = make(map[string][]string)
		for field, value := range message {
			if field == "description" || field == "hash" {
				if text, ok := value.(string); !ok {
					add(fmt.Sprintf("%s.%s: metadata must be a string", id, field))
				} else if !norm.NFC.IsNormalString(text) {
					add(fmt.Sprintf("%s.%s: decoded metadata must use NFC normalization", id, field))
				}
				continue
			}
			// Fixed delimiters keep source and placeholder analysis unambiguous.
			if !slices.Contains(pluralForms, field) {
				add(fmt.Sprintf("%s.%s: unknown message field (use a plural form, description, or hash)", id, field))
				continue
			}
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				add(fmt.Sprintf("%s.%s: translation must be a nonempty string", id, field))
				continue
			}
			if !norm.NFC.IsNormalString(text) {
				add(fmt.Sprintf("%s.%s: decoded translation must use NFC normalization", id, field))
				continue
			}
			fields, err := templateFields(text)
			if err != nil {
				add(fmt.Sprintf("%s.%s: invalid translation template: %v", id, field, err))
				continue
			}
			result[id][field] = fields
		}
		if _, ok := message["other"]; !ok {
			add(fmt.Sprintf("%s: missing required other translation", id))
		}
	}
	sortIssues(issues)
	return result, issues
}

func compareCatalogs(catalogs map[string]catalog, canonical string, used map[string]string) []Issue {
	var issues []Issue
	base := catalogs[canonical]
	// Use the union, not just the canonical catalog: a new key added only to a
	// noncanonical language must also be reported missing in every other one.
	all := make(map[string]bool)
	for _, messages := range catalogs {
		for id := range messages {
			all[id] = true
		}
	}
	for id := range used {
		all[id] = true
	}
	for name, messages := range catalogs {
		for id := range all {
			forms, present := messages[id]
			reference, referenced := used[id]
			if !present {
				origin := "another catalog"
				if referenced {
					origin = reference
				}
				issues = append(issues, Issue{Path: name, Message: fmt.Sprintf("%s: missing translation (referenced by %s)", id, origin)})
				continue
			}
			if !referenced {
				issues = append(issues, Issue{Path: name, Message: fmt.Sprintf("%s: unused translation; remove it from all catalogs or add its production reference", id)})
			}
			if expected, exists := base[id]; exists {
				for form, fields := range forms {
					if !slices.Equal(fields, expected["other"]) {
						issues = append(issues, Issue{Path: name, Message: fmt.Sprintf("%s.%s: placeholders %v do not match %s other %v", id, form, fields, canonical, expected["other"])})
					}
				}
			}
		}
	}
	sortIssues(issues)
	return issues
}

// UI messages use plain root-data placeholders ({{.Count}}, {{.Page}}, ...).
// Reject computed/scoped accesses rather than silently missing their variables.
// Plural choice belongs to go-i18n, not conditional logic inside translations.
func templateFields(text string) ([]string, error) {
	tmpl, err := template.New("message").Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]any)
	for _, node := range tmpl.Tree.Root.Nodes {
		switch n := node.(type) {
		case *parse.TextNode, *parse.CommentNode:
		case *parse.ActionNode:
			if len(n.Pipe.Decl) != 0 || len(n.Pipe.Cmds) != 1 || len(n.Pipe.Cmds[0].Args) != 1 {
				return nil, fmt.Errorf("use plain root placeholders, for example {{.Count}}")
			}
			field, ok := n.Pipe.Cmds[0].Args[0].(*parse.FieldNode)
			if !ok || len(field.Ident) != 1 {
				return nil, fmt.Errorf("use plain root placeholders, for example {{.Count}}")
			}
			fields[field.Ident[0]] = 1
		default:
			return nil, fmt.Errorf("use plain placeholders; conditional logic and nested templates are unsupported")
		}
	}
	if len(tmpl.Templates()) != 1 {
		return nil, fmt.Errorf("nested templates are unsupported")
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, fields); err != nil {
		return nil, err
	}
	if strings.TrimSpace(rendered.String()) == "" {
		return nil, fmt.Errorf("translation renders blank")
	}
	result := make([]string, 0, len(fields))
	for field := range fields {
		result = append(result, field)
	}
	slices.Sort(result)
	return result, nil
}
