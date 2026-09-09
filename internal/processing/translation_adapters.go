package processing

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	TranslationAdapterStructured     = "structured"
	TranslationAdapterTranslateGemma = "translategemma"
	TranslationAdapterHyMT2          = "hy-mt2"
	TranslationAdapterSeedX          = "seed-x"
)

type TranslationAdapterOption struct {
	Key         string
	DisplayName string
}

func TranslationAdapterOptions(languageCode string) []TranslationAdapterOption {
	options := []TranslationAdapterOption{
		{Key: TranslationAdapterStructured, DisplayName: "Structured chat (general LLM)"},
		{Key: TranslationAdapterTranslateGemma, DisplayName: "TranslateGemma native"},
	}
	if _, supported := hyMT2LanguageNames[languageCode]; supported {
		options = append(options, TranslationAdapterOption{Key: TranslationAdapterHyMT2, DisplayName: "HY-MT2 native"})
	}
	if _, supported := seedXLanguages[languageCode]; supported && languageCode != "de" {
		options = append(options, TranslationAdapterOption{Key: TranslationAdapterSeedX, DisplayName: "Seed-X native"})
	}
	return options
}

func TranslationAdapterSupports(adapter, languageCode string) bool {
	for _, option := range TranslationAdapterOptions(languageCode) {
		if option.Key == strings.TrimSpace(adapter) {
			return true
		}
	}
	return false
}

func normalizedTranslationAdapter(adapter string) string {
	if adapter = strings.TrimSpace(adapter); adapter != "" {
		return adapter
	}
	return TranslationAdapterStructured
}

type adapterStepGeneratorProvider interface {
	StepGeneratorFor(model, adapter string) (StepGenerator, error)
}

func stepGeneratorFor(provider StepGeneratorProvider, model, adapter string) (StepGenerator, error) {
	adapter = strings.TrimSpace(adapter)
	if adapter == "" || adapter == TranslationAdapterStructured {
		return provider.StepGenerator(model)
	}
	adapterProvider, ok := provider.(adapterStepGeneratorProvider)
	if !ok {
		return nil, fmt.Errorf("generator provider does not support adapter %q", adapter)
	}
	return adapterProvider.StepGeneratorFor(model, adapter)
}

type nativeTextTranslator interface {
	Translate(context.Context, string) (string, string, error)
}

type nativeTranslationStepGenerator struct {
	provider *OllamaGeneratorProvider
	model    string
	adapter  string
}

func (g *nativeTranslationStepGenerator) ModelIdentity() string { return g.model }

func (g *nativeTranslationStepGenerator) GenerateStep(ctx context.Context, step StepDefinition, input StepInput) (StepOutput, string, error) {
	language, translated := strings.CutPrefix(step.Key, TranslationModelStep+"/")
	if !translated || !TranslationAdapterSupports(g.adapter, language) {
		return StepOutput{}, "", errorOf(ErrorConfiguration, "adapter %q does not support step %q", g.adapter, step.Key)
	}
	var translator nativeTextTranslator
	var err error
	switch g.adapter {
	case TranslationAdapterTranslateGemma:
		translator, err = NewTranslateGemmaNativeAdapter(g.provider.baseURL, g.model, language, g.provider.timeout, g.provider.contextSize, g.provider.baseClient)
	case TranslationAdapterHyMT2:
		translator, err = NewHyMT2NativeAdapter(g.provider.baseURL, g.model, language, g.provider.timeout, g.provider.contextSize, g.provider.baseClient)
	case TranslationAdapterSeedX:
		translator, err = NewSeedXNativeAdapter(g.provider.baseURL, g.model, language, g.provider.timeout, g.provider.contextSize, g.provider.baseClient)
	default:
		err = errors.New("unknown native translation adapter")
	}
	if err != nil {
		return StepOutput{}, "", errorOf(ErrorConfiguration, "%v", err)
	}
	title, model, err := translator.Translate(ctx, input.Value("title_de"))
	if err != nil {
		return StepOutput{}, model, err
	}
	summary, summaryModel, err := translator.Translate(ctx, input.Value("summary_de"))
	if err != nil {
		return StepOutput{}, summaryModel, err
	}
	if summaryModel != model {
		return StepOutput{}, summaryModel, errorOf(ErrorOutput, "translation model identity changed between title and summary")
	}
	output := StepOutput{Values: map[string]string{"title": title, "summary": summary}}
	if err := ValidateStepOutput(step, input, &output); err != nil {
		return StepOutput{}, model, err
	}
	return output, model, nil
}
