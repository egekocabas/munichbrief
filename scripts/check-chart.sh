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
  'path: /robots.txt'
  'path: /sitemap.xml'
  'kind: NetworkPolicy'
  'MUNICHBRIEF_PRESENTATION_MODE: "public"'
  'MUNICHBRIEF_PUBLIC_HOSTS: "munichbrief.egekocabas.com,munichbrief.de"'
  'MUNICHBRIEF_CANONICAL_ORIGIN: "https://munichbrief.de"'
  'MUNICHBRIEF_ADMIN_ENABLED: "true"'
  'MUNICHBRIEF_SECURE_COOKIES: "true"'
  'MUNICHBRIEF_AI_ENABLED: "true"'
  'MUNICHBRIEF_AI_IMMEDIATE: "false"'
  'MUNICHBRIEF_AI_WINDOW: "03:00-08:00"'
  'MUNICHBRIEF_AI_TIMEOUT: "10m"'
  'cidr: 192.168.178.102/32'
  'port: 11434'
  'name: munichbrief-lan-admin'
  'traefik.ingress.kubernetes.io/router.priority: "100"'
  'path: /admin'
  'path: /api/admin'
  'name: munichbrief-admin-auth'
  'secret: munichbrief-admin-basic-auth'
  'host: "munichbrief.egekocabas.com"'
  'host: "munichbrief.de"'
)

for pattern in "${required_patterns[@]}"; do
  grep -F -- "$pattern" "$rendered_chart" >/dev/null || {
    printf 'Rendered chart is missing required pattern: %s\n' "$pattern" >&2
    exit 1
  }
done

public_root_count="$(awk 'NF >= 2 && $(NF - 1) == "path:" && $NF == "/" { getline; if ($1 == "pathType:" && $2 == "Exact") count++ } END { print count + 0 }' "$rendered_chart")"
[[ "$public_root_count" -ge 2 ]] || {
  printf 'Rendered chart does not constrain the public root path to Exact\n' >&2
  exit 1
}

if grep -F -- 'kind: PodDisruptionBudget' "$rendered_chart" >/dev/null; then
  printf 'Single-replica SQLite deployment must not render a PodDisruptionBudget\n' >&2
  exit 1
fi
