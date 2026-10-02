package translationcheck

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/egekocabas/munichbrief/internal/languages"
)

func parseFixture(t *testing.T, name, contents string) catalog {
	t.Helper()
	messages, issues := readCatalog(fstest.MapFS{name: {Data: []byte(contents)}}, name)
	if len(issues) > 0 {
		t.Fatalf("fixture failed: %v", issues)
	}
	return messages
}

func TestCatalogStructure(t *testing.T) {
	for _, test := range []struct{ name, contents, want string }{
		{"empty", "", "no messages"},
		{"syntax", "[Broken\n", "invalid TOML"},
		{"duplicate", "[Title]\nother='One'\nother='Two'", "invalid TOML"},
		{"scalar", "Title='Hello'", "expected a message table"},
		{"missing other", "Title={one='Hello'}", "missing required other"},
		{"blank", "Title={other='  '}", "nonempty string"},
		{"numeric", "Title={other=123}", "nonempty string"},
		{"blank plural", "Title={other='Hello',one=' '}", "Title.one"},
		{"field typo", "Title={other='Hello',othre='Typo'}", "unknown message field"},
		{"delimiter", "Title={other='Hello',leftDelim='[['}", "unknown message field"},
		{"template syntax", "Title={other='Hello {{.Name'}", "invalid translation template"},
		{"hidden field", `Title={other='{{index . "Name"}}'}`, "plain root placeholders"},
		{"scoped field", `Title={other='{{with .User}}{{.Name}}{{end}}'}`, "conditional logic"},
		{"nested field", `Title={other='{{.User.Name}}'}`, "plain root placeholders"},
		{"defined template", `Title={other='{{define "unused"}}{{.Name}}{{end}}Hello'}`, "nested templates"},
		{"normalization", "Title={other='e\u0301'}", "NFC"},
		{"escaped normalization", `Title={other="e\u0301"}`, "decoded translation must use NFC"},
		{"escaped ID", `"e\u0301"={other='Title'}`, "decoded message ID must use NFC"},
		{"comment-only label", `Title={other='{{/* TODO */}}'}`, "translation renders blank"},
		{"invalid utf8", "Title={other='\xff'}", "UTF-8"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, issues := readCatalog(fstest.MapFS{"en.toml": {Data: []byte(test.contents)}}, "en.toml")
			if !strings.Contains(fmt.Sprint(issues), test.want) {
				t.Fatalf("got %v; want diagnostic containing %q", issues, test.want)
			}
		})
	}
	_, issues := readCatalog(fstest.MapFS{}, "missing.toml")
	if !strings.Contains(fmt.Sprint(issues), "cannot read registered catalog") {
		t.Fatalf("missing catalog accepted: %v", issues)
	}
}

func TestDifferentPluralFormsAndPlaceholderOrderAreValid(t *testing.T) {
	catalogs := map[string]catalog{
		"de.toml": parseFixture(t, "de.toml", "[Reports]\none='{{.Count}} Meldung'\nother='{{.Count}} Meldungen'\n[Page]\nother='{{.Page}} / {{.Total}}'"),
		"zh.toml": parseFixture(t, "zh.toml", "Reports={other='{{.Count}} 条通报'}\nPage={other='共 {{.Total}} 页，第 {{.Page}} 页'}"),
		"ru.toml": parseFixture(t, "ru.toml", "Reports={one='{{.Count}} сообщение',few='{{.Count}} сообщения',many='{{.Count}} сообщений',other='{{.Count}} сообщения'}\nPage={other='{{.Page}} / {{.Total}}'}"),
	}
	if issues := compareCatalogs(catalogs, "de.toml", map[string]string{"Reports": "template.html:1", "Page": "reader.go:1"}); len(issues) != 0 {
		t.Fatal(issues)
	}
}

func TestCatalogUsageAndPlaceholderDiagnostics(t *testing.T) {
	catalogs := map[string]catalog{
		"de.toml": parseFixture(t, "de.toml", "Reports={one='Meldung',other='{{.Count}} Meldungen'}\nUnused={other='Alt'}"),
		"en.toml": parseFixture(t, "en.toml", "Reports={other='{{.Typo}} reports'}\nOnlyEnglish={other='Hello'}\nUnused={other='Old'}"),
	}
	issues := compareCatalogs(catalogs, "de.toml", map[string]string{
		"Reports": "reader.html:4", "MissingEverywhere": "public.go:10", "OnlyEnglish": "reader.html:8",
	})
	for _, want := range []string{
		"de.toml 0 OnlyEnglish: missing translation (referenced by reader.html:8)",
		"de.toml 0 MissingEverywhere: missing translation (referenced by public.go:10)",
		"en.toml 0 MissingEverywhere: missing translation (referenced by public.go:10)",
		"de.toml 0 Unused: unused translation", "en.toml 0 Unused: unused translation",
		"Reports.one: placeholders [] do not match de.toml other [Count]",
		"Reports.other: placeholders [Typo] do not match de.toml other [Count]",
	} {
		if !strings.Contains(fmt.Sprint(issues), want) {
			t.Errorf("missing diagnostic %q in %v", want, issues)
		}
	}
	if len(issues) != 7 {
		t.Fatalf("got %d issues, want 7: %v", len(issues), issues)
	}
}

func TestCatalogOnlyKeyIsMissingFromOtherLanguages(t *testing.T) {
	catalogs := map[string]catalog{
		"de.toml": parseFixture(t, "de.toml", "Used={other='Gut'}"),
		"en.toml": parseFixture(t, "en.toml", "Used={other='Good'}\nUnused={other='Old'}"),
	}
	issues := compareCatalogs(catalogs, "de.toml", map[string]string{"Used": "view.html:1"})
	if len(issues) != 2 || !strings.Contains(fmt.Sprint(issues), "referenced by another catalog") || !strings.Contains(fmt.Sprint(issues), "unused translation") {
		t.Fatal(issues)
	}
}

func TestTemplateFields(t *testing.T) {
	fields, err := templateFields("Page {{.Page}} of {{.TotalPages}}; again {{.Page}}")
	if err != nil || !slices.Equal(fields, []string{"Page", "TotalPages"}) {
		t.Fatalf("fields=%v, err=%v", fields, err)
	}
}

func TestStructuralErrorsPrecedeSourceUsage(t *testing.T) {
	_, issues := Check(t.TempDir())
	if len(issues) != len(languages.Registered()) {
		t.Fatalf("expected one missing-file diagnostic per registered language, got %v", issues)
	}
	for _, issue := range issues {
		if !strings.Contains(issue.Message, "cannot read registered catalog") {
			t.Errorf("misleading follow-on diagnostic: %v", issue)
		}
	}
}
