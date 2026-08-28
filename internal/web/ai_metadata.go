package web

import (
	"net/http"
	"strings"

	"github.com/egekocabas/munichbrief/internal/store"
)

const (
	iptcTrainedAlgorithmicMedia              = "http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia"
	iptcCompositeWithTrainedAlgorithmicMedia = "http://cv.iptc.org/newscodes/digitalsourcetype/compositeWithTrainedAlgorithmicMedia"
	schemaTrainedAlgorithmicMedia            = "https://schema.org/TrainedAlgorithmicMediaDigitalSource"
)

type machineReadableAIMetadata struct {
	AIGenerated        bool   `json:"ai_generated"`
	AIGeneratedState   string `json:"ai_generated_state"`
	AIModel            string `json:"ai_model,omitempty"`
	AIMetadataModel    string `json:"ai_metadata_model,omitempty"`
	AITranslationModel string `json:"ai_translation_model,omitempty"`
	DigitalSourceType  string `json:"digital_source_type,omitempty"`
}

func (page *basePage) setAIMetadata(state string, record *store.IncidentRecord) {
	page.AIGeneratedState = state
	page.AIModel = ""
	page.AIMetadataModel = ""
	page.AITranslationModel = ""
	metadata := machineReadableAIMetadata{AIGenerated: state != "false", AIGeneratedState: state}
	if state != "false" {
		metadata.DigitalSourceType = iptcTrainedAlgorithmicMedia
	}
	if record != nil && record.HasAI {
		page.AIModel = record.AIModel
		page.AIMetadataModel = record.AIMetadataModel
		if page.Lang != canonicalReaderLanguage().Code {
			page.AITranslationModel = record.AITranslationModel
		}
		metadata.AIModel = page.AIModel
		metadata.AIMetadataModel = page.AIMetadataModel
		metadata.AITranslationModel = page.AITranslationModel
	}
	page.AIContentMetadata = structuredJSON(metadata)
}

func aiGeneratedState(records []store.IncidentRecord) string {
	generated := 0
	for _, record := range records {
		if record.HasAI {
			generated++
		}
	}
	switch {
	case generated == 0:
		return "false"
	case generated == len(records):
		return "true"
	default:
		return "mixed"
	}
}

func setAIResponseHeaders(header http.Header, page basePage) {
	header.Set("X-AI-Generated", page.AIGeneratedState)
	if page.AIModel != "" {
		header.Set("X-AI-Model", safeMetadataHeader(page.AIModel))
	}
	if page.AITranslationModel != "" {
		header.Set("X-AI-Translation-Model", safeMetadataHeader(page.AITranslationModel))
	}
	if page.AIGeneratedState != "false" {
		header.Set("X-IPTC-Digital-Source-Type", iptcTrainedAlgorithmicMedia)
	}
}

func safeMetadataHeader(value string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(value)
}
