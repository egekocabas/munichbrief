# Repository checks

Scripts in this directory validate deployment renders and repository
documentation. They are designed to run both locally and in CI:

- `check-default-chart.sh` asserts that chart defaults cannot enable live
  ingestion, AI, ingress, or a home-network dependency.
- `check-chart.sh` checks the opt-in public example render for required security
  and routing properties.
- `check-docs.mjs` verifies local file and heading links in repository Markdown.
- `check-tracked-files.mjs` rejects tracked files covered by Git ignore rules,
  including force-added credentials, databases, and backups. When
  `REPOSITORY_SAFETY_BASE` contains the PR/push base SHA, it also checks incoming
  commit paths so deleting a prohibited file later does not hide it.
- `node --test scripts/check-tracked-files.test.mjs` checks the file guard with
  synthetic files in isolated temporary Git repositories.

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
