package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-text/typesetting/di"
	textfont "github.com/go-text/typesetting/font"
	textopentype "github.com/go-text/typesetting/font/opentype"
	textlanguage "github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	xdraw "golang.org/x/image/draw"
	xfont "golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"

	"github.com/egekocabas/munichbrief/internal/store"
)

const (
	socialCardWidth  = 1200
	socialCardHeight = 630
	// Bump when renderer layout or colors change; asset bytes are hashed below.
	socialCardDesignVersion     = "reader-wide-incident-title-v4"
	socialTextLeft              = 70
	socialTextMaxWidth          = 550
	socialTextRightEdge         = socialTextLeft + socialTextMaxWidth
	socialHomeTitleSize         = 46
	socialIncidentTitleSize     = 48
	socialIncidentTitleMaxWidth = 650
	socialSubtitleMaxWidth      = 700
	socialSubtitleSize          = 26
	socialSubtitleLineHeight    = 34
	socialSubtitleMaxLines      = 3
	socialCardXMP               = `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?><x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description rdf:about="" xmlns:Iptc4xmpExt="http://iptc.org/std/Iptc4xmpExt/2008-02-29/" xmlns:xmp="http://ns.adobe.com/xap/1.0/" Iptc4xmpExt:DigitalSourceType="` + iptcCompositeWithTrainedAlgorithmicMedia + `" xmp:CreatorTool="MunichBrief"/></rdf:RDF></x:xmpmeta><?xpacket end="w"?>`
)

var (
	socialInk                = color.RGBA{R: 38, G: 60, B: 53, A: 255}
	socialCivic              = color.RGBA{R: 50, G: 99, B: 79, A: 255}
	socialMuted              = color.RGBA{R: 104, G: 116, B: 110, A: 255}
	socialAlert              = color.RGBA{R: 149, G: 97, B: 35, A: 255}
	sharedSocialCardRenderer = sync.OnceValues(buildSocialCardRenderer)
)

type socialCardRenderer struct {
	background            *image.RGBA
	brand                 image.Image
	aiLabel               image.Image
	boldFont              *opentype.Font
	regularFont           *opentype.Font
	chineseFont           *textfont.Face
	devanagariFont        *textfont.Face
	chineseRegularFont    *textfont.Face
	devanagariRegularFont *textfont.Face
	shapeMu               sync.Mutex
	shaper                shaping.HarfbuzzShaper
	version               string
}

type socialTextFace interface {
	measure(string) int
	draw(draw.Image, color.Color, string, int, int)
	wordSeparator() string
}

type basicSocialTextFace struct{ face xfont.Face }

func (f basicSocialTextFace) measure(value string) int {
	return xfont.MeasureString(f.face, value).Ceil()
}
func (f basicSocialTextFace) wordSeparator() string { return " " }
func (f basicSocialTextFace) draw(destination draw.Image, textColor color.Color, value string, x, baseline int) {
	drawText(destination, f.face, textColor, value, x, baseline)
}

type shapedSocialTextFace struct {
	renderer *socialCardRenderer
	face     *textfont.Face
	size     fixed.Int26_6
	script   textlanguage.Script
	language textlanguage.Language
}

type socialCardSpec struct {
	Eyebrow       string
	Title         string
	Subtitle      string
	LanguageTag   string
	AIGenerated   bool
	AIModel       string
	CacheIdentity string
}

func newSocialCardRenderer() (*socialCardRenderer, error) {
	return sharedSocialCardRenderer()
}

func buildSocialCardRenderer() (*socialCardRenderer, error) {
	source, err := png.Decode(bytes.NewReader(socialCardBackground))
	if err != nil {
		return nil, fmt.Errorf("decode background: %w", err)
	}
	if source.Bounds().Dx() != socialCardWidth || source.Bounds().Dy() != socialCardHeight {
		return nil, fmt.Errorf("background dimensions are %dx%d, want %dx%d", source.Bounds().Dx(), source.Bounds().Dy(), socialCardWidth, socialCardHeight)
	}
	background := image.NewRGBA(image.Rect(0, 0, socialCardWidth, socialCardHeight))
	draw.Draw(background, background.Bounds(), source, source.Bounds().Min, draw.Src)
	brand, err := png.Decode(bytes.NewReader(socialCardBrand))
	if err != nil {
		return nil, fmt.Errorf("decode social brand: %w", err)
	}
	aiLabel, err := png.Decode(bytes.NewReader(euAISocialLabel))
	if err != nil {
		return nil, fmt.Errorf("decode EU AI social label: %w", err)
	}
	boldFont, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, fmt.Errorf("parse bold font: %w", err)
	}
	regularFont, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, fmt.Errorf("parse regular font: %w", err)
	}
	chineseFont, err := textfont.ParseTTF(bytes.NewReader(notoSansSC))
	if err != nil {
		return nil, fmt.Errorf("parse Noto Sans SC: %w", err)
	}
	chineseFont.SetVariations([]textfont.Variation{{Tag: textopentype.MustNewTag("wght"), Value: 700}})
	devanagariFont, err := textfont.ParseTTF(bytes.NewReader(notoSansDevanagari))
	if err != nil {
		return nil, fmt.Errorf("parse Noto Sans Devanagari: %w", err)
	}
	devanagariFont.SetVariations([]textfont.Variation{{Tag: textopentype.MustNewTag("wght"), Value: 700}})
	// Keep regular faces separate: variation settings and glyph caches are mutable.
	chineseRegularFont := textfont.NewFace(chineseFont.Font)
	chineseRegularFont.SetVariations([]textfont.Variation{{Tag: textopentype.MustNewTag("wght"), Value: 400}})
	devanagariRegularFont := textfont.NewFace(devanagariFont.Font)
	devanagariRegularFont.SetVariations([]textfont.Variation{{Tag: textopentype.MustNewTag("wght"), Value: 400}})
	versionInput := bytes.Join([][]byte{[]byte(socialCardDesignVersion), socialCardBackground, socialCardBrand, euAISocialLabel, notoSansSC, notoSansDevanagari, []byte(socialCardXMP)}, nil)
	digest := sha256.Sum256(versionInput)
	return &socialCardRenderer{
		background: background, brand: brand, aiLabel: aiLabel, boldFont: boldFont, regularFont: regularFont,
		chineseRegularFont: chineseRegularFont, devanagariRegularFont: devanagariRegularFont,
		chineseFont: chineseFont, devanagariFont: devanagariFont, version: fmt.Sprintf("%x", digest[:8]),
	}, nil
}

func (r *socialCardRenderer) render(spec socialCardSpec) ([]byte, error) {
	canvas := image.NewRGBA(r.background.Bounds())
	draw.Draw(canvas, canvas.Bounds(), r.background, image.Point{}, draw.Src)

	eyebrowFace, closeEyebrow, err := r.face(r.boldFont, 16)
	if err != nil {
		return nil, err
	}
	defer closeEyebrow()
	titleSize := 52.0
	titleMaxWidth := socialTextMaxWidth
	if strings.TrimSpace(spec.Eyebrow) != "" {
		titleSize = socialIncidentTitleSize
		titleMaxWidth = socialIncidentTitleMaxWidth
	} else if spec.Subtitle != "" {
		titleSize = socialHomeTitleSize
	}
	titleFace, closeTitle, err := r.face(r.boldFont, titleSize)
	if err != nil {
		return nil, err
	}
	defer closeTitle()
	localizedEyebrowFace := r.localizedFace(spec.LanguageTag, 16, eyebrowFace)
	localizedTitleFace := r.localizedFace(spec.LanguageTag, titleSize, titleFace)
	urlFace, closeURL, err := r.face(r.boldFont, 18)
	if err != nil {
		return nil, err
	}
	defer closeURL()

	draw.Draw(canvas, r.brand.Bounds().Add(image.Pt(socialTextLeft, 49)), r.brand, r.brand.Bounds().Min, draw.Over)
	if spec.AIGenerated {
		xdraw.CatmullRom.Scale(canvas, image.Rect(899, 55, 1130, 129), r.aiLabel, r.aiLabel.Bounds(), draw.Over, nil)
	}

	spec.Eyebrow = normalizeSocialText(spec.Eyebrow)
	spec.Title = normalizeSocialText(spec.Title)
	spec.Subtitle = normalizeSocialText(spec.Subtitle)
	titleY := 235
	if spec.Subtitle != "" {
		titleY = 203
	}
	if strings.TrimSpace(spec.Eyebrow) != "" {
		localizedEyebrowFace.draw(canvas, socialAlert, localizedUpper(spec.LanguageTag, spec.Eyebrow), socialTextLeft, 170)
		titleY = 252
	}
	titleLines := wrapSocialTitle(spec.Title, localizedTitleFace, titleMaxWidth, 2)
	for index, line := range titleLines {
		localizedTitleFace.draw(canvas, socialInk, line, socialTextLeft, titleY+index*62)
	}
	urlY := 385
	if spec.Subtitle != "" {
		subtitleFace, closeSubtitle, err := r.face(r.regularFont, socialSubtitleSize)
		if err != nil {
			return nil, err
		}
		defer closeSubtitle()
		localizedSubtitleFace := r.localizedRegularFace(spec.LanguageTag, socialSubtitleSize, subtitleFace)
		subtitleLines := wrapSocialSubtitle(spec.Subtitle, localizedSubtitleFace, socialSubtitleMaxWidth, socialSubtitleMaxLines)
		subtitleY := titleY + (len(titleLines)-1)*62 + 50
		for index, line := range subtitleLines {
			localizedSubtitleFace.draw(canvas, socialMuted, line, socialTextLeft, subtitleY+index*socialSubtitleLineHeight)
		}
		urlY = max(urlY, subtitleY+(len(subtitleLines)-1)*socialSubtitleLineHeight+50)
	}
	drawText(canvas, urlFace, socialCivic, "munichbrief.de", socialTextLeft, urlY)

	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, fmt.Errorf("encode social card: %w", err)
	}
	// The Olympiapark illustration was generated with AI. Embed provenance in
	// every final card because decoding and re-encoding drops source PNG metadata.
	return embedPNGXMP(output.Bytes(), []byte(socialCardXMP))
}

func (r *socialCardRenderer) localizedFace(tag string, size float64, fallback xfont.Face) socialTextFace {
	base, _ := language.Make(tag).Base()
	switch base.String() {
	case "zh":
		return shapedSocialTextFace{renderer: r, face: r.chineseFont, size: fixed.Int26_6(size * 64), script: textlanguage.Han, language: textlanguage.NewLanguage(tag)}
	case "hi":
		return shapedSocialTextFace{renderer: r, face: r.devanagariFont, size: fixed.Int26_6(size * 64), script: textlanguage.Devanagari, language: textlanguage.NewLanguage(tag)}
	default:
		return basicSocialTextFace{face: fallback}
	}
}

func (r *socialCardRenderer) localizedRegularFace(tag string, size float64, fallback xfont.Face) socialTextFace {
	face := r.localizedFace(tag, size, fallback)
	if shaped, ok := face.(shapedSocialTextFace); ok {
		switch shaped.script {
		case textlanguage.Han:
			shaped.face = r.chineseRegularFont
		case textlanguage.Devanagari:
			shaped.face = r.devanagariRegularFont
		}
		return shaped
	}
	return face
}

func (f shapedSocialTextFace) output(value string) shaping.Output {
	runes := []rune(value)
	return f.renderer.shaper.Shape(shaping.Input{
		Text: runes, RunStart: 0, RunEnd: len(runes), Direction: di.DirectionLTR,
		Face: f.face, Size: f.size, Script: f.script, Language: f.language,
	})
}

func (f shapedSocialTextFace) measure(value string) int {
	f.renderer.shapeMu.Lock()
	defer f.renderer.shapeMu.Unlock()
	return f.output(value).Advance.Ceil()
}

func (f shapedSocialTextFace) wordSeparator() string {
	if f.script == textlanguage.Han {
		return ""
	}
	return " "
}

func (f shapedSocialTextFace) draw(destination draw.Image, textColor color.Color, value string, x, baseline int) {
	f.renderer.shapeMu.Lock()
	defer f.renderer.shapeMu.Unlock()
	output := f.output(value)
	rasterizer := vector.NewRasterizer(destination.Bounds().Dx(), destination.Bounds().Dy())
	dotX := fixed.I(x)
	for _, glyph := range output.Glyphs {
		if glyph.GlyphID == textfont.EmptyGlyph {
			dotX += glyph.Advance
			continue
		}
		outline, ok := output.Face.GlyphData(glyph.GlyphID).(textfont.GlyphOutline)
		if !ok {
			dotX += glyph.Advance
			continue
		}
		glyphX := dotX + glyph.XOffset
		glyphY := fixed.I(baseline) - glyph.YOffset
		for _, segment := range outline.Segments {
			points := segment.ArgsSlice()
			point := func(index int) (float32, float32) {
				return float32(glyphX)/64 + float32(output.FromFontUnit(points[index].X))/64,
					float32(glyphY)/64 - float32(output.FromFontUnit(points[index].Y))/64
			}
			switch segment.Op {
			case textopentype.SegmentOpMoveTo:
				x0, y0 := point(0)
				rasterizer.MoveTo(x0, y0)
			case textopentype.SegmentOpLineTo:
				x0, y0 := point(0)
				rasterizer.LineTo(x0, y0)
			case textopentype.SegmentOpQuadTo:
				x0, y0 := point(0)
				x1, y1 := point(1)
				rasterizer.QuadTo(x0, y0, x1, y1)
			case textopentype.SegmentOpCubeTo:
				x0, y0 := point(0)
				x1, y1 := point(1)
				x2, y2 := point(2)
				rasterizer.CubeTo(x0, y0, x1, y1, x2, y2)
			}
		}
		dotX += glyph.Advance
	}
	rasterizer.Draw(destination, destination.Bounds(), image.NewUniform(textColor), image.Point{})
}

func (r *socialCardRenderer) etag(spec socialCardSpec) string {
	digest := sha256.Sum256([]byte(r.version + "\x00" + spec.LanguageTag + "\x00" + spec.Eyebrow + "\x00" + spec.Title + "\x00" + spec.Subtitle + "\x00" + strconv.FormatBool(spec.AIGenerated) + "\x00" + spec.AIModel + "\x00" + spec.CacheIdentity))
	return fmt.Sprintf(`"%x"`, digest[:12])
}

func normalizeSocialText(value string) string {
	value = norm.NFC.String(value)
	return strings.NewReplacer("\u02bc", "\u2019", "\u2011", "-").Replace(value)
}

func localizedUpper(tag, value string) string {
	return cases.Upper(language.Make(tag)).String(value)
}

func embedPNGXMP(contents, packet []byte) ([]byte, error) {
	if len(contents) < 8 || !bytes.Equal(contents[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return nil, errors.New("embed XMP: invalid PNG signature")
	}
	for offset := 8; offset+12 <= len(contents); {
		length := int(binary.BigEndian.Uint32(contents[offset : offset+4]))
		chunkEnd := offset + 12 + length
		if chunkEnd > len(contents) {
			return nil, errors.New("embed XMP: invalid PNG chunk length")
		}
		if string(contents[offset+4:offset+8]) == "IEND" {
			payload := append([]byte("XML:com.adobe.xmp\x00\x00\x00\x00\x00"), packet...)
			chunk := make([]byte, 12+len(payload))
			binary.BigEndian.PutUint32(chunk[:4], uint32(len(payload)))
			copy(chunk[4:8], "iTXt")
			copy(chunk[8:8+len(payload)], payload)
			binary.BigEndian.PutUint32(chunk[8+len(payload):], crc32.ChecksumIEEE(chunk[4:8+len(payload)]))
			result := make([]byte, 0, len(contents)+len(chunk))
			result = append(result, contents[:offset]...)
			result = append(result, chunk...)
			result = append(result, contents[offset:]...)
			return result, nil
		}
		offset = chunkEnd
	}
	return nil, errors.New("embed XMP: PNG has no IEND chunk")
}

func (r *socialCardRenderer) face(parsed *opentype.Font, size float64) (xfont.Face, func(), error) {
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: xfont.HintingFull})
	if err != nil {
		return nil, func() {}, fmt.Errorf("create %.0fpx font face: %w", size, err)
	}
	closeFace := func() {
		if closer, ok := face.(io.Closer); ok {
			_ = closer.Close()
		}
	}
	return face, closeFace, nil
}

func drawText(destination draw.Image, face xfont.Face, textColor color.Color, text string, x, baseline int) {
	drawer := xfont.Drawer{Dst: destination, Src: image.NewUniform(textColor), Face: face, Dot: fixed.P(x, baseline)}
	drawer.DrawString(text)
}

func wrapSocialTitle(value string, face socialTextFace, maxWidth, maxLines int) []string {
	separator := face.wordSeparator()
	words := strings.Fields(value)
	if separator == "" {
		words = hanSocialTitleTokens(value)
	}
	if len(words) == 0 || maxLines < 1 {
		return nil
	}
	lines := make([]string, 0, maxLines)
	current := ""
	for _, word := range words {
		candidate := strings.TrimLeftFunc(word, unicode.IsSpace)
		if current != "" && separator == "" {
			candidate = current + word
		} else if current != "" {
			candidate = current + separator + word
		}
		if face.measure(candidate) <= maxWidth {
			current = candidate
			continue
		}
		if current != "" {
			lines = append(lines, strings.TrimSpace(current))
			current = strings.TrimLeftFunc(word, unicode.IsSpace)
			if face.measure(current) > maxWidth {
				lines = append(lines, truncateSocialText(current, face, maxWidth))
				current = ""
			}
		} else {
			lines = append(lines, truncateSocialText(word, face, maxWidth))
		}
	}
	if current != "" {
		lines = append(lines, strings.TrimSpace(current))
	}
	if len(lines) <= maxLines {
		return lines
	}
	remainder := strings.Join(lines[maxLines-1:], separator)
	lines = lines[:maxLines]
	lines[maxLines-1] = truncateSocialText(remainder, face, maxWidth)
	return lines
}

// Balance word-spaced subtitles across the minimum number of lines. This avoids
// leaving just the language name on a final line. Han keeps its script-aware
// wrapping, which preserves embedded Latin words and their original spacing.
func wrapSocialSubtitle(value string, face socialTextFace, maxWidth, maxLines int) []string {
	wrapped := wrapSocialTitle(value, face, maxWidth, maxLines)
	if len(wrapped) < 2 || face.wordSeparator() == "" {
		return wrapped
	}
	words := strings.Fields(value)
	bestScore := int(^uint(0) >> 1)
	var best []string
	var fit func(int, []string, int)
	fit = func(start int, lines []string, score int) {
		remaining := len(wrapped) - len(lines)
		for end := start + 1; end <= len(words)-(remaining-1); end++ {
			if remaining == 1 && end != len(words) {
				continue
			}
			line := strings.Join(words[start:end], " ")
			width := face.measure(line)
			if width > maxWidth {
				break
			}
			nextScore := score + (maxWidth-width)*(maxWidth-width)
			if nextScore >= bestScore {
				continue
			}
			next := append(lines, line)
			if remaining == 1 {
				bestScore = nextScore
				best = append([]string(nil), next...)
			} else {
				fit(end, next, nextScore)
			}
		}
	}
	fit(0, nil, 0)
	if best != nil {
		return best
	}
	return wrapped
}

func hanSocialTitleTokens(value string) []string {
	fields := strings.Fields(value)
	tokens := make([]string, 0, len([]rune(value)))
	for fieldIndex, field := range fields {
		fieldTokens := make([]string, 0, len([]rune(field)))
		var nonHan strings.Builder
		flushNonHan := func() {
			if nonHan.Len() == 0 {
				return
			}
			fieldTokens = append(fieldTokens, nonHan.String())
			nonHan.Reset()
		}
		for _, character := range field {
			if unicode.Is(unicode.Han, character) {
				flushNonHan()
				fieldTokens = append(fieldTokens, string(character))
				continue
			}
			nonHan.WriteRune(character)
		}
		flushNonHan()
		if fieldIndex > 0 && len(fieldTokens) > 0 {
			fieldTokens[0] = " " + fieldTokens[0]
		}
		tokens = append(tokens, fieldTokens...)
	}
	return tokens
}

func truncateSocialText(value string, face socialTextFace, maxWidth int) string {
	value = strings.TrimSpace(value)
	if face.measure(value) <= maxWidth {
		return value
	}
	runes := []rune(value)
	for len(runes) > 0 {
		candidate := strings.TrimSpace(string(runes)) + "…"
		if face.measure(candidate) <= maxWidth {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return "…"
}

func (s *Server) socialHome(response http.ResponseWriter, request *http.Request) {
	language, ok := s.socialCardLanguage(request)
	if !ok {
		http.NotFound(response, request)
		return
	}
	s.writeSocialCard(response, request, language, socialCardSpec{
		Title:    s.localization.Text(language, "HeroTitle"),
		Subtitle: s.localization.Text(language, "HeroCopy"),
	})
}

func (s *Server) socialAbout(response http.ResponseWriter, request *http.Request) {
	language, ok := s.socialCardLanguage(request)
	if !ok {
		http.NotFound(response, request)
		return
	}
	s.writeSocialCard(response, request, language, socialCardSpec{Title: s.localization.Text(language, "About") + " MunichBrief"})
}

func (s *Server) socialIncident(response http.ResponseWriter, request *http.Request) {
	language, ok := s.socialCardLanguage(request)
	if !ok {
		http.NotFound(response, request)
		return
	}
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(response, request)
		return
	}
	scope := store.PresentationScope{
		Language: language, TranslationLanguage: language,
		PublicOnly: s.options.PresentationMode == "public" || s.isPublicRequest(request),
	}
	incident, err := s.store.GetPresentationIncident(request.Context(), id, scope)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(response, request)
		return
	}
	if err != nil {
		s.internalError(response, request, "get incident social card", err)
		return
	}
	if (s.options.SourceMode == "fixture" && incident.FetchStatus != "fixture") || (s.options.SourceMode == "live" && incident.FetchStatus == "fixture") {
		http.NotFound(response, request)
		return
	}
	view := s.incidentForLanguage(incident, language)
	cacheIdentity := ""
	var latestVerification *time.Time
	for _, generatedAt := range []*time.Time{view.Record.AIPublicAssistanceVerificationGeneratedAt, view.Record.AICategoryVerificationGeneratedAt} {
		if generatedAt != nil && (latestVerification == nil || generatedAt.After(*latestVerification)) {
			latestVerification = generatedAt
		}
	}
	if latestVerification != nil {
		cacheIdentity = latestVerification.UTC().Format(time.RFC3339Nano)
	}
	s.writeSocialCard(response, request, language, socialCardSpec{Eyebrow: s.localization.Text(language, "SocialIncidentLabel"), Title: view.Title, AIGenerated: view.Record.HasAI, AIModel: view.Record.AIModel, CacheIdentity: cacheIdentity})
}

func (s *Server) socialCardLanguage(request *http.Request) (string, bool) {
	language := request.PathValue("language")
	_, registered := s.languageByCode(language)
	return language, registered
}

func (s *Server) writeSocialCard(response http.ResponseWriter, request *http.Request, language string, spec socialCardSpec) {
	definition, _ := s.languageByCode(language)
	spec.LanguageTag = definition.Tag.String()
	etag := s.socialCards.etag(spec)
	response.Header().Set("Content-Type", "image/png")
	response.Header().Set("Content-Language", definition.Tag.String())
	response.Header().Set("Cache-Control", "public, max-age=3600, stale-while-revalidate=86400")
	response.Header().Set("ETag", etag)
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("X-AI-Generated", strconv.FormatBool(spec.AIGenerated))
	response.Header().Set("X-AI-Generated-Background", "true")
	response.Header().Set("X-IPTC-Digital-Source-Type", iptcCompositeWithTrainedAlgorithmicMedia)
	if spec.AIGenerated {
		response.Header().Set("X-AI-Generated-Text", "true")
	}
	if spec.AIModel != "" {
		response.Header().Set("X-AI-Model", safeMetadataHeader(spec.AIModel))
	}
	if request.Header.Get("If-None-Match") == etag {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	contents, err := s.socialCards.render(spec)
	if err != nil {
		s.internalError(response, request, "render social card", err)
		return
	}
	response.Header().Set("Content-Length", strconv.Itoa(len(contents)))
	_, _ = response.Write(contents)
}
