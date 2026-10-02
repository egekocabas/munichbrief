package translationcheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeUsageFixture(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSourceUsageTracksReceiverAliasesAndPackageInitializers(t *testing.T) {
	root := t.TempDir()
	writeUsageFixture(t, root, "internal/web/page.go", `package web
var localizer *localization
var label = localizer.Text("en", "PackageMessage")
var config = i18n.LocalizeConfig{MessageID: "ConfigMessage"}
func page(loc *localization) {
 alias := loc
 alias.Text("en", "AliasedMessage")
 server.localization.Text("en", "RenamedServer")
 fresh, _ := newLocalization(nil)
 fresh.Text("en", "FreshMessage")
 alias.Text("en", dynamicKey)
}`)
	used, issues := SourceUsage(root)
	for _, key := range []string{"PackageMessage", "ConfigMessage", "AliasedMessage", "RenamedServer", "FreshMessage"} {
		if used[key] == "" {
			t.Errorf("reference missing: %s", key)
		}
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "unrecognized dynamic") {
		t.Fatalf("issues = %v", issues)
	}
}

func TestDynamicProducerChangesCannotHideMissingKeys(t *testing.T) {
	original, err := os.ReadFile("../web/map.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, old, new, wantIssue, wantKey string }{
		{"literal typo", "\"MapAllTime\"", "\"MissingFromEveryCatalog\"", "", "MissingFromEveryCatalog"},
		{"unbounded producer", "\"MapAllTime\"", "requestKey", "non-finite producer", ""},
		{"removed consumer", "s.localization.Text(lang, p.key)", "s.localization.Text(lang, \"MapAllTime\")", "stale dynamic translation contract", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			body := strings.Replace(string(original), test.old, test.new, 1)
			if body == string(original) {
				t.Fatalf("mutation target missing: %s", test.old)
			}
			writeUsageFixture(t, root, "internal/web/map.go", body)
			used, issues := SourceUsage(root)
			if test.wantIssue == "" {
				if len(issues) > 0 {
					t.Fatalf("unexpected issues: %v", issues)
				}
			} else {
				found := false
				for _, issue := range issues {
					found = found || strings.Contains(issue.Message, test.wantIssue)
				}
				if !found {
					t.Fatalf("wanted %q, got %v", test.wantIssue, issues)
				}
			}
			if test.wantKey != "" && used[test.wantKey] == "" {
				t.Fatalf("changed source producer was ignored: %s", test.wantKey)
			}
		})
	}
}

func TestRegistryRejectsUnboundedKeyProducer(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "internal/languages/languages.go", `package languages
var registered = []Definition{{SwitchMessageID: "SwitchOne"}, {SwitchMessageID: externalKey}}`, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := usageScanner{fset: fset, files: []*ast.File{file}}
	if _, err := s.registryKeys("SwitchMessageID"); err == nil {
		t.Fatal("nonliteral registered key silently ignored")
	}
}

func TestTemplateWithoutPipelineAndStaleContract(t *testing.T) {
	root := t.TempDir()
	writeUsageFixture(t, root, "internal/web/templates/page.html", `{{define "child"}}{{t .Lang "Child"}}{{end}}{{template "child"}}`)
	used, issues := SourceUsage(root)
	if len(issues) != 0 || used["Child"] == "" {
		t.Fatalf("used=%v issues=%v", used, issues)
	}
	writeUsageFixture(t, root, "internal/web/templates/legal.html", `<p>Removed dynamic fields</p>`)
	_, issues = SourceUsage(root)
	if len(issues) != 2 {
		t.Fatalf("stale template contracts missed: %v", issues)
	}
}

func TestSourceUsageIgnoresTestsCommentsAndUnrelatedLiterals(t *testing.T) {
	root := t.TempDir()
	writeUsageFixture(t, root, "internal/web/page.go", `package web
// s.localization.Text("en", "CommentOnly")
func page() {
  s.localization.Text("en", "GoMessage")
  s.localization.Format("en", "GoFormatted", data)
  s.localization.Count("en", "GoCount", 3)
  unrelated := "Unrelated"
}`)
	writeUsageFixture(t, root, "internal/web/page_test.go", `package web
func test() { s.localization.Text("en", "TestOnly") }`)
	writeUsageFixture(t, root, "internal/web/templates/page.html", `{{/* {{t .Lang "TemplateComment"}} */}}
{{if .Value}}{{t .Lang "TemplateMessage"}}{{else}}{{message .Lang "TemplateFormatted" .}}{{end}}
{{with .Value}}{{tc $.Lang "TemplateCount" 2}}{{end}}
{{printf "%s" (t .Lang "NestedMessage")}}`)
	used, issues := SourceUsage(root)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	for _, key := range []string{"GoMessage", "GoFormatted", "GoCount", "TemplateMessage", "TemplateFormatted", "TemplateCount", "NestedMessage"} {
		if used[key] == "" {
			t.Errorf("missing usage %s", key)
		}
	}
	for _, key := range []string{"CommentOnly", "TestOnly", "Unrelated", "TemplateComment"} {
		if used[key] != "" {
			t.Errorf("non-executable reference counted: %s", key)
		}
	}
}

func TestSourceUsageRejectsNewDynamicConsumers(t *testing.T) {
	root := t.TempDir()
	writeUsageFixture(t, root, "internal/web/page.go", `package web
func page() { s.localization.Text("en", requestValue) }`)
	writeUsageFixture(t, root, "internal/web/templates/page.html", `{{t .Lang .MessageKey}}`)
	_, issues := SourceUsage(root)
	if len(issues) != 2 {
		t.Fatalf("issues = %v", issues)
	}
	for _, issue := range issues {
		if !strings.Contains(issue.Message, "unrecognized dynamic") {
			t.Errorf("unexpected issue: %v", issue)
		}
	}
}

func TestRepositoryDynamicUsageContracts(t *testing.T) {
	used, issues := SourceUsage("../..")
	if len(issues) != 0 {
		for _, issue := range issues {
			t.Error(issue)
		}
	}
	for _, key := range []string{"PrivacyPreferencesCopy", "PrivacyTitleDescription", "SwitchToPolish", "RussianTranslationStep", "MapUnassigned", "ContactTopicInvalid", "LicensesData", "CategoryTraffic", "TimeNotReported", "DiscoverCopy"} {
		if used[key] == "" {
			t.Errorf("missing dynamic reference %s", key)
		}
	}
}
