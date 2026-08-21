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
official police host. Enable the explicit pi8 CIDR rule only when AI processing
is configured.
