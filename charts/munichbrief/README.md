# MunichBrief Helm chart

The chart installs one security-hardened MunichBrief replica with persistent
SQLite storage. Its defaults are intentionally offline and private: fixture
source, review presentation, AI disabled, and no ingress.

Public ingress renders one explicit Prefix path for every entry in
`ingress.public.languageCodes`. Keep that list synchronized with the compiled
application registry; see [Adding a reader language](../../docs/adding-a-language.md).

```bash
helm install munichbrief ./charts/munichbrief \
  --namespace munichbrief \
  --create-namespace
```

## Safety defaults

- Exactly one replica with a `Recreate` update strategy.
- `ReadWriteOnce` persistent storage retained when the release is removed.
- Non-root execution, dropped capabilities, runtime-default seccomp, and a
  read-only root filesystem.
- Only `/data` and `/tmp` are writable.
- Reader and metrics use separate service ports.
- Both LAN and public ingresses are disabled.
- Ollama processing and its dedicated egress rule are disabled.
- Gazetteer refresh follows the source mode by default: disabled for fixtures
  and enabled for live deployments. An explicit boolean overrides that rule.

The default NetworkPolicy permits DNS, same-namespace traffic, selected ingress
namespaces, metrics scraping, and public HTTPS for opt-in live ingestion. The
application's URL validator independently restricts live article requests to
the official police host.

## Public deployment

Start from [`deploy/example-values.yaml`](../../deploy/example-values.yaml) and
replace every example domain and infrastructure setting. Pin production images
with `image.digest`; tags alone are not immutable.

Public ingress requires:

- `application.sourceMode=live`;
- `application.presentationMode=public`;
- `application.gazetteerEnabled=true` before enabling translation processing;
- every public ingress hostname in `application.publicHosts`;
- an HTTPS `application.canonicalOrigin` using one of those hosts; and
- `ingress.public.languageCodes` matching the compiled reader registry; and
- TLS and external ingress configuration appropriate to the cluster.

Public mode fails closed. With AI disabled or no current privacy-safe
presentations, the reader can legitimately be empty.

## Ollama and administration

Enable AI only after configuring a reachable protected Ollama endpoint and the
matching NetworkPolicy egress. The legacy chart key `networkPolicy.pi8` is kept
for compatibility; it controls the explicit Ollama CIDR and port allowlist.

Fresh databases have no preferred models. Enable the protected admin view,
select one installed model for every canonical pipeline step, and select an
installed model plus supported adapter for every reader language on
`/admin/translations` before allowing scheduled or manual cycles.

Administration requires the LAN ingress plus exactly one of
`admin.basicAuthSecret` or `admin.basicAuthMiddleware`. The chart never creates
credentials. Both `/admin*` and `/api/admin*` contain sensitive review data or
mutating controls and must remain absent from public ingress.

## Validation

```bash
helm lint --strict charts/munichbrief
helm lint --strict charts/munichbrief --values deploy/example-values.yaml
helm template munichbrief charts/munichbrief \
  --namespace munichbrief \
  --values deploy/example-values.yaml \
  > /tmp/munichbrief-rendered.yaml
./scripts/check-chart.sh /tmp/munichbrief-rendered.yaml
```
