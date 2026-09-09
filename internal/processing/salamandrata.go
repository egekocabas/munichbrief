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
	salamandraTAContextLimit        = 8192
	salamandraTAMaximumOutputTokens = 1024
)

// salamandraTALanguageNames contains the model's officially supported names
// that MunichBrief currently exposes, plus canonical German.
var salamandraTALanguageNames = map[string]string{
	"de": "German", "en": "English", "tr": "Turkish", "hr": "Croatian",
	"it": "Italian", "uk": "Ukrainian", "zh": "Chinese (simplified)",
	"hi": "Hindi", "es": "Spanish", "fr": "French", "ro": "Romanian",
	"pl": "Polish", "el": "Greek", "ru": "Russian",
}

var salamandraTALanguageGuidance = map[string]string{
	"ru": "Translate German ‘soll … haben’ as an explicitly alleged action, using ‘предположительно’ or equivalent. When present, use ‘полицейская операция’ for ‘Polizeieinsatz’ and express transitive ‘verzögert’ as causing delays, such as ‘задерживает движение’; never use ‘патруль’ or ‘задержал’. Use plural grammar around plural transit placeholders.",
	"el": "Use ‘αστυνομική επιχείρηση’ for ‘Polizeieinsatz’ and plural grammar around plural transit placeholders. Translate ‘rechtskräftige Verurteilung’ as ‘αμετάκλητη καταδίκη’, never merely ‘τελική απόφαση’. Translate neutral police ‘befragen’ as ‘ρωτώ’ or ‘λαμβάνω κατάθεση’, never ‘ανακρίνω’ unless the German explicitly says interrogation.",
	"hr": "Every reported-action clause containing German ‘soll’ must remain explicitly alleged with Croatian ‘navodno’. Translate a police operation causing delays without saying it stopped a train, and use plural Croatian grammar around plural transit placeholders.",
}

// SalamandraTANativeAdapter uses the model's documented user-only ChatML
// translation contract. Production routing remains an explicit per-language
// choice so every local quantization can be qualified independently.
type SalamandraTANativeAdapter struct {
	client            *OllamaClient
	sourceName        string
	targetName        string
	targetGuidance    string
	generationOptions chatOptions
}

func NewSalamandraTANativeAdapter(baseURL, model, targetCode string, timeout time.Duration, contextSize int, baseClient *http.Client) (*SalamandraTANativeAdapter, error) {
	definitions := langregistry.Registered()
	if err := langregistry.Validate(definitions); err != nil {
		return nil, err
	}
	source := langregistry.Canonical(definitions)
	target, found := langregistry.ByCode(definitions, targetCode)
	if !found || target.Canonical {
		return nil, errors.New("SalamandraTA native adapter requires a translated target language")
	}
	sourceName, sourceSupported := salamandraTALanguageNames[source.Code]
	targetName, targetSupported := salamandraTALanguageNames[target.Code]
	if !sourceSupported {
		return nil, fmt.Errorf("SalamandraTA does not officially support source language %q", source.Code)
	}
	if !targetSupported {
		return nil, fmt.Errorf("SalamandraTA does not officially support target language %q", target.Code)
	}
	if contextSize <= 0 || contextSize > salamandraTAContextLimit {
		contextSize = salamandraTAContextLimit
	}
	client, err := NewOllamaClient(baseURL, model, timeout, contextSize, baseClient)
	if err != nil {
		return nil, err
	}
	return &SalamandraTANativeAdapter{
		client: client, sourceName: sourceName, targetName: targetName,
		targetGuidance: salamandraTALanguageGuidance[target.Code],
		generationOptions: chatOptions{
			Temperature: 0,
			NumPredict:  salamandraTAMaximumOutputTokens,
			NumCtx:      contextSize,
		},
	}, nil
}

func (a *SalamandraTANativeAdapter) Translate(ctx context.Context, text string) (string, string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", errorOf(ErrorOutput, "SalamandraTA native input is empty")
	}
	content, model, err := a.client.chatWithOptions(ctx, true, "", salamandraTANativePrompt(a.sourceName, a.targetName, a.targetGuidance, text), nil, a.generationOptions)
	if err != nil {
		return "", model, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", model, errorOf(ErrorOutput, "SalamandraTA native output is empty")
	}
	return content, model, nil
}

func salamandraTANativePrompt(sourceName, targetName, guidance, text string) string {
	guidance = strings.TrimSpace(guidance)
	if guidance != "" {
		guidance = "\n- " + guidance
	}
	return fmt.Sprintf(
		"Translate the following text from %s into %s.\nRequirements:\n- Output only the translation.\n- Preserve every placeholder matching `__MB_[A-Z_]+_[0-9]{4}__` exactly, character-for-character; translate only the surrounding natural-language text.%s\n%s: %s\n%s:",
		sourceName, targetName, guidance, sourceName, strings.TrimSpace(text), targetName,
	)
}
