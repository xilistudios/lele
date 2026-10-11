# Logging And Observability

Lele writes local logs and exposes simple health endpoints for operational visibility.

## Log Files

Default log path:

```text
~/.lele/logs/
```

Typical files:

- `info-YYYY-MM-DD.log`
- `errors-YYYY-MM-DD.log`
- `web.log`

## Log Behavior

Routing is strictly by level, with no duplication:

- `info-YYYY-MM-DD.log` — INFO entries only
- `errors-YYYY-MM-DD.log` — WARN, ERROR and FATAL entries

The WebUI Logs viewer and the REST API use the same files: `level=info` reads
`info-<date>.log`, `level=error` reads `errors-<date>.log`.

Files rotate daily (the `logs.rotation` config value is currently ignored;
rotation is always daily).

## Config

```json
{
  "logs": {
    "enabled": true,
    "path": "~/.lele/logs",
    "max_days": 7,
    "rotation": "daily"
  }
}
```

## Debugging Gateway Startup

Useful command:

```bash
lele gateway --debug
```

## Health Endpoints

The gateway exposes:

- `/health`
- `/ready`

on the configured host and port.

## What To Check First

### Provider issues

Look at:

- `errors-YYYY-MM-DD.log`
- `lele auth status`
- `lele status`

### Channel issues

Look at:

- `info-YYYY-MM-DD.log`
- `errors-YYYY-MM-DD.log`
- channel-specific config values

## Related Docs

- `docs/deployment.md`
- `docs/troubleshooting.md`
