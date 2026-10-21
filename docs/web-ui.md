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

## Message Ordering (maintainer notes)

The chat list is a merge of two sources with different id schemes:

- canonical history over HTTP (react-query cache, content-hash ids), and
- live WebSocket state (ephemeral UUIDs).

`web/src/hooks/messageMerge.ts` reconciles them. Assistants are matched by
**content identity** (`web/src/lib/messageIdentity.ts`), never by position: the
number of live copies still around depends on timing (the previous turn's live
copies are only stripped once the `history.updated` refetch lands, and a running
tool turn contributes a *completed* assistant per finished iteration), so any
position-based rule eventually pairs a live copy with the wrong base message and
transfers its React key — the bubbles swap places and the list re-orders itself
mid-session.

If you touch that file, keep these suites green (they pin the real production
shapes, not simplifications):

```bash
cd web
bun test src/hooks/messageOrdering.test.ts \
         src/hooks/messageOrderingWindow.test.ts \
         src/hooks/messagePairing.test.ts
```

### Reproducing an ordering bug in a browser

An opt-in monitor (`web/src/lib/orderingMonitor.ts`) records every WS event,
every history snapshot and every merge, and reports the invariants whose
violation *is* the bug (`stable-order`, `duplicate-key`, `base-order`,
`stranded-copy`):

```js
// in the browser console, before reproducing
localStorage.setItem('lele.orderingDebug', '1')
// …reload, reproduce…
window.__leleOrderingReport().print()   // human-readable
window.__leleOrderingReport()           // { violations, trace, mergeCount }
```

The last report is also stored in `localStorage` under `lele.orderingReport`, so
a reload does not lose the evidence. When the flag is off the monitor is a
single boolean check per merge.

## Operational Notes

- the web server serves embedded assets from the built binary
- if assets are missing, rebuild with `make build`
- the web UI depends on the native channel for chat/config/session APIs
- CORS is automatically handled for localhost origins

## Related Docs

- `docs/client-api.md`
- `docs/installation-and-onboarding.md`
- `docs/troubleshooting.md`