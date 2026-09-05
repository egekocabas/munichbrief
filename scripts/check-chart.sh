#!/usr/bin/env bash

set -euo pipefail

rendered_chart="${1:?rendered chart path is required}"

required_patterns=(
  'replicas: 1'
  'type: Recreate'
  'runAsNonRoot: true'
  'readOnlyRootFilesystem: true'
  'allowPrivilegeEscalation: false'
  'drop:'
  'mountPath: /data'
  'mountPath: /tmp'
  'accessModes:'
  'ReadWriteOnce'
  'path: /healthz'
  'path: /readyz'
  'path: /ai-disclosure/acknowledge'
  'path: /robots.txt'
  'path: /sitemap.xml'
  'path: /social'
  'path: /de'
  'path: /en'
  'kind: NetworkPolicy'
  'MUNICHBRIEF_PRESENTATION_MODE: "public"'
  'MUNICHBRIEF_PUBLIC_HOSTS: "brief.example.com"'
  'MUNICHBRIEF_CANONICAL_ORIGIN: "https://brief.example.com"'
  'MUNICHBRIEF_ADMIN_ENABLED: "false"'
  'MUNICHBRIEF_SECURE_COOKIES: "true"'
  'MUNICHBRIEF_AI_ENABLED: "false"'
  'MUNICHBRIEF_AI_IMMEDIATE: "false"'
  'MUNICHBRIEF_AI_WINDOW: "03:00-08:00"'
  'MUNICHBRIEF_AI_TIMEOUT: "15m"'
  'host: "brief.example.com"'
)

for pattern in "${required_patterns[@]}"; do
  grep -F -- "$pattern" "$rendered_chart" >/dev/null || {
    printf 'Rendered chart is missing required pattern: %s\n' "$pattern" >&2
    exit 1
  }
done

for forbidden_pattern in \
  '192.168.178.' \
  'egekocabas.com' \
  'path: /admin' \
  'path: /api/admin' \
  'port: 11434'; do
  if grep -F -- "$forbidden_pattern" "$rendered_chart" >/dev/null; then
    printf 'Rendered example contains forbidden private/admin pattern: %s\n' "$forbidden_pattern" >&2
    exit 1
  fi
done

public_root_count="$(awk 'NF >= 2 && $(NF - 1) == "path:" && $NF == "/" { getline; if ($1 == "pathType:" && $2 == "Exact") count++ } END { print count + 0 }' "$rendered_chart")"
[[ "$public_root_count" -ge 1 ]] || {
  printf 'Rendered chart does not constrain the public root path to Exact\n' >&2
  exit 1
}

disclosure_acknowledgement_count="$(awk 'NF >= 2 && $(NF - 1) == "path:" && $NF == "/ai-disclosure/acknowledge" { getline; if ($1 == "pathType:" && $2 == "Exact") count++ } END { print count + 0 }' "$rendered_chart")"
[[ "$disclosure_acknowledgement_count" -ge 1 ]] || {
  printf 'Rendered chart does not expose the AI disclosure acknowledgement as an Exact path\n' >&2
  exit 1
}

if grep -F -- 'kind: PodDisruptionBudget' "$rendered_chart" >/dev/null; then
  printf 'Single-replica SQLite deployment must not render a PodDisruptionBudget\n' >&2
  exit 1
fi
