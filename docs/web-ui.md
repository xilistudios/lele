# Web UI

Lele includes a built-in local web UI served from embedded assets.

## What It Depends On

The web UI works together with the native channel API.

Typical local setup:

```bash
lele gateway
lele web start
```

## Web Commands

```bash
lele web start
lele web start --host 0.0.0.0 --port 3005
lele web status
lele web stop
```

## Default Behavior

- default host: `0.0.0.0`
- default port: `3005`
- PID file: `~/.lele/web.pid`
- web log: `~/.lele/logs/web.log`

## Configuration

```json
{
  "channels": {
    "web": {
      "enabled": true,
      "host": "0.0.0.0",
      "port": 3005
    },
    "native": {
      "enabled": true,
      "host": "127.0.0.1",
      "port": 18793
    }
  }
}
```

## Pairing Flow

Typical flow:

1. generate a pairing PIN
2. open the web app
3. pair using the PIN
4. use the native API token/session behind the scenes

Generate a PIN:

```bash
lele client pin --device "Browser"
```

## Operational Notes

- the web server serves embedded assets from the built binary
- if assets are missing, rebuild with `make build`
- the web UI depends on the native channel for chat/config/session APIs

## Related Docs

- `docs/client-api.md`
- `docs/installation-and-onboarding.md`
- `docs/troubleshooting.md`
