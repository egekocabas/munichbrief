# Internal packages

The application is intentionally kept in one Go module and one deployable binary.
Packages under this directory separate responsibilities rather than deployment
units:

| Package | Responsibility |
| --- | --- |
| `config` | Environment parsing, defaults, and startup validation |
| `domain` | Source-neutral incident and document values |
| `source` | Fixture and network source clients |
| `parser` | Conversion of source HTML into domain values |
| `ingest` | Synchronization orchestration and refresh policy |
| `gazetteer` | Rebuildable Munich place-name sources, generations, matching, and typed placeholder restoration |
| `languages` | Immutable language identities, validation, negotiation, and date formatting |
| `processing` | Staged AI processing, validation, scheduling, and model access |
| `store` | SQLite schema, transactions, queries, and pipeline persistence |
| `web` | Public/review HTTP presentation and access boundaries |
| `observability` | Prometheus-compatible metrics and HTTP instrumentation |

The expected dependency direction is adapters toward domain and persistence:
`web`, `ingest`, and `processing` coordinate work; `source`, `parser`, and
`store` implement their external boundaries. Avoid importing `web` from other
internal packages or moving HTTP concerns into the store.

Tests live beside the behavior they protect. Cross-package tests are reserved for
boundaries that cannot be exercised through one package, such as live source
shape checks. All routine tests must stay offline and deterministic.

More detailed notes are available for the areas with non-obvious invariants:

- [processing](processing/README.md)
- [store](store/README.md)
- [web](web/README.md)
