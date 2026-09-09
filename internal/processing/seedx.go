package processing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	langregistry "github.com/egekocabas/munichbrief/internal/languages"
)

const (
	seedXContextLimit        = 4096
	seedXMaximumOutputTokens = 512
)

type seedXLanguage struct {
	name string
	tag  string
}

var seedXLanguages = map[string]seedXLanguage{
	"ar": {name: "Arabic", tag: "ar"}, "fr": {name: "French", tag: "fr"},
	"ms": {name: "Malay", tag: "ms"}, "ru": {name: "Russian", tag: "ru"},
	"cs": {name: "Czech", tag: "cs"}, "hr": {name: "Croatian", tag: "hr"},
	"nb": {name: "Norwegian Bokmal", tag: "nb"}, "sv": {name: "Swedish", tag: "sv"},
	"da": {name: "Danish", tag: "da"}, "hu": {name: "Hungarian", tag: "hu"},
	"nl": {name: "Dutch", tag: "nl"}, "th": {name: "Thai", tag: "th"},
	"de": {name: "German", tag: "de"}, "id": {name: "Indonesian", tag: "id"},
	"no": {name: "Norwegian", tag: "no"}, "tr": {name: "Turkish", tag: "tr"},
	"en": {name: "English", tag: "en"}, "it": {name: "Italian", tag: "it"},
	"pl": {name: "Polish", tag: "pl"}, "uk": {name: "Ukrainian", tag: "uk"},
	"es": {name: "Spanish", tag: "es"}, "ja": {name: "Japanese", tag: "ja"},
	"pt": {name: "Portuguese", tag: "pt"}, "vi": {name: "Vietnamese", tag: "vi"},
	"fi": {name: "Finnish", tag: "fi"}, "ko": {name: "Korean", tag: "ko"},
	"ro": {name: "Romanian", tag: "ro"}, "zh": {name: "Chinese", tag: "zh"},
}

var seedXLanguageGuidance = map[string]string{}

// SeedXNativeAdapter uses raw completion because ByteDance explicitly defines
// Seed-X without a chat template. Production routing remains opt-in per
// language because local quantizations require separate evaluation.
type SeedXNativeAdapter struct {
	client            *OllamaClient
	source            seedXLanguage
	target            seedXLanguage
	targetGuidance    string
	generationOptions chatOptions
}

func NewSeedXNativeAdapter(baseURL, model, targetCode string, timeout time.Duration, contextSize int, baseClient *http.Client) (*SeedXNativeAdapter, error) {
	definitions := langregistry.Registered()
	if err := langregistry.Validate(definitions); err != nil {
		return nil, err
	}
	sourceDefinition := langregistry.Canonical(definitions)
	targetDefinition, found := langregistry.ByCode(definitions, targetCode)
	if !found || targetDefinition.Canonical {
		return nil, errors.New("Seed-X native adapter requires a translated target language")
	}
	source, sourceSupported := seedXLanguages[sourceDefinition.Code]
	target, targetSupported := seedXLanguages[targetDefinition.Code]
	if !sourceSupported {
		return nil, fmt.Errorf("Seed-X does not officially support source language %q", sourceDefinition.Code)
	}
	if !targetSupported {
		return nil, fmt.Errorf("Seed-X does not officially support target language %q", targetDefinition.Code)
	}
	if contextSize <= 0 || contextSize > seedXContextLimit {
		contextSize = seedXContextLimit
	}
	client, err := NewOllamaClient(baseURL, model, timeout, contextSize, baseClient)
	if err != nil {
		return nil, err
	}
	return &SeedXNativeAdapter{
		client: client, source: source, target: target,
		targetGuidance:    seedXLanguageGuidance[targetDefinition.Code],
		generationOptions: chatOptions{Temperature: 0, NumPredict: seedXMaximumOutputTokens, NumCtx: contextSize},
	}, nil
}

func (a *SeedXNativeAdapter) Translate(ctx context.Context, text string) (string, string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", errorOf(ErrorOutput, "Seed-X native input is empty")
	}
	content, model, err := a.client.rawGenerate(ctx, seedXNativePrompt(a.source, a.target, a.targetGuidance, text), a.generationOptions)
	if err != nil {
		return "", model, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", model, errorOf(ErrorOutput, "Seed-X native output is empty")
	}
	return content, model, nil
}

func seedXNativePrompt(source, target seedXLanguage, guidance, text string) string {
	guidance = strings.TrimSpace(guidance)
	if guidance != "" {
		guidance = " " + guidance
	}
	return fmt.Sprintf(
		"Translate the following %s sentence into %s:%s\n%s <%s>",
		source.name, target.name, guidance, strings.TrimSpace(text), target.tag,
	)
}
