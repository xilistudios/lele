# Deployment

This page covers practical local deployment considerations for running Lele continuously.

## Main Processes

Typical long-running setup:

```bash
lele gateway
lele web start
```

## What `gateway` Starts

The gateway runtime initializes:

- the agent loop
- enabled channels
- cron service
- heartbeat service
- device service
- config watcher
- health server

## Health Endpoints

The gateway exposes:

- `/health`
- `/ready`

on the configured gateway host and port.

## Local Background Web Process

`lele web start` runs the embedded web app in the background and writes:

- PID file: `~/.lele/web.pid`
- log file: `~/.lele/logs/web.log`

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

## Related Docs

- `docs/logging-and-observability.md`
- `docs/security-and-sandbox.md`
- `docs/troubleshooting.md`
