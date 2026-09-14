#!/usr/bin/env bash
set -euo pipefail

if helm template munichbrief charts/munichbrief --set application.contactEnabled=true >/dev/null 2>&1; then
  echo "contact rendered without protected administration" >&2
  exit 1
fi
if helm template munichbrief charts/munichbrief --values deploy/example-values.yaml --set admin.enabled=true --set ingress.lan.enabled=true --set admin.basicAuthSecret=contact-test-auth --set application.contactEnabled=true --set-string application.existingSecret= >/dev/null 2>&1; then
  echo "contact rendered without a signing-secret reference" >&2
  exit 1
fi
helm template munichbrief charts/munichbrief --values deploy/example-values.yaml \
  --set admin.enabled=true --set ingress.lan.enabled=true --set admin.basicAuthSecret=contact-test-auth \
  --set application.contactEnabled=true --set application.existingSecret=contact-test-secret \
  --set application.contactDailyLimit=20 --set application.contactMonthlyLimit=300 >/dev/null

# ConfigMap environment belongs in data, never Kubernetes ObjectMeta.
rendered=$(helm template munichbrief charts/munichbrief --show-only templates/configmap.yaml)
if printf '%s\n' "$rendered" | awk '/^metadata:/{meta=1} /^data:/{meta=0} meta && /MUNICHBRIEF_/{bad=1} END {exit !bad}'; then
  echo "contact environment values leaked into ConfigMap metadata" >&2
  exit 1
fi
