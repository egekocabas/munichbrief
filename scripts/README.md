# Repository checks

Scripts in this directory validate deployment renders and repository
documentation. They are designed to run both locally and in CI:

- `check-default-chart.sh` asserts that chart defaults cannot enable live
  ingestion, AI, ingress, or a external model endpoint.
- `check-chart.sh` checks the opt-in public example render for required security
  and routing properties.
- `check-docs.mjs` verifies local file and heading links in repository Markdown.
- `generate-munich-map.py --check` verifies the bundled district SVG paths against
  the committed official source snapshot. It uses Python's standard library;
  see [boundary provenance](../docs/munich-map-boundaries.md) before refreshing.
- `go run ./cmd/check-translations` checks UI catalog completeness, source usage,
  message structure, and template placeholders. Its focused tests run with
  `go test ./internal/translationcheck`; see [UI translations](../docs/ui-translations.md)
  for the validation order and how to update messages.

Keep checks deterministic and free of credentials. A failed assertion should
name the file or rendered property that needs attention.

Licence checks:

- `licenses.mjs --check` verifies the reviewed inventory, original text hashes,
  generated notice bundle and linked dependencies for both Linux architectures.
  It is offline; install dependencies and the Go toolchain first.
- `node --test scripts/licenses.test.mjs` exercises rejection of stale or
  incomplete inventories in isolated temporary fixtures.
- `check-license-image.py IMAGE --arch amd64` (or `arm64`) inspects a locally built
  release image and runs its offline licence command with networking disabled.
  Docker must support execution of that architecture; CI uses native runners.

See [the licence review](../docs/licensing-review.md) for the review/update process.
