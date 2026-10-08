# MCP Servers (Model Context Protocol)

Lele speaks MCP as a **client**: it connects to external MCP servers and exposes
their **tools** to the agent. Discovery, connection management and the tool
wrapper live in `pkg/mcp`; the registry-side loader (`load_mcp_tools`) lives in
`pkg/tools` and the per-agent wiring (prompt section, manager lifecycle) in
`pkg/agent`.

**v1 scope: tools only.** MCP *resources*, *prompts*, *sampling*, *elicitation*
and *roots* are not supported, and there is no OAuth — remote servers are
reached with the headers you configure.

## Configuration (`mcp.json`)

Servers are declared in `mcp.json` files — **not** in `config.json`. Every
layer's file uses the same shape:

```text
{
  "mcpServers": {
    "<server-name>": { <entry fields, see table below> }
  }
}
```

| Field | Type | Shape | Description |
| --- | --- | --- | --- |
| `command` | string | stdio | Program to spawn. Mutually exclusive with `url`. |
| `args` | string[] | stdio | Arguments for `command`. |
| `env` | object | stdio | Extra environment variables for the child. Values expand `${VAR}`. |
| `url` | string | remote | Endpoint. Mutually exclusive with `command`. |
| `type` | `"http"` \| `"sse"` | remote | Transport; defaults to `http` when omitted. Ignored for stdio. |
| `headers` | object | remote | Extra HTTP headers. Values expand `${VAR}`. |
| `description` | string | both | What the LLM sees in the `## MCP Servers` prompt section before loading. |
| `disabled` | bool | both | Parsed but never activated. A higher layer can switch off a server enabled below. |

An entry must be exactly one of the two shapes (`command` **or** `url`, never
both). Validation is per entry: one broken entry never takes the other servers
in the file down — it is reported and excluded.

### Examples

stdio with environment expansion:

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/home/alfredo/notes"],
      "env": { "LOG_LEVEL": "info" },
      "description": "Read and write files under ~/notes"
    }
  }
}
```

Remote HTTP with an auth header:

```json
{
  "mcpServers": {
    "issues": {
      "url": "https://mcp.example.com/mcp",
      "type": "http",
      "headers": { "Authorization": "Bearer ${ISSUE_TRACKER_TOKEN}" },
      "description": "Issue tracker tools"
    }
  }
}
```

Legacy SSE endpoint:

```json
{
  "mcpServers": {
    "legacy": {
      "url": "https://sse.example.com/sse",
      "type": "sse",
      "description": "Legacy SSE endpoint"
    }
  }
}
```

A temporarily disabled server (parsed, never dialed):

```json
{
  "mcpServers": {
    "old-server": {
      "command": "old-server-bin",
      "disabled": true,
      "description": "Temporarily switched off"
    }
  }
}
```

## Layered files and precedence

Up to three optional files are merged (lowest to highest):

| Layer | Path | Notes |
| --- | --- | --- |
| global | `<lele dir>/mcp.json` | `<lele dir>` is `~/.lele`, or `$LELE_CONFIG_DIR` when that is set. |
| agent | `<workspace>/mcp.json` | The agent's workspace: `agents.defaults.workspace` (default `~/.lele/workspace`); a named agent without an explicit workspace uses `~/.lele/workspace-<id>`. |
| project | `<process cwd>/.lele/mcp.json` | The cwd is captured **once at process start**, so this layer is identical for every agent of the same lele process. |

Roots may resolve to the same file — for example, running lele from `$HOME`
makes the global and project layers share `~/.lele/mcp.json`. Each distinct
file is read exactly once and the **highest** matching layer owns it.

Merge rules:

- A server defined in several layers takes its **whole** entry from the
  highest layer that mentions it — fields are never merged one by one.
- Missing files are normal (silently skipped); an empty root disables the
  layer entirely.
- A file that fails to parse is reported as a warning and skipped: a broken
  higher layer cannot drop valid lower layers.
- A higher-layer entry that is invalid still shadows lower layers — the name
  stays out of the active set and the failure is reported.
- `disabled: true` in a higher layer switches off a server that a lower layer
  enables.
- Server names are sorted everywhere they are listed (prompt section, error
  messages), so output is deterministic.

## Managing servers (UI, TUI, API)

Three surfaces edit the same layered stack: the **WebUI agent-config tab**,
the **TUI `/mcp`** modal and the **REST API** (`/api/v1/mcp`, routes and
error codes in `docs/client-api.md`). The merge rules above decide which
copy wins at read time; the rules below decide where an edit lands.

| Surface | Offers | Scope |
| --- | --- | --- |
| WebUI agent-config tab | List, toggle and a per-layer raw JSON editor. | The selected agent (workspace filter). |
| TUI `/mcp` | List and toggle; no editor. | The layers of the running agent. |
| REST API | 5 routes — 2 reads (`GET /api/v1/mcp`, `GET …/{layer}/raw`), 2 writes (`PUT …/{layer}/servers/{name}/toggle`, `PUT …/{layer}/raw`) and 1 dry run (`POST /api/v1/mcp/validate`); `layer` ∈ `global`\|`agent`\|`project`\|`auto`. | One `agent_id` per call. |

`auto` is a write-side alias only: it resolves to the layer that owns the
winning entry, while stored-layer operations name `global`, `agent` or
`project`. The raw endpoints refuse `auto` in both directions — a save must
name its physical target, and a read answers "which file backs this layer".

Client reporting: invalid rows show no working toggle in either client and
point at the raw file instead; a toggle that answers `changed: false` is
reported as "no change", not as success.

Write rules:

- **Path guard:** the three routes that touch a layer file — the raw GET, the
  raw PUT and the toggle — resolve it against the agent's roots and refuse
  anything outside the allowed trees — the user's home directory, `/tmp`,
  `/var/folders`, or the process's current directory — with
  `403 mcp_path_not_allowed`, checked BEFORE anything is decoded, read or
  written (one shared predicate, `channels.IsAllowedWorkspacePath`, so a path the
  writes refuse can never be served by the read, and the TUI applies the same
  rule instead of a copy). The inventory GET and the dry run need no guard:
  neither opens a file.
  The config loader may accept a path from elsewhere; the management API does
  not.
- **Write rule:** every management surface writes the layer that OWNS the
  winning entry, never a shadowed copy. The REST API can override that with
  `?force=true`, writing the layer named in the request instead; without it the
  answer is `409 mcp_entry_shadowed` and names the owner layer and path.
- **Stub-deletion rule:** re-enabling a server whose addressed-layer entry is a
  pure `{"disabled": true}` stub DELETES that entry — it does not write
  `"disabled": false` — because a leftover stub would keep shadowing the
  lower layer that actually defines the server.
- **Cross-layer rule:** disabling a server writes a `{"disabled": true}`
  stub into the layer you address rather than into the owner — addressing
  a layer above the winner is how you switch off a lower server for one
  agent only. A disable that would create a stub in a layer ranked BELOW
  the winning layer is refused with `400 mcp_stub_layer_not_allowed`, for
  any layer pair — rank is the index in `mcp.Layers()` (low → high:
  `global` < `agent` < `project`) — and the refusal message names both the
  addressed layer and the winning layer.
- **Invalid entries:** an entry whose EFFECTIVE verdict is `invalid` is not
  toggleable in EITHER direction — the toggle answers
  `400 mcp_entry_invalid` — because a write would mask the invalid verdict
  behind a legit-looking enabled/disabled state (the verdict derivation
  prefers Disabled over Invalid), and the only repair is the raw editor.
  The API and both clients (TUI `/mcp`, WebUI agent-config MCP panel)
  enforce this rule.
- **Secrecy:** no surface returns env/header VALUES or expanded `${VAR}`
  values; listings read the raw file bytes and expose key NAMES only.
- **Liveness:** edits through any surface take effect without a restart —
  the manager re-reads the winning entry by fingerprint (see `Lifecycle`)
  and the API triggers a wiring pass after every successful write. `changed`
  gates the response only, never the reload; a failed reload is logged, not
  surfaced as an error, because the file on disk is already right.

## `${VAR}` expansion

`mcp.json` values are expanded against the **process environment** every time
the file is read (so a changed variable takes effect on the next
discovery/reload, without editing the file):

- **Expanded:** `command`, each `args` value, `url`, each `env` value, each
  `headers` value, `description`.
- **Never expanded:** `env`/`headers` **keys** and the `type` field.
- Unknown references expand to the empty string (`os.LookupEnv` semantics):
  `${UNSET}` disappears from the result.
- Escape policy: `$$` is a literal `$` — write `$${VAR}` to keep the text
  `${VAR}` verbatim. A backslash is **not** an escape (`\${VAR}` still
  expands). A lone `$` and an unterminated `${` are kept as-is.
- Expansion is a single pass: a value that itself contains `${OTHER}` stays
  literal.

## Using servers: `load_mcp_tools`

While at least one active server is configured, the system prompt gains a
section listing **names and descriptions only** (never tool schemas):

```text
## MCP Servers

Use `load_mcp_tools` with a server name to load its tools.

- **files** — Read and write files under ~/notes

---
```

The LLM loads a server when it needs it:

```text
load_mcp_tools
{"server": "files"}
```

The first load registers every tool under its namespaced registry name and
returns the full listing (name, description, input schema):

````text
Loaded 2 tool(s) from MCP server "files": 2 newly registered, 0 already present.

## mcp_files_read_file
Read a file from disk.
inputSchema:
```json
{
  "type": "object",
  "properties": {
    "path": { "type": "string", "description": "File path" }
  },
  "required": ["path"]
}
```

## mcp_files_write_file
Write a file to disk.
inputSchema:
```json
{
  "type": "object",
  "properties": {
    "path": { "type": "string", "description": "File path" },
    "content": { "type": "string", "description": "File content" }
  },
  "required": ["path", "content"]
}
```
````

Rules of the bridge:

- **Namespacing:** remote tools are registered as `mcp_<server>_<tool>`,
  sanitized to `[A-Za-z0-9_-]`. A name longer than 64 characters is truncated
  and suffixed with a 6-hex-char FNV-1a hash, so distinct long names stay
  distinct. A server can never shadow a built-in lele tool.
- **Idempotent:** repeated `load_mcp_tools` calls never duplicate
  registrations (the count in the output stays stable) and always repeat the
  listing, so the model can re-read the specs.
- **Non-destructive:** an existing registry name is never overwritten — a
  collision with a built-in or an earlier load reports as "already present".
- **Unknown server:** fails with a clean error listing the known servers
  (`known servers: …`).
- Calling a loaded tool afterwards works like any built-in: the call is
  forwarded to the server under its original MCP name.

## Lifecycle

- **Sync points:** the server set is re-evaluated at startup and on every
  config reload (the same cadence as the other feature tools). A manager
  exists only while at least one active server is configured; losing the last
  server unregisters `load_mcp_tools`, **unregisters the `mcp_*` tools that
  were already loaded through it** (they would otherwise be offered to the
  model with no server behind them), drops the prompt section and closes the
  connections. Moving the workspace retires the stale manager — closed **and
  dropped**, so coming back builds a fresh, working one. An allowlist that
  excludes `load_mcp_tools` on a reload takes the loader **and its loaded
  `mcp_*` tools** with it; the prompt keeps listing the servers either way
  (they are configuration, not tools). Agents removed from the config need
  none of this: their whole tool registry dies with the instance.
- **Config changes are picked up lazily, without a restart:** the prompt-facing
  `Servers()` re-reads the three layers on every call, so a new name or an
  edited `description`/`url` shows up on the next sync or prompt build. In
  addition, every `LoadServer`/`CallRemote` fingerprints the winning entry of
  the server (FNV-1a over its normalized JSON): when the fingerprint changed
  since the last use, the live connection is closed, the cached specs are
  dropped and the next use dials lazily with the **new** config. The stale
  connection is closed **immediately**: a call already in flight on it fails
  at the transport level and is recovered by the reconnect-once retry (drop,
  re-resolve, one redial with the **new** config, one retry) — the retry
  re-reads the winning entry right before dialing, so it always dials the
  config that is current at that moment, never the retired one it entered
  with.
- **Lazy connect:** no connection is opened until a server's tools are first
  loaded — configuring a server costs nothing at startup.
- **Idempotent loads:** after the first successful load, specs are cached;
  further `load_mcp_tools` calls for the same server return them without
  dialing (a dead context gets its context error back instead).
- **Reconnect once:** a transport-level failure (crashed stdio child, dropped
  socket) drops **that connection** — never a newer one installed by a
  concurrent redial — re-resolves the server's config, redials once with it
  and retries the call once. If the server was removed or disabled between
  the failure and the retry, the original config is kept for the redial
  (which then fails or succeeds with the old config). A second failure surfaces as a clean, wrapped error
  (`… reconnect: …`) — never a hang or a panic.
- **Shutdown:** `CloseMCPManagers` (agent-loop shutdown and the gateway's
  `mcp-stop` hook) closes every connection, terminating stdio children. It is
  idempotent and safe when nothing was ever dialed. The gateway registers
  `mcp-stop` as a **critical** shutdown hook: critical hooks run in the
  coordinator's guaranteed phase after the budgeted pass, so a slow
  `agent-drain` that exhausts the overall shutdown budget can never skip it
  (no orphaned stdio children). It runs after the drain **and** after
  `services-stop` — note that stopping the config watcher does **not** join
  its goroutine, so a reload already in flight may still reach the MCP sync
  after `services-stop`; the manager set's `stopped` flag guarantees such a
  late sync installs no manager (the sync early-returns and `put` refuses the
  entry, closing it on the spot) — while `lock-release` stays the very last
  hook.
- **Tool-list drift:** specs are cached per connection lifetime; a tool added
  on the server *after* the first load appears once the manager is recreated
  (a reload that retires it) **or once the server's `mcp.json` entry is
  edited** (the fingerprint change drops the cached specs and connection on
  next use). Calls to already-loaded tools always reach the live server.

## Security notes

- **`mcp.json` is trusted, user-authored configuration.** `command` is
  executed with the same privileges as lele itself: only point it at servers
  you installed.
- **Secrets stay out of logs.** `${VAR}` expansions from `env`/`headers` may
  hold API keys; they are expanded at read time and never logged. Only server
  names, transport kinds, layer names, file paths and error strings reach a
  log sink.
- **stdio children inherit lele's process environment** (`PATH`, `HOME`, …)
  plus the `env` entries of their own config. Prefer referencing secrets via
  `${VAR}` instead of writing values into the file, and keep the file
  user-readable only.
- **Remote servers:** no OAuth in v1 — pass the bearer token you would use
  manually via `headers` (values expand too).

## Limitations (v1)

- **Tools only** — no resources, prompts, sampling, elicitation or roots.
- **Results:** only `text` parts and `structuredContent` are surfaced to the
  LLM; image, audio and embedded-resource parts are ignored (deferred). The
  combined text is capped at 50000 characters, with an explicit
  `…[truncated, N more bytes]…` marker when anything was dropped.
- **Windows:** `command` is spawned directly, without a shell, so `.cmd`
  shims such as `npx` may fail to start. Use the underlying program instead
  (e.g. `node …\server.js`, `python -m …`) or a wrapper script.
- **Subagents do not receive `load_mcp_tools`** (v1): loading MCP tools
  stays a decision of the main agent.
- **The project layer is process-wide:** one cwd per lele process feeds every
  agent's project layer.

## Manual smoke test (not run in CI)

Tests that spawn real stdio processes are deliberately outside the default
suite. The repo ships a ready-made harness under `pkg/mcp/testdata/`:

1. Build the test server and run the smoke driver from the repo root:

   ```sh
   go build -o /tmp/mcp-smoke/smokeserver ./pkg/mcp/testdata/smokeserver
   go run ./pkg/mcp/testdata/smokerunner /tmp/mcp-smoke
   ```

   The driver writes `/tmp/mcp-smoke/.lele/mcp.json` (its `Paths` alias the
   agent and project layers onto that one file), spawns the server as a
   real child through the production dialer (nil `Dialer` — nothing faked),
   runs the initialize handshake and performs two round trips. It prints
   `SMOKE OK` on success, a `SMOKE FAILED: …` reason and exit code 1
   otherwise.

2. Interactive variant (full agent): point any layer at the same binary, e.g.
   `~/.lele/mcp.json`:

   ```json
   {
     "mcpServers": {
       "smoke": { "command": "/tmp/mcp-smoke/smokeserver", "description": "Smoke test server" }
     }
   }
   ```

3. Start `lele agent` (or `lele tui`) — a global-layer file applies from any
   working directory — and confirm the `## MCP Servers` section lists `smoke`.
4. Ask the agent to `load_mcp_tools` `smoke`, then call one of
   `mcp_smoke_<tool>` — the round trip proves discovery → manager → wrapper →
   registry → live stdio child.
5. Kill the child mid-session and repeat the call: the first attempt fails
  (or recovers) through the reconnect-once path with a clean error.

The automated equivalent — the same code path against in-process servers — is
`pkg/mcp/e2e_test.go` (`go test -race -run TestE2E ./pkg/mcp/`).

## Related Docs

- `docs/tools_configuration.md` — the `load_mcp_tools` tool reference
- `docs/config-reference.md` — where lele's own config lives
- `docs/architecture.md` — runtime map
