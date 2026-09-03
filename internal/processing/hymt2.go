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

const hyMT2MaximumOutputTokens = 4096

var hyMT2LanguageNames = map[string]string{
	"zh": "Chinese", "en": "English", "fr": "French", "pt": "Portuguese",
	"es": "Spanish", "ja": "Japanese", "tr": "Turkish", "ru": "Russian",
	"ar": "Arabic", "ko": "Korean", "th": "Thai", "it": "Italian",
	"de": "German", "vi": "Vietnamese", "ms": "Malay", "id": "Indonesian",
	"tl": "Filipino", "hi": "Hindi", "zh-Hant": "Traditional Chinese", "pl": "Polish",
	"cs": "Czech", "nl": "Dutch", "km": "Khmer", "my": "Burmese",
	"fa": "Persian", "gu": "Gujarati", "ur": "Urdu", "te": "Telugu",
	"mr": "Marathi", "he": "Hebrew", "bn": "Bengali", "ta": "Tamil",
	"uk": "Ukrainian", "bo": "Tibetan", "kk": "Kazakh", "mn": "Mongolian",
	"ug": "Uyghur", "yue": "Cantonese",
}

// HyMT2NativeAdapter follows Tencent's user-only translation contract and
// recommended 1.8B/7B sampling parameters. It remains separate from production
// routing while its translation quality and placeholder handling are evaluated.
type HyMT2NativeAdapter struct {
	client            *OllamaClient
	sourceName        string
	targetName        string
	generationOptions chatOptions
}

func NewHyMT2NativeAdapter(baseURL, model, targetCode string, timeout time.Duration, contextSize int, baseClient *http.Client) (*HyMT2NativeAdapter, error) {
	definitions := langregistry.Registered()
	if err := langregistry.Validate(definitions); err != nil {
		return nil, err
	}
	source := langregistry.Canonical(definitions)
	target, found := langregistry.ByCode(definitions, targetCode)
	if !found || target.Canonical {
		return nil, errors.New("Hy-MT2 native adapter requires a translated target language")
	}
	sourceName, sourceSupported := hyMT2LanguageNames[source.Code]
	targetName, targetSupported := hyMT2LanguageNames[target.Code]
	if !sourceSupported {
		return nil, fmt.Errorf("Hy-MT2 does not officially support source language %q", source.Code)
	}
	if !targetSupported {
		return nil, fmt.Errorf("Hy-MT2 does not officially support target language %q", target.Code)
	}
	client, err := NewOllamaClient(baseURL, model, timeout, contextSize, baseClient)
	if err != nil {
		return nil, err
	}
	return &HyMT2NativeAdapter{
		client:     client,
		sourceName: sourceName,
		targetName: targetName,
		generationOptions: chatOptions{
			Temperature:   0.7,
			TopP:          0.6,
			TopK:          20,
			RepeatPenalty: 1.05,
			NumPredict:    hyMT2MaximumOutputTokens,
			NumCtx:        contextSize,
		},
	}, nil
}

func (a *HyMT2NativeAdapter) Translate(ctx context.Context, text string) (string, string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", errorOf(ErrorOutput, "Hy-MT2 native input is empty")
	}
	content, model, err := a.client.chatWithOptions(ctx, true, "", hyMT2NativePrompt(a.sourceName, a.targetName, text), nil, a.generationOptions)
	if err != nil {
		return "", model, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", model, errorOf(ErrorOutput, "Hy-MT2 native output is empty")
	}
	return content, model, nil
}

func hyMT2NativePrompt(sourceName, targetName, text string) string {
	return fmt.Sprintf("Translate the following text from %s into %s. Never translate or alter code tags or variable placeholders; leave them exactly in their original code form. Note that you must ONLY output the translated result without any additional explanation:\n\n%s", sourceName, targetName, strings.TrimSpace(text))
}
