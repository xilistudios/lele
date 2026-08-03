# Config Reference

This page summarizes the main top-level config sections used by Lele.

Main file:

```text
~/.lele/config.json
```

Example template:

```text
config/config.example.json
```

## Top-Level Sections

- `agents`
- `bindings`
- `session`
- `channels`
- `providers`
- `tools`
- `heartbeat`
- `devices`
- `gateway`
- `logs`

## `agents`

Contains:

- `defaults`
- `list`

See `docs/agents-models-providers.md`.

## `bindings`

Routes a conversation source to a named agent.

## `session`

Controls ephemeral session behavior and identity-link features.

## `channels`

Contains all channel configs such as:

- `telegram`
- `discord`
- `whatsapp`
- `feishu`
- `slack`
- `line`
- `onebot`
- `qq`
- `dingtalk`
- `maixcam`
- `native`
- `web`

See `docs/channel-setup.md`.

## `providers`

Contains built-in and custom provider entries.

See `docs/agents-models-providers.md`.

## `tools`

Contains:

- `web`
- `cron`
- `exec`

See `docs/tools_configuration.md`.

## `heartbeat`

Controls periodic execution of tasks from `HEARTBEAT.md`.

## `devices`

Controls device event monitoring.

## `gateway`

Controls host and port for the health endpoints exposed by the gateway process.

## `updates`

Controls self-update behavior (CLI `lele update` and Web UI).

```json
{
  "updates": {
    "enabled": true,
    "channel": "stable",
    "repo": ""
  }
}
```

- `enabled` — allow checking for and applying updates (default `true`)
- `channel` — release channel; only `stable` is supported for now
- `repo` — override the GitHub repository (`owner/name`); empty means `xilistudios/lele`

Env overrides: `LELE_UPDATES_ENABLED`, `LELE_UPDATES_CHANNEL`, `LELE_UPDATES_REPO`.

## `logs`

Controls:

- whether logs are enabled
- log path
- retention window
- rotation mode

## Environment Variables

Many config keys can be overridden via environment variables.

Examples:

- `LELE_AGENTS_DEFAULTS_WORKSPACE`
- `LELE_AGENTS_DEFAULTS_MODEL`
- `LELE_CHANNELS_NATIVE_ENABLED`
- `LELE_TOOLS_CRON_EXEC_TIMEOUT_MINUTES`
- `LELE_LOGS_PATH`

## Related Docs

- `docs/agents-models-providers.md`
- `docs/channel-setup.md`
- `docs/tools_configuration.md`
