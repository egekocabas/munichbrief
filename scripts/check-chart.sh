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
  'kind: NetworkPolicy'
)

for pattern in "${required_patterns[@]}"; do
  grep -F -- "$pattern" "$rendered_chart" >/dev/null || {
    printf 'Rendered chart is missing required pattern: %s\n' "$pattern" >&2
    exit 1
  }
done

if grep -F -- 'kind: PodDisruptionBudget' "$rendered_chart" >/dev/null; then
  printf 'Single-replica SQLite deployment must not render a PodDisruptionBudget\n' >&2
  exit 1
fi
