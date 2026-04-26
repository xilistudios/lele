# Deployment

This page covers practical local deployment considerations for running Lele continuously.

## Main Processes

Typical long-running setup:

```bash
lele gateway
```

## What `gateway` Starts

The gateway runtime initializes:

- the agent loop
- enabled channels
- cron service
- heartbeat service
- device service
- config watcher
- unified HTTP server (API, Web UI, health endpoints)

## Unified Server

All HTTP services run on a single port:

```json
{
  "server": {
    "host": "0.0.0.0",
    "port": 8080
  }
}
```

Services served:
- `/` - Web UI
- `/api/v1/*` - Native channel API
- `/api/v1/ws` - WebSocket
- `/health` - Health check
- `/ready` - Readiness check
- `/webhook/line` - LINE webhook (if enabled)

## Health Endpoints

The gateway exposes:

- `/health` - Basic health status with uptime
- `/ready` - Readiness check (checks all registered health checks)

Both endpoints are available on the unified server port.

## Docker Deployment

Example Dockerfile:

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN make build

FROM alpine:latest
COPY --from=builder /app/lele /usr/local/bin/lele
EXPOSE 8080
CMD ["lele", "gateway"]
```

Example docker-compose:

```yaml
version: '3'
services:
  lele:
    image: lele:latest
    ports:
      - "8080:8080"
    volumes:
      - ~/.lele:/root/.lele
    command: lele gateway
```

## Kubernetes Deployment

Example deployment:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: lele
spec:
  replicas: 1
  selector:
    matchLabels:
      app: lele
  template:
    metadata:
      labels:
        app: lele
    spec:
      containers:
      - name: lele
        image: lele:latest
        ports:
        - containerPort: 8080
        volumeMounts:
        - name: lele-data
          mountPath: /root/.lele
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /ready
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
      volumes:
      - name: lele-data
        persistentVolumeClaim:
          claimName: lele-pvc
```

## Configuration Reloading

The gateway watches the config file and can reload major runtime configuration without a full restart in some cases.

## Backups

Important paths to back up:

- `~/.lele/config.json`
- `~/.lele/workspace/`
- optionally `~/.lele/logs/`

## Production Notes

- keep logs on persistent storage if you need auditability
- keep the workspace on reliable local storage
- prefer explicit provider/model settings for reproducible behavior
- review CORS and token expiry when exposing native/web access beyond localhost-style setups
- single port simplifies firewall and reverse proxy configuration

## Related Docs

- `docs/logging-and-observability.md`
- `docs/security-and-sandbox.md`
- `docs/troubleshooting.md`