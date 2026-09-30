package location

import "testing"

func TestCaptionOnlySource(t *testing.T) {
	for _, tc := range []struct {
		title, body string
		want        bool
	}{
		{"Pressekonferenz zur Sicherheit auf dem Oktoberfest", "Sonderbeilage Polizei und Behörde", true},
		{"Sonderbeilage zur PK", "Zwischenbilanz des Festes", true},
		{"Unfall – Haar", "Am Morgen kam es in Haar zu einem Unfall.", false},
		{"Sonderbeilage", "Am Morgen kam es auf dem Oktoberfest zu einem Unfall.", false},
		{"Sonderbeilage", "Bilanz: Auf dem Oktoberfest wurden zwei Personen verletzt.", false},
	} {
		if got := CaptionOnlySource(tc.title, tc.body); got != tc.want {
			t.Errorf("%+v: %t", tc, got)
		}
	}
}
func TestSupportedChildSceneIsNotBroadened(t *testing.T) {
	for _, tc := range []struct {
		old, parent, body string
		want              bool
	}{
		{"Riemerling", "Hohenbrunn", "Am Morgen fuhr ein Mann auf der Teststraße in Riemerling.", true},
		{"Neuhadern", "Hadern", "Am Abend fuhr eine Person mit Wohnsitz in München die Teststraße in Neuhadern stadtauswärts.", true},
		{"Neuhausen", "Neuhausen-Nymphenburg", "Auf einer Baustelle in Neuhausen kam es zum Unfall.", true},
		{"Neuhadern", "Hadern", "Der Unfall war in Hadern. Die Person wohnte in der Teststraße in Neuhadern.", false},
		{"Neuhadern", "Hadern", "Eine Person mit Wohnsitz in Neuhadern kam zur Polizei.", false},
		{"Neuhadern", "Hadern", "Bereits zuvor kam es in Neuhadern zu einem Unfall.", false},
		{"Neuhadern", "Hadern", "Ein Mann fuhr mit einer Frau mit Wohnsitz in der Teststraße in Neuhadern.", false},
		{"Neuhadern", "Hadern", "Der Unfall war auf der Teststraße.", false},
	} {
		p := Lookup(tc.parent)[0].Area
		if got := BroadensSupportedScene(Area{Name: tc.old}, p, tc.body); got != tc.want {
			t.Errorf("%+v: %t", tc, got)
		}
	}
}
func TestFestivalTopicDoesNotOfferScene(t *testing.T) {
	for _, tc := range []struct {
		title, body string
		want        bool
	}{
		{"Pressekonferenz zur Sicherheit auf dem Oktoberfest", "Sonderbeilage", false},
		{"Information", "Die Polizei berichtet zur Sicherheit auf dem Oktoberfest.", false},
		{"Vorfall – Wiesn", "", true},
		{"Vorfall", "Die Person befand sich auf dem Oktoberfest.", true},
		{"Vorfall", "Die Polizei begleitete ihn von da Wiesn.", true},
	} {
		cs := SourceCandidates(tc.title, tc.body, Source{})
		found := false
		for _, c := range cs {
			if c.Name == "Theresienwiese" {
				found = true
			}
		}
		if found != tc.want {
			t.Errorf("%+v: %+v", tc, cs)
		}
	}
}
