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

Notes:

- powers the local REST + WebSocket client API
- used by the built-in web UI and native clients
- supports pairing PINs and bearer-token auth

## Web

Fields:

- `enabled`
- `host`
- `port`

Notes:

- the web UI is served by `lele web start`
- enabling Web usually implies using the native channel as the local API backend

## Which Services To Run

### Messaging channels

Run:

```bash
lele gateway
```

### Web UI

Run:

```bash
lele gateway
lele web start
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
