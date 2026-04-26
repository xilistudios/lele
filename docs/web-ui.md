# Web UI

Lele includes a built-in local web UI served from embedded assets.

## What It Depends On

The web UI works together with the native channel API.

Typical local setup:

```bash
lele gateway
```

The web UI is automatically served by the unified gateway server on a single port.

## Configuration

With the unified server architecture, all HTTP services (API, Web UI, health checks, webhooks) run on a single port:

```json
{
  "server": {
    "host": "0.0.0.0",
    "port": 8080
  }
}
```

This replaces the previous separate ports configuration:

```json
// DEPRECATED - use server.host:port instead
{
  "gateway": { "port": 18790 },
  "channels": {
    "web": { "port": 3005 },
    "native": { "port": 18793 }
  }
}
```

The unified server serves:
- `/` - Web UI (SPA)
- `/api/v1/*` - Native channel API
- `/health`, `/ready` - Health checks
- `/api/v1/ws` - WebSocket

## Pairing Flow

Typical flow:

1. generate a pairing PIN
2. open the web app at `http://host:port`
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
- CORS is automatically handled for localhost origins

## Related Docs

- `docs/client-api.md`
- `docs/installation-and-onboarding.md`
- `docs/troubleshooting.md`