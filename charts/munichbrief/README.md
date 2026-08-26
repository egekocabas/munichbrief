# MunichBrief Helm chart

```bash
helm install munichbrief ./charts/munichbrief \
  --namespace munichbrief \
  --create-namespace
```

The chart intentionally enforces one replica and `ReadWriteOnce` persistence.
It uses `Recreate`, retains the PVC on chart removal, runs as a non-root user,
drops all capabilities, and mounts only `/data` and `/tmp` writable.

For production, set `image.digest` to the full `sha256:` digest and configure
an `imagePullSecret` if the GHCR package is private. The reader Service port is
used by Ingress; Prometheus uses the separate metrics port, so `/metrics` is not
published through the browser route.

The default NetworkPolicy allows DNS, public HTTPS, same-namespace access,
Traefik access to the reader port, and observability access to the metrics port.
The application's own URL validator still limits HTTP article requests to the
official police host. The checked-in private deployment enables AI, limits pi8
egress to `192.168.178.102/32:11434`, and selects `public` presentation mode.
The reader therefore serves only current privacy-safe AI output and never
renders originals. Original source text remains available through the protected
admin incident lists and through explicit local `review` mode.

Both ingresses are disabled by default. `ingress.lan` can expose the complete
reader, while `ingress.public` renders only an explicit reader path allowlist.
Set `application.publicHosts` to every public hostname so the application also
enforces public presentation scope for each domain. Every
`ingress.public.hosts` entry must appear there. Set
`application.canonicalOrigin` to the preferred HTTPS origin; its hostname must
also appear in `application.publicHosts`. The public ingress exposes
`/robots.txt` and `/sitemap.xml` alongside the reader routes, while both files
and all document metadata use that canonical origin. To enable the operations dashboard, set
`admin.enabled=true` together with the LAN ingress and exactly one of
`admin.basicAuthSecret` or `admin.basicAuthMiddleware`. The chart never creates
credentials; a Secret-backed option creates only the Traefik Middleware. The
admin dashboard contains retained original incident text and must remain absent
from the public ingress. Confirmed Process now actions persist and wake manual
work outside the configured AI window without bypassing validation, sequential
execution, circuit breaking, or retry delays.

The application discovers installed models from Ollama. Every registered
pipeline step starts unconfigured on a fresh database and is selected from the
protected admin dashboard. Existing installations migrate their former model
preference to German analysis while leaving English translation unconfigured.
Scheduled AI work remains paused until every step has an installed model;
reader health, synchronization, and readiness remain independent.
