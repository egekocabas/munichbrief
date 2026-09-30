// Package location resolves geographic identity independently of the translation
// spelling dictionary. It deliberately contains no street geocoder.
package location

import (
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"
)

const CatalogVersion = "munich-areas-v2"

type Area struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}
type Entity struct {
	Area
	Kind    string   `json:"kind"`
	Aliases []string `json:"aliases,omitempty"`
	Parent  string   `json:"parent,omitempty"`
	Sources []string `json:"sources"`
}

const districtSource = "https://stadt.muenchen.de/rathaus/daten-fakten/bezirke.html"
const neighbourhoodSource = "https://stadt.muenchen.de/infos/stadtbezirke-geschichte.html"
const countySource = "https://familienleben.landkreis-muenchen.de/wissenswertes/kommunen-des-landkreises"
const festivalSource = "https://www.muenchen.de/stadtteile/ludwigsvorstadt-wissenswertes-tipps-und-infos"

var catalog = buildCatalog()

func normalize(s string) string {
	s = strings.ToLower(norm.NFC.String(s))
	s = strings.NewReplacer("–", "-", "—", "-", "/", "-").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(strings.ReplaceAll(s, " -", "-"), "- ", "-")
}

func buildCatalog() []Entity {
	entries := []Entity{{Area: Area{"city:munich", "München", "municipality"}, Kind: "city", Aliases: []string{"Munich", "Minga"}, Sources: []string{districtSource}}}
	districts := []string{"Altstadt-Lehel", "Ludwigsvorstadt-Isarvorstadt", "Maxvorstadt", "Schwabing-West", "Au-Haidhausen", "Sendling", "Sendling-Westpark", "Schwanthalerhöhe", "Neuhausen-Nymphenburg", "Moosach", "Milbertshofen-Am Hart", "Schwabing-Freimann", "Bogenhausen", "Berg am Laim", "Trudering-Riem", "Ramersdorf-Perlach", "Obergiesing-Fasangarten", "Untergiesing-Harlaching", "Thalkirchen-Obersendling-Forstenried-Fürstenried-Solln", "Hadern", "Pasing-Obermenzing", "Aubing-Lochhausen-Langwied", "Allach-Untermenzing", "Feldmoching-Hasenbergl", "Laim"}
	for _, name := range districts {
		entries = append(entries, Entity{Area: Area{"munich:district:" + normalize(name), name, "district"}, Kind: "district", Parent: "city:munich", Sources: []string{districtSource}})
	}
	municipalities := strings.Split("Aschheim|Aying|Baierbrunn|Brunnthal|Feldkirchen|Garching|Gräfelfing|Grasbrunn|Grünwald|Haar|Hohenbrunn|Höhenkirchen-Siegertsbrunn|Ismaning|Kirchheim|Neubiberg|Neuried|Oberhaching|Oberschleißheim|Ottobrunn|Planegg|Pullach|Putzbrunn|Sauerlach|Schäftlarn|Straßlach-Dingharting|Taufkirchen|Unterföhring|Unterhaching|Unterschleißheim", "|")
	for _, name := range municipalities {
		entries = append(entries, Entity{Area: Area{"municipality:" + normalize(name), name, "municipality"}, Kind: "municipality", Sources: []string{countySource}})
	}
	// Relationships here are containment, not a claim that the whole district
	// and its identically named historical neighbourhood have the same boundary.
	neighbourhoods := map[string]string{
		"Altstadt": "Altstadt-Lehel", "Lehel": "Altstadt-Lehel", "Ludwigsvorstadt": "Ludwigsvorstadt-Isarvorstadt", "Isarvorstadt": "Ludwigsvorstadt-Isarvorstadt",
		"Au": "Au-Haidhausen", "Haidhausen": "Au-Haidhausen", "Westend": "Schwanthalerhöhe", "Neuhausen": "Neuhausen-Nymphenburg", "Nymphenburg": "Neuhausen-Nymphenburg",
		"Englschalking": "Bogenhausen", "Neuaubing": "Aubing-Lochhausen-Langwied",
		"Milbertshofen": "Milbertshofen-Am Hart", "Am Hart": "Milbertshofen-Am Hart", "Freimann": "Schwabing-Freimann", "Alte Heide": "Schwabing-Freimann",
		"Trudering": "Trudering-Riem", "Riem": "Trudering-Riem", "Ramersdorf": "Ramersdorf-Perlach", "Neuperlach": "Ramersdorf-Perlach", "Perlach": "Ramersdorf-Perlach",
		"Obergiesing": "Obergiesing-Fasangarten", "Untergiesing": "Untergiesing-Harlaching", "Harlaching": "Untergiesing-Harlaching", "Solln": "Thalkirchen-Obersendling-Forstenried-Fürstenried-Solln",
		"Forstenried": "Thalkirchen-Obersendling-Forstenried-Fürstenried-Solln", "Fürstenried": "Thalkirchen-Obersendling-Forstenried-Fürstenried-Solln", "Neuhadern": "Hadern",
		"Pasing": "Pasing-Obermenzing", "Obermenzing": "Pasing-Obermenzing", "Aubing": "Aubing-Lochhausen-Langwied", "Lochhausen": "Aubing-Lochhausen-Langwied", "Langwied": "Aubing-Lochhausen-Langwied",
		"Allach": "Allach-Untermenzing", "Untermenzing": "Allach-Untermenzing", "Hasenbergl": "Feldmoching-Hasenbergl", "Feldmoching": "Feldmoching-Hasenbergl",
	}
	for name, parent := range neighbourhoods {
		entries = append(entries, Entity{Area: Area{"munich:locality:" + normalize(name), name, "neighbourhood"}, Kind: "neighbourhood", Parent: "munich:district:" + normalize(parent), Sources: []string{neighbourhoodSource}})
	}
	// These names do not establish a unique official district.
	for _, name := range []string{"Schwabing", "Giesing", "Innenstadt", "Am Harthof"} {
		entries = append(entries, Entity{Area: Area{"munich:locality:" + normalize(name), name, "broad_area"}, Kind: "locality", Parent: "city:munich", Sources: []string{neighbourhoodSource}})
	}
	entries = append(entries,
		Entity{Area: Area{"locality:garching-hochbrueck", "Garching-Hochbrück", "neighbourhood"}, Kind: "neighbourhood", Aliases: []string{"Hochbrück"}, Parent: "municipality:garching", Sources: []string{"https://www.garching.de/stadtportr%C3%A4t-leben/stadtportr%C3%A4t/_spuren-der-geschichte_/_/Hochbr%C3%BCck.pdf"}},
		Entity{Area: Area{"venue:schottenhamel", "Schottenhamel-Festzelt", "broad_area"}, Kind: "venue", Aliases: []string{"Schottenhamel", "Festhalle Schottenhamel", "Schottenhamel Festzelt", "Schottenhammel-Festzelt", "Schottenhammel", "Schottenhamel-Festzelts", "Schottenhammel-Festzelts"}, Parent: "munich:locality:ludwigsvorstadt", Sources: []string{"https://www.oktoberfest.de/bierzelte/grosse-zelte/festhalle-schottenhamel", festivalSource}},
		Entity{Area: Area{"locality:riemerling", "Riemerling", "neighbourhood"}, Kind: "neighbourhood", Parent: "municipality:hohenbrunn", Sources: []string{"https://hohenbrunn.de/unser-hohenbrunn/ortsportrait/"}},
		Entity{Area: Area{"venue:theresienwiese", "Theresienwiese", "broad_area"}, Kind: "venue", Aliases: []string{"Oktoberfest", "Wiesn"}, Parent: "munich:locality:ludwigsvorstadt", Sources: []string{festivalSource}},
		Entity{Area: Area{"natural:forstenrieder-park", "Forstenrieder Park", "broad_area"}, Kind: "natural_area", Sources: []string{"https://www.landkreis-muenchen.de/landkreis/gemeinden-und-staedte/baierbrunn/"}},
		Entity{Area: Area{"natural:perlacher-forst", "Perlacher Forst", "broad_area"}, Kind: "natural_area", Sources: []string{"https://www.baysf.de/de/wald-erleben/ausflugsziele-tipps.html"}},
	)
	for i := range entries {
		switch entries[i].Name {
		case "Englschalking":
			entries[i].Sources = []string{"https://stadt.muenchen.de/infos/marienburger.html"}
		case "Neuaubing":
			entries[i].Sources = []string{"https://stadt.muenchen.de/infos/sanierungsgebiet-aubing-neuaubing-westkreuz.html"}
		case "Allach-Untermenzing":
			entries[i].Aliases = []string{"Untermenzing-Allach"}
		case "Alte Heide":
			entries[i].Aliases = []string{"Alten Heide"}
		case "Garching":
			entries[i].Aliases = []string{"Garching b. München", "Garching bei München"}
		case "Kirchheim":
			entries[i].Aliases = []string{"Kirchheim b. München", "Kirchheim bei München"}
		case "Pullach":
			entries[i].Aliases = []string{"Pullach i. Isartal", "Pullach im Isartal"}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries
}

// Lookup never uses substring matching: a person's name or a street spelling
// cannot silently become a same-named neighbourhood.
func Lookup(name string) []Entity {
	var matches []Entity
	for _, e := range catalog {
		found := normalize(e.Name) == normalize(name)
		for _, alias := range e.Aliases {
			found = found || normalize(alias) == normalize(name)
		}
		if found {
			e.Aliases = append([]string(nil), e.Aliases...)
			e.Sources = append([]string(nil), e.Sources...)
			matches = append(matches, e)
		}
	}
	return matches
}
func parent(id string) (Entity, bool) {
	for _, e := range catalog {
		if e.ID == id {
			return e, true
		}
	}
	return Entity{}, false
}
func ancestor(upper, lower string) bool {
	for steps := 0; steps < len(catalog); steps++ {
		e, ok := parent(lower)
		if !ok || e.Parent == "" {
			return false
		}
		if e.Parent == upper {
			return true
		}
		lower = e.Parent
	}
	return false
}
