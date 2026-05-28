# Deploying Prism

Docker is the default path. Run the published image, mount one persistent
volume, and you have a working gateway. Source builds (for systemd) are
secondary.

All configuration fields and the full operational environment-variable table
live in [Configuration](./configuration.md). This guide links to it rather than
duplicating it.

- [Docker](#docker) — single container (default), then Compose
- [systemd](#systemd) — source-built binaries on a host
- [Kubernetes](#kubernetes)
- [Reverse proxy](#reverse-proxy) — Caddy and nginx
- [Production hardening checklist](#production-hardening-checklist)
- [Ports](#ports)

## Docker

### Single container (default)

One container, one persistent volume, optional Docker-sandboxed stdio MCP
servers. This is the recommended way to run Prism.

```bash
docker run -d \
  --name prism \
  -p 8080:8080 \
  -p 9086:9086 \
  -v prism-data:/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e PRISM_KV_KEY_FILE=/data/.prism/kv-encryption.key \
  -e PRISM_SIGNING_KEY_FILE=/data/.prism/signing-key.pem \
  ghcr.io/1broseidon/prism:latest
```

Then open `http://localhost:9086` and add a backend from the Servers page.

- Port `8080` = MCP gateway (agents connect here). Port `9086` = admin UI/API.
- The `prism-data` named volume mounts at `/data` — bbolt DB, signing key, KV
  encryption key, analytics, and the binary store all live under it.
- The Docker socket is optional. When `/var/run/docker.sock` is mounted, Prism
  starts an internal `prism-bridge manage` listener on localhost and spawns
  sandboxed stdio backends. HTTP MCP backends work without the socket.
- The two `-e` flags are belt-and-suspenders. The image already defaults
  `PRISM_KV_KEY_FILE` and `PRISM_SIGNING_KEY_FILE` to `/data/...`; pinning them
  explicitly keeps issued OAuth tokens, admin sessions, refresh tokens, and
  encrypted upstream credentials valid across restarts. Without persistence the
  at-rest key regenerates on each start and invalidates all of them.

The published image bundles:

- `prism` at `/usr/local/bin/prism` (admin UI, embedded OAuth 2.1 server, MCP gateway)
- `prism-bridge` at `/usr/local/bin/prism-bridge` (managed stdio-to-HTTP adapter)
- Node/npm for `npx` MCP servers and Python3/uv for `uvx` MCP servers
- A default config baked in at `/etc/prism/config.json` (copied from
  `deploy/config.container.json`): bbolt store at `/data/prism.db`, audit
  enabled to `stderr`, `stdio_spawn_mode: "auto"`, empty `mcpServers`

The entrypoint is `prism`; the default command is `-config /etc/prism/config.json`.
`prism` runs its `serve` mode implicitly, so no subcommand is required.

Pin a reproducible tag when you need it. The published semver tag is `0.1.0`
with **no** `v` prefix:

```bash
docker run -d --name prism \
  -p 8080:8080 -p 9086:9086 \
  -v prism-data:/data -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/1broseidon/prism:0.1.0
```

Both `ghcr.io/1broseidon/prism` and `ghcr.io/1broseidon/prism-bridge` publish
`latest`, `0.1.0`, `0.1`, and `0`, multi-arch for `linux/amd64` and `linux/arm64`.

If you build a local image, point the sandbox image at your build so spawned
containers use the same code:

```bash
docker build -t prism:dev .
docker run -d \
  --name prism \
  -p 8080:8080 \
  -p 9086:9086 \
  -v prism-data:/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e PRISM_SANDBOX_IMAGE=prism:dev \
  prism:dev
```

### Sidecar bridge (advanced)

Run the gateway/admin process without giving it Docker socket access by putting
the socket on a separate `prism-bridge manage` sidecar. Prism connects to it
over HTTP. Set this in the JSON config:

```json
{
  "bridge_url": "http://prism-bridge:3001",
  "stdio_spawn_mode": "bridge_http"
}
```

For multiple bridge managers, command backends are sharded across them by
backend ID, and Prism tries the next bridge if the selected one cannot spawn:

```json
{
  "bridge_urls": [
    "http://bridge-1:3001",
    "http://bridge-2:3001",
    "http://bridge-3:3001"
  ],
  "stdio_spawn_mode": "bridge_http"
}
```

The same selection can be made with the `PRISM_BRIDGE_URL` /
`PRISM_BRIDGE_URLS` and `PRISM_STDIO_SPAWN_MODE` environment variables, which
override the config. See [Configuration](./configuration.md#environment-variables).

### Docker Compose (advanced)

The repo's canonical stack is `compose.yml`: three services —
`prism-bridge`, `prism`, and `caddy` — on the `prism_default` network, fronted
by a single HTTPS vhost.

```yaml
services:
  prism-bridge:
    build:
      context: .
      dockerfile: cmd/prism-bridge/Dockerfile
    image: prism-bridge:full
    restart: unless-stopped
    expose:
      - "3001"
    command:
      - manage
      - --runtime
      - docker
      - --network
      - prism_default
      - --image-full
      - prism-bridge:full
      - --image-node
      - prism-bridge:full
      - --image-python
      - prism-bridge:full
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:3001/health"]
      interval: 10s
      timeout: 3s
      retries: 6

  prism:
    build: .
    image: prism:dev
    container_name: prism
    restart: unless-stopped
    environment:
      PRISM_KV_KEY_FILE: /data/.prism/kv-encryption.key
      PRISM_SIGNING_KEY_FILE: /data/.prism/signing-key.pem
      PRISM_WORKSPACE_TOKEN: ${PRISM_WORKSPACE_TOKEN:-}
    expose:
      - "8080"
      - "9086"
    # Direct ports kept for local access when you want to bypass Caddy.
    ports:
      - "8080:8080"
      - "9086:9086"
    volumes:
      - prism-data:/data
      - ./deploy/config.json:/etc/prism/config.json:ro
    depends_on:
      prism-bridge:
        condition: service_healthy

  caddy:
    build:
      context: .
      dockerfile: deploy/Caddy.Dockerfile
    image: prism-caddy:cloudflare
    container_name: prism-caddy
    restart: unless-stopped
    env_file:
      - .env
    ports:
      - "443:443"
    volumes:
      - ./deploy/Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy-data:/data
      - caddy-config:/config
    depends_on:
      - prism

volumes:
  prism-data:
  caddy-data:
  caddy-config:

networks:
  default:
    name: prism_default
```

Notes on the canonical stack:

- The `prism` service mounts `deploy/config.json`, which differs from the
  baked-in container config: it adds `public_url` / `admin_public_url`
  (`https://prism.example.com`), `bridge_urls: ["http://prism-bridge:3001"]`,
  and uses `stdio_spawn_mode: "bridge_http"` instead of `"auto"`.
- Only `prism-bridge` needs the Docker socket; it spawns one sandbox container
  per stdio backend. The gateway stays socket-free.
- `prism` keeps `8080`/`9086` published for direct access even though Caddy
  fronts both. Drop the `ports:` block to make Caddy the only ingress.
- The `caddy` service uses an xcaddy build with the `caddy-dns/cloudflare`
  plugin and needs `CLOUDFLARE_API_TOKEN` in `.env`. See
  [Reverse proxy](#reverse-proxy).

> The repo also contains a root `docker-compose.yml`. That is an older,
> separate QA stack (admin on `:9090`, `serve --config` entrypoint) and is
> **not** the documented topology. Use `compose.yml`.

## systemd

For a host install without Docker, build the static binaries and run the
gateway under systemd. Docker is still the preferred path; use systemd only
when you have a reason to avoid containers.

### Install

```bash
# Build static binaries with the OAuth build tag.
go build -tags mcp_go_client_oauth -o /usr/local/bin/prism ./cmd/prism
go build -tags mcp_go_client_oauth -o /usr/local/bin/prism-bridge ./cmd/prism-bridge
# Optional: only for separated OAuth-server deployments.
go build -tags mcp_go_client_oauth -o /usr/local/bin/prism-auth ./cmd/prism-auth

# Config directory.
sudo mkdir -p /etc/prism
sudo cp config.json /etc/prism/config.json
sudo chmod 640 /etc/prism/config.json

# Audit log directory (only if you set audit.output to a file path).
sudo mkdir -p /var/log/prism

# Service user.
sudo useradd -r -s /usr/sbin/nologin prism
sudo chown prism:prism /var/log/prism
```

### Service unit

Save as `/etc/systemd/system/prism.service`:

```ini
[Unit]
Description=Prism MCP Gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=prism
Group=prism
ExecStart=/usr/local/bin/prism -config /etc/prism/config.json
Restart=on-failure
RestartSec=5

# Environment file for env-type backend credentials.
EnvironmentFile=-/etc/prism/env

# Security hardening.
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/log/prism
PrivateTmp=true

# Graceful shutdown.
KillMode=mixed
KillSignal=SIGTERM
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
```

A host install defaults persistent state to `~/.prism` (the `prism` user's
home). To place it elsewhere, set `PRISM_DATA_DIR` in the environment file.

### Environment file

For `env`-type backend credentials, create `/etc/prism/env`. Variable names are
whatever each backend's `credentials.env` references — they are not reserved by
Prism:

```bash
GITHUB_TOKEN=Bearer ghp_xxxxxxxxxxxx
STRIPE_KEY=sk_live_xxxxxxxxxxxx
```

```bash
sudo chmod 640 /etc/prism/env
sudo chown prism:prism /etc/prism/env
```

### Manage

```bash
sudo systemctl daemon-reload
sudo systemctl enable prism
sudo systemctl start prism

# Logs.
journalctl -u prism -f

# Reload config without a restart (SIGHUP).
sudo systemctl reload prism
```

## Kubernetes

Use the published image with a pinned tag. `prism:latest` is a local build tag
and will not pull from a registry; use `ghcr.io/1broseidon/prism:0.1.0`.

### ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: prism-config
data:
  config.json: |
    {
      "listen": ":8080",
      "admin": ":9086",
      "mcpServers": {
        "github": {
          "url": "http://github-mcp:3001/mcp",
          "credentials": { "file": "/secrets/github/token" }
        },
        "infra": {
          "url": "http://vault-mcp:3002/mcp",
          "credentials": {
            "command": "cat /var/run/secrets/kubernetes.io/serviceaccount/token",
            "ttl": "10m"
          }
        }
      },
      "policy": {
        "agents": {
          "ci-agent": { "secret": "${CI_AGENT_SECRET}", "groups": ["deployers"] }
        },
        "groups": {
          "deployers": { "scopes": ["github:*", "infra:*"] }
        }
      },
      "audit": {
        "enabled": true,
        "output": "stdout"
      }
    }
```

### Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: prism-github-token
type: Opaque
stringData:
  token: "Bearer ghp_xxxxxxxxxxxx"
```

### Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: prism
spec:
  replicas: 2
  selector:
    matchLabels:
      app: prism
  template:
    metadata:
      labels:
        app: prism
    spec:
      containers:
        - name: prism
          image: ghcr.io/1broseidon/prism:0.1.0
          ports:
            - containerPort: 8080
              name: mcp
            - containerPort: 9086
              name: admin
          args: ["-config", "/etc/prism/config.json"]
          volumeMounts:
            - name: config
              mountPath: /etc/prism
              readOnly: true
            - name: github-token
              mountPath: /secrets/github
              readOnly: true
          livenessProbe:
            httpGet:
              path: /health
              port: admin
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /health
              port: admin
            initialDelaySeconds: 2
            periodSeconds: 5
          resources:
            requests:
              memory: "32Mi"
              cpu: "50m"
            limits:
              memory: "128Mi"
              cpu: "500m"
      volumes:
        - name: config
          configMap:
            name: prism-config
        - name: github-token
          secret:
            secretName: prism-github-token
```

`/health` is served on the admin port (`9086`). When you pin
`PRISM_KV_KEY_FILE` / `PRISM_SIGNING_KEY_FILE` to a `PersistentVolume` instead
of the ConfigMap-only setup above, replicas share issued tokens and sessions;
otherwise scope each replica to client-credentials agents whose tokens it can
re-mint on demand.

### Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: prism
spec:
  selector:
    app: prism
  ports:
    - name: mcp
      port: 8080
      targetPort: mcp
    - name: admin
      port: 9086
      targetPort: admin
```

### Bridge manager sidecar

For sandboxed stdio backends on Kubernetes, run one `prism-bridge manage`
Deployment and point the gateway at it with `bridge_url` / `bridge_urls`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: prism-bridge
spec:
  replicas: 1
  selector: { matchLabels: { app: prism-bridge } }
  template:
    metadata: { labels: { app: prism-bridge } }
    spec:
      containers:
        - name: bridge
          image: ghcr.io/1broseidon/prism-bridge:0.1.0
          command: ["prism-bridge"]
          args:
            - manage
            - --runtime
            - docker
            - --image-full
            - ghcr.io/1broseidon/prism-bridge:0.1.0
          ports:
            - containerPort: 3001
          livenessProbe: { httpGet: { path: /health, port: 3001 } }
          resources:
            requests: { memory: "64Mi", cpu: "50m" }
            limits:   { memory: "256Mi", cpu: "500m" }
---
apiVersion: v1
kind: Service
metadata:
  name: prism-bridge
spec:
  selector: { app: prism-bridge }
  ports: [{ port: 3001, targetPort: 3001 }]
```

Set the gateway's `bridge_url` to `http://prism-bridge:3001` (in the JSON
config, via `PRISM_BRIDGE_URL`, or in the admin console under
Settings → Network) so stdio backends added from the admin UI route through the
bridge.

> Keep upstream credentials off the bridge. Prism injects backend credentials
> from the gateway side using each server's `credentials` block, so the bridge
> stays credential-blind and rotating a token in Prism propagates without
> restarting the bridge.

If a stdio MCP server must be pinned to its own pod, run it as a one-off
`prism-bridge serve` and add it to Prism as an HTTP backend pointing at that
pod's Service.

## Reverse proxy

Both Prism ports speak plain HTTP — terminate TLS at the proxy (or use Prism's
own TLS termination; see [Configuration](./configuration.md#tls)). The gateway
port (`:8080`) hosts `/mcp` (and `/mcp/`), the OAuth endpoints (`/token`,
`/authorize`, `/register`, `/stepup`), the `/workspace/*` control plane, and
the `/.well-known/*` discovery documents. The admin port (`:9086`) hosts the
SPA at `/`, the JSON API at `/api/v1/*`, and root-level `/health`, `/metrics`,
`/auth/callback`, and `/oauth/callback`.

When you front Prism behind a proxy that injects `X-Forwarded-*`, set
`public_url` and `admin_public_url` (or toggle "Trust proxy headers" under
Settings → Network) so OAuth callbacks and Protected Resource Metadata
advertise the public hostname instead of the in-container `Host`.

### Caddy (single vhost — gateway + admin)

This mirrors `deploy/Caddyfile`: one hostname routes the gateway paths to
`prism:8080` and everything else to the admin console on `prism:9086`. The
example uses Let's Encrypt DNS-01 over Cloudflare, so the hostname can resolve
to a private address while clients still receive a public-trusted cert.

```
{
    # Disable Caddy's admin endpoint — smaller attack surface.
    admin off
}

prism.example.com {
    tls {
        dns cloudflare {env.CLOUDFLARE_API_TOKEN}
    }
    encode gzip zstd

    @gateway {
        path /mcp /mcp/*
        path /token
        path /authorize
        path /register
        path /workspace/*
        path /.well-known/oauth-protected-resource
        path /.well-known/oauth-protected-resource/*
        path /.well-known/oauth-authorization-server
        path /.well-known/jwks.json
    }
    reverse_proxy @gateway prism:8080

    # Everything else (/, /auth/*, /api/v1/*, ...) → admin console.
    reverse_proxy prism:9086
}
```

`CLOUDFLARE_API_TOKEN` must be set in the Caddy container. In the admin
console, set `public_url`, `admin_public_url`, and enable "Trust proxy headers"
so callbacks and resource metadata use the public hostname.

### Nginx (separate vhosts)

Split the gateway and admin onto separate server blocks and restrict the admin
vhost to your operator network.

```nginx
# Public gateway.
server {
    listen 443 ssl;
    server_name prism.example.com;
    ssl_certificate     /etc/ssl/prism.crt;
    ssl_certificate_key /etc/ssl/prism.key;

    location /mcp {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;            # MCP Streamable HTTP uses SSE.
        proxy_cache off;
        proxy_read_timeout 300s;
    }

    location ~ ^/(token|authorize|register|workspace/|\.well-known/) {
        proxy_pass http://127.0.0.1:8080;
    }
}

# Internal admin vhost (restricted to operator network / VPN).
server {
    listen 443 ssl;
    server_name admin.prism.example.com;
    allow 10.0.0.0/8; deny all;
    ssl_certificate     /etc/ssl/prism.crt;
    ssl_certificate_key /etc/ssl/prism.key;

    location / {
        proxy_pass http://127.0.0.1:9086;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

With split vhosts, set `public_url` to the gateway hostname and
`admin_public_url` to the admin hostname so OAuth callbacks resolve correctly.

## Production hardening checklist

### Security

- [ ] **TLS terminated** — either Prism's own [TLS](./configuration.md#tls) (`tls.cert` / `tls.key`) or a reverse proxy. HTTP-in-the-clear is a non-starter in production.
- [ ] **Agent OAuth tokens** — Prism always issues Bearer tokens. Confirm clients send `Authorization: Bearer …`, not custom headers. (`X-API-Key` is for upstream backends only, never agent→gateway.)
- [ ] **Admin SSO enabled** — configure [`admin_auth`](./configuration.md#authentication) so the admin port requires OIDC login, even when it is also network-restricted. The validator requires `issuer`, `client_id`, `client_secret`, `redirect_url`, and at least one rule; `cookie_secure` auto-enables when TLS is configured.
- [ ] **Admin port restricted** — reverse-proxy it behind admin SSO and an IP allowlist, or keep `:9086` on a private interface only.
- [ ] **`PRISM_KV_KEY_FILE` and `PRISM_SIGNING_KEY_FILE` pinned** — to a persistent volume, so the at-rest key and signing key survive restarts. Otherwise admin sessions, refresh tokens, and encrypted credentials are invalidated on each start.
- [ ] **Credentials secured** — use `env`, `file`, or `command` credential types. Avoid `static` literals in the config file.
- [ ] **Config file permissions** — `chmod 640`, owned by the service user; it contains credential references.
- [ ] **Network policy** — in Kubernetes, restrict which pods can reach Prism and which backends Prism can reach.

### Reliability

- [ ] **Circuit breakers configured** — see [Circuit breaker](./configuration.md#circuit-breaker). Prevent cascading failures from unhealthy backends.
- [ ] **Rate limits configured** — see [Rate limiting](./configuration.md#rate-limiting). Use the `rps` and `burst` keys (both must be > 0).
- [ ] **Health checks wired** — point your load balancer or orchestrator at `GET /health` on the admin port.
- [ ] **Graceful shutdown** — `shutdown_timeout` (default `10s`) should exceed your longest expected tool call.
- [ ] **Multiple replicas** — run 2+ behind a load balancer; pin the signing and KV keys to shared, persistent storage so tokens issued by one replica validate on another.

### Observability

- [ ] **Audit logging enabled** — see [Audit logging](./configuration.md#audit-logging). Set `audit.enabled` and route `output` to a file or stdout; `retention_days` defaults to 30.
- [ ] **Audit log ingestion** — feed the one-line JSON entries into your SIEM (Splunk, Elastic, Datadog).
- [ ] **Application logs** — Prism logs to stderr via `slog`; capture with your aggregator.
- [ ] **Tracing** — set `OTEL_EXPORTER_OTLP_ENDPOINT` to enable the OTLP/HTTP tracer; unset, tracing is a zero-overhead no-op. See [Environment variables](./configuration.md#environment-variables).
- [ ] **Backend health monitoring** — poll `GET /api/v1/backends` on the admin port to track backend connection status.
- [ ] **Alerting** — alert on audit entries denying access and on circuit-breaker opens.

### Credential rotation

- [ ] **`env`** — rotate by updating the environment and restarting the process.
- [ ] **`file`** — rotate by updating the file; Prism reads at call time, no restart needed.
- [ ] **`command`** — rotates automatically when the `ttl` expires (default `5m`). Set `ttl` shorter than the credential's real lifetime.
- [ ] **`static`** — avoid in production; rotating requires a config change plus reload.

## Ports

| Port | Purpose | Exposure |
|---|---|---|
| `8080` | MCP gateway — agents connect here; OAuth and `/.well-known/*` live here too | External / agent-facing |
| `9086` | Admin UI/API (`/api/v1/*`), `/health`, `/metrics`, OIDC callbacks | Internal only |
| `3001` | `prism-bridge manage` HTTP listener (sidecar / Compose only) | Internal only |

The gateway and admin ports are configurable via the `listen` and `admin`
config fields. See [Configuration](./configuration.md#top-level-fields).

## See also

- [Configuration](./configuration.md) — full field reference, environment
  variables, TLS, auth, audit, rate limiting, circuit breaker, storage
- [Getting Started](./getting-started.md) — quickstart and agent-harness setup
- [Admin API](./admin-api.md) — admin route reference
- [README](../README.md) — project overview
