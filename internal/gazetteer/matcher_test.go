package gazetteer

import (
	"fmt"
	"strings"
	"testing"
)

func testMatcher(t *testing.T) *Matcher {
	t.Helper()
	names := []string{"Ingolstädter Straße", "Milbertshofen", "Schwabing", "Ganghoferstraße", "Sendling", "Ramersdorf-Perlach", "Schwabing-West", "Oberhaching", "Geiselgasteig"}
	entries := make([]Entry, len(names))
	for i, name := range names {
		entries[i] = Entry{Name: name, Kind: KindNeighbourhood, Priority: 10}
	}
	matcher, err := NewMatcher(entries)
	if err != nil {
		t.Fatal(err)
	}
	return matcher
}

func TestMatcherProtectsRequestedNamesMarkdownAndTransit(t *testing.T) {
	matcher := testMatcher(t)
	title := "U-Bahn an der Ingolsta\u0308dter Straße in Milbertshofen und **Schwabing**"
	summary := "S-Bahn, Ganghoferstraße, Sendling, [Ramersdorf-Perlach](https://munichbrief.de/en/incidents/717), [Schwabing-West](https://munichbrief.de/en/incidents/378?page=2), Oberhaching und Geiselgasteig."
	protected, err := matcher.Protect(title, summary)
	if err != nil {
		t.Fatal(err)
	}
	if len(protected.Replacements) != 11 {
		t.Fatalf("replacements = %d, want 11: %#v", len(protected.Replacements), protected.Replacements)
	}
	if !strings.Contains(protected.Summary, "[__MB_PLACE_") || !strings.Contains(protected.Summary, "](https://munichbrief.de/en/incidents/717)") {
		t.Fatalf("Markdown link was not protected safely: %s", protected.Summary)
	}
	restoredTitle, restoredSummary, err := Restore(protected, protected.Title, protected.Summary)
	if err != nil {
		t.Fatal(err)
	}
	if restoredTitle != "U-Bahn an der Ingolstädter Straße in Milbertshofen und **Schwabing**" || restoredSummary != summary {
		t.Fatalf("restore mismatch:\n%s\n%s", restoredTitle, restoredSummary)
	}
}

func TestRestoreRejectsMissingDuplicateMovedModifiedAndUnknownTokensButAllowsReordering(t *testing.T) {
	matcher := testMatcher(t)
	protected, err := matcher.Protect("Schwabing und Sendling", "Oberhaching")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ title, summary string }{
		{"", protected.Summary},
		{protected.Title + protected.Title, protected.Summary},
		{protected.Summary, protected.Title},
		{protected.Title, protected.Summary + " __MB_PLACE_9999__"},
		{protected.Title + "suffix", protected.Summary},
	}
	for _, test := range tests {
		if _, _, err := Restore(protected, test.title, test.summary); err == nil {
			t.Fatalf("Restore(%q, %q) accepted invalid tokens", test.title, test.summary)
		}
	}
	if title, _, err := Restore(protected, "__MB_PLACE_0002__ und __MB_PLACE_0001__", protected.Summary); err != nil || title != "Sendling und Schwabing" {
		t.Fatalf("Restore rejected target-language token reordering: title=%q err=%v", title, err)
	}
	if _, _, err := Restore(protected, "**"+protected.Title+"**", "["+protected.Summary+"](https://munichbrief.de/en/incidents/1)"); err != nil {
		t.Fatalf("Restore rejected Markdown punctuation around intact tokens: %v", err)
	}
	if title, summary, err := Restore(protected, "警方在__MB_PLACE_0001__和__MB_PLACE_0002__行动", "靠近__MB_PLACE_0003__的现场"); err != nil || title != "警方在Schwabing和Sendling行动" || summary != "靠近Oberhaching的现场" {
		t.Fatalf("Restore rejected normal Han adjacency: title=%q summary=%q err=%v", title, summary, err)
	}
	if title, _, err := Restore(protected, protected.Title+"'da", protected.Summary); err != nil || title != "Schwabing und Sendling'da" {
		t.Fatalf("Restore rejected an apostrophe-delimited suffix: title=%q err=%v", title, err)
	}
}

func TestMatcherReusesStableTokenForRepeatedNameAndChecksEveryOccurrence(t *testing.T) {
	matcher := testMatcher(t)
	protected, err := matcher.Protect("Einsatz in Schwabing", "Schwabing bleibt gesperrt; Schwabing wird geprüft.")
	if err != nil {
		t.Fatal(err)
	}
	if protected.Title != "Einsatz in __MB_PLACE_0001__" || protected.Summary != "__MB_PLACE_0001__ bleibt gesperrt; __MB_PLACE_0001__ wird geprüft." || len(protected.Replacements) != 3 {
		t.Fatalf("stable repeated token = %#v", protected)
	}
	if _, _, err := Restore(protected, protected.Title, "__MB_PLACE_0001__ bleibt gesperrt."); err == nil {
		t.Fatal("Restore accepted a missing repeated occurrence")
	}
	if title, summary, err := Restore(protected, protected.Title, protected.Summary); err != nil || title != "Einsatz in Schwabing" || summary != "Schwabing bleibt gesperrt; Schwabing wird geprüft." {
		t.Fatalf("Restore repeated token: title=%q summary=%q err=%v", title, summary, err)
	}
}

func TestMatcherUsesUnicodeBoundariesAndContextForAmbiguousNames(t *testing.T) {
	matcher, err := NewMatcher([]Entry{
		{Name: "Haar", Kind: KindMunicipality, RequiresContext: true},
		{Name: "Au", Kind: KindNeighbourhood},
		{Name: "Schwabing", Kind: KindNeighbourhood},
	})
	if err != nil {
		t.Fatal(err)
	}
	protected, err := matcher.Protect("Die Haare", "Einsatz in **Haar**, in Au-pair und in Au sowie Schwabinger Straße, später in Schwabing.")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(protected.Title, tokenPrefix) || !strings.Contains(protected.Summary, "Schwabinger Straße") || strings.Count(protected.Summary, tokenPrefix) != 3 {
		t.Fatalf("unexpected boundaries: %#v", protected)
	}
	if len(protected.Replacements) != 3 || protected.Replacements[0].Original != "Haar" || protected.Replacements[1].Original != "Au" || protected.Replacements[2].Original != "Schwabing" {
		t.Fatalf("unexpected contextual matches: %#v", protected.Replacements)
	}
}

func BenchmarkMatcherProtect(b *testing.B) {
	entries := benchmarkEntries(25000)
	matcher, _ := NewMatcher(entries)
	text := "Ein Einsatz an der Teststraße xxxxxA in München."
	b.ResetTimer()
	for range b.N {
		_, _ = matcher.Protect(text, text)
	}
}

func BenchmarkMatcherBuild(b *testing.B) {
	entries := benchmarkEntries(25000)
	b.ResetTimer()
	for range b.N {
		matcher, err := NewMatcher(entries)
		if err != nil || matcher.Count() != len(entries)+4 {
			b.Fatal(err)
		}
	}
}

func benchmarkEntries(count int) []Entry {
	entries := make([]Entry, count)
	for i := range entries {
		entries[i] = Entry{Name: fmt.Sprintf("Teststraße %05d", i), Kind: KindStreet, Priority: 10}
	}
	return entries
}
