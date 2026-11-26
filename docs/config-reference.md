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

### `thinking_level`

Default reasoning effort, accepted in `agents.defaults` and per agent in
`agents.list`.

- values: `"off"`, `"low"`, `"medium"`, `"high"`, `"xhigh"`, `"max"`
- default: unset, which means *inherit* — the field is optional and is never
  written as an empty string
- env override (defaults only): `LELE_AGENTS_DEFAULTS_THINKING_LEVEL`

Resolution order for the effort sent to the provider:

1. session override (`/think`, Web UI thinking chip)
2. `agents.list[].thinking_level`
3. `agents.defaults.thinking_level`
4. provider-model `reasoning` config (`providers.<name>.models.<alias>.reasoning`)

`"off"` from any of the first three layers sends an explicit
`reasoning: {"enabled": false}` so a proxy with a server-side reasoning default
cannot re-enable it (`"off"` is not a valid per-model `reasoning.effort` value).
`"low"` / `"medium"` / `"high"` / `"xhigh"` / `"max"` always send `enabled: true` with the effort. A
`/think` override is per session and is cleared by `/think default` (falling
back to the agent level) and by `/clear` / `/new`; the agent-level value stays.

Per-model `providers.<name>.models.<alias>.thinking_type` selects the wire
dialect used to render that enable/disable (`"auto"`, `"deepseek"`, `"openai"`,
`"openrouter"`, `"qwen"`, `"none"`). Set it when a model's endpoint uses a
non-standard think system — e.g. `"deepseek"` for Xiaomi MiMo so `"off"` sends
`thinking: {"type": "disabled"}`. See
`docs/agents-models-providers.md#thinking_type`.

See `docs/agents-models-providers.md#thinking-level`.

### `subagent_retention_minutes`

How long a **finished** subagent task stays tracked in memory (and keeps its
`native:<parent>:subagent-N` session resident), in `agents.defaults`.

- default: `5` (minutes) — the same 5-minute default the subagent manager
  carries internally
- units: minutes
- env override (defaults only): `LELE_AGENTS_DEFAULTS_SUBAGENT_RETENTION_MINUTES`
- what it does: a task in a terminal status (`completed`, `failed`,
  `cancelled`, `not_done`) is reaped once it has been terminal for longer than
  this window — the task leaves the in-memory task map and its session is
  evicted from memory. Nothing is deleted: the session rows stay in SQLite and
  the session is reloaded on demand. A periodic sweeper (once a minute; that is
  the poll cadence, not the window) does the reaping, so a finished task is
  reaped even when the chat stops spawning new ones and never lingers more than
  one extra interval. The same sweep also runs on the next spawn.
- `0` (and any negative value) does **not** disable the sweep. The value is
  only applied when it is `> 0`, so `0`/negative keeps the effective default of
  5 minutes; the same happens through the editable document behind the Web UI,
  where `0` cannot be told apart from "unset" and is rewritten as the default.
  Turning the sweep off through configuration is deliberately not offered — an
  unset knob must never let the task map grow without bound again.
- Round-trip: the field is carried in `agents.defaults` of the editable config
  document, so the Web UI settings page reads and writes the same value the file
  holds (an explicit value survives a Web UI save).

### `memory_max_lines`

Cap on the number of lines of the workspace `MEMORY.md` written by the
`update_memory` tool, in `agents.defaults`.

- default: `500` (lines)
- units: lines
- env override (defaults only): `LELE_AGENTS_DEFAULTS_MEMORY_MAX_LINES`
- what it does: `update_memory` appends to (default) or replaces `MEMORY.md`
  and counts the lines of the *resulting* file before writing. If the write
  would exceed this cap it is rejected and nothing is written; the error tells
  the agent to compact by rewriting the whole file with `mode: "replace"` in
  `memory_max_lines` lines or fewer. Successful writes report the used/cap
  budget (`n/cap`) back to the model.
- `<= 0` (`0` or negative) resolves to the 500-line default when the agent
  instance is built — the same rule `max_read_lines` uses. Through the
  editable document behind the Web UI/TUI, `0` cannot be told apart from
  "unset" and is rewritten as the default.
- byte ceiling: independent of this line cap, `update_memory` enforces a
  hard, non-configurable 512 KiB (524288 bytes) ceiling on the resulting
  `MEMORY.md`. A write that would exceed it is rejected with nothing
  written — even when the line cap would pass it, because a single
  arbitrarily long line counts as one line and `MEMORY.md` is inlined into
  every system prompt.
- Round-trip: the field is carried in `agents.defaults` of the editable config
  document, so the Web UI and TUI settings pages read and write the same value
  the file holds (an explicit value survives a save).

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

Both spools are drained by a poll loop that ticks **once a second** at rest
(`durable.PumpInterval` / `durable.OutboundPumpInterval`), which is the only
periodic work they do when nothing is queued — a sub-second ticker meant four
`Stats` + claim passes per second per spool on an idle gateway. The latency this
trades away is bounded and only visible when a row is enqueued by something that
cannot wake the pump: an **outbound** row enqueued through the normal path kicks
the pump immediately (`FlushPeer`), so a reply is still delivered without waiting
for the tick, and an **inbound** row published right after a live enqueue is
processed on the next tick, i.e. within one second. Retries keep their own
backoff schedule and are not delayed by the ticker beyond that one second.

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

### `channels.native.rate_limit`

Controls per-source-IP rate limiting on the native REST API, plus a per-client budget for WebSocket messages.

- `enabled` (default `true`) — master switch; all limits are inactive when off
- `pin_per_minute` (default `10`) — `GET /api/v1/auth/pin` requests per minute
- `pair_per_minute` (default `5`) — `POST /api/v1/auth/pair` attempts per minute
- `refresh_per_minute` (default `20`) — `POST /api/v1/auth/refresh` renewals per minute
- `api_per_minute` (default `600`) — all other API requests per minute
- `ws_messages_per_minute` (default `600`) — WebSocket frames per minute

Rate limiting is on for every config that does not opt out — a JSON `null` (or
an absent key) means "unset" and keeps the default, while an explicit
`"enabled": false` is preserved.  **Migration:** two values changed for configs
that leave them unset: the switch flipped from `false` to `true`, and the
API/WebSocket budgets were raised from `120` to `600` per minute so ordinary
browser traffic is not throttled.  A config that names no per-minute value now
inherits `600`/`600`; the PIN, pairing and refresh budgets are unchanged.  Set
`"enabled": false` to restore the previous no-throttling behavior.

Setting a field to `0` (or omitting it) means "use the built-in default"
listed above.  Negative values are rejected by validation.

Env overrides: `LELE_CHANNELS_NATIVE_RATE_LIMIT_ENABLED`,
`LELE_CHANNELS_NATIVE_RATE_LIMIT_PIN_PER_MINUTE`,
`LELE_CHANNELS_NATIVE_RATE_LIMIT_PAIR_PER_MINUTE`,
`LELE_CHANNELS_NATIVE_RATE_LIMIT_REFRESH_PER_MINUTE`,
`LELE_CHANNELS_NATIVE_RATE_LIMIT_API_PER_MINUTE`,
`LELE_CHANNELS_NATIVE_RATE_LIMIT_WS_MESSAGES_PER_MINUTE`.

See `docs/client-api.md` for the rationale behind separate pairing and
renewal buckets.

## `providers`

Contains built-in and custom provider entries.

See `docs/agents-models-providers.md`.

Provider support is mostly behavior, not extra fields. Two examples that need
no configuration, and one field that is opt-in:

- The OpenAI-compatible and Anthropic Messages transports send
  `User-Agent: lele/<version>` on every inference call. The OAuth/CLI provider
  families deliberately do **not**: Claude OAuth (`anthropic` with
  `auth_method: oauth`, which sends the CLI UA `claude-cli/2.x (external,
  sdk-cli)`), Gemini Code Assist (`GeminiCLI-tui/…`) and Antigravity
  (`antigravity/…`) send their official client's User-Agent because those
  endpoints require impersonation — do not "unify" them, a `lele/…` value there
  would be rejected. The `claude-code`/`codex-cli` CLI transports are not in
  this list at all: lele writes no HTTP request for them.
- Entries that resolve to **OpenCode Go** — by explicit name or `type`
  (`opencode_go` / `opencode-go`) or by an `api_base` whose path is an OpenCode
  Go route (`opencode.ai/zen/go` or `opencode.ai/inference/go`; the plain
  `https://opencode.ai/zen/v1` Zen base does not match) — additionally get an
  `x-opencode-session` header on inference calls: a SHA-256 hash of the
  conversation session key, stable per conversation and changed by `/new`
  (calls with no session key share one stable unkeyed bucket, the hash of the
  empty key). The gateway requires it (`400 MissingSessionID` without it,
  checked before authentication): when detection came from the explicit
  name/type, lele fails the call locally with a clear error instead of sending
  a request the gateway would reject; when detection came only from
  `api_base`, the header is opportunistic — sent when a session id is present,
  silently omitted otherwise. The credentials and base live in the ordinary
  named-provider entry, exactly like `opencode`; there is no dedicated field.
  See the OpenCode Go section of `docs/agents-models-providers.md`.
- `headers` is the one opt-in field: an optional per-provider map of extra
  request headers on that provider's inference calls (both API-key transports,
  streaming and not; never `GET /models`, and never read by the OAuth/CLI
  families, which build their own clients). Two placeholders are legal:
  `{{session}}` (the same 64-hex id lele sends natively as `x-opencode-session`)
  and `{{ENV_NAME[:default]}}`, expanded at load. Applied last, so a custom
  entry wins over transport defaults; validated at provider-JSON decode
  and, per field (`invalid_header`), on the editable document. Editable from the
  TUI (`/providers` → *Edit provider* → **Headers**, a masked JSON object step:
  blank keeps, `{}` clears, validated on the spot); the Web UI has no headers
  editor but its document round-trips the map. See
  **Custom headers** in `docs/agents-models-providers.md` (`#custom-headers`).

### `providers.claude-code` (CLI transport)

`claude-code` — aliases `claude-cli`, `claudecode` — runs the user's Claude
Code install through the official Claude Agent SDK and bills the **Claude
subscription** `claude` is logged in with. There is no `api_key`, no `api_base`
and no `proxy`; the catalog marks the type `base_less`. A `providers.claude-code`
entry is therefore optional (`"claude-code": {}` is accepted and changes
nothing) — naming the provider in the agent defaults is what selects it:

```json
{
  "agents": {
    "defaults": {
      "provider": "claude-code",
      "model": "claude-code"
    }
  }
}
```

`model: "claude-code"` is the provider's sentinel (= let Claude Code pick its
default); `"claude-code:claude-sonnet-4-5"` passes that model id through to the
SDK.

Requirements and environment variables:

- The bridge installed first — it lives in its own repository,
  [`xilistudios/lele-claude-bridge`](https://github.com/xilistudios/lele-claude-bridge),
  and its installer drops it in `<lele-dir>/claude-bridge`:
  `curl -fsSL https://raw.githubusercontent.com/xilistudios/lele-claude-bridge/main/install.sh | sh`
  (pin with `... | sh -s -- --ref v0.1.0`). `LELE_CLAUDE_BRIDGE_PATH` points at
  another directory (or directly at its `bridge.mjs`), and `LELE_CONFIG_DIR`
  relocates the default. `bridge.mjs` and `bridge_lib.mjs` must sit side by side.
- Node.js ≥ 18.14 (`LELE_NODE_BIN` overrides the binary, default `node`).
- The SDK, installed by that script next to the bridge (pinned `0.3.329`). A
  global `npm install -g @anthropic-ai/claude-agent-sdk` still works as the
  manual alternative: `CLAUDE_AGENT_SDK_PATH` then points at the `node_modules`
  directory (or the package directory) that holds it, searched first — it takes
  effect when the bridge has no side-by-side `node_modules`, because the bare
  specifier resolves that one before any candidate.
- A logged-in `claude` session: run `claude` and log in with the subscription.
  `CLAUDE_CODE_PATH` pins a specific CLI binary; empty means `PATH`, then the
  SDK's bundled CLI.

The provider never uses an API key, so lele strips `ANTHROPIC_API_KEY`,
`ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_CUSTOM_HEADERS`,
`ANTHROPIC_MODEL`, `ANTHROPIC_DEFAULT_{SONNET,OPUS,HAIKU}_MODEL`,
`CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX`,
`CLAUDE_CODE_SKIP_BEDROCK_AUTH` and `CLAUDE_CODE_SKIP_VERTEX_AUTH` from the
bridge's environment, so billing can never fall back to the API. lele's own
tools execute in lele (built-in Claude Code tools are disabled and tool calls
are captured and interrupted) and every call replays the transcript statelessly
— no Claude Code session files, no `CLAUDE.md`/`settings.json` leakage. See
**Claude Code** in `docs/agents-models-providers.md` for the full behaviour.

## `tools`

Contains:

- `web`
- `cron`
- `exec`

See `docs/tools_configuration.md`.

## `mcp.json` (MCP servers)

MCP servers are not part of `config.json`: lele merges up to three optional
`mcp.json` files instead (project > agent > global):

- project: `<process cwd>/.lele/mcp.json` (cwd captured once at startup, shared by every agent of the process)
- agent: `<agent workspace>/mcp.json`
- global: `<lele dir>/mcp.json` (`~/.lele`, or `$LELE_CONFIG_DIR` when set)

Each file uses `{"mcpServers": {"<name>": { … }}}` (stdio `command` **or**
remote `url`+`type`) and expands `${VAR}` from the environment. The three
write surfaces are `lele mcp add`, the TUI's `/mcp` → `N` wizard and the
WebUI's per-layer raw editor; see `docs/mcp.md`.

## `heartbeat`

Controls periodic execution of tasks from `HEARTBEAT.md`.

## `devices`

Controls device event monitoring.

## `gateway`

Controls host and port for the health endpoints exposed by the gateway process.

```json
{
  "gateway": {
    "host": "127.0.0.1",
    "port": 18790
  }
}
```

- `host` — bind address of the gateway server; defaults to `127.0.0.1` (loopback)
- `port` — bind port; defaults to `18790`

The bind address is resolved as `server.host` → `gateway.host` →
`channels.native.host`, and then `127.0.0.1` when none of them is set, so
`channels.native.host` alone does not expose the server on the network.  Env
overrides: `LELE_GATEWAY_HOST`, `LELE_GATEWAY_PORT` (plus `LELE_SERVER_HOST` and
`LELE_CHANNELS_NATIVE_HOST` for the higher-priority fields).

**Migration:** `gateway.host` used to default to `0.0.0.0`, so a `config.json`
that never named a host only accepts loopback clients after the upgrade.  Set
`"host": "0.0.0.0"` (here or under `server`) to restore LAN access — an explicit
value is preserved exactly as written (it also survives a save from the settings
UI, which never deletes a host the file names), and only an unset host falls back
to the loopback default.  `lele gateway` logs a notice on every start when a
config file names no host and the effective bind is loopback — the same line
appears for a fresh install created by onboarding, so it states the posture
rather than claiming something changed — and it also warns on any non-loopback
bind with `channels.native.enabled`.

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

## Secret placeholders

Any JSON string value in `config.json` may hold a placeholder instead of a
credential, so the file itself never carries the secret — with one exception,
spelled out below:

- `{{ENV_NAME}}` / `{{ENV_NAME:default}}` — replaced by the environment
  variable of that name when the config is loaded (the default is used when the
  variable is unset or empty). The bare `{{ENV_NAME}}` form keeps the file
  credential-free; the `{{ENV_NAME:default}}` fallback **is** a credential stored
  in the file, so the API treats it as a literal secret: `GET
  /api/v1/config` masks it behind `has_default: true`, and a save that does not
  resend it keeps the stored fallback (see `docs/client-api.md`, **Config**).
- `{{SECRET:NAME}}` — replaced by the value stored in the keyring under `NAME` (the
  same names `/secrets` and the `secret` tool manage, e.g.
  `{{SECRET:openai.api_key}}`). The whole string must be the placeholder —
  unlike the env form it is not substituted inside a larger value — and it is
  resolved at config load, after env expansion, only when the keyring is
  available: if the keyring is disabled, the vault cannot be opened, or the name
  does not exist, the placeholder is left verbatim and the affected
  channel/provider sees the placeholder text (lele still starts — a broken
  keyring never blocks startup).

Expansion is in-memory only. Nothing resolves a placeholder back into the file:
load → modify → save diffs the runtime object against the file, so an untouched
`{{ENV_…}}` / `{{SECRET:…}}` stays a placeholder on disk, and the Web UI settings
document round-trips the reference (see `docs/client-api.md`, **Config**), never
the resolved value.

## Environment Variables

Many config keys can be overridden via environment variables.

Examples:

- `LELE_AGENTS_DEFAULTS_WORKSPACE`
- `LELE_AGENTS_DEFAULTS_MODEL`
- `LELE_AGENTS_DEFAULTS_SUBAGENT_RETENTION_MINUTES`
- `LELE_AGENTS_DEFAULTS_MEMORY_MAX_LINES`
- `LELE_CHANNELS_NATIVE_ENABLED`
- `LELE_CHANNELS_NATIVE_RATE_LIMIT_ENABLED`
- `LELE_CHANNELS_NATIVE_RATE_LIMIT_PIN_PER_MINUTE`
- `LELE_CHANNELS_NATIVE_RATE_LIMIT_PAIR_PER_MINUTE`
- `LELE_CHANNELS_NATIVE_RATE_LIMIT_REFRESH_PER_MINUTE`
- `LELE_CHANNELS_NATIVE_RATE_LIMIT_API_PER_MINUTE`
- `LELE_CHANNELS_NATIVE_RATE_LIMIT_WS_MESSAGES_PER_MINUTE`
- `LELE_TOOLS_CRON_EXEC_TIMEOUT_MINUTES`
- `LELE_LOGS_PATH`

## Related Docs

- `docs/agents-models-providers.md`
- `docs/channel-setup.md`
- `docs/mcp.md`
- `docs/tools_configuration.md`
