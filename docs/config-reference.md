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

### Crash-durability flags

All three flags are opt-in (default off) and live under `session` in
`config.json`:

```json
{
  "session": {
    "durable_inbound": true,
    "durable_outbound": true,
    "resume_enabled": true
  }
}
```

- `durable_inbound` — every external inbound message is written to the
  SQLite spool before being published to the bus; whatever was not fully
  processed is replayed after a restart.
- `durable_outbound` — every agent reply is written to the spool before
  being published; undelivered replies are re-sent after a restart.
- `resume_enabled` — active-turn resume. The gateway checkpoints each
  in-flight inbound turn (phase, iteration, agent, model) in the session
  state store. When a restart interrupts a turn, the durable inbound
  replay of that same message **resumes** it from the last persisted step
  instead of re-running it from scratch: the user message is not appended
  twice, pending tool calls are healed (not blindly re-executed), and the
  model is warned about subagents that died with the process.
  **Prerequisite:** requires `durable_inbound` — the replay is the resume
  trigger, so without the inbound spool there is nothing to resume from.

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
