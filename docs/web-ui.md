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
         src/hooks/messagePairing.test.ts \
         src/hooks/stableIdWindow.test.ts
```

### React keys must survive the sliding history window

The render key of a bubble is `stableId ?? id` (`MessageList` /
`virtuoso.computeItemKey`). `stableId` is the ephemeral WebSocket id that the
durable registry (`web/src/hooks/stableIdRegistry.ts`) re-attaches to the
persisted copy, so the bubble is not remounted when history catches up.

That registry is keyed on a **content identity** — role + the first 200 chars of
the content (or of the reasoning, for a text-only-less assistant iteration) —
and it holds one entry per copy this page confirmed. The backend serves history
as a sliding window of the last `limit` messages, so occurrences **must be
counted from the NEWEST end of the page** (`lookupStableIdFromEnd`), never from
its start: when the oldest copy of a repeated identity slides out, a start-based
index makes every remaining copy read its *predecessor's* id, so the React key of
message N is handed to message N+1. React/Virtuoso then reconcile the wrong DOM
node into the slot — bubbles swap places, an old message appears where the newest
one belongs, and thinking blocks pile up as a run of copies until a reload clears
the registry. Tools made that systematic (their identity is the tool RESULT,
identical across `ls`/`git status`/failing-test calls) and every thinking model
repeats reasoning verbatim.

`src/hooks/stableIdWindow.test.ts` pins the invariant: a render key may leave the
page (its message scrolled out) but must never be re-pointed at another message.

### Reproducing an ordering bug in a browser

An opt-in monitor (`web/src/lib/orderingMonitor.ts`) records every WS event,
every history snapshot and every merge, and reports the invariants whose
violation *is* the bug:

- `key-identity` — a render key that already denoted one message now denotes a
  different one. This is the ROOT CAUSE and the first thing to look at.
- `stable-order` — the relative order of two messages flipped between renders
  (the literal user-visible symptom).
- `duplicate-key` — two entries share one render key.
- `base-order` — the merge reordered canonical history.
- `stranded-copy` — a live copy rendered as its own entry while history already
  holds the same message (the "old block below a newer answer" shape).

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

The evidence arrays (`merged`, `base`, `streaming`) hold one JSON entry per
message — `["role","renderKey","identity text"]` — so a saved report can be
checked mechanically for "did a key change meaning?", which the old
colon-joined form could not (both keys and text contain `:`).

## Operational Notes

- the web server serves embedded assets from the built binary
- if assets are missing, rebuild with `make build`
- the web UI depends on the native channel for chat/config/session APIs
- CORS is automatically handled for localhost origins

## Related Docs

- `docs/client-api.md`
- `docs/installation-and-onboarding.md`
- `docs/troubleshooting.md`