// Command check-translations checks UI catalogs without starting the app.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/egekocabas/munichbrief/internal/translationcheck"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	summary, issues := translationcheck.Check(*root)
	for _, issue := range issues {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			fmt.Printf("::error file=%s,line=%d,title=Translation integrity::%s\n", annotation(issue.Path, true), max(1, issue.Line), annotation(issue.Message, false))
		} else {
			fmt.Fprintf(os.Stderr, "%s:%d: %s\n", issue.Path, max(1, issue.Line), issue.Message)
		}
	}
	if len(issues) > 0 {
		fmt.Fprintf(os.Stderr, "Translation integrity failed: %d issue(s).\n", len(issues))
		os.Exit(1)
	}
	fmt.Printf("Translation integrity passed: %d languages, %d messages; source usage, catalog completeness, and placeholders checked.\n", summary.Languages, summary.Messages)
}

func annotation(value string, property bool) string {
	value = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(value)
	if property {
		value = strings.NewReplacer(":", "%3A", ",", "%2C").Replace(value)
	}
	return value
}
