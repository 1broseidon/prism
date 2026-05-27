# Deploying Prism

## Quick Reference

```bash
# Build all source-built binaries
make build
# → bin/prism         (gateway)
# → bin/prism-bridge  (stdio→HTTP adapter)
# → bin/prism-auth    (standalone OAuth server for separated deployments)

# Run the gateway
./bin/prism -config /etc/prism/config.json

# Run a bridge manager for stdio backends (advanced sidecar mode)
./bin/prism-bridge manage --runtime docker --image-full ghcr.io/1broseidon/prism:latest

# Or wrap one stdio backend manually
./bin/prism-bridge serve --port 3001 -- npx @modelcontextprotocol/server-github
```

The source-built `prism`, `prism-bridge`, and `prism-auth` binaries are static. The published container image ships the gateway and bridge plus Node/npm and Python/uv runtimes for common managed stdio backends.

## Operational Environment Variables

These variables are reserved by Prism or `prism-bridge`. Backend credential variables such as `GITHUB_TOKEN` are user-defined and referenced from config credential entries.

| Variable | Component | Default | Purpose |
|---|---|---|---|
| `PRISM_DATA_DIR` | `prism` | `~/.prism` locally; `/data` in the container image | Base directory for persistent state. |
| `PRISM_SIGNING_KEY_FILE` | `prism` | `$PRISM_DATA_DIR/.prism/signing-key.pem` or `~/.prism/signing-key.pem` | Persistent RSA signing key for embedded OAuth tokens. |
| `PRISM_ANALYTICS_DB` | `prism` | `$PRISM_DATA_DIR/grant_events.sqlite` or `~/.prism/grant_events.sqlite` | SQLite analytics database. |
| `PRISM_BINSTORE_DIR` | `prism` | `$PRISM_DATA_DIR/binaries` or `~/.prism/binaries` | Binary backend artifact store. |
| `PRISM_KV_KEY_FILE` | `prism` | `~/.prism/kv-encryption.key` | At-rest encryption key for sensitive KV entries (OAuth client secrets, refresh tokens, admin sessions). Auto-generated on first start. Pin under a persistent volume in containers (`/data/.prism/kv-encryption.key`). |
| `PRISM_WORKSPACE_TOKEN` | `prism`, `prism-bridge workspace` | unset | Shared token for workspace bridge registration. |
| `PRISM_STDIO_SPAWN_MODE` | `prism` | auto-detected | Select local process, bridge HTTP, or container-backed stdio spawning. |
| `PRISM_BRIDGE_URL` / `PRISM_BRIDGE_URLS` | `prism` | unset | One or more sidecar bridge manager base URLs. |
| `PRISM_BRIDGE_NETWORK` | `prism` | unset | Docker network passed to the internal bridge manager. |
| `PRISM_SANDBOX_IMAGE` | `prism` | `ghcr.io/1broseidon/prism:latest` in the container image | Default sandbox image for managed backends. |
| `PRISM_SANDBOX_IMAGE_NODE` | `prism` | `PRISM_SANDBOX_IMAGE` | Node sandbox image override. |
| `PRISM_SANDBOX_IMAGE_PYTHON` | `prism` | `PRISM_SANDBOX_IMAGE` | Python sandbox image override. |
| `PRISM_GATEWAY_URL` | `prism-bridge workspace` | unset | Gateway base URL for workspace bridge mode. |
| `PRISM_AGENT_TOKEN` | `prism-bridge workspace` | unset | Agent OAuth access token; takes precedence over `PRISM_WORKSPACE_TOKEN`. |
| `PRISM_WORKSPACE_ID` | `prism-bridge workspace` | hostname | Stable workspace ID. |
| `PRISM_WORKSPACE_BACKEND` | `prism-bridge workspace` | `Brainfile` | Workspace backend ID. |
| `PRISM_WORKSPACE_NAMESPACE` | `prism-bridge workspace` | `<backend>-<workspace>` | Tool namespace registered with Prism. |
| `PRISM_WORKSPACE_ROOT` | `prism-bridge workspace` | current working directory | Root exposed by workspace bridge mode. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `prism` | unset | OTLP/HTTP collector endpoint (e.g. `http://otel-collector:4318`). When set, Prism initializes an OpenTelemetry tracer; otherwise tracing is a no-op. `OTEL_EXPORTER_OTLP_HEADERS` adds extra exporter headers. |

`prism-bridge manage` also reads `BRIDGE_IMAGE_BASE`, `BRIDGE_IMAGE_NODE`, `BRIDGE_IMAGE_PYTHON`, `BRIDGE_IMAGE_FULL`, and `BRIDGE_NETWORK` as defaults for matching CLI flags.

## Systemd

### Install

```bash
# Build and install binaries
go build -tags mcp_go_client_oauth -o /usr/local/bin/prism ./cmd/prism
go build -tags mcp_go_client_oauth -o /usr/local/bin/prism-bridge ./cmd/prism-bridge
# Optional: only needed for separated OAuth-server deployments.
go build -tags mcp_go_client_oauth -o /usr/local/bin/prism-auth ./cmd/prism-auth

# Create config directory
sudo mkdir -p /etc/prism
sudo cp config.json /etc/prism/config.json
sudo chmod 640 /etc/prism/config.json

# Create audit log directory
sudo mkdir -p /var/log/prism
sudo chown prism:prism /var/log/prism

# Create service user
sudo useradd -r -s /usr/sbin/nologin prism
```

### Service Unit

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

# Environment file for credentials (env-type credentials)
EnvironmentFile=-/etc/prism/env

# Security hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/log/prism
PrivateTmp=true

# Graceful shutdown
KillMode=mixed
KillSignal=SIGTERM
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
```

### Environment File

For `env`-type credentials, create `/etc/prism/env`:

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

# Logs
journalctl -u prism -f

# Audit log (if configured to file)
tail -f /var/log/prism/audit.json | jq .
```

## Docker

### Single Container

This is the default homelab path: one container, one persistent volume, and
optional Docker-sandboxed stdio MCP servers. When `/var/run/docker.sock` is
mounted, Prism starts an internal `prism-bridge manage` listener on localhost
and uses it to spawn sandbox containers. HTTP MCP servers work without the
socket.

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

The image includes:

- `prism` for the admin UI, OAuth server, and MCP gateway
- `prism-bridge` for managed stdio-to-HTTP adapters
- Node/npm and Python/uv for common `npx` and `uvx` MCP servers
- a default config at `/etc/prism/config.json` with bbolt state in `/data`

Pinning `PRISM_KV_KEY_FILE` and `PRISM_SIGNING_KEY_FILE` to the persistent volume keeps issued tokens and encrypted KV entries (admin sessions, refresh tokens, upstream OAuth credentials) valid across container restarts.

If you build a local image, set the sandbox image so spawned containers use
the same local build:

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

### Sidecar Bridge

Use this when you want the gateway/admin process separated from Docker socket
access. Prism connects to one or more bridge managers over HTTP:

```json
{
  "bridge_url": "http://prism-bridge:3001",
  "stdio_spawn_mode": "bridge_http"
}
```

For multiple bridge managers:

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

Backend IDs are assigned to bridges deterministically, and Prism tries the
next bridge if the selected bridge cannot spawn the backend.

### Docker Compose

Advanced stack with gateway + sidecar bridge:

```yaml
services:
  # The gateway — agents connect here
  prism:
    build: .
    ports:
      - "8080:8080"   # MCP gateway
      - "9086:9086"   # Admin API
    volumes:
      - ./config.json:/etc/prism/config.json:ro
      - ./audit:/var/log/prism
    environment:
      PRISM_BRIDGE_URL: http://prism-bridge:3001
      PRISM_STDIO_SPAWN_MODE: bridge_http
    depends_on:
      prism-bridge:
        condition: service_healthy
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:9086/health"]
      interval: 10s
      timeout: 3s
      retries: 3

  # Bridge manager: spawns stdio MCP servers as sandbox containers.
  prism-bridge:
    build:
      context: .
      dockerfile: cmd/prism-bridge/Dockerfile
    image: prism-bridge:full
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
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:3001/health"]
      interval: 10s
      timeout: 3s
      retries: 3
```

The bridge manager is the only sidecar that needs the Docker socket. It spawns
one sandbox container per stdio backend.

## Kubernetes

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
          image: prism:latest
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

### Bridge Manager Sidecar

For Kubernetes, the recommended pattern is one bridge-manager Deployment that
spawns stdio backends on-demand (the same `manage` mode used in the Docker
Compose example). Point Prism at it with `bridge_url` / `bridge_urls`:

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
          image: ghcr.io/1broseidon/prism:latest
          command: ["prism-bridge"]
          args:
            - manage
            - --runtime
            - docker        # or "kubernetes" when running on a cluster that exposes a runtime socket
            - --image-full
            - ghcr.io/1broseidon/prism:latest
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

Set the gateway's runtime config (admin console → Settings → Network, or
top-level JSON) to `"bridge_url": "http://prism-bridge:3001"` so stdio
backends added from the admin UI route through the bridge.

> Don't put upstream credentials on the bridge itself — Prism injects backend
> credentials from the gateway side using the `credentials` block on each
> server entry. The bridge stays credential-blind; rotating a token in Prism
> propagates without restarting the bridge.

If a particular stdio MCP server must be pinned to its own pod (single-instance
constraints, special volumes), run it as a one-off `prism-bridge serve` and
add it to Prism as an HTTP backend pointing at that pod's Service.

## Admin SSO

Enable OIDC login for the admin port by adding `admin_auth` to the JSON config
(or via the admin console under Settings → Sign-In, which persists into the KV
store and takes precedence over the file config):

```json
{
  "admin_auth": {
    "issuer": "https://accounts.google.com",
    "client_id": "…apps.googleusercontent.com",
    "client_secret": "…",
    "redirect_url": "https://prism.example.com/auth/callback",
    "scopes": ["openid", "profile", "email"],
    "groups_claim": "groups",
    "session_ttl": "24h",
    "cookie_secure": true,
    "rules": [
      { "role": "admin",  "emails":  ["ops@example.com"] },
      { "role": "admin",  "groups":  ["prism-admins"] },
      { "role": "viewer", "domains": ["example.com"] }
    ]
  }
}
```

Rules are tried top-to-bottom; the first match wins. Users who match no rule
are rejected. `admin` grants full read/write on `/api/v1/*`; `viewer` grants
read-only.

The `redirect_url` must be reachable by the operator's browser and registered
exactly with the IdP. When you front the admin port with a reverse proxy,
either set `admin_public_url` in the JSON config or toggle "Trust proxy
headers" under Settings → Network so Prism derives the callback host from
`X-Forwarded-*` instead of the in-container `Host`.

## Reverse Proxy

The gateway port (`:8080`) hosts `/mcp`, OAuth endpoints (`/token`,
`/authorize`, `/register`), and `/.well-known/*`. The admin port (`:9086`)
hosts the SPA at `/`, JSON API at `/api/v1/*`, root-level `/health`,
`/metrics`, `/auth/callback`, and `/oauth/callback`. Both speak plain HTTP;
terminate TLS at the proxy.

### Caddy (single vhost — gateway + admin)

```
prism.example.com {
    # MCP gateway + OAuth surface
    handle /mcp*                           { reverse_proxy localhost:8080 { flush_interval -1 } }
    handle /token                          { reverse_proxy localhost:8080 }
    handle /authorize                      { reverse_proxy localhost:8080 }
    handle /register                       { reverse_proxy localhost:8080 }
    handle /.well-known/*                  { reverse_proxy localhost:8080 }

    # Admin console + API + OIDC callbacks
    handle { reverse_proxy localhost:9086 }
}
```

When Caddy injects `X-Forwarded-*`, enable "Trust proxy headers" in the admin
console so OAuth callbacks and Protected Resource Metadata advertise the
public hostname instead of the in-container `Host`.

### Nginx (separate vhosts)

```nginx
# Public gateway
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
        proxy_buffering off;            # MCP Streamable HTTP uses SSE
        proxy_cache off;
        proxy_read_timeout 300s;
    }

    location ~ ^/(token|authorize|register|\.well-known/) {
        proxy_pass http://127.0.0.1:8080;
    }
}

# Internal admin vhost (restricted to operator network / VPN)
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

## Production Checklist

### Security

- [ ] **TLS termination** — Prism can terminate TLS directly (`tls.cert`/`tls.key`) or sit behind a reverse proxy. Use one or the other; HTTP-in-the-clear is a non-starter in production.
- [ ] **Agent OAuth tokens** — Prism always issues Bearer tokens. Confirm clients send `Authorization: Bearer …` and not custom headers.
- [ ] **Admin SSO enabled** — Configure `admin_auth` so the admin port requires an OIDC login, even if it's also network-restricted.
- [ ] **Audit logging enabled** — Write to a file or stdout for log aggregation. Every tool call should be traceable.
- [ ] **Admin port restricted** — Either reverse-proxy it behind admin SSO + an allowlist, or keep `:9086` on a private interface only.
- [ ] **`PRISM_KV_KEY_FILE` pinned** — Avoids regenerating the at-rest key on container restart, which would invalidate admin sessions and encrypted credentials.
- [ ] **Credentials secured** — Use `env`, `file`, or `command` credential types. Avoid `static` with literal secrets in the config file.
- [ ] **Config file permissions** — `chmod 640`, owned by the service user. Contains credential references.
- [ ] **Network policy** — In Kubernetes, restrict which pods can reach Prism and which backends Prism can reach.

### Reliability

- [ ] **Circuit breakers configured** — Prevent cascading failures from unhealthy backends.
- [ ] **Rate limits configured** — Protect backends from runaway agents.
- [ ] **Health checks wired** — Point your load balancer or orchestrator at `GET /health` on the admin port.
- [ ] **Graceful shutdown** — `shutdown_timeout` should be longer than your longest expected tool call.
- [ ] **Multiple replicas** — Prism is stateless. Run 2+ replicas behind a load balancer for availability.

### Observability

- [ ] **Audit log ingestion** — Feed audit JSON into your SIEM (Splunk, Elastic, Datadog, etc.).
- [ ] **Application logs** — Prism logs to stderr via `slog`. Capture with your log aggregator.
- [ ] **Admin API monitoring** — Scrape `/api/v1/backends` on the admin port to track backend health and connection status.
- [ ] **Alerting** — Alert on audit entries with `"allowed": false` (unauthorized access attempts) and circuit breaker opens.

### Credential Rotation

- [ ] **env credentials** — Rotate by updating the environment and restarting the process.
- [ ] **file credentials** — Rotate by updating the file. Prism reads at call time, no restart needed.
- [ ] **command credentials** — Credentials rotate automatically when TTL expires. Set TTL shorter than the credential's actual lifetime.
- [ ] **static credentials** — Avoid in production. If used, requires config change + restart to rotate.

## Ports

| Port | Purpose | Exposure |
|---|---|---|
| 8080 | MCP gateway (agents connect here) | External / agent-facing |
| 9086 | Admin UI/API, `/api/v1/*`, `/health`, `/metrics` | Internal only |

Both ports are configurable via `listen` and `admin`.
