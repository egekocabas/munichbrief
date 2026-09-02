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

// TranslateGemmaNativeAdapter expands Google's official text-translation chat
// template for GGUF runtimes whose generic string-message API cannot carry the
// source_lang_code and target_lang_code properties used by the original model.
// It intentionally remains separate from production routing while evaluated.
type TranslateGemmaNativeAdapter struct {
	client         *OllamaClient
	source, target langregistry.Definition
}

func NewTranslateGemmaNativeAdapter(baseURL, model, targetCode string, timeout time.Duration, contextSize int, baseClient *http.Client) (*TranslateGemmaNativeAdapter, error) {
	definitions := langregistry.Registered()
	if err := langregistry.Validate(definitions); err != nil {
		return nil, err
	}
	source := langregistry.Canonical(definitions)
	target, found := langregistry.ByCode(definitions, targetCode)
	if !found || target.Canonical {
		return nil, errors.New("TranslateGemma native adapter requires a translated target language")
	}
	client, err := NewOllamaClient(baseURL, model, timeout, contextSize, baseClient)
	if err != nil {
		return nil, err
	}
	return &TranslateGemmaNativeAdapter{client: client, source: source, target: target}, nil
}

func (a *TranslateGemmaNativeAdapter) Translate(ctx context.Context, text string) (string, string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", errorOf(ErrorOutput, "TranslateGemma native input is empty")
	}
	content, model, err := a.client.chat(ctx, true, "", translateGemmaNativePrompt(a.source, a.target, text), nil)
	if err != nil {
		return "", model, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", model, errorOf(ErrorOutput, "TranslateGemma native output is empty")
	}
	return content, model, nil
}

func translateGemmaNativePrompt(source, target langregistry.Definition, text string) string {
	return fmt.Sprintf(`You are a professional %s (%s) to %s (%s) translator. Your goal is to accurately convey the meaning and nuances of the original %s text while adhering to %s grammar, vocabulary, and cultural sensitivities.
Produce only the %s translation, without any additional explanations or commentary. Please translate the following %s text into %s:


%s`, source.TranslationName, source.Code, target.TranslationName, target.Code, source.TranslationName, target.TranslationName, target.TranslationName, source.TranslationName, target.TranslationName, strings.TrimSpace(text))
}
