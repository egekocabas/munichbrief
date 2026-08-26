# Contributing to MunichBrief

Thank you for helping improve MunichBrief. Contributions should preserve the
project's privacy-first handling of police reports, clear source attribution,
and deterministic offline development experience.

## Development setup

Install Go 1.26.6 or newer and Node.js 24. Then run:

```bash
go test ./...
npm ci
npm run build
git diff --exit-code -- internal/web/static
```

The default fixture mode is offline and is the expected mode for routine
development. Live source and Ollama checks are explicitly opt-in; see
[the development guide](docs/development.md).

## Proposing a change

1. Open an issue for changes that alter behavior, persistence, source access,
   privacy rules, or deployment contracts.
2. Keep pull requests focused and include tests for changed behavior.
3. Run the complete validation commands documented in
   [the development guide](docs/development.md).
4. Update generated frontend assets whenever their source or dependencies
   change.

Commit messages and pull-request titles follow Conventional Commits, for
example `fix(parser): handle standalone release heading` or
`docs: clarify live-source policy`.

Do not commit real police article copies, databases, credentials, generated AI
responses, or personal deployment configuration. Parser fixtures must remain
handcrafted, minimal, and anonymized structural equivalents.

By contributing, you agree that your contribution is licensed under the MIT
License.
