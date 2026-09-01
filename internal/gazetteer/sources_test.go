package gazetteer

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOfficialAndOSMParsersUseExpectedFields(t *testing.T) {
	streets, err := parseMunichStreets([]byte(`{"features":[{"id":"street.1","properties":{"strassenname":"Ingolstädter Straße"}}]}`))
	if err != nil || len(streets) != 1 || streets[0].Name != "Ingolstädter Straße" || streets[0].Kind != KindStreet {
		t.Fatalf("streets = %#v, err=%v", streets, err)
	}
	districts, err := parseMunichDistricts([]byte(`{"features":[{"id":"district.1","properties":{"sb_name":"Ramersdorf-Perlach"}}]}`))
	if err != nil || len(districts) != 1 || districts[0].Kind != KindDistrict {
		t.Fatalf("districts = %#v, err=%v", districts, err)
	}
	osm, err := parseOSM("osm_transit", []byte(`{"elements":[{"type":"node","id":42,"tags":{"name":"München Hauptbahnhof","name:en":"Munich Central Station","alt_name":"Hauptbahnhof"}}]}`))
	if err != nil || len(osm) != 2 || osm[0].Kind != KindTransit {
		t.Fatalf("OSM = %#v, err=%v", osm, err)
	}
}

func TestGeoNamesParserFiltersAdministrativeCodesAndFeatureTypes(t *testing.T) {
	line := func(id, name, feature, admin3 string) string {
		fields := []string{id, name, name, "", "0", "0", "P", feature, "DE", "", "02", "091", admin3, "", "0", "0", "0", "Europe/Berlin", "2026-01-01"}
		return strings.Join(fields, "\t") + "\n"
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, _ := writer.Create("DE.txt")
	_, _ = file.Write([]byte(line("1", "Oberhaching", "PPLA4", "09184") + line("2", "Geiselgasteig", "PPL", "09184") + line("3", "Berlin", "PPLC", "11000")))
	_ = writer.Close()
	entries, err := parseGeoNames(archive.Bytes())
	if err != nil || len(entries) != 2 || entries[0].Name != "Oberhaching" || entries[0].Sources[0].Kind != KindMunicipality || entries[1].Name != "Geiselgasteig" {
		t.Fatalf("GeoNames = %#v, err=%v", entries, err)
	}
}

func TestFetcherConditionalRequestAndSizeLimit(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Header.Get("If-None-Match") == `"one"` {
			return &http.Response{StatusCode: http.StatusNotModified, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Etag": []string{`"one"`}}, Body: io.NopCloser(strings.NewReader(`{"features":[{"id":"1","properties":{"strassenname":"Ganghoferstraße"}}]}`))}, nil
	})}
	definition := SourceDefinition{Key: "munich_streets", DisplayName: "Test", URL: "https://example.test/source", License: "test", Attribution: "test", MinimumRows: 1, MaximumRows: 2, MaximumSize: 1024, Parse: parseMunichStreets}
	fetcher, _ := NewFetcher(client, "MunichBrief/test", store)
	snapshot, unchanged, err := fetcher.Fetch(ctx, definition)
	if err != nil || unchanged {
		t.Fatalf("first fetch unchanged=%v err=%v", unchanged, err)
	}
	entries := mergeEntries([]SourceSnapshot{snapshot}, nil)
	if _, _, err := store.Activate(ctx, []SourceSnapshot{snapshot}, entries, "hash-one", time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	second, unchanged, err := fetcher.Fetch(ctx, definition)
	if err != nil || !unchanged || len(second.Entries) != 1 || second.Entries[0].Kind != KindStreet || second.Entries[0].Priority != 10 || requests != 2 {
		t.Fatalf("conditional fetch = %#v unchanged=%v requests=%d err=%v", second, unchanged, requests, err)
	}
}

func TestFetcherRejectsOversizedAndImplausibleResponses(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("12345"))}, nil
	})}
	fetcher, _ := NewFetcher(client, "MunichBrief/test", store)
	definition := SourceDefinition{Key: "bounded", DisplayName: "Bounded", URL: "https://example.test/source", License: "test", Attribution: "test", MinimumRows: 1, MaximumRows: 2, MaximumSize: 4, Parse: func([]byte) ([]Entry, error) { return nil, nil }}
	if _, _, err := fetcher.Fetch(ctx, definition); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized fetch error = %v", err)
	}
	client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	definition.MaximumSize = 10
	if _, _, err := fetcher.Fetch(ctx, definition); err == nil || !strings.Contains(err.Error(), "outside safety range") {
		t.Fatalf("implausible row count error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
