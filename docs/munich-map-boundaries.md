# Munich district map boundaries

The public map displays the 25 official Munich city districts. It uses a bundled
snapshot of the city's [Stadtbezirke dataset](https://opendata.muenchen.de/dataset/vablock_stadtbezirke_opendata),
provided by Landeshauptstadt München – GeodatenService under
[Datenlizenz Deutschland – Namensnennung – Version 2.0](https://www.govdata.de/dl-de/by-2-0).
The source and licence are credited beside the map and in the generated
[third-party notices](../THIRD_PARTY_NOTICES.md). The visible credit identifies
the boundaries as simplified.

## Snapshot and transformations

The snapshot was downloaded on 2026-10-03 (Europe/Berlin). The source WFS response
uses EPSG:25832, a metric projection suitable for the Munich area. Its 27 polygon
features describe 25 districts: districts 18 and 19 each have one small separate
polygon. These pieces remain part of their district, rather than becoming
additional countable areas. The source snapshot is stored losslessly as
[compressed GeoJSON](../internal/munichmap/source/stadtbezirke.geojson.gz).

The [generator](../scripts/generate-munich-map.py) uses only Python's standard
library and works offline. It groups polygons by official district number,
splits boundary chains at their shared junctions, simplifies each chain once
with a maximum Douglas–Peucker tolerance of 10 metres, and reuses that same chain
for neighboring polygons. This prevents independently simplified borders from
creating gaps. All coordinates use one uniform scale and are centered inside a
760 by 700 SVG viewBox, with north at the top. Coordinates are rounded to 0.01
viewBox units. Interior label points are calculated on the largest polygon of
each district.

The generated [JSON asset](../internal/munichmap/districts.json) is embedded by
the [Go package](../internal/munichmap/map.go); the original snapshot is only
needed to regenerate and verify it. No external map scripts, tiles, fonts,
geocoding service, browser location permission, or remote map requests are
needed to display these boundaries.

These display paths do not determine where reports belong. Report assignment
uses the reviewed location catalog and the applicable location-verification
result described in [location verification](location-verification.md). Map
geometry is deliberately kept separate from exact addresses, street hints,
and private verifier evidence. Counts describe reports assigned to districts;
they do not establish a count of unique events or measure neighborhood safety.

## Reproduce or update

From the repository root:

```sh
python3 scripts/generate-munich-map.py --check
GOCACHE=/tmp/munichbrief-go-build go test ./internal/munichmap
```

To review a fresh source snapshot, download it to a temporary file first:

```sh
curl -fsSL 'https://geoportal.muenchen.de/geoserver/gsm_wfs/ows?service=WFS&version=1.0.0&request=GetFeature&typeName=gsm_wfs:vablock_stadtbezirk&outputFormat=application/json' -o /tmp/munich-districts.geojson
python3 scripts/generate-munich-map.py --source /tmp/munich-districts.geojson --archive-source
```

Review boundary and district-identity changes, inspect the rendered map at
desktop and mobile widths, update the snapshot date and licence inventory,
then run the checks above and the repository's licence checks. The generator
rejects an unexpected projection, missing district, inconsistent district name,
or malformed ring. The generated asset records the SHA-256 of the decompressed
source bytes, which the Go tests verify against the committed snapshot.
