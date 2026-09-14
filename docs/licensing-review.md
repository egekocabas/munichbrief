# Licences and credits

MunichBrief code uses [MIT](../LICENSE). Dependencies, fonts, geographic data,
and separately installed models retain their own terms.

## What ships

| Material | Where to find its terms |
| --- | --- |
| Go dependencies and runtime, browser assets, fonts | [Third-party notices](../THIRD_PARTY_NOTICES.md) |
| Original licence texts and packaged source archives | [LICENSES](../LICENSES/) |
| Exact versions, hashes, asset owners, model provenance | [Reviewed inventory](../internal/licensing/manifest.json) |

Model weights and Ollama run separately and are not included in the application
image. Police releases are not covered by the project's MIT licence. Geographic
attribution appears on the reader's About page.

## Visible in the product

- `/en/licenses` serves public credits and the original licence texts.
- `munichbrief licenses` prints embedded notices without a database or network.
- Release images include the bundle under `/usr/share/munichbrief/`.
- Protected `/admin/licenses` compares cached model digests with reviewed entries.
  Warnings do not change processing settings.

A model tag can change. The inventory records exact reviewed artefacts, and
public references are not a live list of models selected by a deployment.

## How the inventory stays current

The [licence script](../scripts/licenses.mjs) verifies dependency coverage,
input hashes, original texts, and generated notices. CI checks both Linux
architectures and inspects the actual release images.

Changed dependencies or assets need a review of their upstream terms before
updating the inventory. Generated notices are reproducible from that inventory;
a green check verifies consistency, not every possible legal use.
