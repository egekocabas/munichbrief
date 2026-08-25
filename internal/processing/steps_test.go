package processing

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRegisteredPipelineStepsAreStableAndOrdered(t *testing.T) {
	steps := RegisteredSteps()
	if len(steps) != 2 || steps[0].Key != GermanAnalysisStep || steps[0].PromptVersion != GermanAnalysisPrompt || steps[1].Key != EnglishTranslationStep || steps[1].PromptVersion != EnglishTranslationPrompt {
		t.Fatalf("registered steps = %#v", steps)
	}
	if PipelineVersion != "incident-pipeline-v1" {
		t.Fatalf("pipeline version = %q", PipelineVersion)
	}
	for _, expected := range []string{"Du erstellst", "missing_wanted für Vermisstenmeldungen", "in Maxvorstadt", "other nur"} {
		if !strings.Contains(steps[0].SystemPrompt, expected) {
			t.Errorf("German analysis prompt does not contain %q", expected)
		}
	}
}

func TestGermanAnalysisValidatesCategoryAreaAndPrivacy(t *testing.T) {
	step, _ := StepByKey(GermanAnalysisStep)
	area, areaType := "Maxvorstadt", "neighbourhood"
	output := StepOutput{
		TitleDE: "Zusammenstoß in Maxvorstadt", SummaryDE: "Zwei Fahrzeuge stießen in Maxvorstadt zusammen.",
		Category: "traffic", AreaName: &area, AreaType: &areaType, PrivacyStatus: "safe", PrivacyFlags: []string{"age", "age"},
	}
	if err := ValidateStepOutput(step, StepInput{OriginalTitle: "Verkehrsunfall", IncidentBody: "In Maxvorstadt stießen zwei Fahrzeuge zusammen."}, &output); err != nil {
		t.Fatalf("valid German analysis rejected: %v", err)
	}
	if len(output.PrivacyFlags) != 1 || CategoryLabel(output.Category, "de") != "Verkehr" || CategoryLabel(output.Category, "en") != "Traffic" {
		t.Fatalf("normalized output = %#v", output)
	}

	inferredArea, districtType := "Schwabing", "district"
	output.AreaName, output.AreaType = &inferredArea, &districtType
	if err := ValidateStepOutput(step, StepInput{IncidentBody: "Der Vorfall ereignete sich an der Leopoldstraße 1, 80802 München."}, &output); KindOf(err) != ErrorOutput {
		t.Fatalf("inferred area error = %v, kind %q", err, KindOf(err))
	}

	output.AreaName, output.AreaType = nil, nil
	output.Category = "unknown"
	if err := ValidateStepOutput(step, StepInput{}, &output); KindOf(err) != ErrorOutput {
		t.Fatalf("invalid category error = %v, kind %q", err, KindOf(err))
	}

	output.Category = "other"
	output.PrivacyFlags = []string{"uncertain"}
	if err := ValidateStepOutput(step, StepInput{}, &output); KindOf(err) != ErrorPrivacy {
		t.Fatalf("privacy uncertainty error = %v, kind %q", err, KindOf(err))
	}
}

func TestEnglishTranslationReceivesOnlyAcceptedGermanPresentation(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		user := payload.Messages[1].Content
		for _, forbidden := range []string{"private original body", "original_title", "incident_body"} {
			if strings.Contains(user, forbidden) {
				t.Fatalf("translation request leaked %q: %s", forbidden, user)
			}
		}
		if !strings.Contains(user, "Sicherer Titel") || !strings.Contains(user, "Sichere Zusammenfassung") {
			t.Fatalf("translation request omitted accepted German text: %s", user)
		}
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(chatResponse{Model: "translategemma:4b", Done: true, Message: chatMessage{Role: "assistant", Content: `{"title_en":"Safe title","summary_en":"Safe summary."}`}})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
	})
	client, err := NewOllamaClient("http://ollama.test:11434", "translategemma:4b", time.Second, 8192, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	step, _ := StepByKey(EnglishTranslationStep)
	output, _, err := client.GenerateStep(context.Background(), step, StepInput{
		OriginalTitle: "private original title", IncidentBody: "private original body",
		TitleDE: "Sicherer Titel", SummaryDE: "Sichere Zusammenfassung.",
	})
	if err != nil || output.TitleEN != "Safe title" {
		t.Fatalf("translation output = %#v, err=%v", output, err)
	}
}
