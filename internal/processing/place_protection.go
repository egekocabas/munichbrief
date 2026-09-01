package processing

import (
	"context"
	"errors"
	"strings"

	"github.com/egekocabas/munichbrief/internal/gazetteer"
)

type TranslationProtector interface {
	Ready() bool
	Protect(title, summary string) (gazetteer.Protected, error)
}

type protectedGeneratorProvider struct {
	inner     StepGeneratorProvider
	protector TranslationProtector
}

func NewProtectedGeneratorProvider(inner StepGeneratorProvider, protector TranslationProtector) (StepGeneratorProvider, error) {
	if inner == nil || protector == nil {
		return nil, errors.New("protected generator provider requires inner provider and gazetteer")
	}
	return &protectedGeneratorProvider{inner: inner, protector: protector}, nil
}

func (p *protectedGeneratorProvider) StepGenerator(model string) (StepGenerator, error) {
	inner, err := p.inner.StepGenerator(model)
	if err != nil {
		return nil, err
	}
	return &protectedGenerator{inner: inner, protector: p.protector}, nil
}

type protectedGenerator struct {
	inner     StepGenerator
	protector TranslationProtector
}

func (g *protectedGenerator) ModelIdentity() string { return g.inner.ModelIdentity() }

func (g *protectedGenerator) GenerateStep(ctx context.Context, step StepDefinition, input StepInput) (StepOutput, string, error) {
	if !strings.HasPrefix(step.Key, TranslationModelStep+"/") {
		return g.inner.GenerateStep(ctx, step, input)
	}
	protected, err := g.protector.Protect(input.Value("title_de"), input.Value("summary_de"))
	if err != nil {
		return StepOutput{}, "", errorOf(ErrorConfiguration, "protect translation place names: %v", err)
	}
	protectedInput := input.clone()
	protectedInput.Values["title_de"] = protected.Title
	protectedInput.Values["summary_de"] = protected.Summary
	output, model, err := g.inner.GenerateStep(ctx, step, protectedInput)
	if err != nil {
		return StepOutput{}, model, err
	}
	title, summary, err := gazetteer.Restore(protected, output.Values["title"], output.Values["summary"])
	if err != nil {
		return StepOutput{}, model, errorOf(ErrorOutput, "restore translation place names: %v", err)
	}
	output.Values["title"] = title
	output.Values["summary"] = summary
	if err := validateTranslation(input, &output); err != nil {
		return StepOutput{}, model, err
	}
	return output, model, nil
}
