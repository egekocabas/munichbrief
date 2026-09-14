package web

import "testing"

func TestRecordedModelDisplay(t *testing.T) {
	for _, tc := range []struct{ raw, name, variant string }{
		{"qwen3.5:4b", "Qwen3.5 · 4B", ""},
		{"qwen3.5:4b-q8_0", "Qwen3.5 · 4B", "Q8_0"},
		{"translategemma:4b-it-q8_0", "TranslateGemma · 4B-IT", "Q8_0"},
		{"hf.co/unsloth/gemma-4-E4B-it-GGUF:UD-Q4_K_XL", "gemma-4-E4B-it", "UD-Q4_K_XL"},
		{"hf.co/owner/model-Q4_K_M-GGUF:Q8_0", "model-Q4_K_M", "Q8_0"},
		{"hf.co/owner/TowerInstruct-7B-v0.2-GGUF:Q6_K", "TowerInstruct-7B-v0.2", "Q6_K"},
		{"hf.co/owner/model-GGUF:model-q5_k_m-imat.gguf", "model", "Q5_K_M"},
		{"custom-model:latest", "custom-model", ""},
		{"", "—", ""},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			s := processingStepView{Model: tc.raw}
			if s.ModelDisplay() != tc.name || s.ModelVariant() != tc.variant {
				t.Fatalf("got %q/%q", s.ModelDisplay(), s.ModelVariant())
			}
			if s.Model != tc.raw {
				t.Fatal("recorded identity changed")
			}
		})
	}
	for _, tc := range []struct{ raw, want string }{{"incident-metadata-v1", "v1"}, {"incident-presentation-de-v2", "v2"}, {"incident-public-assistance-verification-v2", "v2"}, {"custom-2026-09", "custom-2026-09"}, {"v3", "v3"}} {
		s := processingStepView{PromptVersion: tc.raw}
		if s.PromptDisplay() != tc.want || s.PromptVersion != tc.raw {
			t.Fatalf("prompt %q", tc.raw)
		}
	}
}
