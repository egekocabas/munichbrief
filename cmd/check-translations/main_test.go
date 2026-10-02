package main

import "testing"

func TestAnnotationEscapesUntrustedDiagnosticText(t *testing.T) {
	if got := annotation("bad%\r\n::warning::fake", false); got != "bad%25%0D%0A::warning::fake" {
		t.Fatal(got)
	}
	if got := annotation("dir/a,b:c.go\n", true); got != "dir/a%2Cb%3Ac.go%0A" {
		t.Fatal(got)
	}
}
