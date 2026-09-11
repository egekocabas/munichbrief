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
// Production routing selects it explicitly through a durable per-language
// adapter setting; unconfigured upgrades retain structured-chat behavior.
type TranslateGemmaNativeAdapter struct {
	client         *OllamaClient
	source, target langregistry.Definition
	targetGuidance string
}

const translateGemmaContextLimit = 2048

var translateGemmaLanguageGuidance = map[string]string{
	"bs": "Piši samo standardnim bosanskim jezikom na latinici, bez srpske ćirilice, alternativa ili objašnjenja. Kopiraj svaki placeholder potpuno doslovno: ne prevodi englesku oznaku unutar njega; na primjer, __MB_MUNICIPALITY_0001__ mora ostati __MB_MUNICIPALITY_0001__. ‘Soll … haben’ mora ostati navod, koristeći ‘navodno’. Razlikuj ‘um genau’ → ‘tačno u’ od ‘gegen’ → ‘oko’. Koristi bosanske oblike ‘augusta’, ‘ljekar’, ‘dvije’, ‘povrijeđen’ i ‘svjedok’. Prevedi ‘während die U-Bahn nicht betroffen war’ kao ‘dok U-Bahn nije bio pogođen’. Za incident u zgradi koristi ‘u petak navečer’, ‘stambena zgrada s više stanova’, ‘smiriti situaciju’ i ‘uhapšen bez otpora’, te sačuvaj kasnije puštanje. ‘Linienbus’ je ‘autobus na redovnoj liniji’, ‘stationär in ein Krankenhaus gebracht’ je ‘hospitalizirana’, a ‘Verkehrspolizei’ je ‘saobraćajna policija’. Sačuvaj množinu signala i crvenih semafora. ‘Gesichert und bei der Überprüfung festgenommen’ znači ‘stavljen pod kontrolu i uhapšen tokom provjere’. ‘Hinweise’ i ‘Auffälligkeiten’ ostaju neizvjesne naznake, nikada potvrđena upotreba alkohola ili droga. Sačuvaj S-Bahnen i U-Bahn kao različite sisteme i zadrži svaki podatak iz izvora.",
	"hr": "Piši samo standardnim hrvatskim jezikom na latinici, bez srpskih riječi, alternativa ili objašnjenja. Obvezno: svaka konstrukcija ‘soll … haben’ mora sadržavati ‘navodno’. Pravnu rečenicu prevedi potpuno: ‘Još nije jasno je li bio uključen; osuda nije donesena i do pravomoćne osude vrijedi pretpostavka nedužnosti.’ Ne izostavljaj ‘osuda nije donesena’. Primijeni ove parove samo kada se njemački izraz pojavljuje: ‘Polizeieinsatz’ → ‘policijska intervencija’ (nikada ‘policijski intervencija’); ‘medizinisch untersucht’ → ‘medicinski pregledana’; ‘nicht betroffen’ → ‘nije pogođen’; ‘kollidierte … mit einer Mauer’ → ‘automobil se sudario sa zidom’; ‘befragte einen Zeugen’ → ‘razgovarala s jednim svjedokom’; ‘Mehrfamilienhaus’ → ‘stambena zgrada s više stanova’; ‘widerstandslos festgenommen’ → ‘uhićen bez otpora’; ‘Kriminalpolizei’ → ‘kriminalistička policija’; ‘Verkehrsunfall’ → ‘prometna nesreća’ (nikada ‘saobraćajna’); ‘Linienbus’ → ‘autobus na redovnoj liniji’; ‘stationär in ein Krankenhaus gebracht’ → ‘hospitalizirana’; ‘Verkehrspolizei’ → ‘prometna policija’. Za policijsku kontrolu sačuvaj množinu crvenih semafora te prevedi ‘gesichert und bei der Überprüfung festgenommen’ kao ‘osiguran i uhićen tijekom provjere’, nikada ‘uhapšen’. ‘Hinweise’ i ‘Auffälligkeiten’ moraju ostati neizvjesne indicije, ne potvrđene činjenice. Naslovi moraju imati najviše 90 znakova; naslov A glasi ‘Policijska intervencija na [ulica] uzrokuje kašnjenje [vlakovi]’.",
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
	client, err := NewOllamaClient(baseURL, model, timeout, min(contextSize, translateGemmaContextLimit), baseClient)
	if err != nil {
		return nil, err
	}
	return &TranslateGemmaNativeAdapter{client: client, source: source, target: target, targetGuidance: translateGemmaLanguageGuidance[target.Code]}, nil
}

func (a *TranslateGemmaNativeAdapter) Translate(ctx context.Context, text string) (string, string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", errorOf(ErrorOutput, "TranslateGemma native input is empty")
	}
	content, model, err := a.client.chat(ctx, true, "", translateGemmaNativePrompt(a.source, a.target, a.targetGuidance, text), nil)
	if err != nil {
		return "", model, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", model, errorOf(ErrorOutput, "TranslateGemma native output is empty")
	}
	return content, model, nil
}

func translateGemmaNativePrompt(source, target langregistry.Definition, guidance, text string) string {
	guidance = strings.TrimSpace(guidance)
	if guidance != "" {
		guidance = "Target-language guidance: " + guidance + "\n"
	}
	return fmt.Sprintf(`You are a professional %s (%s) to %s (%s) translator. Your goal is to accurately convey the meaning and nuances of the original %s text while adhering to %s grammar, vocabulary, and cultural sensitivities.
%sProduce only the %s translation, without any additional explanations or commentary. Please translate the following %s text into %s:


%s`, source.TranslationName, source.Code, target.TranslationName, target.Code, source.TranslationName, target.TranslationName, guidance, target.TranslationName, source.TranslationName, target.TranslationName, strings.TrimSpace(text))
}
