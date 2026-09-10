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

func TestHyMT2NativeAdapterUsesOfficialContract(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Format) != 0 || len(payload.Messages) != 1 || payload.Messages[0].Role != "user" || payload.Think {
			t.Fatalf("native request unexpectedly used a schema, system role, or thinking: %#v", payload)
		}
		wantPrompt := "### Task\n" +
			"Translate the following text from German into Chinese.\n\n" +
			"### Strict Rules\n" +
			"1. Output only the translated plain text. Do not add explanations, notes, headings, commentary, Markdown, or URLs.\n" +
			"2. Treat every token matching `__MB_[A-Z_]+_[0-9]{4}__` as an immutable placeholder. Copy every occurrence exactly, character-for-character, and preserve the same number of occurrences.\n" +
			"3. Never translate, transliterate, inflect, decline, conjugate, modify, split, remove, duplicate, or replace a placeholder.\n" +
			"4. When a placeholder contains an entity type such as `STREET`, `DISTRICT`, `TRAIN_STATION`, `COMMUTER_TRAIN`, or `SUBWAY_SYSTEM`, use that type only to understand the sentence and produce natural grammar around the placeholder. Do not alter the placeholder itself.\n" +
			"5. If the target language would normally require changing the hidden entity, restructure the surrounding sentence so the placeholder remains unchanged.\n" +
			"6. Preserve the original meaning, tone, factual details, numbers, dates, times, negation, uncertainty, attribution, and relationships. Do not add or infer information.\n" +
			"7. Produce natural, fluent Chinese rather than a word-for-word translation.\n" +
			"8. Target-language guidance: " + hyMT2LanguageGuidance["zh"] + "\n\n" +
			"### Source Data\nEinsatz am <KEEP>__MB_STREET_0001__</KEEP>"
		if payload.Messages[0].Content != wantPrompt {
			t.Fatalf("prompt = %q, want %q", payload.Messages[0].Content, wantPrompt)
		}
		if strings.Contains(payload.Messages[0].Content, "Simplified Chinese") || strings.Contains(payload.Messages[0].Content, "zh-CN") {
			t.Fatalf("prompt did not use Tencent's exact language name: %q", payload.Messages[0].Content)
		}
		if strings.Count(payload.Messages[0].Content, "__MB_[A-Z_]+_[0-9]{4}__") != 1 || !strings.Contains(payload.Messages[0].Content, "### Source Data\nEinsatz") {
			t.Fatalf("prompt omitted the exact placeholder pattern or source separator: %q", payload.Messages[0].Content)
		}
		options := payload.Options
		if options.Temperature != 0.7 || options.TopP != 0.6 || options.TopK != 20 || options.RepeatPenalty != 1.05 || options.NumPredict != 4096 || options.NumCtx != 8192 {
			t.Fatalf("generation options = %#v", options)
		}
		var response bytes.Buffer
		_ = json.NewEncoder(&response).Encode(chatResponse{Model: "hy-mt2:test", Done: true, Message: chatMessage{Role: "assistant", Content: "在 <KEEP>__MB_STREET_0001__</KEEP> 的行动"}})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response.Bytes()))}, nil
	})
	adapter, err := NewHyMT2NativeAdapter("http://ollama.test:11434", "hy-mt2:test", "zh", time.Second, 8192, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	translated, model, err := adapter.Translate(context.Background(), "Einsatz am <KEEP>__MB_STREET_0001__</KEEP>")
	if err != nil || model != "hy-mt2:test" || translated != "在 <KEEP>__MB_STREET_0001__</KEEP> 的行动" {
		t.Fatalf("native translation=%q model=%q err=%v", translated, model, err)
	}
}

func TestHyMT2NativeAdapterMapsEverySupportedReaderLanguage(t *testing.T) {
	supported := map[string]string{
		"en": "English", "tr": "Turkish", "it": "Italian", "uk": "Ukrainian",
		"zh": "Chinese", "hi": "Hindi", "es": "Spanish", "fr": "French",
		"pl": "Polish", "ru": "Russian",
	}
	for code, name := range supported {
		t.Run(code, func(t *testing.T) {
			adapter, err := NewHyMT2NativeAdapter("http://ollama.test:11434", "hy-mt2:test", code, time.Second, 8192, nil)
			if err != nil {
				t.Fatal(err)
			}
			if adapter.sourceName != "German" || adapter.targetName != name {
				t.Fatalf("language mapping = %q -> %q", adapter.sourceName, adapter.targetName)
			}
		})
	}
}

func TestHyMT2NativeAdapterAddsOnlyConfiguredTargetGuidance(t *testing.T) {
	chinese := hyMT2NativePrompt("German", "Chinese", hyMT2LanguageGuidance["zh"], "Quelle")
	for _, expected := range []string{"‘Verurteilung’ means conviction (‘定罪’)", "‘最终定罪’ or ‘已生效的定罪判决’", "never merely a formal/final judgment", "‘正式判决’ or ‘最终判决’", "judgment may be an acquittal", "‘无罪推定’", "generic vehicle glass generic", "‘Scheibe’ means ‘车窗’ or ‘车窗玻璃’", "‘Windschutzscheibe’", "‘挡风玻璃’", "Preserve evidentiary uncertainty", "‘Hinweise’ and ‘Auffälligkeiten’", "‘与酒精或毒品有关的异常表现’", "never as ‘饮酒或吸毒问题’"} {
		if !strings.Contains(chinese, expected) {
			t.Errorf("Chinese guidance omitted %q: %s", expected, chinese)
		}
	}
	hindi := hyMT2NativePrompt("German", "Hindi", hyMT2LanguageGuidance["hi"], "Quelle")
	for _, expected := range []string{"only when its corresponding German wording occurs", "never add glossary rules or examples", "‘कथित तौर पर’ or ‘आरोप है कि’", "‘beschädigt’", "‘क्षतिग्रस्त’", "‘rechtskräftige Verurteilung’", "‘अंतिम दोषसिद्धि’", "until final conviction", "‘संबंधित व्यक्ति’", "never ‘पीड़ित’", "‘अस्पष्ट खतरे की स्थिति’", "‘अनिश्चित खतरे की स्थिति’", "‘यातायात पुलिस’", "never transport police", "‘Mehrfamilienhaus’", "‘बहु-परिवारीय आवासीय इमारत’", "‘Linienbus’", "‘नियमित सार्वजनिक बस’", "never an ordinary bus"} {
		if !strings.Contains(hindi, expected) {
			t.Errorf("Hindi guidance omitted %q: %s", expected, hindi)
		}
	}
	russian := hyMT2NativePrompt("German", "Russian", hyMT2LanguageGuidance["ru"], "Quelle")
	for _, expected := range []string{"Every reported allegation", "‘предположительно’", "never an established fact", "‘Polizeieinsatz’", "never ‘патрулирование’", "Keep headlines concise", "‘Полицейская операция … задерживает [placeholder]’", "never add ‘движение поездов типа’", "plural Russian grammar", "‘осуждение’", "no conviction at all", "‘осуждения нет’", "‘до вступления в законную силу обвинительного приговора’", "never say merely ‘до вынесения окончательного приговора’", "final verdict may be an acquittal", "‘презумпция невиновности’", "‘опросить’", "never ‘допросить’", "‘одного свидетеля’", "never one of several", "‘an der … Straße’", "‘на улице’", "‘у улицы’", "‘Medizinisch untersucht’", "‘медицински осмотрели’", "never given treatment", "‘соответствующее лицо’", "never ‘пострадавший’", "‘госпитализирована’", "‘помещена в стационар’", "‘рейсовый автобус’"} {
		if !strings.Contains(russian, expected) {
			t.Errorf("Russian guidance omitted %q: %s", expected, russian)
		}
	}
	english := hyMT2NativePrompt("German", "English", hyMT2LanguageGuidance["en"], "Quelle")
	for _, expected := range []string{"neutral German person labels", "admitted to hospital as an inpatient", "police stop signals or orders"} {
		if !strings.Contains(english, expected) {
			t.Errorf("English guidance omitted %q: %s", expected, english)
		}
	}
	if !strings.HasSuffix(english, "### Source Data\nQuelle") {
		t.Fatalf("English source boundary changed: %q", english)
	}
	spanish := hyMT2NativePrompt("German", "Spanish", hyMT2LanguageGuidance["es"], "Quelle")
	for _, expected := range []string{"vehicle glass generic", "presunción de inocencia", "‘condena firme’ or ‘condena definitiva’", "never merely ‘sentencia firme’", "judgment may be an acquittal", "not ‘interrogó’", "never ‘trasladada de forma permanente’", "‘autobús de línea’ or ‘autobús regular’", "never replace it with only a route or line"} {
		if !strings.Contains(spanish, expected) {
			t.Errorf("Spanish guidance omitted %q: %s", expected, spanish)
		}
	}
	if strings.Contains(spanish, "Betroffene") {
		t.Fatalf("English guidance leaked into Spanish prompt: %q", spanish)
	}
	french := hyMT2NativePrompt("German", "French", hyMT2LanguageGuidance["fr"], "Quelle")
	for _, expected := range []string{"‘Verkehrspolizei’", "never public-transport police", "‘hospitalisé(e)’", "‘présomption d’innocence’", "‘condamnation définitive’", "never merely until a final decision"} {
		if !strings.Contains(french, expected) {
			t.Errorf("French guidance omitted %q: %s", expected, french)
		}
	}
	if strings.Contains(french, "vehicle glass generic") || strings.Contains(french, "admitted to hospital as an inpatient") {
		t.Fatalf("other target guidance leaked into French prompt: %q", french)
	}
	italian := hyMT2NativePrompt("German", "Italian", hyMT2LanguageGuidance["it"], "Quelle")
	for _, expected := range []string{"‘Scheibe’", "‘vetro’ or ‘finestrino’", "‘presunzione d’innocenza’", "‘condanna definitiva’ or ‘condanna passata in giudicato’", "never use ‘sentenza definitiva’", "a final judgment may be an acquittal", "ongoing investigations remain ongoing", "a check (‘Überprüfung’ or ‘Kontrolle’) is not a search (‘perquisizione’)", "not confirmed consumption", "grammatically complete Italian headlines"} {
		if !strings.Contains(italian, expected) {
			t.Errorf("Italian guidance omitted %q: %s", expected, italian)
		}
	}
	if strings.Contains(italian, "public-transport police") || strings.Contains(italian, "presunción de inocencia") {
		t.Fatalf("other target guidance leaked into Italian prompt: %q", italian)
	}
	polish := hyMT2NativePrompt("German", "Polish", hyMT2LanguageGuidance["pl"], "Quelle")
	for _, expected := range []string{"‘Verurteilung’ means ‘skazanie’", "‘rechtskräftige Verurteilung’ means ‘prawomocne skazanie’", "never merely ‘wyrok’ or ‘orzeczenie’", "may be an acquittal", "‘Betroffener’ or ‘Betroffene’", "never ‘poszkodowany’ or ‘ofiara’", "‘Hinweise’ and ‘Auffälligkeiten’", "never ‘dowody’", "never ‘intervencja’"} {
		if !strings.Contains(polish, expected) {
			t.Errorf("Polish guidance omitted %q: %s", expected, polish)
		}
	}
	if strings.Contains(polish, "vehicle glass generic") || strings.Contains(polish, "presunzione d’innocenza") {
		t.Fatalf("other target guidance leaked into Polish prompt: %q", polish)
	}
	turkish := hyMT2NativePrompt("German", "Turkish", hyMT2LanguageGuidance["tr"], "Quelle")
	for _, expected := range []string{"‘beschädigen’ as ‘zarar vermek’ or ‘hasar vermek’", "never ‘kırmak’", "‘Verurteilung’ means ‘mahkûmiyet’", "‘rechtskräftige Verurteilung’ means ‘kesinleşmiş mahkûmiyet’", "never merely ‘kesinleşmiş hüküm’", "‘genau’ means ‘tam olarak’ without ‘sularında’ or ‘civarında’", "‘befragen’", "never ‘sorgulamak’", "‘Betroffener’ or ‘Betroffene’", "never ‘mağdur’", "‘hastaneye yatırıldı’", "not merely ‘hastaneye kaldırıldı’", "‘polisin dur ihtarları’ or ‘dur emri’", "never road ‘dur işaretleri’", "‘Hinweise’ and ‘Auffälligkeiten’", "never ‘kanıt’, ‘delil’"} {
		if !strings.Contains(turkish, expected) {
			t.Errorf("Turkish guidance omitted %q: %s", expected, turkish)
		}
	}
	if strings.Contains(turkish, "vehicle glass generic") || strings.Contains(turkish, "‘Verurteilung’ means ‘skazanie’") {
		t.Fatalf("other target guidance leaked into Turkish prompt: %q", turkish)
	}
	ukrainian := hyMT2NativePrompt("German", "Ukrainian", hyMT2LanguageGuidance["uk"], "Quelle")
	for _, expected := range []string{"every German reported-allegation construction", "‘soll … haben’", "explicitly mark the alleged action", "‘нібито’ or ‘як стверджується’", "Never express this construction with ‘мав’, ‘мала’, or ‘мали’ plus an infinitive", "supposed or required to act", "‘Verurteilung’ means ‘засудження’", "‘rechtskräftige Verurteilung’", "never merely ‘вирок’ or ‘остаточне рішення’", "‘презумпція невинуватості’", "‘befragen’ as ‘опитувати’", "never ‘допитувати’", "‘an der … Straße’", "not merely near it (‘біля вулиці’)", "‘Betroffener’ or ‘Betroffene’", "‘відповідна особа’ or ‘особа, якої це стосується’", "never ‘постраждалий’ or ‘потерпілий’", "‘Hinweise’ and ‘Auffälligkeiten’", "never ‘докази’, proof", "confirmed intoxication"} {
		if !strings.Contains(ukrainian, expected) {
			t.Errorf("Ukrainian guidance omitted %q: %s", expected, ukrainian)
		}
	}
	if strings.Contains(ukrainian, "vehicle glass generic") || strings.Contains(ukrainian, "‘Verurteilung’ means ‘skazanie’") {
		t.Fatalf("other target guidance leaked into Ukrainian prompt: %q", ukrainian)
	}
	fallback := hyMT2NativePrompt("German", "Portuguese", hyMT2LanguageGuidance["pt"], "Quelle")
	if strings.Contains(fallback, "Target-language guidance") || strings.Contains(fallback, "Scheibe") {
		t.Fatalf("configured guidance leaked into fallback prompt: %q", fallback)
	}
	if !strings.Contains(fallback, "7. Produce natural, fluent Portuguese rather than a word-for-word translation.\n\n### Source Data\nQuelle") {
		t.Fatalf("fallback prompt structure changed: %q", fallback)
	}
}

func TestHyMT2NativeAdapterRejectsUnsupportedReaderLanguages(t *testing.T) {
	for _, code := range []string{"hr", "bs", "el", "ro"} {
		t.Run(code, func(t *testing.T) {
			_, err := NewHyMT2NativeAdapter("http://ollama.test:11434", "hy-mt2:test", code, time.Second, 8192, nil)
			if err == nil || !strings.Contains(err.Error(), "does not officially support target language") {
				t.Fatalf("unsupported target error = %v", err)
			}
		})
	}
}

func TestHyMT2NativeAdapterRejectsCanonicalAndEmptyInput(t *testing.T) {
	if _, err := NewHyMT2NativeAdapter("http://ollama.test:11434", "hy-mt2:test", "de", time.Second, 8192, nil); err == nil {
		t.Fatal("canonical German target was accepted")
	}
	adapter, err := NewHyMT2NativeAdapter("http://ollama.test:11434", "hy-mt2:test", "en", time.Second, 8192, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := adapter.Translate(context.Background(), " \n "); err == nil || KindOf(err) != ErrorOutput {
		t.Fatalf("empty input error = %v", err)
	}
}
