# Deploying Prism

## Quick Reference

```bash
# Build both binaries
make build
# → bin/prism         (gateway)
# → bin/prism-bridge  (stdio→HTTP adapter)

# Run the gateway
./bin/prism -config /etc/prism/config.json

# Run a bridge manager for stdio backends (advanced sidecar mode)
./bin/prism-bridge manage --runtime docker --image-full ghcr.io/1broseidon/prism:latest

# Or wrap one stdio backend manually
./bin/prism-bridge serve --port 3001 -- npx @modelcontextprotocol/server-github
```

Both are static binaries. No runtime dependencies. Run on any Linux/macOS/Windows amd64 or arm64 system.

## Systemd

### Install

```bash
# Build and install binaries
go build -o /usr/local/bin/prism ./cmd/prism
go build -o /usr/local/bin/prism-bridge ./cmd/prism-bridge

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
  ghcr.io/1broseidon/prism:latest
```

The image includes:

- `prism` for the admin UI, OAuth server, and MCP gateway
- `prism-bridge` for managed stdio-to-HTTP adapters
- Node/npm and Python/uv for common `npx` and `uvx` MCP servers
- a default config at `/etc/prism/config.json` with bbolt state in `/data`

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

### Bridge Deployment (per stdio backend)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: bridge-github
spec:
  replicas: 1
  selector:
    matchLabels:
      app: bridge-github
  template:
    metadata:
      labels:
        app: bridge-github
    spec:
      containers:
        - name: bridge
          image: ghcr.io/prism-gateway/bridge
          args: ["serve", "--port", "3001", "--", "npx", "@modelcontextprotocol/server-github"]
          ports:
            - containerPort: 3001
          env:
            - name: GITHUB_PERSONAL_ACCESS_TOKEN
              valueFrom:
                secretKeyRef:
                  name: github-token
                  key: token
          livenessProbe:
            httpGet:
              path: /health
              port: 3001
          resources:
            requests:
              memory: "64Mi"
              cpu: "50m"
            limits:
              memory: "256Mi"
              cpu: "500m"
---
apiVersion: v1
kind: Service
metadata:
  name: bridge-github
spec:
  selector:
    app: bridge-github
  ports:
    - port: 3001
      targetPort: 3001
```

Then reference in Prism's config as `"url": "http://bridge-github:3001/mcp"`.

## Reverse Proxy

### Nginx

```nginx
upstream prism {
    server 127.0.0.1:8080;
}

server {
    listen 443 ssl;
    server_name prism.example.com;

    ssl_certificate     /etc/ssl/prism.crt;
    ssl_certificate_key /etc/ssl/prism.key;

    location /mcp {
        proxy_pass http://prism;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # MCP Streamable HTTP uses SSE for server-to-client streaming
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 300s;
    }

    location /.well-known/oauth-protected-resource {
        proxy_pass http://prism;
    }

    # Block admin API from external access
    location /admin {
        deny all;
    }
}
```

### Caddy

```
prism.example.com {
    reverse_proxy /mcp* localhost:8080 {
        flush_interval -1
    }
    reverse_proxy /.well-known/* localhost:8080
}
```

## Production Checklist

### Security

- [ ] **TLS termination** — Prism itself doesn't terminate TLS. Use a reverse proxy (nginx, Caddy, cloud LB) or a sidecar.
- [ ] **OAuth enabled** — Don't use API key auth in production. Configure OAuth 2.1 with a proper authorization server.
- [ ] **Audit logging enabled** — Write to a file or stdout for log aggregation. Every tool call should be traceable.
- [ ] **Admin API not exposed** — The admin port (`:9086`) should only be reachable from internal networks or monitoring systems.
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
- [ ] **Admin API monitoring** — Scrape `/backends` to track backend health and connection status.
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
| 9086 | Admin API (health, status) | Internal only |

Both ports are configurable via `listen` and `admin`.
