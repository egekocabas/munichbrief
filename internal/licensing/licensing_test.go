package licensing

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestExactArtefactReviewAndExclusions(t *testing.T) {
	var reviewed, conditional, excluded Component
	for _, c := range Components() {
		if c.ModelTag == "qwen3.5:4b" {
			reviewed = c
		}
		if c.ModelTag == "translategemma:4b-it-q8_0" {
			conditional = c
		}
		if c.ReviewStatus == "excluded" {
			excluded = c
		}
	}
	for _, c := range []Component{reviewed, conditional, excluded} {
		if c.ModelTag == "" {
			t.Fatal("missing baseline model")
		}
	}
	for _, tc := range []struct {
		tag, digest string
		available   bool
		label       string
		warning     bool
	}{
		{reviewed.ModelTag, reviewed.Digest, true, "Reviewed", false},
		{reviewed.ModelTag, strings.Repeat("a", 64), true, "Artefact changed", true},
		{reviewed.ModelTag, "", true, "Review needed", true},
		{reviewed.ModelTag, reviewed.Digest, false, "Catalogue unavailable", true},
		{reviewed.ModelTag + "-new", reviewed.Digest, true, "Review needed", true},
		{conditional.ModelTag, conditional.Digest, true, "Conditions apply", true},
		{excluded.ModelTag, excluded.Digest, true, "Review needed", true},
	} {
		r := Assess(tc.tag, tc.digest, tc.available)
		if r.Label != tc.label || r.Warning != tc.warning {
			t.Errorf("%q = %s/%v; want %s/%v", tc.tag, r.Label, r.Warning, tc.label, tc.warning)
		}
	}
	if excluded.Public {
		t.Fatal("excluded SalamandraTA model exposed in public credits")
	}
}

func TestNoticesAreAllowlistedAndInventoryIsIndependent(t *testing.T) {
	for _, id := range []string{"../LICENSE", "/etc/passwd", "gemma-terms.txt", "model-8", "debian-base-files", ""} {
		if _, ok := PublicNotice(id); ok {
			t.Errorf("unexpected public notice %q", id)
		}
	}
	n, ok := PublicNotice("munichbrief-mit")
	if !ok || !strings.Contains(n.Text, "Permission is hereby granted") {
		t.Fatal("missing original MIT notice")
	}
	items := Components()
	items[0].Notices[0] = "changed"
	items[0].Locations[0] = "changed"
	if Components()[0].Notices[0] == "changed" || Components()[0].Locations[0] == "changed" {
		t.Fatal("registry was mutable")
	}
}

type failedNoticeWriter struct{}

func (failedNoticeWriter) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestOfflineNoticeOutputAndWriteFailure(t *testing.T) {
	var b bytes.Buffer
	if err := WriteNotices(&b); err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"MunichBrief", "Noto Sans", "SIL OPEN FONT LICENSE", "Permission is hereby granted", "htmx"} {
		if !strings.Contains(b.String(), phrase) {
			t.Errorf("missing %q", phrase)
		}
	}
	if err := WriteNotices(failedNoticeWriter{}); err == nil {
		t.Fatal("lost output failure")
	}
}
