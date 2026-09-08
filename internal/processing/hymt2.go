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

var hyMT2LanguageGuidance = map[string]string{
	"en": "Keep neutral German person labels neutral: do not translate ‘Betroffener’ or ‘Betroffene’ as ‘victim’ unless the source explicitly identifies a victim. Preserve explicit medical and police-control meaning: ‘stationär in ein Krankenhaus gebracht’ means admitted to hospital as an inpatient, and ‘Anhaltesignale’ in a police-control context means police stop signals or orders, not road stop signs.",
	"es": "Keep generic German vehicle glass generic: translate ‘Scheibe’ as ‘cristal’ or ‘ventanilla’ unless the source explicitly says ‘Windschutzscheibe’, which means ‘parabrisas’. Use ‘presunción de inocencia’ for ‘Unschuldsvermutung’, and translate neutral ‘befragte’ as ‘preguntó’ or ‘entrevistó’, not ‘interrogó’, unless the source explicitly describes an interrogation. In medical reporting, ‘stationär in ein Krankenhaus gebracht’ means ‘ingresada en un hospital’ or ‘hospitalizada’, never ‘trasladada de forma permanente’.",
	"fr": "Use standard French police and medical terminology: German ‘Verkehrspolizei’ means traffic or road police (‘police de la circulation’ or ‘police routière’), never public-transport police (‘police des transports’). In medical reporting, ‘stationär in ein Krankenhaus gebracht’ means admitted or hospitalized for inpatient care (‘admis(e) à l’hôpital’ or ‘hospitalisé(e)’), not a permanent transfer. In legal reporting, translate ‘Unschuldsvermutung’ as ‘présomption d’innocence’; it applies until a final conviction (‘condamnation définitive’), never merely until a final decision or judgment.",
	"it": "Keep generic German vehicle glass generic: translate ‘Scheibe’ as ‘vetro’ or ‘finestrino’ unless the source explicitly says ‘Windschutzscheibe’, which means ‘parabrezza’. In legal reporting, translate ‘Unschuldsvermutung’ as ‘presunzione d’innocenza’; it applies until a final conviction (‘condanna definitiva’), never merely until any final judgment (‘sentenza definitiva’).",
}

// HyMT2NativeAdapter follows Tencent's user-only translation contract and
// recommended 1.8B/7B sampling parameters. Production routing selects it only
// through a durable setting for an officially supported target language.
type HyMT2NativeAdapter struct {
	client            *OllamaClient
	sourceName        string
	targetName        string
	targetGuidance    string
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
		client:         client,
		sourceName:     sourceName,
		targetName:     targetName,
		targetGuidance: hyMT2LanguageGuidance[target.Code],
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
	content, model, err := a.client.chatWithOptions(ctx, true, "", hyMT2NativePrompt(a.sourceName, a.targetName, a.targetGuidance, text), nil, a.generationOptions)
	if err != nil {
		return "", model, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", model, errorOf(ErrorOutput, "Hy-MT2 native output is empty")
	}
	return content, model, nil
}

func hyMT2NativePrompt(sourceName, targetName, guidance, text string) string {
	guidance = strings.TrimSpace(guidance)
	if guidance != "" {
		guidance = "8. Target-language guidance: " + guidance + "\n"
	}
	return fmt.Sprintf(
		"### Task\n"+
			"Translate the following text from %s into %s.\n\n"+
			"### Strict Rules\n"+
			"1. Output only the translated plain text. Do not add explanations, notes, headings, commentary, Markdown, or URLs.\n"+
			"2. Treat every token matching `__MB_[A-Z_]+_[0-9]{4}__` as an immutable placeholder. Copy every occurrence exactly, character-for-character, and preserve the same number of occurrences.\n"+
			"3. Never translate, transliterate, inflect, decline, conjugate, modify, split, remove, duplicate, or replace a placeholder.\n"+
			"4. When a placeholder contains an entity type such as `STREET`, `DISTRICT`, `TRAIN_STATION`, `COMMUTER_TRAIN`, or `SUBWAY_SYSTEM`, use that type only to understand the sentence and produce natural grammar around the placeholder. Do not alter the placeholder itself.\n"+
			"5. If the target language would normally require changing the hidden entity, restructure the surrounding sentence so the placeholder remains unchanged.\n"+
			"6. Preserve the original meaning, tone, factual details, numbers, dates, times, negation, uncertainty, attribution, and relationships. Do not add or infer information.\n"+
			"7. Produce natural, fluent %s rather than a word-for-word translation.\n"+
			"%s\n"+
			"### Source Data\n%s",
		sourceName, targetName, targetName, guidance, strings.TrimSpace(text),
	)
}
