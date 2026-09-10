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
	llamax3ContextLimit       = 8192
	euroLLMContextLimit       = 8192
	towerPlusContextLimit     = 2048
	towerInstructContextLimit = 2048
	gemma4ContextLimit        = 2048
	madlad400ContextLimit     = 2048
	evaluationMaxOutputTokens = 1024
	madlad400MaxOutputTokens  = 512
)

var llamax3LanguageNames = map[string]string{
	"de": "German", "en": "English", "tr": "Turkish", "hr": "Croatian",
	"it": "Italian", "uk": "Ukrainian", "zh": "Chinese", "hi": "Hindi",
	"es": "Spanish", "fr": "French", "ro": "Romanian", "pl": "Polish",
	"el": "Greek", "ru": "Russian", "bs": "Bosnian",
}

var euroLLMLanguageNames = map[string]string{
	"de": "German", "en": "English", "tr": "Turkish", "hr": "Croatian",
	"it": "Italian", "uk": "Ukrainian", "zh": "Chinese", "hi": "Hindi",
	"es": "Spanish", "fr": "French", "ro": "Romanian", "pl": "Polish",
	"el": "Greek", "ru": "Russian",
}

var towerPlusLanguageNames = map[string]string{
	"de": "German", "en": "English", "it": "Italian", "uk": "Ukrainian",
	"zh": "Chinese (Simplified)", "hi": "Hindi", "es": "Spanish",
	"fr": "French", "ro": "Romanian", "pl": "Polish", "ru": "Russian",
}

var towerInstructLanguageNames = map[string]string{
	"de": "German", "en": "English", "it": "Italian", "zh": "Chinese",
	"es": "Spanish", "fr": "French", "ru": "Russian",
}

// Gemma 4 is being evaluated only for the unresolved MunichBrief targets. Its
// upstream card describes broad multilingual coverage but does not publish the
// exact higher-quality subset, so these entries express evaluation eligibility,
// not a production-quality claim.
var gemma4LanguageNames = map[string]string{
	"de": "German", "bs": "Bosnian", "el": "Greek", "hi": "Hindi",
	"hr": "Croatian", "ru": "Russian",
}

var madlad400LanguageTokens = map[string]string{
	"bs": "bs", "el": "el", "hi": "hi", "hr": "hr", "ru": "ru",
}

// Guidance starts empty. The readiness protocol adds only general, evidenced
// language rules after reproducible failures rather than fixture-specific text.
var (
	llamax3LanguageGuidance = map[string]string{
		"bs": "Use standard Bosnian in Latin script, including Bosnian month names such as august, and natural Bosnian police terminology. Keep the agent and affected object of every active clause in the same roles; never reverse them or turn the agent into the thing being delayed. Render German evidential forms such as 'soll ... haben' explicitly with Bosnian allegation wording such as 'navodno', never as established fact. Preserve negation, uncertainty, cause and effect, and exactly which service was delayed or unaffected.",
		"hr": "Use only natural standard Croatian. Translate only the source, without adding or repeating text. Preserve who affects what, cause and effect, negation, and certainty: render 'soll ... haben' as 'navodno', 'keine Verurteilung' as a definite absence of conviction, and 'gegen' before a time as approximate. Preserve which transit service was delayed or unaffected.",
		"hi": "Translate every sentence fully into concise, natural standard Hindi in Devanagari; keep headlines within 90 characters after placeholder restoration. Crucially, preserve evidential and legal modality: render German 'soll ... haben' as an explicit allegation such as 'पर ... करने का आरोप है', never as fact, and retain Unschuldsvermutung as the presumption that a person is innocent until guilt is finally established. Keep subject, object, cause, gender, negation, uncertainty, and every legal clause unchanged. Use 'वाहन की खिड़की' or 'शीशा' for vehicle glass and 'गवाहों' once for paired witness forms.",
	}
	euroLLMLanguageGuidance       = map[string]string{}
	towerPlusLanguageGuidance     = map[string]string{}
	towerInstructLanguageGuidance = map[string]string{
		"ru": "Используй естественный русский язык. Немецкое 'soll ... haben' в полицейском сообщении никогда не означает 'должен был' и не устанавливает факт. Всегда передавай его по общей схеме 'По данным полиции, X предположительно совершил Y' либо 'X подозревается в том, что совершил Y'. Переводи 'Polizeieinsatz' только нейтрально как 'полицейская операция' или 'действия полиции', не как захват или рейд. Сохраняй отрицание, правовой статус, субъект, объект и причинность.",
	}
	gemma4LanguageGuidance = map[string]string{
		"bs": "Use only natural standard Bosnian in the Latin script, with consistent Ijekavian wording and no words from other languages. Translate every German verb and connective fully into Bosnian; use Bosnian nastaviti for continuing and never insert Indonesian or Malay wording. Preserve evidential and legal modality: German 'soll ... haben' reports an allegation and must use wording such as 'navodno' or 'sumnjiči se', never an established fact. Translate Tatverdächtiger as osumnjičeni, never optuženi, unless the source says the person was formally accused. Translate Unschuldsvermutung as pretpostavka nevinosti, never an approximate or invented expression. Keep a medical examination distinct from treatment or aid and preserve whether a time is approximate, using oko where appropriate. Preserve the distinction between merely being taken to a hospital and being admitted for inpatient care, using Ijekavian liječenje where needed. Translate Verkehrspolizei as saobraćajna policija and preserve that it conducts the investigation; translate a scheduled-service bus as a bus, not a route or an invented vehicle type.",
		"el": "Use only natural standard Greek. Apart from unchanged placeholders, every translated word must use the Greek alphabet; never mix in Latin or Cyrillic letters. Preserve evidential and legal modality: German 'soll ... haben' reports an allegation and should use wording such as 'φέρεται να', never an established fact. Translate Unschuldsvermutung as τεκμήριο αθωότητας. Translate Polizeieinsatz neutrally as αστυνομική επιχείρηση or αστυνομική επέμβαση. Translate neutral German befragen as ρωτώ or παίρνω κατάθεση από, never as συλλαμβάνω or κρατώ: questioning a witness is not arrest or detention. Keep a medical examination distinct from treatment or aid.",
		"hi": "Use only natural standard Hindi in Devanagari for words. Preserve every source numeral with the same ASCII digits 0-9 exactly, including leading zeroes in times and numbers such as 110; never convert digits to Devanagari numerals or mix numeral scripts. Preserve evidential and legal modality: every German 'soll ... haben' reports an allegation, so the sentence describing the alleged act itself must explicitly contain wording such as 'पर ... का आरोप है' or 'कथित तौर पर'; police attribution alone is not sufficient, and the act must never become established fact. Keep a medical examination distinct from treatment or aid. Preserve whether a time is approximate: every approximate source marker such as German gegen must be rendered explicitly with लगभग or करीब, never omitted. Preserve every explicitly stated gender: render female role nouns such as Radfahrerin explicitly with wording such as महिला साइकिल चालक, never as an unmarked or male role.",
		"hr": "Use only natural standard Croatian, including prometna nesreća and prometna policija rather than Serbian or Bosnian saobraćaj wording, and use the Croatian adjective minhenska for Münchner. Preserve evidential and legal modality: German 'soll ... haben' reports an allegation and must use wording such as 'navodno', never an established fact. Translate Tatverdächtiger as osumnjičenik, never optuženi, unless the source says the person was formally accused. Translate Mehrfamilienhaus as a multi-unit residential building such as višestambena zgrada. Preserve arrest and release precisely: festgenommen means uhićen or priveden, never primljen, and later released must remain explicit. Keep a medical examination distinct from treatment or aid and preserve whether a time is approximate, using oko where appropriate. Preserve inpatient admission: stationär in ein Krankenhaus gebracht means hospitalizirana or primljena na bolničko liječenje, never merely transported to hospital.",
		"ru": "Use only natural standard Russian. Preserve evidential and legal modality: German 'soll ... haben' reports an allegation and should use wording such as 'предположительно' or 'подозревается', never an established fact. Keep a medical examination distinct from treatment or aid, and use neutral wording for police operations. Preserve explicit causality, including whether a driver caused a crash rather than merely became involved in one, and preserve grammatical gender such as велосипедистка for a female cyclist.",
	}
)

type evaluationNativeAdapter struct {
	client            *OllamaClient
	adapterName       string
	sourceName        string
	targetName        string
	targetGuidance    string
	raw               bool
	rawChatML         bool
	generationOptions chatOptions
}

func newEvaluationNativeAdapter(baseURL, model, targetCode, adapterName string, names map[string]string, guidance map[string]string, raw, rawChatML bool, timeout time.Duration, contextSize, contextLimit int, baseClient *http.Client) (*evaluationNativeAdapter, error) {
	definitions := langregistry.Registered()
	if err := langregistry.Validate(definitions); err != nil {
		return nil, err
	}
	source := langregistry.Canonical(definitions)
	target, found := langregistry.ByCode(definitions, targetCode)
	if !found || target.Canonical {
		return nil, fmt.Errorf("%s native adapter requires a translated target language", adapterName)
	}
	sourceName, sourceSupported := names[source.Code]
	targetName, targetSupported := names[target.Code]
	if !sourceSupported {
		return nil, fmt.Errorf("%s does not officially support source language %q", adapterName, source.Code)
	}
	if !targetSupported {
		return nil, fmt.Errorf("%s does not officially support target language %q", adapterName, target.Code)
	}
	if contextSize <= 0 || contextSize > contextLimit {
		contextSize = contextLimit
	}
	client, err := NewOllamaClient(baseURL, model, timeout, contextSize, baseClient)
	if err != nil {
		return nil, err
	}
	return &evaluationNativeAdapter{
		client: client, adapterName: adapterName, sourceName: sourceName,
		targetName: targetName, targetGuidance: guidance[target.Code], raw: raw, rawChatML: rawChatML,
		generationOptions: chatOptions{Temperature: 0, NumPredict: evaluationMaxOutputTokens, NumCtx: contextSize},
	}, nil
}

func NewLLaMAX3NativeAdapter(baseURL, model, targetCode string, timeout time.Duration, contextSize int, baseClient *http.Client) (*evaluationNativeAdapter, error) {
	return newEvaluationNativeAdapter(baseURL, model, targetCode, "LLaMAX3", llamax3LanguageNames, llamax3LanguageGuidance, true, false, timeout, contextSize, llamax3ContextLimit, baseClient)
}

func NewEuroLLMNativeAdapter(baseURL, model, targetCode string, timeout time.Duration, contextSize int, baseClient *http.Client) (*evaluationNativeAdapter, error) {
	return newEvaluationNativeAdapter(baseURL, model, targetCode, "EuroLLM", euroLLMLanguageNames, euroLLMLanguageGuidance, false, false, timeout, contextSize, euroLLMContextLimit, baseClient)
}

func NewTowerPlusNativeAdapter(baseURL, model, targetCode string, timeout time.Duration, contextSize int, baseClient *http.Client) (*evaluationNativeAdapter, error) {
	return newEvaluationNativeAdapter(baseURL, model, targetCode, "Tower+", towerPlusLanguageNames, towerPlusLanguageGuidance, false, false, timeout, contextSize, towerPlusContextLimit, baseClient)
}

func NewTowerInstructNativeAdapter(baseURL, model, targetCode string, timeout time.Duration, contextSize int, baseClient *http.Client) (*evaluationNativeAdapter, error) {
	return newEvaluationNativeAdapter(baseURL, model, targetCode, "TowerInstruct", towerInstructLanguageNames, towerInstructLanguageGuidance, true, true, timeout, contextSize, towerInstructContextLimit, baseClient)
}

func NewGemma4NativeAdapter(baseURL, model, targetCode string, timeout time.Duration, contextSize int, baseClient *http.Client) (*evaluationNativeAdapter, error) {
	return newEvaluationNativeAdapter(baseURL, model, targetCode, "Gemma 4", gemma4LanguageNames, gemma4LanguageGuidance, false, false, timeout, contextSize, gemma4ContextLimit, baseClient)
}

type madlad400NativeAdapter struct {
	client      *OllamaClient
	targetToken string
}

func NewMADLAD400NativeAdapter(baseURL, model, targetCode string, timeout time.Duration, contextSize int, baseClient *http.Client) (*madlad400NativeAdapter, error) {
	definitions := langregistry.Registered()
	if err := langregistry.Validate(definitions); err != nil {
		return nil, err
	}
	target, found := langregistry.ByCode(definitions, targetCode)
	if !found || target.Canonical {
		return nil, errors.New("MADLAD-400 native adapter requires a translated target language")
	}
	token, supported := madlad400LanguageTokens[target.Code]
	if !supported {
		return nil, fmt.Errorf("MADLAD-400 evaluation does not support target language %q", target.Code)
	}
	if contextSize <= 0 || contextSize > madlad400ContextLimit {
		contextSize = madlad400ContextLimit
	}
	client, err := NewOllamaClient(baseURL, model, timeout, contextSize, baseClient)
	if err != nil {
		return nil, err
	}
	return &madlad400NativeAdapter{client: client, targetToken: token}, nil
}

func (a *madlad400NativeAdapter) Translate(ctx context.Context, text string) (string, string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", errorOf(ErrorOutput, "MADLAD-400 native input is empty")
	}
	content, model, err := a.client.rawGenerate(ctx, "<2"+a.targetToken+"> "+text, chatOptions{
		Temperature: 0,
		NumPredict:  madlad400MaxOutputTokens,
		NumCtx:      madlad400ContextLimit,
	})
	if err != nil {
		return "", model, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", model, errorOf(ErrorOutput, "MADLAD-400 native output is empty")
	}
	return content, model, nil
}

func (a *evaluationNativeAdapter) Translate(ctx context.Context, text string) (string, string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", errorOf(ErrorOutput, "%s native input is empty", a.adapterName)
	}
	prompt := labelledNativePrompt(a.sourceName, a.targetName, a.targetGuidance, text)
	var content, model string
	var err error
	if a.raw {
		if a.rawChatML {
			prompt = "<|im_start|>user\n" + prompt + "<|im_end|>\n<|im_start|>assistant\n"
		} else {
			prompt = llamax3NativePrompt(a.sourceName, a.targetName, a.targetGuidance, text)
		}
		content, model, err = a.client.rawGenerate(ctx, prompt, a.generationOptions)
	} else {
		content, model, err = a.client.chatWithOptions(ctx, true, "", prompt, nil, a.generationOptions)
	}
	if err != nil {
		return "", model, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", model, errorOf(ErrorOutput, "%s native output is empty", a.adapterName)
	}
	return content, model, nil
}

func llamax3NativePrompt(sourceName, targetName, guidance, text string) string {
	instruction := fmt.Sprintf("Translate the following sentences from %s to %s. Output only the translation. Preserve every placeholder matching `__MB_[A-Z_]+_[0-9]{4}__` exactly, character-for-character; translate only the surrounding text.", sourceName, targetName)
	if guidance = strings.TrimSpace(guidance); guidance != "" {
		instruction += " " + guidance
	}
	return fmt.Sprintf(
		"Below is an instruction that describes a task, paired with an input that provides further context. Write a response that appropriately completes the request.\n### Instruction:\n%s\n### Input:\n%s\n### Response:",
		instruction, strings.TrimSpace(text),
	)
}

func labelledNativePrompt(sourceName, targetName, guidance, text string) string {
	requirement := "Preserve every placeholder matching `__MB_[A-Z_]+_[0-9]{4}__` exactly, character-for-character, and output only the translation."
	if guidance = strings.TrimSpace(guidance); guidance != "" {
		requirement += " " + guidance
	}
	return fmt.Sprintf(
		"Translate the following %s source text to %s:\n%s\n%s: %s\n%s:",
		sourceName, targetName, requirement, sourceName, strings.TrimSpace(text), targetName,
	)
}

func translationAdapterUsesRawGenerate(adapter string) bool {
	switch strings.TrimSpace(adapter) {
	case TranslationAdapterSeedX, TranslationAdapterLLaMAX3, TranslationAdapterTowerInstruct, TranslationAdapterMADLAD400:
		return true
	default:
		return false
	}
}

var _ nativeTextTranslator = (*evaluationNativeAdapter)(nil)
var _ nativeTextTranslator = (*madlad400NativeAdapter)(nil)
