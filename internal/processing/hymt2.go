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
	hyMT2ContextLimit        = 8192
	hyMT2MaximumOutputTokens = 4096
)

var hyMT2LanguageNames = map[string]string{
	"zh": "Chinese", "en": "English", "fr": "French", "pt": "Portuguese",
	"es": "Spanish", "ja": "Japanese", "tr": "Turkish", "ru": "Russian",
	"ar": "Arabic", "ko": "Korean", "th": "Thai", "it": "Italian",
	"de": "German", "vi": "Vietnamese", "ms": "Malay", "id": "Indonesian",
	"tl": "Filipino", "hi": "Hindi", "zh-Hant": "Traditional Chinese", "pl": "Polish",
	"cs": "Czech", "nl": "Dutch", "km": "Khmer", "my": "Burmese",
	"fa": "Persian", "gu": "Gujarati", "ur": "Urdu", "te": "Telugu",
	"mr": "Marathi", "he": "Hebrew", "bn": "Bengali", "ta": "Tamil",
	"uk": "Ukrainian", "bo": "Tibetan", "kk": "Kazakh", "mn": "Mongolian",
	"ug": "Uyghur", "yue": "Cantonese",
}

var hyMT2LanguageGuidance = map[string]string{
	"zh": "Use exact Chinese legal terms: German ‘Verurteilung’ means conviction (‘定罪’), and ‘rechtskräftige Verurteilung’ means a final or legally effective conviction (‘最终定罪’ or ‘已生效的定罪判决’), never merely a formal/final judgment (‘正式判决’ or ‘最终判决’), because a judgment may be an acquittal. Translate ‘Unschuldsvermutung’ as ‘无罪推定’, which applies until a final conviction. Keep generic vehicle glass generic: German ‘Scheibe’ means ‘车窗’ or ‘车窗玻璃’ unless the source explicitly says ‘Windschutzscheibe’, which means ‘挡风玻璃’. Preserve evidentiary uncertainty: German ‘Hinweise’ and ‘Auffälligkeiten’ are tentative ‘迹象’, ‘征兆’, or observations, never proof, confirmed drinking or drug use, or established impairment. Render alcohol- or drug-typical abnormalities as ‘与酒精或毒品有关的异常表现’ or equally tentative wording, never as ‘饮酒或吸毒问题’.",
	"hi": "Apply each distinction only when its corresponding German wording occurs; never add glossary rules or examples to the translation. Mark ‘soll … haben’ as alleged with ‘कथित तौर पर’ or ‘आरोप है कि’, not as established fact. Render ‘beschädigt’ as completed damage, ‘क्षतिग्रस्त’, not an attempt or breakage. Render ‘rechtskräftige Verurteilung’ as final conviction, ‘अंतिम दोषसिद्धि’, not a formal sentence or judgment; the presumption of innocence applies until final conviction. Keep ‘Betroffener’ or ‘Betroffene’ neutral as ‘संबंधित व्यक्ति’, never ‘पीड़ित’ unless the source identifies a victim. Keep ‘unklare Gefahrenlage’ uncertain, ‘अस्पष्ट खतरे की स्थिति’ or ‘अनिश्चित खतरे की स्थिति’, not a definite dangerous situation. ‘Verkehrspolizei’ means road traffic police, ‘यातायात पुलिस’, never transport police, ‘परिवहन पुलिस’. ‘Mehrfamilienhaus’ means an apartment or multi-family residential building, ‘बहु-परिवारीय आवासीय इमारत’, not merely a multi-storey building. ‘Linienbus’ means a scheduled public-service bus, ‘नियमित सार्वजनिक बस’, never an ordinary bus, ‘साधारण बस’.",
	"ru": "Apply each distinction only when its German wording occurs; never add guidance to the translation. Every reported allegation ‘soll … haben’ must remain explicit with ‘предположительно’ or equivalent, never an established fact. ‘Polizeieinsatz’ means ‘полицейская операция’ or ‘вмешательство полиции’, never ‘патрулирование’. Keep headlines concise: translate an operation delaying a transit placeholder directly as ‘Полицейская операция … задерживает [placeholder]’; never add ‘движение поездов типа’. Use plural Russian grammar around plural S-Bahnen. ‘Verurteilung’ means ‘осуждение’; ‘eine Verurteilung liegt nicht vor’ means no conviction at all, ‘осуждения нет’, not merely no final conviction. For ‘bis zu einer rechtskräftigen Verurteilung’, say ‘до вступления в законную силу обвинительного приговора’; never say merely ‘до вынесения окончательного приговора’, because a final verdict may be an acquittal. Translate ‘Unschuldsvermutung’ as ‘презумпция невиновности’. Translate ordinary ‘befragen’ as neutral ‘опросить’, never ‘допросить’ unless the source explicitly says interrogation; ‘einen Zeugen’ means one witness, ‘одного свидетеля’, never one of several witnesses. German ‘an der … Straße’ means ‘на улице’, never merely near or by it, ‘у улицы’. ‘Medizinisch untersucht’ means medically examined, ‘медицински осмотрели’ or ‘провели медицинский осмотр’, never given treatment or medical assistance. Keep ‘Betroffener’ or ‘Betroffene’ neutral as ‘соответствующее лицо’ or ‘участник’, never ‘пострадавший’ unless the source identifies a victim. ‘Stationär in ein Krankenhaus gebracht’ means admitted as an inpatient, ‘госпитализирована’ or ‘помещена в стационар’, not merely taken for treatment. ‘Linienbus’ means a scheduled route bus, ‘рейсовый автобус’, not merely a city bus.",
	"en": "Keep neutral German person labels neutral: do not translate ‘Betroffener’ or ‘Betroffene’ as ‘victim’ unless the source explicitly identifies a victim. Preserve explicit medical and police-control meaning: ‘stationär in ein Krankenhaus gebracht’ means admitted to hospital as an inpatient, and ‘Anhaltesignale’ in a police-control context means police stop signals or orders, not road stop signs.",
	"es": "Keep generic German vehicle glass generic: translate ‘Scheibe’ as ‘cristal’ or ‘ventanilla’ unless the source explicitly says ‘Windschutzscheibe’, which means ‘parabrisas’. Use ‘presunción de inocencia’ for ‘Unschuldsvermutung’. ‘Rechtskräftige Verurteilung’ means a final conviction: translate it as ‘condena firme’ or ‘condena definitiva’, never merely ‘sentencia firme’ or a final decision, because a judgment may be an acquittal. Translate neutral ‘befragte’ as ‘preguntó’ or ‘entrevistó’, not ‘interrogó’, unless the source explicitly describes an interrogation. In medical reporting, ‘stationär in ein Krankenhaus gebracht’ means ‘ingresada en un hospital’ or ‘hospitalizada’, never ‘trasladada de forma permanente’. ‘Linienbus’ is a scheduled bus: translate it as ‘autobús de línea’ or ‘autobús regular’; preserve the bus as the vehicle and never replace it with only a route or line (‘ruta’ or ‘línea’).",
	"fr": "Use standard French police and medical terminology: German ‘Verkehrspolizei’ means traffic or road police (‘police de la circulation’ or ‘police routière’), never public-transport police (‘police des transports’). In medical reporting, ‘stationär in ein Krankenhaus gebracht’ means admitted or hospitalized for inpatient care (‘admis(e) à l’hôpital’ or ‘hospitalisé(e)’), not a permanent transfer. In legal reporting, translate ‘Unschuldsvermutung’ as ‘présomption d’innocence’; it applies until a final conviction (‘condamnation définitive’), never merely until a final decision or judgment. German ‘Scheibe’ means generic vehicle glass or a window (‘vitre’) unless the source explicitly says ‘Windschutzscheibe’; never infer ‘pare-brise’. German ‘an der [street]’ locates an event on or at that street, so use ‘dans la rue’ or ‘sur’ as natural in context; never change it to nearby (‘près de’ or ‘à proximité de’).",
	"it": "REGOLA PRIORITARIA PER I TITOLI: ogni verbo tedesco finito deve diventare un verbo italiano coniugato. Traduci ‘flieht’ con ‘fugge’ o ‘si dà alla fuga’; non scrivere mai l’agrammaticale ‘fuga dalla polizia’. Prima di rispondere, verifica mentalmente che ogni azione del titolo abbia un verbo coniugato. Usa un italiano standard naturale e restituisci soltanto la traduzione. Mantieni generico ‘Scheibe’ con ‘vetro’ o ‘finestrino’, salvo che la fonte dica esplicitamente ‘Windschutzscheibe’ (‘parabrezza’). Traduci ‘Unschuldsvermutung’ con ‘presunzione d’innocenza’ e ‘rechtskräftige Verurteilung’ con ‘condanna definitiva’ o ‘condanna passata in giudicato’, mai con la generica ‘sentenza definitiva’, che può anche assolvere. Conserva esattamente azioni e stato delle indagini: le indagini in corso restano in corso; ‘Überprüfung’ o ‘Kontrolle’ indica un controllo o una verifica, non una ‘perquisizione’; ‘Hinweise’ e ‘Auffälligkeiten’ sono indizi o segnali incerti, non consumo accertato.",
	"pl": "Use exact Polish legal terms: ‘Verurteilung’ means ‘skazanie’, and ‘rechtskräftige Verurteilung’ means ‘prawomocne skazanie’, never merely ‘wyrok’ or ‘orzeczenie’, because a judgment or ruling may be an acquittal. Keep neutral German person labels neutral: render ‘Betroffener’ or ‘Betroffene’ with a neutral Polish person label such as ‘osoba, której dotyczy sprawa’, never ‘poszkodowany’ or ‘ofiara’ unless the source explicitly identifies an injured party or victim. Preserve evidentiary uncertainty: ‘Hinweise’ and ‘Auffälligkeiten’ are ‘przesłanki’, ‘oznaki’, or neutral observations, never ‘dowody’, confirmed intoxication, or confirmed substance use. Use standard Polish spelling in headlines: write ‘interwencja’, never ‘intervencja’.",
	"tr": "Preserve the degree of damage: translate ‘beschädigen’ as ‘zarar vermek’ or ‘hasar vermek’, never ‘kırmak’ unless the source explicitly states breakage. Use exact Turkish legal terms: ‘Verurteilung’ means ‘mahkûmiyet’, and ‘rechtskräftige Verurteilung’ means ‘kesinleşmiş mahkûmiyet’, never merely ‘kesinleşmiş hüküm’ or ‘kesinleşmiş karar’, because a final judgment may be an acquittal. Preserve time precision: ‘genau’ means ‘tam olarak’ without ‘sularında’ or ‘civarında’, while ‘gegen’ is approximate. Keep ordinary police questioning neutral: translate ‘befragen’ as ‘soru sormak’, ‘görüşmek’, or ‘ifadesine başvurmak’, never ‘sorgulamak’ unless the source explicitly describes an interrogation. Keep neutral person labels neutral: render ‘Betroffener’ or ‘Betroffene’ as ‘ilgili kişi’ or ‘söz konusu kişi’, never ‘mağdur’ unless the source explicitly says ‘Opfer’ or ‘Geschädigter’. In medical reporting, ‘stationär in ein Krankenhaus gebracht’ means admitted for inpatient care, ‘hastaneye yatırıldı’, not merely ‘hastaneye kaldırıldı’ or taken for treatment. In a police-control context, ‘Anhaltesignale’ means police stop orders or signals, ‘polisin dur ihtarları’ or ‘dur emri’, never road ‘dur işaretleri’. Preserve evidentiary uncertainty: ‘Hinweise’ and ‘Auffälligkeiten’ are tentative ‘belirtiler’ or ‘işaretler’, never ‘kanıt’, ‘delil’, confirmed intoxication, or confirmed substance use.",
	"uk": "For every German reported-allegation construction ‘soll … haben’, the Ukrainian translation must explicitly mark the alleged action with ‘нібито’ or ‘як стверджується’. Never express this construction with ‘мав’, ‘мала’, or ‘мали’ plus an infinitive; those forms can mean that someone was supposed or required to act. Do not turn an allegation into an established fact. Use exact Ukrainian legal terms: ‘Verurteilung’ means ‘засудження’, and ‘rechtskräftige Verurteilung’ means ‘остаточне засудження’ or ‘набрання законної сили обвинувальним вироком’, never merely ‘вирок’ or ‘остаточне рішення’, because a verdict or decision may be an acquittal. Translate ‘Unschuldsvermutung’ as ‘презумпція невинуватості’. Keep ordinary police questioning neutral: translate ‘befragen’ as ‘опитувати’, never ‘допитувати’ unless the source explicitly describes an interrogation. Preserve street-location precision: German ‘an der … Straße’ means on/at the street (‘на вулиці’), not merely near it (‘біля вулиці’). Keep neutral person roles neutral: translate ‘Betroffener’ or ‘Betroffene’ as ‘відповідна особа’ or ‘особа, якої це стосується’, never ‘постраждалий’ or ‘потерпілий’ unless the source explicitly identifies that person as injured or as a victim. Preserve evidentiary uncertainty: ‘Hinweise’ and ‘Auffälligkeiten’ are tentative ‘ознаки’, ‘підозри’, or observations, never ‘докази’, proof, confirmed intoxication, or confirmed substance use.",
}

// HyMT2NativeAdapter follows Tencent's user-only translation contract and
// recommended 1.8B/7B sampling parameters. Production routing selects it only
// through a durable setting for an officially supported target language.
type HyMT2NativeAdapter struct {
	client            *OllamaClient
	sourceName        string
	targetName        string
	targetGuidance    string
	generationOptions chatOptions
}

func NewHyMT2NativeAdapter(baseURL, model, targetCode string, timeout time.Duration, contextSize int, baseClient *http.Client) (*HyMT2NativeAdapter, error) {
	definitions := langregistry.Registered()
	if err := langregistry.Validate(definitions); err != nil {
		return nil, err
	}
	source := langregistry.Canonical(definitions)
	target, found := langregistry.ByCode(definitions, targetCode)
	if !found || target.Canonical {
		return nil, errors.New("Hy-MT2 native adapter requires a translated target language")
	}
	sourceName, sourceSupported := hyMT2LanguageNames[source.Code]
	targetName, targetSupported := hyMT2LanguageNames[target.Code]
	if !sourceSupported {
		return nil, fmt.Errorf("Hy-MT2 does not officially support source language %q", source.Code)
	}
	if !targetSupported {
		return nil, fmt.Errorf("Hy-MT2 does not officially support target language %q", target.Code)
	}
	if contextSize <= 0 || contextSize > hyMT2ContextLimit {
		contextSize = hyMT2ContextLimit
	}
	client, err := NewOllamaClient(baseURL, model, timeout, contextSize, baseClient)
	if err != nil {
		return nil, err
	}
	return &HyMT2NativeAdapter{
		client:         client,
		sourceName:     sourceName,
		targetName:     targetName,
		targetGuidance: hyMT2LanguageGuidance[target.Code],
		generationOptions: chatOptions{
			Temperature:   0.7,
			TopP:          0.6,
			TopK:          20,
			RepeatPenalty: 1.05,
			NumPredict:    hyMT2MaximumOutputTokens,
			NumCtx:        contextSize,
		},
	}, nil
}

func (a *HyMT2NativeAdapter) Translate(ctx context.Context, text string) (string, string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", errorOf(ErrorOutput, "Hy-MT2 native input is empty")
	}
	content, model, err := a.client.chatWithOptions(ctx, true, "", hyMT2NativePrompt(a.sourceName, a.targetName, a.targetGuidance, text), nil, a.generationOptions)
	if err != nil {
		return "", model, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", model, errorOf(ErrorOutput, "Hy-MT2 native output is empty")
	}
	return content, model, nil
}

func hyMT2NativePrompt(sourceName, targetName, guidance, text string) string {
	guidance = strings.TrimSpace(guidance)
	if guidance != "" {
		guidance = "8. Target-language guidance: " + guidance + "\n"
	}
	return fmt.Sprintf(
		"### Task\n"+
			"Translate the following text from %s into %s.\n\n"+
			"### Strict Rules\n"+
			"1. Output only the translated plain text. Do not add explanations, notes, headings, commentary, Markdown, or URLs.\n"+
			"2. Treat every token matching `__MB_[A-Z_]+_[0-9]{4}__` as an immutable placeholder. Copy every occurrence exactly, character-for-character, and preserve the same number of occurrences.\n"+
			"3. Never translate, transliterate, inflect, decline, conjugate, modify, split, remove, duplicate, or replace a placeholder.\n"+
			"4. When a placeholder contains an entity type such as `STREET`, `DISTRICT`, `TRAIN_STATION`, `COMMUTER_TRAIN`, or `SUBWAY_SYSTEM`, use that type only to understand the sentence and produce natural grammar around the placeholder. Do not alter the placeholder itself.\n"+
			"5. If the target language would normally require changing the hidden entity, restructure the surrounding sentence so the placeholder remains unchanged.\n"+
			"6. Preserve the original meaning, tone, factual details, numbers, dates, times, negation, uncertainty, attribution, and relationships. Do not add or infer information.\n"+
			"7. Produce natural, fluent %s rather than a word-for-word translation.\n"+
			"%s\n"+
			"### Source Data\n%s",
		sourceName, targetName, targetName, guidance, strings.TrimSpace(text),
	)
}
