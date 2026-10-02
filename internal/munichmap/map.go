// Package munichmap contains the official Munich district boundaries as small,
// locally bundled SVG paths. Geographic report assignment belongs to location;
// these display paths must never be used for address inference or geocoding.
package munichmap

import (
	_ "embed"
	"encoding/json"
)

//go:generate python3 ../../scripts/generate-munich-map.py

const (
	ViewBox     = "0 0 760 700"
	SourceURL   = "https://opendata.muenchen.de/dataset/vablock_stadtbezirke_opendata"
	LicenseURL  = "https://www.govdata.de/dl-de/by-2-0"
	Attribution = "Landeshauptstadt München – GeodatenService"
)

// District is one official district, including all its separate polygons.
// X and Y locate a label inside the largest polygon. LabelRadius is its
// approximate available radius, in viewBox units, before reaching a boundary.
// Path contains only generated SVG M/L/Z commands and numeric coordinates;
// callers can pass it as an ordinary string into an html/template attribute.
type District struct {
	ID          string  `json:"id"`
	Number      int     `json:"number"`
	Name        string  `json:"name"`
	Path        string  `json:"path"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	LabelRadius float64 `json:"label_radius"`
}

//go:embed districts.json
var data []byte

var districts = mustLoad()

func mustLoad() []District {
	var document struct {
		ViewBox   string     `json:"view_box"`
		Districts []District `json:"districts"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		panic("munichmap: invalid bundled geometry: " + err.Error())
	}
	if document.ViewBox != ViewBox || len(document.Districts) != 25 {
		panic("munichmap: invalid bundled district set")
	}
	return document.Districts
}

// Districts returns the 25 districts in official district-number order.
// The returned slice is independent; callers may add counts or reorder it.
func Districts() []District {
	return append([]District(nil), districts...)
}
