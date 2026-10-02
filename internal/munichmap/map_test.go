package munichmap

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/egekocabas/munichbrief/internal/location"
)

func TestDistrictGeometry(t *testing.T) {
	allowed := regexp.MustCompile(`^(M[0-9]+(?:\.[0-9]+)?,[0-9]+(?:\.[0-9]+)?(?:L[0-9]+(?:\.[0-9]+)?,[0-9]+(?:\.[0-9]+)?){2,}Z)+$`)
	seen := make(map[string]bool)
	for i, district := range Districts() {
		if district.Number != i+1 || seen[district.ID] {
			t.Fatalf("invalid district ordering/identity: %+v", district)
		}
		seen[district.ID] = true
		matches := location.Lookup(district.Name)
		if len(matches) != 1 || matches[0].Kind != "district" || matches[0].ID != district.ID {
			t.Errorf("district %s does not match the geographic catalog", district.Name)
		}
		if !allowed.MatchString(district.Path) {
			t.Fatalf("district %s contains invalid SVG path data", district.Name)
		}
		inside := false
		for _, ring := range parseRings(district.Path) {
			if len(ring) < 3 {
				t.Fatal("collapsed district ring")
			}
			for j, point := range ring {
				if point[0] < 28 || point[0] > 732 || point[1] < 28 || point[1] > 672 {
					t.Errorf("%s has coordinate outside viewBox padding: %v", district.Name, point)
				}
				next := ring[(j+1)%len(ring)]
				if (point[1] > district.Y) != (next[1] > district.Y) && district.X < (next[0]-point[0])*(district.Y-point[1])/(next[1]-point[1])+point[0] {
					inside = !inside
				}
			}
		}
		if !inside || district.LabelRadius < 10 || math.IsNaN(district.X) || math.IsNaN(district.Y) {
			t.Errorf("%s label does not fit inside its polygon", district.Name)
		}
	}
	// The source has two districts with small separate polygons. They remain
	// present as additional subpaths rather than duplicate countable districts.
	for _, number := range []int{18, 19} {
		if strings.Count(districts[number-1].Path, "M") != 2 {
			t.Errorf("district %d lost its separate polygon", number)
		}
	}
}

func TestSharedBorders(t *testing.T) {
	type point [2]float64
	type edge [2]point
	edges := make(map[edge]int)
	for _, district := range Districts() {
		for _, ring := range parseRings(district.Path) {
			for i, p := range ring {
				a, b := point(p), point(ring[(i+1)%len(ring)])
				if a[0] > b[0] || (a[0] == b[0] && a[1] > b[1]) {
					a, b = b, a
				}
				edges[edge{a, b}]++
			}
		}
	}
	shared := 0
	for _, count := range edges {
		if count == 2 {
			shared++
		} else if count > 2 {
			t.Fatal("a district edge is shared by more than two polygons")
		}
	}
	if shared < 300 {
		t.Fatalf("expected substantial exact shared boundaries; got %d", shared)
	}
	// Simplification must not create self-crossings or let one district's
	// boundary cross another. Shared vertices and coincident edges are valid.
	unique := make([]edge, 0, len(edges))
	for e := range edges {
		unique = append(unique, e)
	}
	orientation := func(a, b, c point) float64 {
		return (b[0]-a[0])*(c[1]-a[1]) - (b[1]-a[1])*(c[0]-a[0])
	}
	for i, first := range unique {
		for _, second := range unique[i+1:] {
			a, b, c, d := first[0], first[1], second[0], second[1]
			if b[0] < c[0] || d[0] < a[0] || math.Max(a[1], b[1]) < math.Min(c[1], d[1]) || math.Max(c[1], d[1]) < math.Min(a[1], b[1]) {
				continue
			}
			if orientation(a, b, c)*orientation(a, b, d) < -1e-6 && orientation(c, d, a)*orientation(c, d, b) < -1e-6 {
				t.Fatalf("simplified district boundaries cross: %v and %v", first, second)
			}
		}
	}
}

func TestSourceSnapshotIntegrity(t *testing.T) {
	compressed, err := os.ReadFile("source/stadtbezirke.geojson.gz")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		SourceSHA256 string `json:"source_sha256"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != document.SourceSHA256 {
		t.Fatal("bundled geometry does not identify the committed official source snapshot")
	}
}

func TestDistrictsReturnsCopy(t *testing.T) {
	copy := Districts()
	copy[0].Name = "changed"
	if Districts()[0].Name == "changed" {
		t.Fatal("caller mutated global district data")
	}
}

func parseRings(path string) [][][2]float64 {
	var rings [][][2]float64
	for _, part := range strings.Split(strings.TrimSuffix(path, "Z"), "Z") {
		var ring [][2]float64
		for _, coordinate := range strings.Split(strings.TrimPrefix(part, "M"), "L") {
			components := strings.Split(coordinate, ",")
			x, _ := strconv.ParseFloat(components[0], 64)
			y, _ := strconv.ParseFloat(components[1], 64)
			ring = append(ring, [2]float64{x, y})
		}
		rings = append(rings, ring)
	}
	return rings
}
