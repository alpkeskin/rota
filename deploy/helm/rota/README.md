# Rota Helm chart

Deploys the Rota core (proxy, optional SOCKS5 and REST API) and the
dashboard. See the [main README](../../../README.md#kubernetes-helm) for how
several core replicas cooperate.

## Prerequisites

- Kubernetes 1.23+ and Helm 3.
- **PostgreSQL with the TimescaleDB extension**, which this chart does not
  install. Connect directly or through a pooler in *session* mode.
- **Redis** when running more than one core replica. Without it, limits and
  sticky sessions are enforced per replica.

## Install

```bash
kubectl create secret generic rota-db --from-literal=password='…'

helm install rota ./deploy/helm/rota \
  --set database.host=timescaledb.db.svc \
  --set database.existingSecret=rota-db \
  --set redis.url=redis://redis-master.redis.svc:6379/0 \
  --set secrets.encryptionKey="$(openssl rand -base64 32)" \
  --set ingress.enabled=true --set ingress.host=rota.example.com
```

Unless you set `secrets.adminPassword`, the first admin password is generated
on first start and printed once in the core logs; `helm status rota` shows the
command to find it.

Keep `secrets.encryptionKey` somewhere safe: stored upstream proxy passwords
can't be decrypted without it. When you rotate it, list the old key in
`secrets.encryptionKeysPrevious`.

## Values

| Key | Default | Description |
|---|---|---|
| `core.image.repository` / `.tag` | `ghcr.io/alpkeskin/rota` / appVersion | Core image; pin a release tag in production |
| `core.replicaCount` | `2` | Core replicas (ignored with autoscaling) |
| `core.proxyPort` / `core.apiPort` | `8000` / `8001` | Proxy and API ports |
| `core.socksPort` | `0` | SOCKS5 port; `0` disables it |
| `core.shutdownDrainSeconds` | `15` | Seconds a terminating pod stays not-ready but serving |
| `core.terminationGracePeriodSeconds` | `60` | Must exceed the drain plus ~35 s |
| `core.trustProxyHeaders` | `true` | Trust `X-Forwarded-For` from the ingress for login throttling |
| `core.extraEnv` / `core.extraEnvFrom` | `[]` | Extra environment (e.g. `OTEL_TRACES_SAMPLER`) |
| `core.resources` | 250m / 256Mi, limit 1Gi | Core resources |
| `core.autoscaling.*` | disabled, 2–10, 70% CPU | HPA; scales in slowly because removed pods close their tunnels |
| `core.podDisruptionBudget.*` | enabled, `minAvailable: 1` | Disable for a single replica, or it blocks node drains |
| `core.proxyService.type` | `LoadBalancer` | Service your proxy clients connect to |
| `core.proxyService.externalTrafficPolicy` | `Local` | Keeps client IPs for the per-IP rate limit |
| `core.proxyService.loadBalancerSourceRanges` | `[]` | Restrict who can reach the proxy |
| `core.apiService.type` | `ClusterIP` | API service used by the ingress and Prometheus |
| `dashboard.enabled` | `true` | Deploy the dashboard |
| `dashboard.image.*`, `.replicaCount`, `.resources` | | Dashboard image, replicas and resources |
| `ingress.enabled` | `false` | Route `/api`, `/ws`, `/docs`, `/health` to the core and `/` to the dashboard on one host |
| `ingress.className`, `.host`, `.tls`, `.annotations` | | Ingress settings |
| `database.host` | _(required)_ | PostgreSQL/TimescaleDB host |
| `database.port`, `.name`, `.user`, `.sslMode` | `5432`, `rota`, `rota`, `require` | Connection settings |
| `database.password` / `.existingSecret` + `.existingSecretKey` | | Password inline, or from an existing Secret (key `password`) |
| `redis.url` / `.existingSecret` + `.existingSecretKey` | | Redis URL inline, or from an existing Secret (key `url`) |
| `redis.keyPrefix` | `rota:` | Key prefix in a shared Redis |
| `secrets.existingSecret` | | Secret with any of `ROTA_ENCRYPTION_KEY`, `ROTA_ENCRYPTION_KEYS_PREVIOUS`, `JWT_SECRET`, `ROTA_ADMIN_USER`, `ROTA_ADMIN_PASSWORD`, `METRICS_TOKEN` (replaces the values below) |
| `secrets.encryptionKey` | | Encrypts upstream proxy passwords at rest; unset = key stored in the database |
| `secrets.encryptionKeysPrevious` | | Retired keys, comma-separated |
| `secrets.jwtSecret` | | Session signing key; unset = generated and stored in the database |
| `secrets.adminUser` / `.adminPassword` | `admin` / | First admin account (used only on an empty database) |
| `secrets.metricsToken` | | Bearer token required on `/metrics` |
| `tracing.otlpEndpoint` | | OTLP/HTTP endpoint for traces, e.g. `http://otel-collector:4318` |
| `tracing.serviceName` | `rota-core` | `service.name` of the traces |
| `metrics.serviceMonitor.enabled` | `false` | Create a Prometheus Operator ServiceMonitor for `/metrics` |
| `metrics.serviceMonitor.tokenSecret` | `{}` | `{name, key}` of the metrics token when it lives in `secrets.existingSecret` |

## Notes

- The core runs with a read-only root filesystem. The GeoIP database is
  downloaded to an `emptyDir` per pod.
- Background jobs run on one elected core pod (`rota_cluster_leader == 1`). A
  terminating pod hands leadership off before it drains.
- The dashboard calls the API on its own origin. Without the ingress, put a
  reverse proxy in front that routes `/api` and `/ws` to the `-api` service.
