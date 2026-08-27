# Repository checks

Scripts in this directory validate deployment renders and repository
documentation. They are designed to run both locally and in CI:

- `check-default-chart.sh` asserts that chart defaults cannot enable live
  ingestion, AI, ingress, or a home-network dependency.
- `check-chart.sh` checks the opt-in public example render for required security
  and routing properties.
- `check-docs.mjs` verifies local file and heading links in repository Markdown.

Keep checks deterministic and free of credentials. A failed assertion should
name the file or rendered property that needs attention.
