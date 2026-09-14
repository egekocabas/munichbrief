package gazetteer

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

const (
	// sourceContractVersion invalidates conditional HTTP validators whenever parsing,
	// filtering, or source-entry semantics change. Bump it with those changes so a
	// 304 cannot silently reuse entries produced by an older contract.
	sourceContractVersion = "2026-09-02-2"
	munichStreetURL       = "https://geoportal.muenchen.de/geoserver/gsm_wfs/ows?service=WFS&version=1.0.0&request=GetFeature&typeName=gsm_wfs:erlaeuterung_strassennamen&outputFormat=application/json"
	munichDistrictURL     = "https://geoportal.muenchen.de/geoserver/gsm_wfs/ows?service=WFS&version=1.0.0&request=GetFeature&typeName=gsm_wfs:vablock_stadtbezirk&outputFormat=application/json"
	geoNamesURL           = "https://download.geonames.org/export/dump/DE.zip"
	overpassURL           = "https://overpass-api.de/api/interpreter?data="
)

func DefaultSources() []SourceDefinition {
	const munichAttribution = "Landeshauptstadt München – GeodatenService"
	return []SourceDefinition{
		{Key: "munich_streets", DisplayName: "Munich official street names", URL: munichStreetURL, License: "dl-de/by-2.0", Attribution: munichAttribution, MinimumRows: 5000, MaximumRows: 10000, MaximumSize: 10 << 20, Parse: parseMunichStreets},
		{Key: "munich_districts", DisplayName: "Munich official districts", URL: munichDistrictURL, License: "dl-de/by-2.0", Attribution: munichAttribution, MinimumRows: 25, MaximumRows: 40, MaximumSize: 5 << 20, Parse: parseMunichDistricts},
		{Key: "geonames_munich", DisplayName: "GeoNames Munich places", URL: geoNamesURL, License: "CC BY 4.0", Attribution: "GeoNames", MinimumRows: 150, MaximumRows: 500, MaximumSize: 20 << 20, Parse: parseGeoNames},
		osmSource("osm_places", "OpenStreetMap Munich places", `[out:json][timeout:90];area[boundary=administrative]["de:amtlicher_gemeindeschluessel"~"^(09162000|09184)"]->.a;(nwr(area.a)[place~"^(city|town|village|suburb|quarter|neighbourhood|hamlet|locality|square)$"][name];);out tags;`, 100, 10000),
		osmSource("osm_transit", "OpenStreetMap Munich transit", `[out:json][timeout:90];area[boundary=administrative]["de:amtlicher_gemeindeschluessel"~"^(09162000|09184)"]->.a;(nwr(area.a)[railway~"^(station|halt|tram_stop|subway_entrance)$"][name];nwr(area.a)[public_transport~"^(station|stop_position|platform)$"][name];);out tags;`, 100, 20000),
		osmSource("osm_landmarks", "OpenStreetMap Munich landmarks", `[out:json][timeout:90];area[boundary=administrative]["de:amtlicher_gemeindeschluessel"~"^(09162000|09184)"]->.a;(nwr(area.a)[leisure~"^(park|garden|stadium)$"][name];nwr(area.a)[tourism~"^(attraction|museum|zoo)$"][name];nwr(area.a)[historic~"^(castle|monument|memorial)$"][name];nwr(area.a)[amenity=marketplace][name];nwr(area.a)[aeroway=aerodrome][name];nwr(area.a)[natural~"^(wood|heath|scrub)$"][name];nwr(area.a)[landuse~"^(forest|recreation_ground)$"][name];nwr(area.a)[boundary~"^(protected_area|national_park)$"][name];);out tags;`, 50, 20000),
		osmSource("osm_roads", "OpenStreetMap Munich roads", `[out:json][timeout:120];area[boundary=administrative]["de:amtlicher_gemeindeschluessel"~"^(09162000|09184)"]->.a;(way(area.a)[highway][name];);out tags;`, 5000, 150000),
	}
}

func osmSource(key, name, query string, minimum, maximum int) SourceDefinition {
	return SourceDefinition{Key: key, DisplayName: name, URL: overpassURL + url.QueryEscape(query), License: "ODbL 1.0", Attribution: "© OpenStreetMap contributors", MinimumRows: minimum, MaximumRows: maximum, MaximumSize: 48 << 20, Parse: func(data []byte) ([]Entry, error) { return parseOSM(key, data) }}
}

type Fetcher struct {
	client    *http.Client
	userAgent string
	store     *Store
	clock     func() time.Time
}

func NewFetcher(client *http.Client, userAgent string, store *Store) (*Fetcher, error) {
	if client == nil || store == nil || strings.TrimSpace(userAgent) == "" {
		return nil, errors.New("gazetteer fetcher requires client, user agent, and store")
	}
	return &Fetcher{client: client, userAgent: userAgent, store: store, clock: time.Now}, nil
}

func (f *Fetcher) Fetch(ctx context.Context, definition SourceDefinition) (SourceSnapshot, bool, error) {
	snapshot, notModified, _, err := f.FetchWithDiagnostics(ctx, definition)
	return snapshot, notModified, err
}

func (f *Fetcher) FetchWithDiagnostics(ctx context.Context, definition SourceDefinition) (snapshot SourceSnapshot, notModified bool, diagnostic SourceFetchDiagnostic, err error) {
	started := f.clock()
	defer func() { diagnostic.Duration = f.clock().Sub(started) }()
	if err := validateSourceDefinition(definition); err != nil {
		diagnostic.FailureStage = "validation"
		return SourceSnapshot{}, false, diagnostic, err
	}
	etag, modified, oldHash, oldURL, contractVersion, err := f.store.SourceValidators(ctx, definition.Key)
	if err != nil {
		diagnostic.FailureStage = "storage"
		return SourceSnapshot{}, false, diagnostic, err
	}
	if oldURL != definition.URL || contractVersion != sourceContractVersion {
		etag, modified, oldHash = "", "", ""
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, definition.URL, nil)
	if err != nil {
		diagnostic.FailureStage = "download"
		return SourceSnapshot{}, false, diagnostic, err
	}
	request.Header.Set("User-Agent", f.userAgent)
	request.Header.Set("Accept", "application/json, application/zip, text/plain;q=0.8")
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	if modified != "" {
		request.Header.Set("If-Modified-Since", modified)
	}
	response, err := f.client.Do(request)
	if err != nil {
		diagnostic.FailureStage = "download"
		return SourceSnapshot{}, false, diagnostic, err
	}
	defer response.Body.Close()
	diagnostic.HTTPStatus = response.StatusCode
	if response.Request != nil && response.Request.URL.Scheme != "https" {
		diagnostic.FailureStage = "redirect"
		return SourceSnapshot{}, false, diagnostic, fmt.Errorf("fetch %s: redirected to non-HTTPS URL", definition.Key)
	}
	if response.StatusCode == http.StatusNotModified {
		entries, err := f.store.ActiveSourceEntries(ctx, definition.Key)
		if err != nil || len(entries) == 0 || oldHash == "" {
			diagnostic.FailureStage = "storage"
			return SourceSnapshot{}, false, diagnostic, fmt.Errorf("reuse unchanged %s: no active source snapshot", definition.Key)
		}
		if len(entries) < definition.MinimumRows || len(entries) > definition.MaximumRows {
			diagnostic.RowCount = len(entries)
			diagnostic.FailureStage = "row_count"
			return SourceSnapshot{}, false, diagnostic, fmt.Errorf("reuse unchanged %s: %d rows outside safety range %d..%d", definition.Key, len(entries), definition.MinimumRows, definition.MaximumRows)
		}
		diagnostic.RowCount = len(entries)
		return SourceSnapshot{Definition: definition, ContentHash: oldHash, ETag: etag, LastModified: modified, FetchedAt: f.clock(), Entries: entries}, true, diagnostic, nil
	}
	if response.StatusCode != http.StatusOK {
		diagnostic.FailureStage = "http_status"
		return SourceSnapshot{}, false, diagnostic, &sourceHTTPError{key: definition.Key, status: response.StatusCode, retryAt: retryAfterTime(response.Header.Get("Retry-After"), f.clock())}
	}
	limited := io.LimitReader(response.Body, definition.MaximumSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		diagnostic.FailureStage = "download"
		return SourceSnapshot{}, false, diagnostic, err
	}
	diagnostic.ResponseSize = int64(len(data))
	if int64(len(data)) > definition.MaximumSize {
		diagnostic.FailureStage = "response_size"
		return SourceSnapshot{}, false, diagnostic, fmt.Errorf("fetch %s: response exceeds %d bytes", definition.Key, definition.MaximumSize)
	}
	entries, err := definition.Parse(data)
	if err != nil {
		diagnostic.FailureStage = "parse"
		return SourceSnapshot{}, false, diagnostic, fmt.Errorf("parse %s: %w", definition.Key, err)
	}
	diagnostic.RowCount = len(entries)
	if len(entries) < definition.MinimumRows || len(entries) > definition.MaximumRows {
		diagnostic.FailureStage = "row_count"
		return SourceSnapshot{}, false, diagnostic, fmt.Errorf("parse %s: %d rows outside safety range %d..%d", definition.Key, len(entries), definition.MinimumRows, definition.MaximumRows)
	}
	hash := sha256.Sum256(data)
	return SourceSnapshot{Definition: definition, ContentHash: hex.EncodeToString(hash[:]), ETag: response.Header.Get("ETag"), LastModified: response.Header.Get("Last-Modified"), FetchedAt: f.clock(), Entries: entries}, false, diagnostic, nil
}

func validateSourceDefinition(definition SourceDefinition) error {
	parsed, err := url.Parse(definition.URL)
	if strings.TrimSpace(definition.Key) == "" || err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || definition.Parse == nil || definition.MinimumRows < 1 || definition.MaximumRows < definition.MinimumRows || definition.MaximumSize < 1 {
		return fmt.Errorf("invalid gazetteer source definition %q", definition.Key)
	}
	return nil
}

func parseMunichStreets(data []byte) ([]Entry, error) {
	return parseFeatureProperties(data, func(properties map[string]any, id string) []Entry {
		return entriesForNames("munich_streets", id, KindStreet, 10, properties, "strassenname")
	})
}

func parseMunichDistricts(data []byte) ([]Entry, error) {
	return parseFeatureProperties(data, func(properties map[string]any, id string) []Entry {
		return entriesForNames("munich_districts", id, KindDistrict, 20, properties, "sb_name")
	})
}

func parseFeatureProperties(data []byte, convert func(map[string]any, string) []Entry) ([]Entry, error) {
	var collection struct {
		Features []struct {
			ID         any            `json:"id"`
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&collection); err != nil {
		return nil, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	var entries []Entry
	for index, feature := range collection.Features {
		id := fmt.Sprint(feature.ID)
		if id == "<nil>" || id == "" {
			id = strconv.Itoa(index)
		}
		entries = append(entries, convert(feature.Properties, id)...)
	}
	return entries, nil
}

func entriesForNames(source, id, kind string, priority int, properties map[string]any, keys ...string) []Entry {
	seen := make(map[string]struct{})
	var result []Entry
	for _, key := range keys {
		value, found := properties[key]
		if !found || value == nil {
			continue
		}
		for _, raw := range strings.Split(fmt.Sprint(value), ";") {
			name := norm.NFC.String(strings.TrimSpace(raw))
			if !validName(name) {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			result = append(result, Entry{Name: name, Kind: kind, Priority: priority, Sources: []EntrySource{{Key: source, ExternalID: id, Kind: kind, Priority: priority}}})
		}
	}
	return result
}

func parseGeoNames(data []byte) ([]Entry, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var file *zip.File
	for _, candidate := range archive.File {
		if candidate.Name == "DE.txt" {
			file = candidate
			break
		}
	}
	if file == nil || file.UncompressedSize64 > 100<<20 {
		return nil, errors.New("GeoNames archive has no bounded DE.txt")
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	csvReader := csv.NewReader(bufio.NewReader(io.LimitReader(reader, 100<<20)))
	csvReader.Comma = '\t'
	csvReader.FieldsPerRecord = -1
	csvReader.LazyQuotes = true
	allowedCodes := map[string]bool{"09162": true, "09184": true}
	allowedTypes := map[string]bool{"PPLA": true, "PPLA4": true, "PPL": true, "PPLX": true}
	var entries []Entry
	for {
		record, err := csvReader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(record) < 19 || record[6] != "P" || !allowedTypes[record[7]] || !allowedCodes[record[12]] {
			continue
		}
		kind := KindNeighbourhood
		if record[7] == "PPLA" || record[7] == "PPLA4" || record[7] == "PPL" {
			kind = KindMunicipality
		}
		entries = append(entries, Entry{Name: norm.NFC.String(record[1]), Kind: kind, Priority: 30, Sources: []EntrySource{{Key: "geonames_munich", ExternalID: record[0], Kind: kind, Priority: 30}}})
	}
	return entries, nil
}

func parseOSM(source string, data []byte) ([]Entry, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("OSM response must be a JSON object")
	}
	var entries []Entry
	foundElements := false
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyToken.(string)
		if key != "elements" {
			var ignored json.RawMessage
			if err := decoder.Decode(&ignored); err != nil {
				return nil, err
			}
			continue
		}
		foundElements = true
		if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
			return nil, errors.New("OSM elements must be an array")
		}
		for decoder.More() {
			var element struct {
				Type string            `json:"type"`
				ID   int64             `json:"id"`
				Tags map[string]string `json:"tags"`
			}
			if err := decoder.Decode(&element); err != nil {
				return nil, err
			}
			kind := osmKind(source, element.Tags)
			id := element.Type + "/" + strconv.FormatInt(element.ID, 10)
			entries = append(entries, entriesForNames(source, id, kind, 40, stringMapAny(element.Tags), "official_name", "name", "name:de", "short_name", "alt_name")...)
		}
		if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
			if err == nil {
				err = errors.New("OSM elements must end with an array delimiter")
			}
			return nil, err
		}
	}
	if !foundElements {
		return nil, errors.New("OSM response has no elements array")
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		if err == nil {
			err = errors.New("OSM response must end with an object delimiter")
		}
		return nil, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	return entries, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("JSON response has trailing data: %w", err)
	}
	return errors.New("JSON response contains more than one value")
}

func osmKind(source string, tags map[string]string) string {
	switch source {
	case "osm_roads":
		return KindStreet
	case "osm_transit":
		return KindTransit
	case "osm_places":
		if tags["place"] == "city" || tags["place"] == "town" || tags["place"] == "village" {
			return KindMunicipality
		}
		if tags["place"] == "square" {
			return KindSquare
		}
		return KindNeighbourhood
	default:
		if tags["leisure"] == "park" || tags["leisure"] == "garden" {
			return KindPark
		}
		return KindLandmark
	}
}

func stringMapAny(values map[string]string) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

type sourceHTTPError struct {
	key     string
	status  int
	retryAt time.Time
}

func (e *sourceHTTPError) Error() string { return fmt.Sprintf("fetch %s: HTTP %d", e.key, e.status) }

// Ignore malformed/past values and bound an upstream delay to seven days.
func retryAfterTime(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return time.Time{}
		}
		return now.Add(time.Duration(min(seconds, int64(7*24*60*60))) * time.Second)
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		if at.After(now.Add(7 * 24 * time.Hour)) {
			return now.Add(7 * 24 * time.Hour)
		}
		return at
	}
	return time.Time{}
}
