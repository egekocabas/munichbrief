package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/egekocabas/munichbrief/internal/location"
	"github.com/egekocabas/munichbrief/internal/store"
)

func TestDistrictFiltersNormalizeAndRejectInvalidOverrides(t *testing.T) {
	const district = "munich:district:ramersdorf-perlach"
	for _, tc := range []struct {
		name, query, district, area string
		areas                       []string
	}{
		{"preserved", "district=" + district + "&category=traffic&q=bike", district, "", nil},
		{"exact area overrides", "district=" + district + "&area=Sendling", "", "Sendling", nil},
		{"areas override", "district=" + district + "&neighborhood=Neuperlach&neighborhood=Ramersdorf", "", "", []string{"Neuperlach", "Ramersdorf"}},
		{"outside", "district=outside", location.DistrictOutside, "", nil},
		{"unassigned", "district=unassigned", location.DistrictUnassigned, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values, _ := url.ParseQuery(tc.query)
			f, err := readerFiltersFromValues(values)
			if err != nil || f.District != tc.district || f.Area != tc.area || !reflect.DeepEqual(f.Areas, tc.areas) {
				t.Fatalf("filters=%+v err=%v", f, err)
			}
			got, explicit, err := readerURLFilters(httptest.NewRequest("GET", listingURL("en", f, "published", 20, 1), nil))
			if err != nil || !explicit || !reflect.DeepEqual(got, f) {
				t.Fatalf("roundtrip=%+v explicit=%v err=%v", got, explicit, err)
			}
		})
	}
	for _, query := range []string{
		"district=invalid", "district=munich:district:schwabing", "district=%00", "district=outside&district=unassigned",
		"district=invalid&area=Sendling", "district=invalid&neighborhood=Sendling", "district=outside&district=outside&area=Sendling",
	} {
		if _, _, err := readerURLFilters(httptest.NewRequest("GET", "/en/search?"+query, nil)); err == nil {
			t.Errorf("accepted invalid district input %q", query)
		}
	}
}

func TestDistrictListingStateSurvivesAdvancedControlsAndNavigation(t *testing.T) {
	s, _ := newContactServer(t)
	f := store.ReaderFilters{District: "munich:district:ramersdorf-perlach", Text: "bicycle", Category: "traffic", Number: "1201", Assistance: "no", From: "2026-09-01", To: "2026-09-30"}
	data := timelinePage{basePage: basePage{LanguageSwitches: []languageLink{{Code: "de"}, {Code: "tr"}}}, Page: 3, TotalPages: 5}
	s.decorateReader(&data, "en", true, "incident", 30, f)
	links := []string{data.TodayURL, data.WeekURL, data.AllDatesURL, data.AssistanceURL, data.PublishedURL, data.IncidentViewURL, data.NextURL, data.PreviousURL}
	for _, link := range data.LanguageSwitches {
		links = append(links, link.URL)
	}
	for _, page := range data.Pages {
		if !page.Gap {
			links = append(links, page.URL)
		}
	}
	for _, link := range links {
		got, explicit, err := readerURLFilters(httptest.NewRequest("GET", strings.SplitN(link, "#", 2)[0], nil))
		if err != nil || !explicit || got.District != f.District || got.Text != f.Text || got.Category != f.Category || got.Number != f.Number {
			t.Fatalf("district or advanced state lost: %s %+v %v", link, got, err)
		}
	}
	foundField, foundChip := false, false
	for _, field := range data.FilterFields {
		foundField = foundField || (field.Key == "district" && field.Value == f.District)
	}
	for _, chip := range data.ActiveFilters {
		foundChip = foundChip || (chip.Key == "district" && chip.Value == "Ramersdorf-Perlach")
	}
	if !foundField || !foundChip {
		t.Fatal("district missing from pagination controls or removable chips", data.FilterFields, data.ActiveFilters)
	}
	// Native GET forms retain a district when only other advanced controls change.
	values := readerFilterValues(f)
	values.Set("q", "train")
	got, err := readerFiltersFromValues(values)
	if err != nil || got.District != f.District || got.Text != "train" {
		t.Fatal(got, err)
	}
	path := listingURL("en", f, "incident", 30, 1)
	w := contactRequest(s, "GET", path, nil)
	if w.Code != http.StatusOK || strings.Count(w.Body.String(), `name="district" value="`+f.District+`"`) < 2 {
		t.Fatalf("advanced and pagination forms lost district: status %d", w.Code)
	}
	var saved *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == searchCookieName {
			saved = cookie
		}
	}
	if saved == nil {
		t.Fatal("district search not remembered")
	}
	r := httptest.NewRequest("GET", "/en", nil)
	r.AddCookie(saved)
	if got := readSearch(r); !reflect.DeepEqual(got, f) {
		t.Fatal("remembered district search changed", got)
	}
	// An explicit district URL replaces the saved search rather than intersecting it.
	explicit := contactRequest(s, "GET", "/en/search?district=outside", nil, saved)
	if explicit.Code != http.StatusOK {
		t.Fatal("explicit district request failed", explicit.Code)
	}
	for _, cookie := range explicit.Result().Cookies() {
		if cookie.Name == searchCookieName {
			r := httptest.NewRequest("GET", "/en", nil)
			r.AddCookie(cookie)
			if got := readSearch(r); !reflect.DeepEqual(got, store.ReaderFilters{District: location.DistrictOutside}) {
				t.Fatal("explicit district inherited another tab's filters", got)
			}
		}
	}
}

func TestDistrictSearchActionsApplyOneGeographicScope(t *testing.T) {
	s, _ := newContactServer(t)
	const district = "munich:district:ramersdorf-perlach"
	path := "/en/search?district=" + district + "&q=bicycle&category=traffic&view=incident&page_size=30"
	pref := httptest.NewRecorder()
	if err := s.writeReaderPreferences(pref, neighborhoodsCookieName, store.ReaderFilters{Areas: []string{"Neuperlach"}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, action, value, district, area string
		areas                               []string
		cookie                              bool
	}{
		{"remove district", "remove", "district", "", "", nil, false},
		{"save areas", "neighborhoods_action", "save", "", "", []string{"Neuperlach"}, false},
		{"enable areas", "neighborhoods_action", "enable", "", "", []string{"Neuperlach"}, true},
		{"delete saved choice", "neighborhoods_action", "save", district, "", nil, false},
		{"disable inactive areas", "neighborhoods_action", "disable", district, "", nil, false},
		{"expired saved choice", "neighborhoods_action", "enable", district, "", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := url.Values{tc.action: {tc.value}}
			if tc.name == "save areas" {
				values.Set("saved_neighborhood", "Neuperlach")
			}
			var cookies []*http.Cookie
			if tc.cookie {
				cookies = pref.Result().Cookies()
			}
			w := contactRequest(s, "POST", path, values, cookies...)
			if w.Code != http.StatusSeeOther {
				t.Fatal(w.Code, w.Body.String())
			}
			u, _ := url.Parse(w.Header().Get("Location"))
			f, _, err := readerURLFilters(httptest.NewRequest("GET", u.RequestURI(), nil))
			if err != nil || f.District != tc.district || f.Area != tc.area || !reflect.DeepEqual(f.Areas, tc.areas) || f.Text != "bicycle" || f.Category != "traffic" || u.Query().Get("view") != "incident" || u.Query().Get("page_size") != "30" {
				t.Fatal("action lost search state", f, u, err)
			}
		})
	}
	for _, path := range []string{"/en/search?district=invalid&area=Sendling", "/en/search?district=outside&district=unassigned"} {
		w := contactRequest(s, "GET", path, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid district route %s returned %d", path, w.Code)
		}
	}
}

func TestDistrictMetadataLinksAndSpecialChips(t *testing.T) {
	s, _ := newContactServer(t)
	f := store.ReaderFilters{District: "munich:district:ramersdorf-perlach", Text: "bicycle", Category: "traffic"}
	data := timelinePage{Groups: []dayGroup{{Incidents: []incidentView{{AreaName: "Neuperlach", Record: store.IncidentRecord{AICategory: "violence"}}}}}}
	s.decorateReader(&data, "en", true, "incident", 30, f)
	s.decorateNeighborhoods(&data, httptest.NewRequest("GET", listingURL("en", f, "incident", 30, 1), nil))
	incident := data.Groups[0].Incidents[0]
	area, _, err := readerURLFilters(httptest.NewRequest("GET", strings.SplitN(incident.AreaURL, "#", 2)[0], nil))
	if err != nil || area.District != "" || area.Area != "Neuperlach" || area.Text != f.Text || area.Category != f.Category {
		t.Fatal("area link did not replace district", area, err)
	}
	category, _, err := readerURLFilters(httptest.NewRequest("GET", strings.SplitN(incident.CategoryURL, "#", 2)[0], nil))
	if err != nil || category.District != f.District || category.Category != "violence" || category.Text != f.Text {
		t.Fatal("category link did not retain district", category, err)
	}
	for _, special := range []struct{ id, label string }{{location.DistrictOutside, "MapOutside"}, {location.DistrictUnassigned, "MapUnassigned"}} {
		data := timelinePage{}
		s.decorateReader(&data, "en", true, "published", 20, store.ReaderFilters{District: special.id})
		if len(data.ActiveFilters) != 1 || data.ActiveFilters[0].Key != "district" || data.ActiveFilters[0].Value != s.localization.Text("en", special.label) {
			t.Fatal("special district missing a removable localized chip", data.ActiveFilters)
		}
	}
}
