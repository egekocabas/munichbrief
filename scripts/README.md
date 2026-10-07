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

## Frontend dependency updates

`package.json` uses a scoped [npm override](https://docs.npmjs.com/cli/v11/configuring-npm/package-json/#overrides)
for Tailwind CLI's `@parcel/watcher`: version 2.6.0 removes the vulnerable
`micromatch` / `braces` dependency chain. Tailwind CLI 4.3.3 pins watcher 2.5.1;
remove the override when an updated CLI resolves a safe watcher without it.

Use npm commands to regenerate `package-lock.json`; do not edit it manually.
Verify dependency changes with `npm ci`, `npm audit --audit-level=high`,
`npm run build`, and `git diff --exit-code -- internal/web/static`.
For watcher updates, also check that `npm run watch:css` rebuilds after a template
change. Review dependency terms and refresh the licence inventory as described
above.
