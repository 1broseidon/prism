# Deploying Prism

## Quick Reference

```bash
# Build
go build -o prism ./cmd/prism

# Validate config before deploying
./prism -config config.json  # fails fast on invalid config

# Run
./prism -config /etc/prism/config.json
```

Prism is a single static binary. No runtime dependencies. Runs on any Linux/macOS/Windows amd64 or arm64 system.

## Systemd

### Install

```bash
# Build and install binary
go build -o /usr/local/bin/prism ./cmd/prism

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

### Dockerfile

```dockerfile
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /prism ./cmd/prism

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /prism /usr/local/bin/prism
EXPOSE 8080 9090
ENTRYPOINT ["prism"]
CMD ["-config", "/etc/prism/config.json"]
```

### Build and Run

```bash
docker build -t prism .
docker run -d \
  --name prism \
  -p 8080:8080 \
  -p 9090:9090 \
  -v ./config.json:/etc/prism/config.json:ro \
  -e GITHUB_TOKEN="Bearer ghp_xxx" \
  prism
```

### Docker Compose

```yaml
services:
  prism:
    build: .
    ports:
      - "8080:8080"   # MCP gateway
      - "9090:9090"   # Admin API
    volumes:
      - ./config.json:/etc/prism/config.json:ro
      - ./audit:/var/log/prism
    environment:
      - GITHUB_TOKEN=Bearer ghp_xxxxxxxxxxxx
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:9090/health"]
      interval: 10s
      timeout: 3s
      retries: 3
```

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
      "listen_addr": ":8080",
      "admin_addr": ":9090",
      "servers": [
        {
          "id": "github",
          "url": "http://github-mcp:3001/mcp",
          "namespace": "github",
          "credentials": {
            "type": "file",
            "header": "Authorization",
            "path": "/secrets/github/token"
          }
        },
        {
          "id": "vault-backed",
          "url": "http://vault-mcp:3002/mcp",
          "namespace": "infra",
          "credentials": {
            "type": "command",
            "header": "Authorization",
            "command": "cat /var/run/secrets/kubernetes.io/serviceaccount/token",
            "ttl": "10m"
          }
        }
      ],
      "auth": {
        "oauth": {
          "issuer_url": "https://auth.example.com/realms/mcp",
          "audience": "https://prism.example.com",
          "required_scopes": ["mcp:connect"]
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
            - containerPort: 9090
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
      port: 9090
      targetPort: admin
```

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
- [ ] **Admin API not exposed** — The admin port (`:9090`) should only be reachable from internal networks or monitoring systems.
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
| 9090 | Admin API (health, status) | Internal only |

Both ports are configurable via `listen_addr` and `admin_addr`.
