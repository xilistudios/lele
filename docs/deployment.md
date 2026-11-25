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

`host: "0.0.0.0"` is a **non-default, explicit opt-in**: `gateway.host` and
`server.host` default to `127.0.0.1` (loopback), so nothing outside the host is
reachable unless a host is named (`gateway.host`, `server.host`,
`LELE_GATEWAY_HOST`/`LELE_SERVER_HOST`).  Reachability from other machines
always requires an explicit non-loopback bind.

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
    environment:
      - LELE_GATEWAY_HOST=0.0.0.0
    volumes:
      - ~/.lele:/root/.lele
    command: lele gateway
```

**The bind host must be explicit in a container.**  A fresh volume has no
`config.json`, so `gateway.host` resolves to the loopback default: the process
binds `127.0.0.1`, the published port answers nothing from outside the
container, and — because no config file exists — no warning is logged either.
`LELE_GATEWAY_HOST=0.0.0.0` (or `"gateway": {"host": "0.0.0.0"}` in the mounted
config) opts into the container's own network namespace; the published port is
the boundary that decides who reaches it.

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
        env:
        - name: LELE_GATEWAY_HOST
          value: "0.0.0.0"
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

`LELE_GATEWAY_HOST` is required here for the same reason as in the compose
recipe: the `httpGet` probes connect to the **pod IP**, which only answers when
the process binds `0.0.0.0`, and an empty `persistentVolumeClaim` carries no
config file to say so.  Without it both probes fail (the image's `HEALTHCHECK`
uses `localhost`, so the container can still look healthy while every probe and
client connection is refused).

## Configuration Reloading

The gateway watches the config file and can reload major runtime configuration without a full restart in some cases.

## Self-Update

Lele can update itself from GitHub Releases (stable channel only), from both the CLI and the Web UI.

CLI:

```bash
lele update --check     # check only
lele update             # install latest (with confirmation)
lele update --rollback  # restore previous binary from backup
```

Web UI: Settings → Updates (check, install with progress, rollback).

Behavior:

- Downloads are verified against the release `checksums.txt` before install.
- Install is atomic: the new binary is written to a temp file and swapped in place.
- The previous binary is backed up to `~/.lele/backups/` (newest 3 kept).
- Restart uses the detected supervisor: systemd user service (`systemctl --user restart lele`), or self-exec fallback.
- Dev builds (`version == "dev"`) are skipped unless `--force` is passed.
- Docker deployments are detected and refused (update the image instead).

Disable with `updates.enabled: false` in config (or `LELE_UPDATES_ENABLED=false`).

## Backups

Important paths to back up:

- `~/.lele/config.json`
- `~/.lele/workspace/`
- optionally `~/.lele/logs/`

## Production Notes

- keep logs on persistent storage if you need auditability
- keep the workspace on reliable local storage
- prefer explicit provider:model settings for reproducible behavior
- review CORS and token expiry when exposing native/web access beyond localhost-style setups
- single port simplifies firewall and reverse proxy configuration

## Related Docs

- `docs/logging-and-observability.md`
- `docs/security-and-sandbox.md`
- `docs/release-signing.md`
- `docs/troubleshooting.md`