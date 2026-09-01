package web

import (
	"bytes"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestSocialCardsRenderURLSpecificPNGResponses(t *testing.T) {
	t.Parallel()
	server, job := publicDiscoveryServer(t, fixtureStore(t), testPresentation{
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
		TitleEN: "Safe title", SummaryEN: "Safe summary.",
	})
	handler := server.Handler()

	home := httptest.NewRecorder()
	handler.ServeHTTP(home, publicDiscoveryRequest(http.MethodGet, "/social/en/home"))
	if home.Code != http.StatusOK || home.Header().Get("Content-Type") != "image/png" || home.Header().Get("Content-Language") != "en-GB" || !strings.Contains(home.Header().Get("Cache-Control"), "public") || home.Header().Get("ETag") == "" {
		t.Fatalf("home social card = %d/%q/%q/%q/%q", home.Code, home.Header().Get("Content-Type"), home.Header().Get("Content-Language"), home.Header().Get("Cache-Control"), home.Header().Get("ETag"))
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

	for _, path := range []string{"/social/pt/home", "/social/en/incidents/0", "/social/en/incidents/999999"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, publicDiscoveryRequest(http.MethodGet, path))
		if response.Code != http.StatusNotFound {
			t.Errorf("social card %s status = %d, want 404", path, response.Code)
		}
	}
}

func TestSocialCardCacheIdentityChangesAfterCategoryCorrection(t *testing.T) {
	t.Parallel()
	renderer, err := newSocialCardRenderer()
	if err != nil {
		t.Fatal(err)
	}
	spec := socialCardSpec{Title: "Safe title", AIGenerated: true, AIModel: "presenter:4b"}
	before := renderer.etag(spec)
	spec.CacheIdentity = "2026-08-29T10:15:00Z"
	if before == renderer.etag(spec) {
		t.Fatal("category-verification completion did not invalidate the social-card cache identity")
	}
}

func TestSocialCardRendererIsShared(t *testing.T) {
	t.Parallel()
	first, err := newSocialCardRenderer()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newSocialCardRenderer()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("social card renderer was initialized more than once")
	}
}

func TestSocialCardTextNormalizationAndLocaleAwareCasing(t *testing.T) {
	if got := normalizeSocialText("Украі\u0308нська ʼслово non‑breaking"); got != "Українська ’слово non-breaking" {
		t.Fatalf("normalized social text = %q", got)
	}
	if got := localizedUpper("tr-TR", "içerik ıslak"); got != "İÇERİK ISLAK" {
		t.Fatalf("Turkish uppercase = %q", got)
	}
}

func TestSocialCardFontCoversReaderAlphabets(t *testing.T) {
	renderer, err := newSocialCardRenderer()
	if err != nil {
		t.Fatal(err)
	}
	face, closeFace, err := renderer.face(renderer.boldFont, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFace()
	const representativeCharacters = "çğıİöşüčćđšžàáâèéêìíîòóôùúûüñœÀÈÉÌÍÒÓÙÚăâîșțĂÂÎȘȚąćęłńóśźżĄĆĘŁŃÓŚŹŻΑΒΓΔΕΖΗΘΙΚΛΜΝΞΟΠΡΣΤΥΦΧΨΩάέήίόύώϊϋΐΰАБВГҐДЕЁЄЖЗИІЇЙКЛМНОПРСТУФХЦЧШЩЪЫЬЭЮЯабвгґдеёєжзиіїйклмнопрстуфхцчшщъыьэюя"
	for _, character := range representativeCharacters {
		if _, ok := face.GlyphAdvance(character); !ok {
			t.Errorf("embedded social font does not cover %q (U+%04X)", character, character)
		}
	}
}

func TestSocialCardFontsShapeChineseAndHindi(t *testing.T) {
	renderer, err := newSocialCardRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, tag, text, characters string
	}{
		{name: "Chinese", tag: "zh-CN", text: "警方在慕尼黑市中心展开调查", characters: "警方在慕尼黑市中心展开调查"},
		{name: "Hindi", tag: "hi-IN", text: "म्यूनिख में पुलिस कार्रवाई", characters: "अआइईउऊऋॠऌॡएऐओऔअंअःकखगघङचछजझञटठडढणतथदधनपफबभमयरलवशषसहक्षत्रज्ञड़ढ़क़ख़ग़ज़फ़म्यूनिखपुलिसकार्रवाई"},
	} {
		t.Run(test.name, func(t *testing.T) {
			face, ok := renderer.localizedFace(test.tag, 52, nil).(shapedSocialTextFace)
			if !ok {
				t.Fatalf("%s did not select a shaped social font", test.tag)
			}
			for _, character := range test.characters {
				if _, found := face.face.NominalGlyph(character); !found {
					t.Errorf("%s social font does not cover %q (U+%04X)", test.name, character, character)
				}
			}
			renderer.shapeMu.Lock()
			shaped := face.output(test.text)
			renderer.shapeMu.Unlock()
			if shaped.Advance <= 0 || len(shaped.Glyphs) == 0 {
				t.Fatalf("%s shaped output is empty: %#v", test.name, shaped)
			}
			contents, err := renderer.render(socialCardSpec{LanguageTag: test.tag, Eyebrow: test.text, Title: test.text})
			if err != nil {
				t.Fatal(err)
			}
			assertSocialCardDimensions(t, contents)
			rendered, err := png.Decode(bytes.NewReader(contents))
			if err != nil {
				t.Fatal(err)
			}
			changed := 0
			for y := 130; y < 380; y++ {
				for x := socialTextLeft; x < socialTextRightEdge; x++ {
					if rgba(rendered.At(x, y)) != rgba(renderer.background.At(x, y)) {
						changed++
					}
				}
			}
			if changed < 100 {
				t.Fatalf("%s social text changed only %d pixels", test.name, changed)
			}
		})
	}
	hindiFace := renderer.localizedFace("hi-IN", 52, nil).(shapedSocialTextFace)
	renderer.shapeMu.Lock()
	conjunct := hindiFace.output("क्षेत्र")
	renderer.shapeMu.Unlock()
	if len(conjunct.Glyphs) >= len([]rune("क्षेत्र")) {
		t.Fatalf("Devanagari conjunct was not shaped: %d glyphs for %d runes", len(conjunct.Glyphs), len([]rune("क्षेत्र")))
	}

	chineseFace := renderer.localizedFace("zh-CN", 52, nil)
	lines := wrapSocialTitle("慕尼黑警方正在调查一起情况尚不明确的事件并继续征集相关线索", chineseFace, socialTextMaxWidth, 2)
	if len(lines) != 2 || strings.Contains(strings.Join(lines, ""), " ") {
		t.Fatalf("Chinese title wrapping = %#v", lines)
	}
	for _, line := range lines {
		if width := chineseFace.measure(line); width > socialTextMaxWidth {
			t.Errorf("Chinese wrapped line is %dpx wide, max %dpx: %q", width, socialTextMaxWidth, line)
		}
	}
}

func TestSocialCardShapingIsSafeForConcurrentRequests(t *testing.T) {
	renderer, err := newSocialCardRenderer()
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errors := make(chan error, 8)
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			tag, title := "zh-CN", "慕尼黑警方正在调查"
			if index%2 == 1 {
				tag, title = "hi-IN", "म्यूनिख में पुलिस जाँच जारी"
			}
			_, err := renderer.render(socialCardSpec{LanguageTag: tag, Eyebrow: title, Title: title})
			errors <- err
		}(index)
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSocialCardTextStaysInsideSkySafeArea(t *testing.T) {
	t.Parallel()
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
	socialFace := basicSocialTextFace{face: titleFace}
	lines := wrapSocialTitle(longestPublishedTitle, socialFace, socialTextMaxWidth, 2)
	if len(lines) > 2 {
		t.Fatalf("wrapped title has %d lines, want at most 2: %#v", len(lines), lines)
	}
	for _, line := range lines {
		if width := socialFace.measure(line); width > socialTextMaxWidth {
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
