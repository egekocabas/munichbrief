# MunichBrief

[![CI](https://github.com/egekocabas/munichbrief/actions/workflows/ci.yml/badge.svg)](https://github.com/egekocabas/munichbrief/actions/workflows/ci.yml)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Official Munich Police reports, made easier to read. MunichBrief splits daily
releases into individual incidents, creates shorter German summaries, and
translates them into 13 other languages with links back to the source.

**[Visit MunichBrief →](https://munichbrief.de)**

> MunichBrief is an independent project. It is not affiliated with the Bavarian
> Police or any other public authority and does not speak on their behalf.
>
> Summaries and translations are AI-generated; the original police release
> remains authoritative. The presumption of innocence applies.

## What it does

- **Follow the reports:** search, filter, and browse by publication or incident date.
- **Choose a language:** each translation publishes independently when ready.
- **Keep the context:** source links, incident timing, and AI provenance travel
  with each report.

## How it works

An RSS synchronizer fetches official releases. A parser separates incidents,
and an Ollama pipeline extracts metadata, produces German summaries, and runs
independent verification and translation jobs. Place names are protected before
translation and restored afterward.

| Guide | What it explains |
| --- | --- |
| [Architecture](docs/architecture.md) | Follow a report through the system |
| [Translation](docs/translation.md) | Languages, model adapters, and place names |
| [Privacy and sources](docs/source-policy.md) | Publication rules, disclosure, and retained data |
| [Running MunichBrief](docs/operations.md) | Configuration, Helm deployment, and storage |
| [Licences and credits](docs/licensing-review.md) | Dependencies, assets, and model provenance |

## Reports and support

External code contributions and pull requests are not accepted. Reproducible
bug reports and private security reports are welcome; see
[CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).
Use the [contact page](https://munichbrief.de/en/contact) for operational issues
or content corrections.

## License

Project code is [MIT licensed](LICENSE). Third-party software, fonts, data, and
models retain their own terms; see [third-party notices](THIRD_PARTY_NOTICES.md).
