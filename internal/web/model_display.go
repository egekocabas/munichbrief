package web

import (
	"regexp"
	"strings"
)

var modelQuantization = regexp.MustCompile(`(?i)(?:^|[-_:])((?:UD-)?(?:I?Q)[0-9]+(?:_[A-Z0-9]+)+)(?:[-.]|$)`)
var modelParameters = regexp.MustCompile(`(?i)^([0-9]+(?:\.[0-9]+)?)b(?:$|-)`)
var promptRevision = regexp.MustCompile(`(?:^|-)v([0-9]+)$`)

// Display fields are derived only from the recorded identity, never the current
// Ollama catalogue: a mutable tag is not proof of a historical quantization.
func (step processingStepView) ModelDisplay() string {
	identity := step.Model
	if identity == "" {
		return "—"
	}
	name, tag, found := strings.Cut(identity, ":")
	if strings.HasPrefix(name, "hf.co/") || strings.HasPrefix(name, "huggingface.co/") {
		name = name[strings.LastIndex(name, "/")+1:]
		name = strings.TrimSuffix(name, "-GGUF")
		name = strings.TrimSuffix(name, "-gguf")
	} else if found && tag != "latest" {
		if friendly, ok := map[string]string{"qwen3.5": "Qwen3.5", "translategemma": "TranslateGemma", "granite4.1": "Granite 4.1"}[name]; ok {
			name = friendly
		}
		// Keep parameter size and other tag qualifiers, removing only the explicit
		// quantization also shown in the variant badge.
		remainder := tag
		if q := step.ModelVariant(); q != "" {
			remainder = strings.TrimRight(strings.TrimSuffix(strings.ToUpper(tag), q), "-_")
		}
		if match := modelParameters.FindStringSubmatchIndex(remainder); len(match) > 0 {
			end := match[3]
			remainder = remainder[:end] + "B" + remainder[end+1:]
		}
		if remainder != "" {
			name += " · " + remainder
		}
	}
	return name
}
func (step processingStepView) ModelVariant() string {
	_, tag, found := strings.Cut(step.Model, ":")
	if !found {
		return ""
	}
	if m := modelQuantization.FindStringSubmatch(tag); len(m) > 1 {
		return strings.ToUpper(m[1])
	}
	return ""
}
func (step processingStepView) PromptDisplay() string {
	if m := promptRevision.FindStringSubmatch(step.PromptVersion); len(m) > 1 {
		return "v" + m[1]
	}
	if step.PromptVersion == "" {
		return "—"
	}
	return step.PromptVersion
}
