<div align="center" style="margin-bottom: 20px;">
  <img src="static/rota_logo.png" alt="rota" width="100px">
  <h1 align="center">
  Rota - Proxy Rotation Platform
  </h1>
</div>

<p align="center">
  <a href="https://github.com/alpkeskin/rota/releases"><picture><source media="(prefers-color-scheme: dark)" srcset="https://www.shieldcn.dev/github/release/alpkeskin/rota.svg?size=sm&mode=dark"><img alt="Release" src="https://www.shieldcn.dev/github/release/alpkeskin/rota.svg?size=sm&mode=light"></picture></a>
  <a href="https://github.com/alpkeskin/rota/actions"><picture><source media="(prefers-color-scheme: dark)" srcset="https://www.shieldcn.dev/github/ci/alpkeskin/rota.svg?variant=secondary&size=sm&mode=dark"><img alt="CI" src="https://www.shieldcn.dev/github/ci/alpkeskin/rota.svg?variant=secondary&size=sm&mode=light"></picture></a>
  <a href="https://opensource.org/licenses/Apache-2.0"><picture><source media="(prefers-color-scheme: dark)" srcset="https://www.shieldcn.dev/github/license/alpkeskin/rota.svg?variant=ghost&size=sm&mode=dark"><img alt="License" src="https://www.shieldcn.dev/github/license/alpkeskin/rota.svg?variant=ghost&size=sm&mode=light"></picture></a>
  <a href="https://github.com/alpkeskin/rota/stargazers"><picture><source media="(prefers-color-scheme: dark)" srcset="https://www.shieldcn.dev/github/stars/alpkeskin/rota.svg?variant=secondary&size=sm&mode=dark"><img alt="GitHub stars" src="https://www.shieldcn.dev/github/stars/alpkeskin/rota.svg?variant=secondary&size=sm&mode=light"></picture></a>
  <a href="https://github.com/alpkeskin/rota/commits/main"><picture><source media="(prefers-color-scheme: dark)" srcset="https://www.shieldcn.dev/github/last-commit/alpkeskin/rota.svg?variant=secondary&size=sm&mode=dark"><img alt="Last commit" src="https://www.shieldcn.dev/github/last-commit/alpkeskin/rota.svg?variant=secondary&size=sm&mode=light"></picture></a>
</p>
<p align="center">
  <a href="https://golang.org"><picture><source media="(prefers-color-scheme: dark)" srcset="https://www.shieldcn.dev/badge/Go-1.25-00ADD8.svg?logo=go&variant=branded&size=sm&mode=dark"><img alt="Go 1.25" src="https://www.shieldcn.dev/badge/Go-1.25-00ADD8.svg?logo=go&variant=branded&size=sm&mode=light"></picture></a>
  <a href="https://nextjs.org"><picture><source media="(prefers-color-scheme: dark)" srcset="https://www.shieldcn.dev/badge/Next.js-16-000000.svg?logo=nextdotjs&variant=branded&size=sm&mode=dark"><img alt="Next.js 16" src="https://www.shieldcn.dev/badge/Next.js-16-000000.svg?logo=nextdotjs&variant=branded&size=sm&mode=light"></picture></a>
  <a href="https://www.timescale.com/"><picture><source media="(prefers-color-scheme: dark)" srcset="https://www.shieldcn.dev/badge/TimescaleDB-2.22-FDB515.svg?logo=timescale&variant=branded&size=sm&mode=dark"><img alt="TimescaleDB 2.22" src="https://www.shieldcn.dev/badge/TimescaleDB-2.22-FDB515.svg?logo=timescale&variant=branded&size=sm&mode=light"></picture></a>
  <a href="https://ghcr.io/alpkeskin/rota"><picture><source media="(prefers-color-scheme: dark)" srcset="https://www.shieldcn.dev/badge/Container-Docker-2496ED.svg?logo=docker&variant=branded&size=sm&mode=dark"><img alt="Docker" src="https://www.shieldcn.dev/badge/Container-Docker-2496ED.svg?logo=docker&variant=branded&size=sm&mode=light"></picture></a>
</p>


<picture>
  <source media="(prefers-color-scheme: light)" srcset="static/dashboard-light.png">
  <img src="static/dashboard.png" alt="Rota dashboard — overview page">
</picture>


## 🎯 Overview

**Rota** is a self-hosted proxy rotation platform: a Go proxy server that rotates thousands of upstream proxies with health checks, pools and per-user routing, plus a real-time dashboard on TimescaleDB. One `docker compose up` gives you the proxy on `:8000` and everything else behind a single origin.

---

## ✨ What it does

<table>
<tr>
<td width="50%" valign="top">

**🔄 Proxy engine**
- Random, round-robin, least-connections or time-based rotation
- HTTP, HTTPS, SOCKS4, SOCKS4A, SOCKS5 upstreams
- Pooled keep-alive transports, `splice(2)` tunneling on Linux, batched telemetry
- Health checks, unhealthy-proxy removal, dead-proxy cleanup
- Timeouts, retries, redirects, rate limiting, upstream chaining (Burp, ZAP)

</td>
<td width="50%" valign="top">

**📥 Sources & GeoIP**
- Remote `ip:port` lists fetched on a per-source schedule
- Per-source protocol and stale-proxy cleanup
- Country / region / city / ISP via ip-api.com or a local MaxMind DB
- Geo explorer: countries → cities, with proxy counts

</td>
</tr>
<tr>
<td valign="top">

**🗂️ Pools**
- Group proxies by geo, ISP substring or custom tags — mixed freely
- Per-pool rotation: round-robin, random or sticky (N requests per IP)
- Auto or manual membership sync; cron health checks with live progress
- TXT / CSV export and webhook alerts (Slack, Telegram topics, anything)

</td>
<td valign="top">

**👤 Users & routing**
- `http://user:pass@host:8000` — each user gets a main pool + ordered fallbacks
- Automatic failover across the chain, fresh proxy on every retry
- Per-user limits: requests per minute, concurrent connections, monthly bandwidth quota
- Exit country, city and sticky sessions chosen in the username (`alice-country-de-session-x`)
- HTTP and optional SOCKS5 inbound, working-proxy export API
- All requests, success rates and response times tracked per proxy

</td>
</tr>
<tr>
<td valign="top">

**🔐 Security**
- JWT-protected API; signing key persisted so sessions survive restarts
- Bcrypt admin credentials, changeable from the dashboard
- Login brute-force protection (per-IP + global), spoof-safe behind a proxy
- WebSocket origin validation

</td>
<td valign="top">

**📊 Dashboard**
- Live overview over WebSocket, response-time and outcome charts
- Every filter, sort, page and tab is in the URL — a screen is a link
- Proxies: tag, test, import `.txt`, export `txt` / `json` / `csv`, bulk edit
- Live log stream, system metrics, settings editable without a restart
- Dense hairline design, light / dark / system, works at 375px

</td>
</tr>
</table>

---

## 🚀 Quick Start

### Using Docker Compose (Recommended)

Everything runs behind a single entry point, so there's just one URL to open and
no API URL to configure.

```bash
# 1. Clone and start — no config file needed
git clone https://github.com/alpkeskin/rota.git
cd rota
docker compose up -d          # or: make up

# 2. Grab the auto-generated admin password from the logs
make password                  # or: docker compose logs rota-core | grep -i password
```

Then open **http://localhost** and log in with user `admin` and the password
from the logs. That's it — the dashboard, API and live logs are all served from
the same origin, so it works the same whether you're on `localhost` or a remote
server's IP.

**What's exposed:**
- 🌐 **Web UI + API**: http://localhost (everything — `/`, `/api`, `/docs`)
- 🔄 **Proxy**: `localhost:8000` (what your clients connect through)

> First-boot credentials are seeded once. Leave `ROTA_ADMIN_PASSWORD` unset to
> get a strong random password (shown in the logs), or set it in `.env` to pick
> your own. Change it anytime via **Settings → Your account**, and add more
> accounts under **Access → Accounts**.

`make help` lists the shortcuts: `up`, `build`, `down`, `restart`, `logs`, `ps`,
`password`, `dev-core`, `dev-dashboard`.

### Configuration

No `.env` is required — defaults work out of the box. Copy `cp .env.example .env`
only to change something. The common knobs:

| Variable | Default | Description |
|---|---|---|
| `SITE_ADDRESS` | `:80` | Web entry address. Set a domain for automatic HTTPS |
| `ROTA_ADMIN_PASSWORD` | _(random)_ | Initial admin password; blank → generated & logged |
| `ROTA_ADMIN_USER` | `admin` | Initial dashboard username (seeded once) |
| `JWT_SECRET` | _(generated, stored in DB)_ | Dashboard session signing key. Leave unset; set only to manage rotation yourself (changing it logs everyone out) |
| `AUDIT_LOG_RETENTION_DAYS` | `365` | How long audit log entries are kept; `0` keeps them forever |
| `PROXY_PORT` | `8000` | Host port for the proxy your clients connect to |
| `HTTP_PORT` / `HTTPS_PORT` | `80` / `443` | Web entry ports (Caddy) |
| `DB_PASSWORD` | `rota_password` | TimescaleDB password |
| `CORS_ALLOWED_ORIGINS` | `*` | API CORS allowlist (irrelevant behind the proxy) |
| `TRUST_PROXY_HEADERS` | `true` | Trust `X-Forwarded-For`/`X-Real-IP` for the login rate limiter. Keep `true` behind the bundled Caddy; set `false` if the API is exposed directly |
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `SOCKS_PORT` | _(off)_ | Also serve the proxy over SOCKS5 on this port (see [SOCKS5](#socks5)) |
| `ROTA_ENCRYPTION_KEY` | _(generated, stored in DB)_ | Key that encrypts upstream proxy passwords at rest. Set it (e.g. `openssl rand -base64 32`) so a database leak alone doesn't expose them — see [Encryption at rest](#encryption-at-rest) |
| `ROTA_ENCRYPTION_KEYS_PREVIOUS` | _(empty)_ | Comma-separated retired keys, still accepted for decryption during key rotation |
| `METRICS_TOKEN` | _(empty)_ | Require `Authorization: Bearer <token>` on `/metrics` |
| `REDIS_URL` | _(off)_ | Redis for limits and sticky sessions shared by all replicas (see [Running several replicas](#running-several-replicas)) |
| `REDIS_KEY_PREFIX` | `rota:` | Prefix for Rota's keys in a shared Redis |
| `SHUTDOWN_DRAIN_SECONDS` | `0` | On shutdown, report not ready and keep serving this long before closing listeners |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | _(off)_ | Export OpenTelemetry traces over OTLP/HTTP (see [Tracing](#tracing)) |

See `.env.example` for the full list including auth brute-force protection.

> **Note**: `ROTA_ADMIN_USER` / `ROTA_ADMIN_PASSWORD` are only used when the
> database is empty (first start). Afterwards, manage accounts under **Access**.

### Encryption at rest

Upstream proxy passwords are stored encrypted (AES-256-GCM). The key comes from
`ROTA_ENCRYPTION_KEY`; when it is unset, a key is generated on first boot and
stored in the database. That default still keeps passwords out of table dumps
and exports, but only a key kept **outside** the database protects them if the
whole database leaks — set `ROTA_ENCRYPTION_KEY` in production and back it up:
without it the stored passwords cannot be recovered.

On startup Rota encrypts any plaintext passwords left from older versions and
re-encrypts values sealed with a retired key. To rotate the key:

```bash
# .env
ROTA_ENCRYPTION_KEY=new-key
ROTA_ENCRYPTION_KEYS_PREVIOUS=old-key   # keep until one restart has completed
```

Switching from the generated database key to `ROTA_ENCRYPTION_KEY` needs no
extra step — the stored key is used to read existing values automatically.

If a stored password can't be decrypted with any configured key (for example
`ROTA_ENCRYPTION_KEY` was removed or mistyped), the core **refuses to start**
and writes nothing, rather than dialing upstreams without credentials.

**Several core replicas?** Startup re-encrypts immediately, so roll a new key
out in two deploys: first give every replica the new key as
`ROTA_ENCRYPTION_KEYS_PREVIOUS` (primary unchanged), then make it the primary
and move the old key to `ROTA_ENCRYPTION_KEYS_PREVIOUS`.

> **Downgrading** below the release that introduced encryption is not supported
> without first clearing the passwords: older versions would send the encrypted
> value to your upstream proxies as the password.

### Production Deployment (HTTPS)

Point a domain at the server and set one variable — Caddy obtains and renews a
TLS certificate automatically:

```bash
# .env
SITE_ADDRESS=rota.example.com
DB_PASSWORD=a-strong-random-password
ROTA_ADMIN_PASSWORD=a-strong-password
ROTA_ENCRYPTION_KEY=output-of-openssl-rand-base64-32
```

```bash
docker compose up -d --build
```

Everything is then served over HTTPS at `https://rota.example.com` — no separate
API host, no dashboard rebuild when the domain changes.

### Using Docker

Both services are published to GitHub Container Registry on every release:

| Image | What it is |
|---|---|
| `ghcr.io/alpkeskin/rota` | Core — proxy server (`:8000`) + REST API (`:8001`) |
| `ghcr.io/alpkeskin/rota-dashboard` | Next.js dashboard (`:3000`), meant to sit behind a reverse proxy that forwards `/api`, `/ws` and `/docs` to the core (see `Caddyfile`) |

Run the core on its own:

```bash
# Pull from GitHub Container Registry
docker pull ghcr.io/alpkeskin/rota:latest

# Run with basic configuration
docker run -d \
  --name rota-core \
  -p 8000:8000 \
  -p 8001:8001 \
  -e DB_HOST=your-db-host \
  -e DB_USER=rota \
  -e DB_PASSWORD=your-password \
  ghcr.io/alpkeskin/rota:latest
```

### Standalone binary (no Docker)

Every release ships the core as a single static binary for Linux (amd64, arm64),
macOS (arm64, amd64) and Windows (amd64) — see the
[releases page](https://github.com/alpkeskin/rota/releases). It runs the proxy
(`:8000`) and the REST API (`:8001`, Swagger UI at `/docs`); the web dashboard
is not included, so you manage proxies, pools and users through the API.

It still needs a **TimescaleDB** to connect to — install it natively
([Timescale ships packages and a Windows installer](https://docs.timescale.com/self-hosted/latest/install/))
or point it at any existing instance.

```bash
# 1. Unpack the archive for your platform, then put the DB connection next to it
cp .env.example .env        # set DB_HOST / DB_USER / DB_PASSWORD / DB_NAME (+ ROTA_ADMIN_PASSWORD)

# 2. Run — .env is read from the working directory; real env vars take precedence
./rota                      # Windows: rota.exe

# 3. Use it
curl -x http://localhost:8000 https://api.ipify.org
open http://localhost:8001/docs
```

`checksums.txt` on the release lists the SHA-256 of every archive.

### From Source

```bash
# Prerequisites: Go 1.25.3+, Node.js 20+, pnpm, and TimescaleDB reachable

# Clone the repository
git clone https://github.com/alpkeskin/rota.git
cd rota

# Start the Go core (serves API on :8001, proxy on :8000)
make dev-core            # or: cd core && go run ./cmd/server

# In a second terminal, start the dashboard.
# Dev runs on separate ports, so point the browser at the core directly:
cd dashboard
cp .env.local.example .env.local     # sets NEXT_PUBLIC_API_URL=http://localhost:8001
make -C .. dev-dashboard             # or: pnpm install && pnpm dev
```

> The DB connection and admin credentials come from the same environment
> variables as the Docker setup (see `.env.example`).

### Testing the Proxy

```bash
# Route traffic through Rota proxy
curl -x http://localhost:8000 https://api.ipify.org?format=json

# Per-user pool routing (after creating a Proxy User in the dashboard)
curl -x http://myuser:mypassword@localhost:8000 https://api.ipify.org?format=json

# Using environment variables
export HTTP_PROXY=http://localhost:8000
export HTTPS_PROXY=http://localhost:8000
curl https://api.ipify.org?format=json
```

---

## 📚 API Documentation

### Interactive API Documentation (Swagger)

Rota provides interactive API documentation. Once the stack is running, you can access it at:

```
http://localhost/docs
```

The docs interface allows you to:
- 📖 Browse all available API endpoints
- 🧪 Test API requests directly from your browser
- 📝 View request/response schemas
- 🔍 Explore authentication requirements

**Quick Access:**
- **API docs**: http://localhost/docs
- **OpenAPI Spec**: http://localhost/api/v1/swagger.json

---

## 🏗️ Architecture

Rota is a monorepo. A single reverse proxy (Caddy) is the only web entry point,
so the browser talks to one origin; the dashboard, API and WebSockets are all
same-origin behind it. Only the proxy port is exposed separately.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="static/architecture-dark.png">
  <img src="static/architecture.png" alt="Architecture: admin browser → Caddy → dashboard and Go core; core ↔ TimescaleDB; clients → core :8000 → upstream proxies">
</picture>

<sub>Diagram sources live in <code>docs/diagrams/</code> (self-contained HTML/SVG, generated with <a href="https://github.com/cathrynlavery/diagram-design">diagram-design</a>).</sub>

---

### Rotation Strategies

- **Random**: Select a random proxy for each request
- **Round Robin**: Distribute requests evenly across all proxies
- **Least Connections**: Route to the proxy with fewest active connections
- **Time-Based**: Rotate proxies at fixed intervals

---

## 🐳 Deployment

### Docker Compose

```bash
docker compose up -d
```

The bundled `docker-compose.yml` runs one core, the dashboard, TimescaleDB and
Caddy, restarting them automatically. See [Production Deployment
(HTTPS)](#production-deployment-https) for a domain with automatic TLS.

### Kubernetes (Helm)

A chart lives in [`deploy/helm/rota`](deploy/helm/rota). It runs the core
(proxy, optional SOCKS5 and API) and the dashboard, with an ingress that
routes `/api`, `/ws` and `/docs` to the core like the bundled Caddy does.
PostgreSQL with TimescaleDB is **not** bundled — point it at a managed
database or one run by an operator.

```bash
helm install rota ./deploy/helm/rota \
  --set database.host=timescaledb.db.svc \
  --set database.existingSecret=rota-db \
  --set redis.url=redis://redis-master.redis.svc:6379/0 \
  --set secrets.encryptionKey="$(openssl rand -base64 32)" \
  --set ingress.enabled=true --set ingress.host=rota.example.com
```

See the [chart README](deploy/helm/rota/README.md) for every value.

### Running several replicas

Any number of core instances can share one database:

- **Startup** (migrations, key setup, seeding the first admin) runs on one
  instance at a time, under a Postgres advisory lock.
- **Background jobs** (fetching sources, pool health checks, alerts, proxy/log/
  audit cleanup) run on one elected leader. If it stops or loses its database
  session, another instance takes over within seconds
  (`rota_cluster_leader` shows which one leads).
- **Configuration changes** (settings, proxies, proxy users, pools) made on
  one instance reach the others at once through Postgres `LISTEN/NOTIFY`. If
  a notification is lost, proxies and users are refreshed within a minute
  anyway, and each instance checks once a minute whether the settings changed.
- **Limits and sticky sessions** need Redis (`REDIS_URL`): per-user requests
  per minute and connection caps, the per-IP proxy rate limit, sticky
  sessions and login throttling are then enforced across all instances.
  Without Redis each instance enforces them on its own (a cap of N allows N
  per instance). If Redis becomes unreachable, instances fall back to their
  own limits until it's back (`rota_sharedstate_errors_total`). Use a single
  Redis endpoint (standalone, or a managed service's primary endpoint; Redis
  Cluster is not supported), version 5 or newer.
- **Bandwidth quotas** are always kept in the database; users with a quota
  reload their monthly total every 30 seconds, so usage on other instances
  counts against it within about half a minute.

Leader election and notifications hold a session on the database, so connect
directly or through a pooler in **session** mode (PgBouncer in transaction
mode breaks them). The MaxMind GeoIP database is kept per instance.

### Graceful shutdown

On `SIGTERM` an instance hands off leadership, then reports `503` on
`/readyz` for `SHUTDOWN_DRAIN_SECONDS` while still serving, so load balancers
stop sending it new clients; then it stops accepting connections, waits up to
30 seconds for requests in flight, and closes the remaining tunnels (their
bytes are counted). A second signal skips the drain. Give the process at least
drain + 35 seconds (the Helm chart sets `terminationGracePeriodSeconds: 60`
with a 15 s drain).

---

## 🗂️ Proxy Sources & Pools

### How Proxy Sources work

1. Go to **Sources** in the dashboard
2. Add a URL pointing to a plain-text proxy list (one `ip:port` per line)
3. Choose the protocol and refresh interval
4. Pick **Fetch now** from the row menu or wait for the scheduler

The system will:
- Download and parse the list
- Upsert proxies into the database (duplicates ignored)
- Automatically look up GeoIP data for every new proxy
- Re-sync all pools that have `Auto-sync` enabled

### Geo Distribution & Pools

After proxies are geolocated, open the **Pools → Geo distribution** tab:

- Browse all proxy-holding countries; click a country to expand cities
- Check individual countries or cities; mix them freely
- Click **Create Pool from selection** — the pool is created and filled instantly

Pools also support **ISP filters** (substring match, OR logic) and **tag filters** (AND logic — proxy must carry all specified tags). Combine geo + ISP + tags in any combination.

#### Pool Sync Modes

| Mode | Behaviour |
|------|-----------|
| `auto` | Pool membership is rebuilt automatically after every proxy import or geo-enrichment |
| `manual` | Membership only changes when you press **Sync** — useful for curated pools |

#### Exporting a Pool

```bash
# Plain text — one protocol://ip:port per line
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost/api/v1/pools/{id}/export?format=txt" -o pool.txt

# CSV — with status, geo, ISP, success rate
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost/api/v1/pools/{id}/export?format=csv" -o pool.csv
```

#### Webhook Alerts

Add an alert rule to a pool to be notified when the active proxy count drops below a threshold:

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  "http://localhost/api/v1/pools/{id}/alert-rules" \
  -d '{
    "enabled": true,
    "min_active_proxies": 10,
    "webhook_url": "https://hooks.slack.com/...",
    "cooldown_minutes": 30
  }'
```

Payload sent to the webhook:
```json
{
  "event": "pool.degraded",
  "pool_id": 1,
  "pool_name": "US Residential",
  "active_proxies": 3,
  "total_proxies": 50,
  "threshold": 10,
  "fired_at": "2026-04-02T04:30:00Z"
}
```

##### Telegram alerts (group topics)

Any other URL receives the generic JSON payload above. When the webhook host is
`api.telegram.org`, Rota instead calls the Telegram Bot API `sendMessage` method
and **generates the message text for you** — so you don't need a `text=`
parameter. To alert a topic inside a group:

1. Create a bot with [@BotFather](https://t.me/BotFather) and copy its token.
2. Add the bot to your group and give it permission to post in the topic.
3. Find the group's numeric `chat_id` (a negative number, e.g. `-1001234567890`)
   and the topic's `message_thread_id`.
4. Set the alert rule's **Webhook URL** to:

```
https://api.telegram.org/bot<TOKEN>/sendMessage?chat_id=<-100...>&message_thread_id=<topic_id>
```

- `message_thread_id` is optional — omit it to post to the group's main channel.
- The `bot` prefix on the token is required by Telegram; Rota adds it
  automatically if you leave it out.
- `webhook_method` is ignored for Telegram (always `POST`).

The delivered message looks like:

```
🔴 Rota pool alert
Pool: US Residential (#1)
Active proxies: 8 / 9
Threshold: 9
```

### Per-User Routing

1. Create pools for each location/use-case
2. Go to **Users**, click **Add user**
3. Set a main pool and optional fallback pools (in priority order)
4. Configure max retries and, optionally, limits: requests per minute, a
   monthly bandwidth quota and a cap on concurrent connections

Users connect as:
```
http://username:password@your-proxy-host:8000
```

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="static/routing-dark.png">
  <img src="static/routing.png" alt="Flowchart: request on :8000 → with credentials use the user's main pool, fall back to the next pool when none is alive, forward via a proxy, retry with a fresh proxy until it answers; without credentials use global rotation" width="640">
</picture>

If the main pool has no live IPs the request automatically cascades to the next fallback pool; each retry picks a fresh proxy and skips the ones that already failed. A user without any pool uses the global rotation.

#### Choosing the exit: country, city and sticky sessions

Routing options ride in the username, the convention commercial proxy
networks use — no client changes needed:

```bash
# Exit from Germany
curl -x http://alice-country-de:password@proxy-host:8000 https://api.ipify.org

# Exit from New York (use _ for spaces)
curl -x http://alice-country-us-city-new_york:password@proxy-host:8000 https://api.ipify.org

# Sticky session: the same id keeps the same exit IP for 10 minutes
# (sesstime sets 1-1440 minutes)
curl -x http://alice-session-a1b2c3-sesstime-30:password@proxy-host:8000 https://api.ipify.org
```

| Option | Value | Effect |
|---|---|---|
| `country` | ISO 3166-1 alpha-2 (`us`, `de`) | Only proxies geolocated in that country |
| `city` | city name, `_` for spaces | Only proxies in that city |
| `session` | 1-64 letters/digits | Pin the exit proxy for the session's lifetime; if it stops working the session moves to another matching proxy and stays there |
| `sesstime` | minutes, 1-1440 (default 10) | Session lifetime, counted from its first request |

Options apply within the user's pools (main, then fallbacks), so the user
needs a pool. No match gives `502` with a message saying so; malformed
options give `400`. Usernames of new accounts can't contain `-country-`,
`-city-`, `-session-` or `-sesstime-`.

#### Limits and usage

| Limit | Over the limit |
|---|---|
| Requests per minute | `429` with `Retry-After` |
| Concurrent connections (requests + tunnels) | `429` |
| Monthly bandwidth (up + down, calendar month in UTC) | `429`; open tunnels and downloads in progress are cut within ~10 s |

Usage this month shows in **Users**; it is counted in memory and written to
the database every 10 seconds (`rota_proxy_bytes_total` has the totals).

#### Circuit breaker

Independently of scheduled health checks, a proxy that can't be reached 5
times in a row on live traffic is skipped for 30 s (doubling on repeated
failures, up to 5 min), then gets one trial request. Only failures to reach
the proxy itself count — not a refused or unreachable destination, which a
client could otherwise use to take healthy proxies away from other users. This applies to every user and the
global rotation; if every candidate is tripped, Rota still tries one rather
than failing outright. `rota_proxy_circuit_open` shows how many are out.

#### SOCKS5

Set `SOCKS_PORT` (e.g. `1080`) to also serve the proxy over SOCKS5 with the
same users, routing options, limits and accounting (username/password auth;
CONNECT only — no UDP):

```bash
curl -x socks5h://alice-country-de:password@proxy-host:1080 https://api.ipify.org
```

With Docker Compose, publish the port in a `docker-compose.override.yml`:

```yaml
services:
  rota-core:
    ports:
      - "1080:1080"
```

#### Exporting a user's working proxies

Turn on **Export API** for the user and generate an **export token** (**Users → ⌄ → Export link… → Generate token**). The token is shown once; regenerating it revokes the old one, and **Revoke** disables it. Then fetch the alive proxies of the user's main pool, or of one of its fallback pools — handy for tools that want a raw list instead of routing through Rota:

```bash
# One proxy per line; default = the user's main pool, raw address[:user:pass]
curl -H "Authorization: Bearer rota_exp_..." \
  "http://localhost/api/v1/proxy-users/export-working-proxies"

# A fallback pool, capped, as protocol://[user:pass@]address
curl -H "Authorization: Bearer rota_exp_..." \
  "http://localhost/api/v1/proxy-users/export-working-proxies?pool=US%20Residential&count=50&format=url"

# Tools that only take a URL can pass the token as ?token=rota_exp_...
```

A user can only export its own pools (main + fallbacks); any other pool returns `404`. The endpoint also accepts the user's proxy credentials via HTTP Basic auth (`curl -u myuser:mypassword ...`). The old `?username=&password=` form still works but is **deprecated** — it puts the password in URLs and access logs — and responses to it carry a `Deprecation: true` header. Failed password attempts are throttled per IP with the same thresholds as the login endpoint (token requests aren't — tokens can't be guessed).

---

## 🔐 Access Control

### Accounts and roles

Everyone who signs in to the dashboard or API has an **account** with one role
(**Access → Accounts**, admins only). Roles are cumulative:

| Role | Can |
|---|---|
| `viewer` | Read everything except accounts and the audit log; secrets in configuration (webhook and source URLs past the host, health-check header values, the MaxMind key) are shown redacted |
| `operator` | …and change proxies, pools and proxy users (including export tokens), fetch/delete sources, delete alert rules |
| `admin` | …and change settings, **set source and webhook URLs** (they make the core call an address of the caller's choosing), manage accounts and others' API keys, read the audit log |

Role changes apply to open sessions immediately — including open live views
(WebSockets), which re-check their credentials every 15 seconds. Disabling an
account or resetting its password signs it out everywhere, and anyone can
**Sign out everywhere** from the account menu. At least one enabled admin
always remains: the last one can't be demoted, disabled or deleted.

> Upgrading from a version without accounts: the existing admin login becomes
> an `admin` account, and open dashboard sessions must sign in once more.

### Sessions and API keys

Interactive use signs in for a 24-hour session token:

```bash
TOKEN=$(curl -s -X POST http://localhost/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"yourpassword"}' | jq -r '.token')

curl -H "Authorization: Bearer $TOKEN" http://localhost/api/v1/proxies
```

Scripts and integrations should use an **API key** instead (**Access → API
keys**). A key is shown once, can expire, can be revoked, and acts with its own
role capped by its owner's current role. Keys can't manage accounts, keys or
passwords, so a leaked key can't mint new credentials, and creating a key asks
for your password, so a stolen session token can't either (five wrong
passwords in a row sign the account out everywhere). Keys are separate
from sessions: signing out (everywhere) or a password reset doesn't revoke
them — revoke them explicitly if an account may be compromised; disabling or
deleting the account stops its keys at once.

```bash
curl -H "Authorization: Bearer rota_key_..." http://localhost/api/v1/proxies
```

### Audit log

Every change made through the API — who, what route, which ids, the result and
the client IP — is recorded, including requests refused for lack of a role or
with a revoked/invalid credential (API keys are identified by their public
prefix), sign-in attempts and bulk exports. Attempts the login rate limiter
turns away before they reach the check are logged by the core, not audited. Admins browse it under **Access → Audit
log** or `GET /api/v1/audit-log?actor=&action=&from=&to=&page=`. Request bodies
are never stored. Entries are kept for `AUDIT_LOG_RETENTION_DAYS` (365).

Public endpoints (no token required):
- `GET /health`, `GET /livez`, `GET /readyz`
- `POST /api/v1/auth/login`
- `GET /api/v1/proxy-users/export-working-proxies` (authenticated with an export token, see above)

---

## 📈 Monitoring

The core exposes probes and Prometheus metrics on the API port (`:8001`):

| Endpoint | Purpose |
|---|---|
| `GET /livez` | Liveness — `200` while the process serves HTTP; never checks dependencies |
| `GET /readyz` | Readiness — `200` when the database answers a ping, `503` otherwise or while draining on shutdown |
| `GET /metrics` | Prometheus metrics |

The bundled Caddy does **not** route `/metrics`, `/livez` or `/readyz`, so they are
only reachable on the internal Docker network (e.g. `http://rota-core:8001/metrics`).
If you expose `:8001` directly, set `METRICS_TOKEN` and scrape with
`Authorization: Bearer <token>`.

Useful series:

| Metric | What it tells you |
|---|---|
| `rota_proxy_requests_total{kind,outcome}` | Proxy traffic by `http`/`connect` and `success`, `upstream_error`, `internal_error`, `rejected_auth`, `rejected_rate_limit` (a CONNECT counts as `success` once the tunnel is established) |
| `rota_proxy_tunnels_closed_total{result}` | CONNECT tunnels by how they ended: `clean` or `error` (includes resets during normal teardown — watch the ratio) |
| `rota_proxy_request_duration_seconds` | Time to upstream response (HTTP) or tunnel establishment (CONNECT) |
| `rota_proxy_active_tunnels` | Open CONNECT and SOCKS5 tunnels |
| `rota_proxy_bytes_total{direction}` | Proxied payload bytes, `up` (client→upstream) and `down` |
| `rota_proxy_limit_rejections_total{reason}` | Requests refused by per-user limits: `rate_limit`, `concurrency`, `quota` |
| `rota_proxy_circuit_open` / `rota_proxy_circuit_transitions_total{to}` | Proxies skipped by the circuit breaker, and its state changes |
| `rota_upstream_proxies{status}` | Upstream inventory by status (read from the DB at scrape time) |
| `rota_api_requests_total{route,method,status}` / `rota_api_request_duration_seconds` | REST API traffic by route pattern |
| `rota_log_hook_dropped_total` | Log events dropped because the DB log queue was full |
| `rota_secrets_decrypt_failures_total` | Stored proxy passwords no configured key could decrypt |
| `rota_cluster_leader` / `rota_cluster_leader_transitions_total{event}` | Whether this instance runs the background jobs, and leadership changes |
| `rota_cluster_change_events_total{topic}` | Configuration changes received from other instances |
| `rota_sharedstate_errors_total{op}` | Redis calls that failed; the instance used its own limits meanwhile |

Plus the standard `go_*` and `process_*` series and `rota_build_info{version}`.

### Tracing

Set `OTEL_EXPORTER_OTLP_ENDPOINT` (e.g. `http://otel-collector:4318`) to export
OpenTelemetry traces over OTLP/HTTP. The standard `OTEL_*` variables apply
(`OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_EXPORTER_OTLP_HEADERS`,
`OTEL_TRACES_SAMPLER`/`_ARG`, `OTEL_SDK_DISABLED`).

- **API requests** get a span named after the route (`GET /api/v1/pools/{id}`),
  continuing the caller's `traceparent`. Probes, `/metrics` and WebSockets
  aren't traced.
- **Proxied requests and tunnels** (HTTP, CONNECT, SOCKS5) get a root span with
  the user, target host and port, routing options and the request id
  (`X-Rota-Request-Id`), and a child span per upstream proxy attempt. A CONNECT
  or SOCKS5 span lasts as long as the tunnel.
- **Database queries** made while handling a traced request get a span each.

The proxy stays transparent: trace context sent by proxy clients is ignored
(a client can't join your traces or force sampling), nothing is added to
forwarded requests, and paths and query strings are never recorded.

### Brute-Force Protection

The login endpoint has two independent rate-limit mechanisms:

| Mechanism | Trigger | Response |
|-----------|---------|----------|
| **Per-IP block** | ≥ `AUTH_IP_MAX_ATTEMPTS` failed attempts from one IP within `AUTH_IP_WINDOW_MINUTES` minutes | `429` — IP blocked for `AUTH_IP_BLOCK_MINUTES` minutes |
| **Global lockout** | ≥ `AUTH_GLOBAL_MAX_PER_MINUTE` total attempts per minute across all IPs | `429` — login disabled for everyone for `AUTH_GLOBAL_LOCKOUT_MINUTES` minute(s) |

Both responses include a `Retry-After` header. All thresholds are configurable via `.env`.

> **Behind a reverse proxy?** Per-IP tracking uses the socket peer address by
> default. Set `TRUST_PROXY_HEADERS=true` (the default in the bundled Caddy
> setup) so the real client IP is read from `X-Forwarded-For` instead of the
> proxy's address. Leave it `false` when the API is exposed directly, otherwise
> a client could forge the header to dodge the per-IP block.

The dashboard automatically redirects to the login page with a *"Session expired"* message when a `401` response is received.

---

## 🤝 Contributing

Contributions are welcome! We appreciate meaningful contributions that add value to the project.

### How to Contribute

1. **Fork the repository**
2. **Create a feature branch**: `git checkout -b feature/amazing-feature`
3. **Make your changes**
4. **Commit your changes**: `git commit -m 'Add amazing feature'`
5. **Push to the branch**: `git push origin feature/amazing-feature`
6. **Open a Pull Request**

### Contribution Guidelines

- Write clear, descriptive commit messages
- Add tests for new features
- Update documentation as needed
- Follow existing code style and conventions
- Ensure all tests pass before submitting PR
- One feature/fix per pull request

**Note**: Pull requests that do not contribute significant improvements or fixes will not be accepted.

### Development Workflow

```bash
# 1. Create feature branch
git checkout -b feature/my-feature

# 2. Make changes and test
make test

# 3. Commit changes
git add .
git commit -m "feat: add my feature"

# 4. Push and create PR
git push origin feature/my-feature
```

---

## 📝 License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

---

<div align="center">
  <p>
    <sub>Built with ❤️ by <a href="https://github.com/alpkeskin">Alp Keskin</a></sub>
  </p>
  <p>
    <sub>⭐ Star this repository if you find it useful!</sub>
  </p>
</div>
