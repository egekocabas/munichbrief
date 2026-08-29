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

	xdraw "golang.org/x/image/draw"
	xfont "golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/egekocabas/munichbrief/internal/store"
)

const (
	socialCardWidth     = 1200
	socialCardHeight    = 630
	socialTextLeft      = 70
	socialTextMaxWidth  = 550
	socialTextRightEdge = socialTextLeft + socialTextMaxWidth
	socialCardXMP       = `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?><x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description rdf:about="" xmlns:Iptc4xmpExt="http://iptc.org/std/Iptc4xmpExt/2008-02-29/" xmlns:xmp="http://ns.adobe.com/xap/1.0/" Iptc4xmpExt:DigitalSourceType="` + iptcCompositeWithTrainedAlgorithmicMedia + `" xmp:CreatorTool="MunichBrief"/></rdf:RDF></x:xmpmeta><?xpacket end="w"?>`
)

var (
	socialInk                = color.RGBA{R: 20, G: 32, B: 43, A: 255}
	socialCivic              = color.RGBA{R: 23, G: 75, B: 115, A: 255}
	socialAlert              = color.RGBA{R: 163, G: 58, B: 48, A: 255}
	socialPaper              = color.RGBA{R: 255, G: 254, B: 250, A: 255}
	sharedSocialCardRenderer = sync.OnceValues(buildSocialCardRenderer)
)

type socialCardRenderer struct {
	background  *image.RGBA
	aiLabel     image.Image
	boldFont    *opentype.Font
	regularFont *opentype.Font
	version     string
}

type socialCardSpec struct {
	Eyebrow       string
	Title         string
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
	versionInput := append(append(append([]byte{}, socialCardBackground...), euAISocialLabel...), socialCardXMP...)
	digest := sha256.Sum256(versionInput)
	return &socialCardRenderer{background: background, aiLabel: aiLabel, boldFont: boldFont, regularFont: regularFont, version: fmt.Sprintf("%x", digest[:8])}, nil
}

func (r *socialCardRenderer) render(spec socialCardSpec) ([]byte, error) {
	canvas := image.NewRGBA(r.background.Bounds())
	draw.Draw(canvas, canvas.Bounds(), r.background, image.Point{}, draw.Src)

	logoFace, closeLogo, err := r.face(r.boldFont, 36)
	if err != nil {
		return nil, err
	}
	defer closeLogo()
	brandFace, closeBrand, err := r.face(r.boldFont, 30)
	if err != nil {
		return nil, err
	}
	defer closeBrand()
	eyebrowFace, closeEyebrow, err := r.face(r.boldFont, 16)
	if err != nil {
		return nil, err
	}
	defer closeEyebrow()
	titleFace, closeTitle, err := r.face(r.boldFont, 52)
	if err != nil {
		return nil, err
	}
	defer closeTitle()
	urlFace, closeURL, err := r.face(r.boldFont, 18)
	if err != nil {
		return nil, err
	}
	defer closeURL()

	drawRoundedRect(canvas, image.Rect(70, 55, 122, 107), 8, socialCivic)
	drawCenteredText(canvas, logoFace, socialPaper, "M", image.Rect(70, 55, 122, 107), 94)
	drawText(canvas, brandFace, socialInk, "MunichBrief", 141, 91)
	if spec.AIGenerated {
		xdraw.CatmullRom.Scale(canvas, image.Rect(899, 55, 1130, 129), r.aiLabel, r.aiLabel.Bounds(), draw.Over, nil)
	}

	titleY := 235
	if strings.TrimSpace(spec.Eyebrow) != "" {
		drawText(canvas, eyebrowFace, socialAlert, strings.ToUpper(spec.Eyebrow), socialTextLeft, 170)
		titleY = 252
	}
	for index, line := range wrapSocialTitle(spec.Title, titleFace, socialTextMaxWidth, 2) {
		drawText(canvas, titleFace, socialInk, line, socialTextLeft, titleY+index*62)
	}
	drawText(canvas, urlFace, socialCivic, "munichbrief.de", socialTextLeft, 385)

	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, fmt.Errorf("encode social card: %w", err)
	}
	// The Olympiapark illustration was generated with AI. Embed provenance in
	// every final card because decoding and re-encoding drops source PNG metadata.
	return embedPNGXMP(output.Bytes(), []byte(socialCardXMP))
}

func (r *socialCardRenderer) etag(spec socialCardSpec) string {
	digest := sha256.Sum256([]byte(r.version + "\x00" + spec.Eyebrow + "\x00" + spec.Title + "\x00" + strconv.FormatBool(spec.AIGenerated) + "\x00" + spec.AIModel + "\x00" + spec.CacheIdentity))
	return fmt.Sprintf(`"%x"`, digest[:12])
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

func drawCenteredText(destination draw.Image, face xfont.Face, textColor color.Color, text string, bounds image.Rectangle, baseline int) {
	width := xfont.MeasureString(face, text).Ceil()
	drawText(destination, face, textColor, text, bounds.Min.X+(bounds.Dx()-width)/2, baseline)
}

func drawRoundedRect(destination draw.Image, bounds image.Rectangle, radius int, fill color.Color) {
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dx := max(bounds.Min.X+radius-x, x-(bounds.Max.X-radius-1), 0)
			dy := max(bounds.Min.Y+radius-y, y-(bounds.Max.Y-radius-1), 0)
			if dx*dx+dy*dy <= radius*radius {
				destination.Set(x, y, fill)
			}
		}
	}
}

func wrapSocialTitle(value string, face xfont.Face, maxWidth, maxLines int) []string {
	words := strings.Fields(value)
	if len(words) == 0 || maxLines < 1 {
		return nil
	}
	lines := make([]string, 0, maxLines)
	current := ""
	for _, word := range words {
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if xfont.MeasureString(face, candidate).Ceil() <= maxWidth {
			current = candidate
			continue
		}
		if current != "" {
			lines = append(lines, current)
			current = word
		} else {
			lines = append(lines, truncateSocialText(word, face, maxWidth))
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	if len(lines) <= maxLines {
		return lines
	}
	remainder := strings.Join(lines[maxLines-1:], " ")
	lines = lines[:maxLines]
	lines[maxLines-1] = truncateSocialText(remainder, face, maxWidth)
	return lines
}

func truncateSocialText(value string, face xfont.Face, maxWidth int) string {
	value = strings.TrimSpace(value)
	if xfont.MeasureString(face, value).Ceil() <= maxWidth {
		return value
	}
	runes := []rune(value)
	for len(runes) > 0 {
		candidate := strings.TrimSpace(string(runes)) + "…"
		if xfont.MeasureString(face, candidate).Ceil() <= maxWidth {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return "…"
}

func (s *Server) socialHome(response http.ResponseWriter, request *http.Request) {
	language, ok := socialCardLanguage(request)
	if !ok {
		http.NotFound(response, request)
		return
	}
	s.writeSocialCard(response, request, socialCardSpec{Title: s.localization.Text(language, "BrandTagline")})
}

func (s *Server) socialAbout(response http.ResponseWriter, request *http.Request) {
	language, ok := socialCardLanguage(request)
	if !ok {
		http.NotFound(response, request)
		return
	}
	s.writeSocialCard(response, request, socialCardSpec{Title: s.localization.Text(language, "About") + " MunichBrief"})
}

func (s *Server) socialIncident(response http.ResponseWriter, request *http.Request) {
	language, ok := socialCardLanguage(request)
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
	s.writeSocialCard(response, request, socialCardSpec{Eyebrow: s.localization.Text(language, "SocialIncidentLabel"), Title: view.Title, AIGenerated: view.Record.HasAI, AIModel: view.Record.AIModel, CacheIdentity: cacheIdentity})
}

func socialCardLanguage(request *http.Request) (string, bool) {
	language := request.PathValue("language")
	_, registered := readerLanguageByCode(language)
	return language, registered
}

func (s *Server) writeSocialCard(response http.ResponseWriter, request *http.Request, spec socialCardSpec) {
	etag := s.socialCards.etag(spec)
	response.Header().Set("Content-Type", "image/png")
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
