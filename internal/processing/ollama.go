package processing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrorKind classifies failures for retry, configuration, and privacy handling.
type ErrorKind string

const (
	ErrorTransient     ErrorKind = "transient"
	ErrorConfiguration ErrorKind = "configuration"
	ErrorOutput        ErrorKind = "output"
	ErrorPrivacy       ErrorKind = "privacy"
)

// ProcessingError attaches a stable operational classification to an error.
type ProcessingError struct {
	Kind ErrorKind
	Err  error
}

func (e *ProcessingError) Error() string { return e.Err.Error() }
func (e *ProcessingError) Unwrap() error { return e.Err }

func errorOf(kind ErrorKind, format string, arguments ...any) error {
	return &ProcessingError{Kind: kind, Err: fmt.Errorf(format, arguments...)}
}

// KindOf returns the pipeline classification, defaulting unknown errors to a
// retryable transient failure.
func KindOf(err error) ErrorKind {
	var processingError *ProcessingError
	if errors.As(err, &processingError) {
		return processingError.Kind
	}
	return ErrorTransient
}

// OllamaGeneratorProvider creates constrained clients for selected local models.
type OllamaGeneratorProvider struct {
	baseURL     string
	timeout     time.Duration
	contextSize int
	baseClient  *http.Client
}

func NewOllamaGeneratorProvider(baseURL string, timeout time.Duration, contextSize int, baseClient *http.Client) (*OllamaGeneratorProvider, error) {
	// Validate the shared connection settings once. Per-model clients retain the
	// exact model identity selected for each durable job.
	if _, err := NewOllamaClient(baseURL, "validation-model", timeout, contextSize, baseClient); err != nil {
		return nil, err
	}
	return &OllamaGeneratorProvider{baseURL: baseURL, timeout: timeout, contextSize: contextSize, baseClient: baseClient}, nil
}

func (p *OllamaGeneratorProvider) StepGenerator(model string) (StepGenerator, error) {
	return NewOllamaClient(p.baseURL, model, p.timeout, p.contextSize, p.baseClient)
}

func (p *OllamaGeneratorProvider) StepGeneratorFor(model, adapter string) (StepGenerator, error) {
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("ollama model is required")
	}
	if adapter == "" || adapter == TranslationAdapterStructured {
		return p.StepGenerator(model)
	}
	if adapter != TranslationAdapterTranslateGemma && adapter != TranslationAdapterHyMT2 && adapter != TranslationAdapterSeedX {
		return nil, fmt.Errorf("unknown translation adapter %q", adapter)
	}
	return &nativeTranslationStepGenerator{provider: p, model: strings.TrimSpace(model), adapter: adapter}, nil
}

// OllamaClient calls one model and treats every response as untrusted until the
// registered step's schema and validator both accept it.
type OllamaClient struct {
	endpoint         string
	generateEndpoint string
	model            string
	contextSize      int
	client           *http.Client
}

type chatRequest struct {
	Model     string          `json:"model"`
	Messages  []chatMessage   `json:"messages"`
	Stream    bool            `json:"stream"`
	Think     bool            `json:"think"`
	Format    json.RawMessage `json:"format,omitempty"`
	Options   chatOptions     `json:"options"`
	KeepAlive string          `json:"keep_alive"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatOptions struct {
	Temperature   float64 `json:"temperature"`
	TopP          float64 `json:"top_p,omitempty"`
	TopK          int     `json:"top_k,omitempty"`
	RepeatPenalty float64 `json:"repeat_penalty,omitempty"`
	NumPredict    int     `json:"num_predict,omitempty"`
	NumCtx        int     `json:"num_ctx"`
}

type chatResponse struct {
	Model   string      `json:"model"`
	Message chatMessage `json:"message"`
	Done    bool        `json:"done"`
}

type generateRequest struct {
	Model     string      `json:"model"`
	Prompt    string      `json:"prompt"`
	Stream    bool        `json:"stream"`
	Think     bool        `json:"think"`
	Raw       bool        `json:"raw"`
	Options   chatOptions `json:"options"`
	KeepAlive string      `json:"keep_alive"`
}

type generateResponse struct {
	Model    string `json:"model"`
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

func NewOllamaClient(baseURL, model string, timeout time.Duration, contextSize int, baseClient *http.Client) (*OllamaClient, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("ollama base URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("ollama model is required")
	}
	if timeout <= 0 || contextSize < 2048 {
		return nil, errors.New("ollama timeout and context size must be positive")
	}

	httpClient := &http.Client{Timeout: timeout}
	if baseClient != nil {
		clone := *baseClient
		httpClient = &clone
		httpClient.Timeout = timeout
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	chatURL := *parsed
	chatURL.Path = basePath + "/api/chat"
	generateURL := *parsed
	generateURL.Path = basePath + "/api/generate"
	return &OllamaClient{
		endpoint: chatURL.String(), generateEndpoint: generateURL.String(), model: strings.TrimSpace(model),
		contextSize: contextSize, client: httpClient,
	}, nil
}

func (c *OllamaClient) ModelIdentity() string { return c.model }

// GenerateStep minimizes input, requests structured output, and validates the
// decoded response before returning values to persistence.
func (c *OllamaClient) GenerateStep(ctx context.Context, step StepDefinition, input StepInput) (StepOutput, string, error) {
	if step.Generator == nil {
		return StepOutput{}, "", errorOf(ErrorConfiguration, "unknown pipeline step %q", step.Key)
	}
	requestInput, userContent, err := step.Generator(input)
	if err != nil {
		return StepOutput{}, "", err
	}
	content, modelIdentity, err := c.chat(ctx, step.UserOnly, step.SystemPrompt, userContent, step.Schema)
	if err != nil {
		return StepOutput{}, "", err
	}
	var output StepOutput
	if step.OutputDecoder != nil {
		output, err = step.OutputDecoder(content)
	} else {
		err = decodeStrictJSON(content, &output)
	}
	if err != nil {
		return StepOutput{}, "", errorOf(ErrorOutput, "decode structured %s output: %v", step.Key, err)
	}
	if err := ValidateStepOutput(step, requestInput, &output); err != nil {
		return StepOutput{}, "", err
	}
	return output, modelIdentity, nil
}

func (c *OllamaClient) chat(ctx context.Context, userOnly bool, system, user string, schema json.RawMessage) (string, string, error) {
	return c.chatWithOptions(ctx, userOnly, system, user, schema, chatOptions{Temperature: 0, NumCtx: c.contextSize})
}

func (c *OllamaClient) chatWithOptions(ctx context.Context, userOnly bool, system, user string, schema json.RawMessage, options chatOptions) (string, string, error) {
	if userOnly && strings.TrimSpace(system) != "" {
		return "", "", errorOf(ErrorConfiguration, "user-only Ollama prompt cannot include a system message")
	}
	if !userOnly && strings.TrimSpace(system) == "" {
		return "", "", errorOf(ErrorConfiguration, "Ollama system message is required")
	}
	messages := []chatMessage{{Role: "user", Content: user}}
	if !userOnly {
		messages = append([]chatMessage{{Role: "system", Content: system}}, messages...)
	}
	payload, err := json.Marshal(chatRequest{
		Model:    c.model,
		Messages: messages,
		Stream:   false, Think: false, Format: schema,
		Options: options, KeepAlive: "10m",
	})
	if err != nil {
		return "", "", errorOf(ErrorOutput, "encode ollama request: %v", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", "", errorOf(ErrorConfiguration, "create ollama request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return "", "", errorOf(ErrorTransient, "call Ollama: %v", err)
	}
	defer response.Body.Close()
	bodyBytes, err := readBounded(response.Body, 1<<20)
	if err != nil {
		return "", "", errorOf(ErrorTransient, "read ollama response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		kind := ErrorConfiguration
		if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			kind = ErrorTransient
		}
		return "", "", errorOf(kind, "Ollama returned HTTP %d", response.StatusCode)
	}
	var result chatResponse
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return "", "", errorOf(ErrorOutput, "decode ollama response: %v", err)
	}
	if !result.Done {
		return "", "", errorOf(ErrorTransient, "ollama response was incomplete")
	}
	modelIdentity := strings.TrimSpace(result.Model)
	if modelIdentity == "" {
		modelIdentity = c.model
	}
	return result.Message.Content, modelIdentity, nil
}

func (c *OllamaClient) rawGenerate(ctx context.Context, prompt string, options chatOptions) (string, string, error) {
	payload, err := json.Marshal(generateRequest{
		Model: c.model, Prompt: prompt, Stream: false, Think: false, Raw: true,
		Options: options, KeepAlive: "10m",
	})
	if err != nil {
		return "", "", errorOf(ErrorOutput, "encode ollama request: %v", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.generateEndpoint, bytes.NewReader(payload))
	if err != nil {
		return "", "", errorOf(ErrorConfiguration, "create ollama request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return "", "", errorOf(ErrorTransient, "call Ollama: %v", err)
	}
	defer response.Body.Close()
	bodyBytes, err := readBounded(response.Body, 1<<20)
	if err != nil {
		return "", "", errorOf(ErrorTransient, "read ollama response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		kind := ErrorConfiguration
		if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			kind = ErrorTransient
		}
		return "", "", errorOf(kind, "Ollama returned HTTP %d", response.StatusCode)
	}
	var result generateResponse
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return "", "", errorOf(ErrorOutput, "decode ollama response: %v", err)
	}
	if !result.Done {
		return "", "", errorOf(ErrorTransient, "ollama response was incomplete")
	}
	modelIdentity := strings.TrimSpace(result.Model)
	if modelIdentity == "" {
		modelIdentity = c.model
	}
	return result.Response, modelIdentity, nil
}

var privacyDetectors = []struct {
	label      string
	expression *regexp.Regexp
}{
	{label: "email address", expression: regexp.MustCompile(`(?i)\b[[:alnum:]._%+-]+@[[:alnum:].-]+\.[a-z]{2,}\b`)},
	{label: "web address", expression: regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+`)},
	{label: "social handle", expression: regexp.MustCompile(`(?:^|\s)@[[:alnum:]_]{2,}\b`)},
	{label: "telephone number", expression: regexp.MustCompile(`(?:\+49|\b0)[\d ()/.-]{7,}\d\b`)},
	{label: "date of birth", expression: regexp.MustCompile(`(?i)(?:geboren(?: am)?|geburtsdatum|born(?: on)?)\s*:?\s*\d{1,2}[./-]\d{1,2}[./-](?:19|20)?\d{2}\b`)},
	{label: "exact age", expression: regexp.MustCompile(`(?i)\b\d{1,3}[- ]?(?:jährig(?:e[rsn]?)?|year[- ]old)\b`)},
	{label: "vehicle registration", expression: regexp.MustCompile(`\b[A-ZÄÖÜ]{1,3}-[A-Z]{1,2}\s?\d{1,4}\b`)},
	{label: "case number", expression: regexp.MustCompile(`(?i)(?:aktenzeichen|vorgangsnummer|case(?: number)?|reference)\s*:?\s*(?:[A-Z]{1,10}[-/]?)?\d[A-Z0-9/-]{3,}`)},
	{label: "redaction marker", expression: regexp.MustCompile(`(?i)\[private detail omitted\]`)},
}

var (
	preciseStreetAddressPattern   = regexp.MustCompile(`(?i)\b[[:alpha:]ÄÖÜäöüß-]+(?:straße|strasse|str\.|weg|platz|allee|gasse)\s+(\d+)([a-z]?)\b`)
	localizedMonthAfterDayPattern = regexp.MustCompile(`(?i)^(?:[.,]\s*|\s+)(?:de\s+)?(?:` +
		`january|february|march|april|may|june|july|august|september|october|november|december|` +
		`januar|februar|märz|mai|juni|juli|oktober|dezember|` +
		`ocak|şubat|mart|nisan|mayıs|haziran|temmuz|ağustos|eylül|ekim|kasım|aralık|` +
		`siječnja|veljače|ožujka|travnja|svibnja|lipnja|srpnja|kolovoza|rujna|listopada|studenoga|prosinca|` +
		`gennaio|febbraio|marzo|maggio|giugno|luglio|agosto|settembre|ottobre|novembre|dicembre|` +
		`січня|лютого|березня|квітня|травня|червня|липня|серпня|вересня|жовтня|листопада|грудня|` +
		`januar|februar|mart|april|maj|juni|juli|avgust|august|septembar|oktobar|novembar|decembar|` +
		`जनवरी|फ़रवरी|मार्च|अप्रैल|मई|जून|जुलाई|अगस्त|सितंबर|अक्टूबर|नवंबर|दिसंबर|` +
		`enero|febrero|marzo|abril|mayo|junio|julio|agosto|septiembre|octubre|noviembre|diciembre|` +
		`janvier|février|mars|avril|mai|juin|juillet|août|septembre|octobre|novembre|décembre|` +
		`ιανουαρίου|φεβρουαρίου|μαρτίου|απριλίου|μαΐου|ιουνίου|ιουλίου|αυγούστου|σεπτεμβρίου|οκτωβρίου|νοεμβρίου|δεκεμβρίου|` +
		`ianuarie|februarie|martie|aprilie|mai|iunie|iulie|august|septembrie|octombrie|noiembrie|decembrie|` +
		`stycznia|lutego|marca|kwietnia|maja|czerwca|lipca|sierpnia|września|października|listopada|grudnia|` +
		`января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря` +
		`)(?:\s+de)?\s+\d{4}\b`)
	eastAsianDateAfterYearPattern = regexp.MustCompile(`^年(?:1[0-2]|[1-9])月(?:3[01]|[12]\d|[1-9])日`)
)

func containsPreciseStreetAddress(value string) bool {
	for _, indices := range preciseStreetAddressPattern.FindAllStringSubmatchIndex(value, -1) {
		number, err := strconv.Atoi(value[indices[2]:indices[3]])
		if err != nil {
			return true
		}
		// A letter suffix is an address component, never part of a calendar day.
		if indices[4] >= 0 && indices[4] != indices[5] {
			return true
		}
		tail := value[indices[1]:]
		if number >= 1 && number <= 31 && localizedMonthAfterDayPattern.MatchString(tail) {
			continue
		}
		if number >= 1900 && number <= 2100 && eastAsianDateAfterYearPattern.MatchString(tail) {
			continue
		}
		return true
	}
	return false
}

var sourceSensitiveDetectors = []*regexp.Regexp{
	regexp.MustCompile(`(?i)[^.!?]{0,160}\b(?:Hinweise|Kontakt|contact)\s+(?:an|unter|at|via)\b[^.!?]{0,200}[.!?]?`),
	regexp.MustCompile(`(?i)[^.!?]{0,80}\b(?:Aktenzeichen|Vorgangsnummer|case number|reference number)\b[^.!?]{0,120}[.!?]?`),
	regexp.MustCompile(`(?i)[^.!?]{0,80}\b(?:ignoriere|ignore)\b[^.!?]{0,240}[.!?]?`),
	regexp.MustCompile(`(?i)\b(?:[[:alpha:]ÄÖÜäöüß-]+\s+)?Staatsangehörig(?:e|er|en|em|es|keit)\b`),
	regexp.MustCompile(`(?i)[^.!?]{0,160}\b(?:wegen|aufgrund)\s+[^.!?]{0,80}(?:Erkrankung|Krankheit|Diagnose)\b[^.!?]{0,160}[.!?]?`),
	regexp.MustCompile(`(?i)[^.!?]{0,80}\bArbeitgeber(?:in)?\b[^.!?]{0,120}[.!?]?`),
	regexp.MustCompile(`(?i)[^.!?]{0,80}\b(?:besucht|attends)\b[^.!?]{0,120}[.!?]?`),
}

var sourceDirectReplacements = []struct {
	expression  *regexp.Regexp
	replacement string
}{
	{expression: regexp.MustCompile(`\b(?:Die|Der|Das|Eine|Ein)\s+\d{1,3}[- ]?jährig(?:e|er|es|en)?\s+[A-ZÄÖÜ][[:alpha:]ÄÖÜäöüß'-]{1,40}(?:\s+[A-ZÄÖÜ][[:alpha:]ÄÖÜäöüß'-]{1,40}){0,2}\b`), replacement: "Eine Person"},
	{expression: regexp.MustCompile(`(?i)[^.!?]{0,100}\bFahrzeug\s+[A-ZÄÖÜ]{1,3}-[A-Z]{1,2}\s?\d{1,4}\b[^.!?]{0,100}[.!?]?`), replacement: " "},
	{expression: regexp.MustCompile(`(?i)\b(?:in|an|aus)\s+(?:der|dem|den)?\s*[[:alpha:]ÄÖÜäöüß-]+(?:straße|strasse|str\.|weg|platz|allee|gasse)\s+\d+[a-z]?\b`), replacement: " "},
}

var repeatedFullStops = regexp.MustCompile(`(?:\s*\.\s*){2,}`)

var missingPersonAppealDetector = regexp.MustCompile(`(?i)\b(?:vermisst|vermisste[rsn]?|vermisstensuche|vermisstenfall)\b`)
var wantedPersonAppealDetector = regexp.MustCompile(`(?i)(?:öffentlichkeitsfahndung|fahndungsaufruf)\b`)

func minimizeIncidentSource(title, body string) (string, string) {
	source := title + "\n" + body
	if missingPersonAppealDetector.MatchString(source) {
		return "Vermisstenmeldung", "Die Polizei hat eine Vermisstenmeldung veröffentlicht. Identität, Beschreibung, Bild- und Kontaktdaten wurden aus Datenschutzgründen entfernt. Für Einzelheiten und Hinweise ist die offizielle Quelle maßgeblich."
	}
	if wantedPersonAppealDetector.MatchString(source) {
		return "Fahndungsaufruf", "Die Polizei hat einen Fahndungsaufruf veröffentlicht. Identität, Beschreibung, Bild- und Kontaktdaten wurden aus Datenschutzgründen entfernt. Für Einzelheiten und Hinweise ist die offizielle Quelle maßgeblich."
	}
	return redactDirectIdentifiers(title), redactDirectIdentifiers(body)
}

func redactDirectIdentifiers(value string) string {
	redacted := value
	for _, replacement := range sourceDirectReplacements {
		redacted = replacement.expression.ReplaceAllString(redacted, replacement.replacement)
	}
	for _, detector := range privacyDetectors {
		if detector.label == "redaction marker" {
			continue
		}
		redacted = detector.expression.ReplaceAllString(redacted, " ")
	}
	for _, expression := range sourceSensitiveDetectors {
		redacted = expression.ReplaceAllString(redacted, " ")
	}
	redacted = repeatedFullStops.ReplaceAllString(redacted, ". ")
	return strings.Join(strings.Fields(redacted), " ")
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	limited := io.LimitReader(reader, maximum+1)
	contents, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > maximum {
		return nil, fmt.Errorf("response exceeds %d bytes", maximum)
	}
	return contents, nil
}
