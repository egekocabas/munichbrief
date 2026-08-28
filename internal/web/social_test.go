package web

import (
	"bytes"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	xfont "golang.org/x/image/font"

	"github.com/egekocabas/munichbrief/internal/store"
)

func TestSocialCardsRenderURLSpecificPNGResponses(t *testing.T) {
	server, job := publicDiscoveryServer(t, fixtureStore(t), store.AIPresentation{
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
		TitleEN: "Safe title", SummaryEN: "Safe summary.", PrivacyStatus: "safe",
	})
	handler := server.Handler()

	home := httptest.NewRecorder()
	handler.ServeHTTP(home, publicDiscoveryRequest(http.MethodGet, "/social/en/home"))
	if home.Code != http.StatusOK || home.Header().Get("Content-Type") != "image/png" || !strings.Contains(home.Header().Get("Cache-Control"), "public") || home.Header().Get("ETag") == "" {
		t.Fatalf("home social card = %d/%q/%q/%q", home.Code, home.Header().Get("Content-Type"), home.Header().Get("Cache-Control"), home.Header().Get("ETag"))
	}
	if home.Header().Get("X-AI-Generated") != "false" || home.Header().Get("X-AI-Generated-Background") != "true" || home.Header().Get("X-IPTC-Digital-Source-Type") != iptcCompositeWithTrainedAlgorithmicMedia || !bytes.Contains(home.Body.Bytes(), []byte(iptcCompositeWithTrainedAlgorithmicMedia)) {
		t.Fatal("home social card does not distinguish its AI-generated background from its non-AI text")
	}
	assertSocialCardDimensions(t, home.Body.Bytes())

	detail := httptest.NewRecorder()
	handler.ServeHTTP(detail, publicDiscoveryRequest(http.MethodGet, "/social/en/incidents/"+formatID(job.IncidentID)))
	if detail.Code != http.StatusOK {
		t.Fatalf("incident social card status = %d, want 200", detail.Code)
	}
	if detail.Header().Get("X-AI-Generated") != "true" || detail.Header().Get("X-AI-Generated-Text") != "true" || detail.Header().Get("X-AI-Generated-Background") != "true" || detail.Header().Get("X-IPTC-Digital-Source-Type") != iptcCompositeWithTrainedAlgorithmicMedia {
		t.Fatal("incident social card AI provenance headers are incomplete")
	}
	assertSocialCardDimensions(t, detail.Body.Bytes())
	if bytes.Equal(home.Body.Bytes(), detail.Body.Bytes()) {
		t.Error("incident social card is identical to the homepage card")
	}

	notModifiedRequest := publicDiscoveryRequest(http.MethodGet, "/social/en/home")
	notModifiedRequest.Header.Set("If-None-Match", home.Header().Get("ETag"))
	notModified := httptest.NewRecorder()
	handler.ServeHTTP(notModified, notModifiedRequest)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Errorf("conditional social card response = %d/%d bytes", notModified.Code, notModified.Body.Len())
	}

	for _, path := range []string{"/social/fr/home", "/social/en/incidents/0", "/social/en/incidents/999999"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, publicDiscoveryRequest(http.MethodGet, path))
		if response.Code != http.StatusNotFound {
			t.Errorf("social card %s status = %d, want 404", path, response.Code)
		}
	}
}

func TestSocialCardTextStaysInsideSkySafeArea(t *testing.T) {
	const longestPublishedTitle = "Verkehrsunfall am Mittleren Ring verursacht längere Sperrungen während des Berufsverkehrs."
	if length := utf8.RuneCountInString(longestPublishedTitle); length != 90 {
		t.Fatalf("longest published title fixture has %d characters, want 90", length)
	}
	renderer, err := newSocialCardRenderer()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := renderer.render(socialCardSpec{
		Eyebrow: "Incident",
		Title:   longestPublishedTitle,
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := png.Decode(bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	maxChangedX := -1
	for y := 0; y < socialCardHeight; y++ {
		for x := 0; x < socialCardWidth; x++ {
			if rgba(rendered.At(x, y)) != rgba(renderer.background.At(x, y)) && x > maxChangedX {
				maxChangedX = x
			}
		}
	}
	if maxChangedX > socialTextRightEdge+2 {
		t.Errorf("social text changed pixels through x=%d, beyond safe edge %d", maxChangedX, socialTextRightEdge)
	}

	titleFace, closeTitle, err := renderer.face(renderer.boldFont, 52)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTitle()
	lines := wrapSocialTitle(longestPublishedTitle, titleFace, socialTextMaxWidth, 2)
	if len(lines) > 2 {
		t.Fatalf("wrapped title has %d lines, want at most 2: %#v", len(lines), lines)
	}
	for _, line := range lines {
		if width := xfont.MeasureString(titleFace, line).Ceil(); width > socialTextMaxWidth {
			t.Errorf("wrapped line %q is %dpx wide, max %dpx", line, width, socialTextMaxWidth)
		}
	}
}

func assertSocialCardDimensions(t *testing.T, contents []byte) {
	t.Helper()
	configuration, err := png.DecodeConfig(bytes.NewReader(contents))
	if err != nil {
		t.Fatalf("decode social card configuration: %v", err)
	}
	if configuration.Width != socialCardWidth || configuration.Height != socialCardHeight {
		t.Errorf("social card dimensions = %dx%d, want %dx%d", configuration.Width, configuration.Height, socialCardWidth, socialCardHeight)
	}
}

func rgba(value color.Color) color.RGBA {
	return color.RGBAModel.Convert(value).(color.RGBA)
}
