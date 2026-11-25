# Channel Setup

Lele can expose the same agent runtime through multiple channels.

This document summarizes the configuration shape and operational notes for each current integration.

## General Pattern

Channels live under `channels` in `~/.lele/config.json`.

Example:

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "allow_from": ["123456789"]
    }
  }
}
```

After configuring one or more channels, start:

```bash
lele gateway
```

## Telegram

Required fields:

- `enabled`
- `token`
- `allow_from`

Notes:

- initialized only when `enabled=true` and `token` is present
- supports approval flows
- can use Groq voice transcription when Groq is configured

## Discord

Required fields:

- `enabled`
- `token`
- `allow_from`

Notes:

- initialized only when token is present
- can use Groq voice transcription

## WhatsApp

Required fields:

- `enabled`
- `bridge_url`
- `allow_from`

Notes:

- depends on an external bridge endpoint

## Feishu

Fields:

- `enabled`
- `app_id`
- `app_secret`
- `encrypt_key`
- `verification_token`
- `allow_from`

## Slack

Fields:

- `enabled`
- `bot_token`
- `app_token`
- `allow_from`

Notes:

- can use Groq voice transcription

## LINE

Fields:

- `enabled`
- `channel_secret`
- `channel_access_token`
- `webhook_host`
- `webhook_port`
- `webhook_path`
- `allow_from`

Notes:

- requires webhook exposure

## OneBot

Fields:

- `enabled`
- `ws_url`
- `access_token`
- `reconnect_interval`
- `group_trigger_prefix`
- `allow_from`

Notes:

- can use Groq voice transcription

## QQ

Fields:

- `enabled`
- `app_id`
- `app_secret`
- `allow_from`

## DingTalk

Fields:

- `enabled`
- `client_id`
- `client_secret`
- `allow_from`

## MaixCam

Fields:

- `enabled`
- `host`
- `port`
- `allow_from`

## Native

Fields:

- `enabled`
- `host`
- `port`
- `token_expiry_days`
- `pin_expiry_minutes`
- `max_clients`
- `cors_origins`
- `session_expiry_days`
- `max_upload_size_mb`
- `upload_ttl_hours`
- `rate_limit`

Notes:

- powers the local REST + WebSocket client API
- used by the built-in web UI and native clients
- supports pairing PINs and bearer-token auth
- rate limiting on native routes is **enabled by default**
  (`channels.native.rate_limit.enabled: true`) with per-source-IP budgets of
  10 PINs/min, 5 pairings/min, 20 token refreshes/min, 600 API requests/min and
  600 WebSocket messages/min.  An explicit `enabled: false` turns every traffic
  limiter off; individual rates can be overridden and `0` or omitted means the
  built-in default.  Requires a restart.  The authentication log-line sampler
  (6/min) is **not** a traffic limiter and remains always active.
- **the gateway binds loopback by default** — `gateway.host` defaults to
  `127.0.0.1` and wins over `channels.native.host`, which defaults to
  `127.0.0.1` but does **not** control the bind address.  **Migration:** the
  `gateway.host` default used to be `0.0.0.0`, so a config that never named a
  host stops answering LAN clients after upgrading.  Set `gateway.host` (or
  `server.host`) to `0.0.0.0` to restore LAN access; an explicit value is
  preserved exactly as written (the settings UI keeps a host the file names,
  even when it equals the default), and `--desktop` keeps forcing loopback.  A
  gateway started from a config file that names no host logs a per-startup
  notice that the bind is loopback-only and how to opt into LAN access — fresh
  installs included, because onboarding writes no host either — and any
  non-loopback bind with the native channel enabled logs a warning whether rate
  limiting is on or not,
  because the unauthenticated `POST /api/v1/auth/pair` endpoint (6-digit PIN,
  5-minute expiry) is then reachable from the network.  **Keep rate limiting
  enabled on any machine reachable from an untrusted network.**

## Web

Fields:

- `enabled`

Notes:

- the web UI is served by `lele gateway` on the unified server, so the port and
  bind address come from `server.port`/`server.host` (then
  `gateway.port`/`gateway.host`)
- `channels.web.host` and `channels.web.port` are not configuration keys: they
  are ignored, and the gateway logs nothing about them
- enabling Web usually implies using the native channel as the local API backend

## Which Services To Run

### Messaging channels & Web UI

Run:

```bash
lele gateway
```

## Channel Initialization Rules

The gateway only initializes a channel when its required config is present. For example:

- Telegram requires a token
- Discord requires a token
- WhatsApp requires a bridge URL
- OneBot requires a WebSocket URL
- Native requires `enabled=true`

## Related Docs

- `docs/client-api.md`
- `docs/web-ui.md`
- `docs/troubleshooting.md`
