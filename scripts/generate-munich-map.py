#!/usr/bin/env python3
"""Generate the bundled district SVG paths, offline, using Python's stdlib.

Shared boundary chains are simplified once and reused in either direction.
Coordinates stay in the source's metric EPSG:25832 projection; this is a map
illustration, not a geocoder. See docs/munich-map-boundaries.md for provenance.
"""

import argparse
import gzip
import hashlib
import heapq
import json
import math
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "internal/munichmap/source/stadtbezirke.geojson.gz"
OUTPUT = ROOT / "internal/munichmap/districts.json"
WIDTH, HEIGHT, PADDING = 760, 700, 28
TOLERANCE_METRES = 10


def segment_distance_sq(point, start, end):
    dx, dy = end[0] - start[0], end[1] - start[1]
    t = ((point[0] - start[0]) * dx + (point[1] - start[1]) * dy) / (dx * dx + dy * dy) if dx or dy else 0
    t = max(0, min(1, t))
    return (point[0] - start[0] - t * dx) ** 2 + (point[1] - start[1] - t * dy) ** 2


def simplify(points):
    """Iterative Douglas–Peucker with a stable direction for every chain."""
    keep = {0, len(points) - 1}
    work = [(0, len(points) - 1)]
    while work:
        first, last = work.pop()
        distance, index = 0, first
        for i in range(first + 1, last):
            candidate = segment_distance_sq(points[i], points[first], points[last])
            if candidate > distance:
                distance, index = candidate, i
        if distance > TOLERANCE_METRES ** 2:
            keep.add(index)
            work.extend(((first, index), (index, last)))
    return tuple(points[i] for i in sorted(keep))


def simplify_rings(rings):
    """Split the planar edge graph at junctions, preserving shared borders."""
    graph = {}
    for ring in rings:
        for a, b in zip(ring, ring[1:]):
            graph.setdefault(a, set()).add(b)
            graph.setdefault(b, set()).add(a)
    junctions = {point for point, neighbors in graph.items() if len(neighbors) != 2}
    chains = {}
    result = []
    for ring in rings:
        points = ring[:-1]
        splits = [i for i, point in enumerate(points) if point in junctions]
        if len(splits) < 2:
            # A disconnected island or a ring touching only one junction still
            # needs two fixed endpoints; never simplify a closed chain to zero.
            anchor = splits[0] if splits else min(range(len(points)), key=lambda i: points[i])
            opposite = max(range(len(points)), key=lambda i: math.dist(points[anchor], points[i]))
            splits = sorted({anchor, opposite})
        new_ring = []
        for first, last in zip(splits, splits[1:] + [splits[0]]):
            chain = tuple(points[first:last + 1] if last > first else points[first:] + points[:last + 1])
            reverse = chain[0] > chain[-1]
            key = chain[::-1] if reverse else chain
            if key not in chains:
                chains[key] = simplify(key)
            simplified = chains[key][::-1] if reverse else chains[key]
            new_ring.extend(simplified[:-1])
        if len(new_ring) < 3:
            raise ValueError("Simplification collapsed a polygon")
        result.append(new_ring + [new_ring[0]])
    return result


def signed_distance(point, polygon):
    inside, nearest = False, float("inf")
    x, y = point
    for ring in polygon:
        for a, b in zip(ring, ring[1:]):
            if (a[1] > y) != (b[1] > y) and x < (b[0] - a[0]) * (y - a[1]) / (b[1] - a[1]) + a[0]:
                inside = not inside
            nearest = min(nearest, segment_distance_sq(point, a, b))
    return math.sqrt(nearest) * (1 if inside else -1)


def label_position(polygon):
    """Find an interior point with room for a label, to half a viewBox unit."""
    outer = polygon[0]
    min_x, max_x = min(p[0] for p in outer), max(p[0] for p in outer)
    min_y, max_y = min(p[1] for p in outer), max(p[1] for p in outer)
    half = max(max_x - min_x, max_y - min_y) / 2
    work = []

    def push(x, y, radius):
        distance = signed_distance((x, y), polygon)
        heapq.heappush(work, (-(distance + radius * math.sqrt(2)), x, y, radius, distance))

    best = (outer[0][0], outer[0][1], 0)
    push((min_x + max_x) / 2, (min_y + max_y) / 2, half)
    while work:
        negative_bound, x, y, radius, distance = heapq.heappop(work)
        if distance > best[2]:
            best = (x, y, distance)
        if -negative_bound - best[2] <= 0.5:
            continue
        for dx in (-radius / 2, radius / 2):
            for dy in (-radius / 2, radius / 2):
                push(x + dx, y + dy, radius / 2)
    return best


def area(ring):
    return abs(sum(a[0] * b[1] - b[0] * a[1] for a, b in zip(ring, ring[1:]))) / 2


def generate(raw):
    source = json.loads(raw)
    if source.get("type") != "FeatureCollection" or source.get("crs", {}).get("properties", {}).get("name") != "urn:ogc:def:crs:EPSG::25832":
        raise ValueError("Expected the official EPSG:25832 FeatureCollection")
    grouped, rings = {}, []
    for feature in source["features"]:
        properties, geometry = feature["properties"], feature["geometry"]
        number, name = int(properties["sb_nummer"]), properties["sb_name"]
        if not 1 <= number <= 25 or not name:
            raise ValueError("Unexpected district identity")
        district = grouped.setdefault(number, {"name": name, "polygons": []})
        if district["name"] != name or geometry["type"] != "Polygon":
            raise ValueError("Unexpected district name or geometry type")
        indexes = []
        for coordinates in geometry["coordinates"]:
            ring = [tuple(point) for point in coordinates]
            if len(ring) < 4 or ring[0] != ring[-1] or any(len(p) != 2 or not all(math.isfinite(v) for v in p) for p in ring):
                raise ValueError("Invalid polygon ring")
            indexes.append(len(rings))
            rings.append(ring)
        district["polygons"].append(indexes)
    if set(grouped) != set(range(1, 26)):
        raise ValueError("Expected exactly 25 districts")
    min_x, max_x = min(p[0] for r in rings for p in r), max(p[0] for r in rings for p in r)
    min_y, max_y = min(p[1] for r in rings for p in r), max(p[1] for r in rings for p in r)
    scale = min((WIDTH - PADDING * 2) / (max_x - min_x), (HEIGHT - PADDING * 2) / (max_y - min_y))
    offset_x = (WIDTH - (max_x - min_x) * scale) / 2
    offset_y = (HEIGHT - (max_y - min_y) * scale) / 2
    projected = [[(round(offset_x + (x - min_x) * scale, 2), round(offset_y + (max_y - y) * scale, 2)) for x, y in ring] for ring in simplify_rings(rings)]
    districts = []
    for number, district in sorted(grouped.items()):
        polygons = [[projected[i] for i in indexes] for indexes in district["polygons"]]
        label_x, label_y, label_radius = label_position(max(polygons, key=lambda p: area(p[0])))
        path = "".join("M" + "L".join(f"{x:g},{y:g}" for x, y in ring[:-1]) + "Z" for polygon in polygons for ring in polygon)
        districts.append({"id": "munich:district:" + district["name"].lower(), "number": number, "name": district["name"], "path": path, "x": round(label_x, 2), "y": round(label_y, 2), "label_radius": round(label_radius, 2)})
    return (json.dumps({"source_sha256": hashlib.sha256(raw).hexdigest(), "source_crs": "EPSG:25832", "tolerance_metres": TOLERANCE_METRES, "view_box": f"0 0 {WIDTH} {HEIGHT}", "districts": districts}, ensure_ascii=False, indent=2) + "\n").encode()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, default=SOURCE)
    parser.add_argument("--output", type=Path, default=OUTPUT)
    parser.add_argument("--archive-source", action="store_true", help="Replace the bundled source snapshot after reviewing a fresh download")
    parser.add_argument("--check", action="store_true", help="Check that the committed asset matches the generator")
    args = parser.parse_args()
    if args.check and args.archive_source:
        parser.error("--check does not modify the archived source")
    raw = args.source.read_bytes()
    if raw[:2] == b"\x1f\x8b":
        raw = gzip.decompress(raw)
    generated = generate(raw)
    if args.check:
        if args.output.read_bytes() != generated:
            raise SystemExit("Bundled map is stale; regenerate it with scripts/generate-munich-map.py")
    else:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_bytes(generated)
        if args.archive_source:
            SOURCE.parent.mkdir(parents=True, exist_ok=True)
            SOURCE.write_bytes(gzip.compress(raw, mtime=0))
    print(f"Verified 25 Munich districts; SVG asset {len(generated):,} bytes")


if __name__ == "__main__":
    main()
