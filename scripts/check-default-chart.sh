#!/usr/bin/env bash

set -euo pipefail

rendered_chart="${1:?rendered chart path is required}"

required_patterns=(
  'MUNICHBRIEF_SOURCE_MODE: "fixture"'
  'MUNICHBRIEF_PRESENTATION_MODE: "review"'
  'MUNICHBRIEF_ADMIN_ENABLED: "false"'
  'MUNICHBRIEF_AI_ENABLED: "false"'
  'MUNICHBRIEF_OLLAMA_BASE_URL: "http://127.0.0.1:11434"'
)

for pattern in "${required_patterns[@]}"; do
  grep -F -- "$pattern" "$rendered_chart" >/dev/null || {
    printf 'Default chart is missing safe setting: %s\n' "$pattern" >&2
    exit 1
  }
done

for forbidden_pattern in \
  'kind: Ingress' \
  'path: /admin' \
  'path: /api/admin' \
  'port: 11434'; do
  if grep -F -- "$forbidden_pattern" "$rendered_chart" >/dev/null; then
    printf 'Default chart unexpectedly exposes an external/admin dependency: %s\n' "$forbidden_pattern" >&2
    exit 1
  fi
done
