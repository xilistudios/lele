# MCP Servers (Model Context Protocol)

Lele speaks MCP as a **client**: it connects to external MCP servers and exposes
their **tools** to the agent. Discovery, connection management and the tool
wrapper live in `pkg/mcp`; the registry-side loader (`load_mcp_tools`) lives in
`pkg/tools` and the per-agent wiring (prompt section, manager lifecycle) in
`pkg/agent`.

**v1 scope: tools only.** MCP *resources*, *prompts*, *sampling*, *elicitation*
and *roots* are not supported. A remote server that answers `401` with an
**OAuth 2.1** challenge is supported through an `auth` block plus an
interactive login (`lele mcp auth`, `/mcp auth` in the TUI, the agent panel in
the WebUI — see [OAuth 2.1](#oauth-21-remote-servers)); a server that only needs
a static token is still reached with the `headers` you configure.

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
| `auth` | object | remote | OAuth 2.1 settings for this server. See [OAuth 2.1](#oauth-21-remote-servers). |
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

## OAuth 2.1 (remote servers)

A remote server that wants credentials answers `401` with a `WWW-Authenticate`
challenge instead of serving tools. Add an `auth` block to tell lele the server
is expected to need a login, then run it once interactively:

```json
{
  "mcpServers": {
    "issues": {
      "url": "https://mcp.example.com/mcp",
      "type": "http",
      "auth": { "type": "oauth", "scopes": ["read", "write"] },
      "description": "Issue tracker tools"
    }
  }
}
```

| Auth field | Type | Description |
| --- | --- | --- |
| `type` | `"oauth"` \| `"none"` | `oauth` opts the entry into the flow. `none` (or an empty block) declares that no login is expected — the challenge is then reported as a plain failure instead of starting a flow. Omit the block entirely for a server that carries its own token in `headers`. |
| `scopes` | string[] | Scopes to request. Empty means "whatever the server's metadata advertises". |
| `client_id` | string | A pre-registered client id. Empty means *dynamic client registration*: lele registers itself with the authorization server and persists the result. |
| `client_secret` | string | Secret of a pre-registered client. Requires `client_id` (`auth client_secret requires client_id`). |
| `redirect_uri` | string | Registered redirect URI of a pre-registered client (default: an ephemeral loopback `http://127.0.0.1:<port>/callback`). |
| `auth_server_metadata_url` | string | Override authorization-server metadata discovery. |
| `protected_resource_metadata_url` | string | Override protected-resource metadata discovery (RFC 9728). |

`client_id`, `client_secret`, `redirect_uri` and both metadata URLs expand
`${VAR}` at read time, exactly like `env`/`headers`; `type` and `scopes` are
literal-ish (scopes expand too, but are never a secret).

The attach rule is deliberately **narrow**: OAuth is attached to a dial when
the entry declares `auth.type: "oauth"` **or** when a `401` has actually been
observed for that server. Attaching it to a healthy server would break it (an
OAuth transport refuses to send anything without a usable token), so a bare
`client_id` left over from a cancelled attempt never opts a server in.

### Logging in

The login is interactive and per server; the credential is stored in
`<lele dir>/mcp-auth.json` (mode `0600`), never in `mcp.json`:

- **CLI:** `lele mcp auth issues` (see [`lele mcp auth`](#lele-mcp-auth)).
- **TUI:** `/mcp`, open the entry with `ENTER`, then press `a` (the key is owned
  by the detail view — in the list it does nothing).
- **WebUI:** the agent's MCP panel → **Authenticate**.

The flow is OAuth 2.1 with PKCE and a loopback redirect: lele probes the server
without credentials, reads its metadata (RFC 9728 / RFC 8414), registers a
client if the entry has no `client_id`, opens the authorization URL and waits
for the browser to come back. A server that advertises no registration endpoint
is refused (`authentication is not supported … add a pre-registered client with
--client-id/--client-secret`), and a server that completes the plain handshake
is refused too (`does not require authentication`) — the two mean different
things and neither starts a flow.

`mcp-auth.json` is machine-local and per-lele-directory: it holds tokens a
*specific* machine obtained from a *specific* authorization server, so it is not
a file to sync or commit. It sits next to `mcp.json` because it is that file's
other half — `mcp.json` says what to connect to, `mcp-auth.json` remembers what
the server asked us to prove.

Once stored, the token is used on every dial (the transport refreshes it when
it expires) and the gateway picks it up on the next reload — no restart. To
forget a server's token *and* its dynamic registration, use `lele mcp logout`
(or `D: sign out` in the TUI's `/mcp` list and detail, **Sign out** in the
WebUI panel).

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
| REST API | 10 routes — 2 reads (`GET /api/v1/mcp`, `GET …/{layer}/raw`), 2 writes (`PUT …/{layer}/servers/{name}/toggle`, `PUT …/{layer}/raw`), 1 dry run (`POST /api/v1/mcp/validate`) and 5 OAuth routes — start, poll, complete and cancel a login, plus logout (routes and error codes in `docs/client-api.md`); `layer` ∈ `global`\|`agent`\|`project`\|`auto`. | One `agent_id` per call. |

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

## Managing servers from the CLI (`lele mcp`)

A fourth surface, the **CLI** (`lele mcp`), edits the same layered files
without a running gateway. It is a thin front-end over the inventory and the
byte-splice writers: it reads the merged view and mutates the layer files, and
never re-implements discovery, precedence or validation. Bare `lele mcp` (or
`lele mcp --help`) prints the help and exits 0; an unknown subcommand prints
the help and exits 1.

Resolving which files to read is **read-only**: unlike the runtime, the CLI
never initializes an agent workspace, so `lele mcp list` (and every other
subcommand, including one that fails) creates no directory and writes nothing
outside the layer file it was asked to change.

The **path guard** above is enforced here too, by the same predicate the
REST/TUI surfaces use: `add`, `edit` and `remove` refuse to touch a layer file
outside the allowed trees — the user's home directory, `/tmp`, `/var/folders`
or the process's current directory — printing
`✗ refusing to write "<path>": outside the allowed workspace roots` before any
write happens. A workspace legitimately living under `/srv` or `/opt` is
therefore not editable from the CLI; move the workspace under one of the
allowed roots (or arrange for the file to be reached through one).

One escaping rule applies to every subcommand: a bare `--` ends flag parsing,
so a server whose name begins with a dash stays addressable —
`lele mcp show -- -dash`.

Unlike the toggle surfaces (which write the layer that **owns** the winning
entry — see the write rules above), `add` writes exactly the layer named by
`--layer` (default `global`) and merely **warns** when the new entry is
shadowed; `edit` and `remove` default to the winning copy, i.e. they behave
like the toggle surfaces. `add` is the CLI's counterpart of the REST raw
`PUT`, not of the toggle.

```text
lele mcp list [--agent <id>] [--layer global|agent|project]
lele mcp show <name> [--agent <id>]
lele mcp add <name> (--command <bin> | --url <u>) [flags]
lele mcp remove <name> [--agent <id>] [--layer global|agent|project] [--all]
lele mcp edit <name> [--agent <id>] [--layer global|agent|project]
lele mcp test <name> [--agent <id>] [--timeout 30s]
lele mcp auth <name> [--agent <id>] [--no-browser] [--manual] [--timeout 5m] [--port <n>]
lele mcp logout <name> [--agent <id>]
```

Every subcommand accepts `--agent <id>`: the agent whose workspace provides the
**agent** layer (default `main`). An unknown id fails and lists the known
agents. `--layer` names one of `global`, `agent` or `project` (low → high
precedence, as above); an unknown layer is a usage error. Both the
`--flag value` and `--flag=value` forms are understood.

### `lele mcp list`

Lists the effective (winner) servers, one per line — state glyph, name, kind,
winning layer and the entry's description (truncated to 60 characters). The
kind column is `stdio` (the entry carries a `command`), `remote` (it carries a
`url`) — an entry carrying both is reported as `stdio` and flagged invalid —
or `unknown` for a copy that defines neither, i.e. a pure `{"disabled": true}`
stub. Glyphs:

| Glyph | State |
| --- | --- |
| `●` | enabled |
| `○` | disabled |
| `!` | invalid |

`--layer L` keeps only the servers whose **winner** is layer `L`. A summary
line and the per-layer file table follow the rows; discovery warnings are
printed as `⊘ warning: …`. With no servers, the command prints
`No MCP servers configured.`, a `lele mcp add …` hint and the global file path.

A row ends with an **auth marker** when the server's OAuth 2.1 state requires
action — `[needs auth]` until a credential is stored for that server,
`[authenticated]` once it is (the TUI's `mcpAuthRequired` / `mcpAuthOK` and the
WebUI's `auth.required` / `auth.authenticated` render the same two words, so all
three surfaces speak one vocabulary). The marker is **appended** after the
description, so the rest of the row keeps its shape; a server that needs no
login, and any entry discovery does not report (disabled, invalid), carries no
marker at all. With no lele directory there is no store to ask, so no server is
ever reported as needing a login.

```console
$ lele mcp list
!  broken             stdio   project  Issue tracker tools
●  filesystem         stdio   global  Read and write files under ~/notes
○  old-server         unknown agent
●  issues             remote  global  GitHub issues [needs auth]

4 server(s): 2 enabled, 1 disabled, 1 invalid

Layers (low → high):
  global           /home/alfredo/.lele/mcp.json                (exists)
  agent            /home/alfredo/.lele/workspace-main/mcp.json (exists)
  project          /home/alfredo/proj/.lele/mcp.json           (exists)
⊘ warning: mcp config /home/alfredo/.lele/workspace-main/mcp.json: mcp server "old-server": invalid entry: needs command (stdio) or url (remote)
```

(Real output, not a mock-up: `broken` carries both a `command` and a `url`
under `project`, and `agent` holds `{"old-server": {"disabled": true}}` — the
stub that switches the `global` definition off, hence `unknown`.)

### `lele mcp show`

Prints the winning entry and a per-layer breakdown marking which layer wins
(`← winner`) and which lower copies are inert (`(shadowed)`). `env keys:` and
`header keys:` list **key names only** — a value is never printed (see
[Secret safety](#secret-safety-and-liveness)). `command`, `args` and `url` are
shown verbatim, so a `${VAR}` there stays literal. An unknown name exits 1 and
suggests close matches (`did you mean: …?`).

`state:` is always the **runtime's** verdict — computed after `${VAR}`
expansion, exactly like `pkg/mcp`'s discovery. A copy's own verdict, read from
the file's raw bytes without expansion, is shown separately in the breakdown as
`raw verdict: "…"`; the two legitimately disagree when expansion is what makes
an entry valid or not (e.g. an unset `${VAR}` in a `url`). When the runtime
rejects an entry for which the raw read found no problem, the state line still
carries a reason instead of an empty parenthesis —
`invalid (the runtime rejects it — its ${VAR} references may expand to empty)`.

```console
$ lele mcp show filesystem
MCP server "filesystem"
  state:       enabled
  layer:       global
  file:        /home/alfredo/.lele/mcp.json
  kind:        stdio
  command:     npx
  args:        ["-y", "@modelcontextprotocol/server-filesystem", "/home/alfredo/notes"]
  description: Read and write files under ~/notes
  env keys:    LOG_LEVEL
  header keys: (none)

Per-layer breakdown (low → high):
  global   present=yes defines=yes disabled=no  /home/alfredo/.lele/mcp.json  ← winner
  agent    present=no  /home/alfredo/.lele/workspace-main/mcp.json
  project  present=no  /home/alfredo/proj/.lele/mcp.json
```

An entry that declares an `auth` block also prints that block — verbatim (a
`${VAR}` stays literal) and **keys-side only**, like the other fields: `auth:`
is the declared type, `scopes:` the requested scopes, `client id:` reports
`(set)` / `(none)` and never the value, and `redirect:`, `as metadata:` and
`pr metadata:` are printed only when set. A `client_secret` is never printed at
all, under any flag. The block is followed by the **live** verdict, resolved
through `mcp.CheckAuth` exactly like the `list` marker, so the two cannot
disagree:

```console
$ lele mcp show issues
MCP server "issues"
  state:       enabled
  layer:       global
  file:        /home/alfredo/.lele/mcp.json
  kind:        remote
  url:         https://mcp.example.com/mcp
  type:        http
  env keys:    (none)
  header keys: (none)
  auth:        oauth
  scopes:      read, write
  client id:   (set)
  redirect:    http://127.0.0.1:9000/callback
  auth state:  [needs auth]
  auth method: oauth
```

`auth state:` uses the same two bracket words as `list` (`[needs auth]`,
`[authenticated]`, or `(none)` when no login is required), and `auth method:`
names the carrier the engine reports (`oauth`, `headers`, or `(none)`). It is
shown for any auth-relevant entry — a declared block, a recorded 401, or a
stored credential — and only when discovery reports the entry, so a disabled or
invalid copy prints the declared block but no verdict.

### `lele mcp add`

Creates one entry in a layer. Flags:

| Flag | Notes |
| --- | --- |
| `--command <bin>` | stdio launcher. Mutually exclusive with `--url`. |
| `--arg <v>` | stdio argument; repeatable, order preserved. |
| `--url <u>` | remote endpoint. Mutually exclusive with `--command`. |
| `--type http\|sse` | remote transport; only valid with `--url`. |
| `--description <d>` | what the LLM sees before the server is loaded. |
| `--env KEY=VALUE` | child environment variable; repeatable. |
| `--header KEY=VALUE` | remote HTTP header; repeatable. |
| `--layer <l>` | target layer (default `global`). |
| `--force` | replace an existing entry in the target layer. |
| `--auth oauth\|none` | opt a remote entry into the OAuth 2.1 flow (`oauth`) or declare no login (`none`). |
| `--scope <s>` | OAuth scope to request; repeatable, order preserved. |
| `--client-id <id>` | pre-registered client id (dynamic registration fills it when empty). |
| `--client-secret <s>` | pre-registered client secret; requires `--client-id`. |
| `--redirect-uri <uri>` | registered redirect URI of a pre-registered client. |
| `--auth-server-metadata-url <u>` | override authorization-server metadata discovery. |
| `--protected-resource-metadata-url <u>` | override protected-resource metadata discovery (RFC 9728). |

Exactly one of `--command` / `--url` is required. `--type` must be `http` or
`sse` and only accompanies `--url`. An `--env`/`--header` pair without `=` or
with an empty key is a usage error; an empty value (`KEY=`) is allowed.

The auth flags are **remote-only**: any of them on a stdio entry is refused
(`the auth flags require --url (an auth block is remote-only)`) before the file
is touched, and a `--client-secret` without a `--client-id` is refused with the
same rule the reader applies (`--client-secret requires --client-id`). No auth
flag at all writes no `auth` block, so an entry that never needed OAuth stays
byte-identical to what it was.

Values are stored **raw**: `${VAR}` is written verbatim and only expanded when
the file is read (see [`${VAR}` expansion](#var-expansion)), so `add` never
needs the secret to be present in the environment. The entry is serialized as
one canonical, compact, single-line object:

```json
{"mcpServers":{"filesystem":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/home/alfredo/notes"],"env":{"LOG_LEVEL":"info"},"description":"Read and write files under ~/notes"}}}
```

The target defaults to `global`, and `add` writes exactly that layer. If a
higher layer shadows the name, the write still succeeds and prints
`⊘ note: … shadows this entry …`. If the name already exists in the target
layer, the command refuses (`✗ server "…" already exists in …`) until `--force`
is given; with `--force`, an entry whose bytes already match reports
`✓ … unchanged`. Like every write here, the target file must pass the
[path guard](#managing-servers-from-the-cli-lele-mcp).

```console
$ lele mcp add filesystem --command npx \
    --arg -y --arg @modelcontextprotocol/server-filesystem --arg /home/alfredo/notes \
    --env LOG_LEVEL=info --description "Read and write files under ~/notes"
✓ added "filesystem" to global (/home/alfredo/.lele/mcp.json)

$ lele mcp add issues --url https://mcp.example.com/mcp --type http \
    --header "Authorization=Bearer ${ISSUE_TRACKER_TOKEN}" --description "Issue tracker tools"
✓ added "issues" to global (/home/alfredo/.lele/mcp.json)
```

### `lele mcp remove`

Removes one or more copies of a server:

- **Default** removes only the copy that **wins** the merge, then prints a
  `⊘ note:` for every remaining (lower) copy and suggests `--all`.
- `--layer L` removes **that layer's** copy — how you delete a specific
  (possibly shadowed) copy.
- `--all` removes every copy in every layer.

`--layer` and `--all` are mutually exclusive, and a name defined nowhere exits 1.
All three forms run the path guard before writing.

Because layers replace whole entries, removing a copy that kept the server OFF
(a pure `{"disabled": true}` stub, or a definition carrying `"disabled": true`)
can switch the lower definition **ON**. The command says so explicitly instead
of silently re-enabling a server:

```console
$ lele mcp remove old-server
✓ removed "old-server" from agent (/home/alfredo/.lele/workspace-main/mcp.json)
⚠ this re-enables "old-server" from global (/home/alfredo/.lele/mcp.json)
⊘ note: "old-server" is still defined in global (/home/alfredo/.lele/mcp.json)
  run: lele mcp remove old-server --all
```

The warning appears only when the removal actually changes the verdict: the
deleted copy was the reason the server was off **and** the copy that takes over
is an enabled definition. A surviving copy above the definition (a disable stub
or a disabled definition) still wins the merge and keeps the server off — no
warning in that case.

### `lele mcp edit`

Opens one entry in your editor. By default it targets the copy that **wins**
the merge — even when that copy is a bare `{"disabled": true}` stub — so `edit`
is the **repair path** for a broken server (the one `test` points at): it edits
exactly the copy a reader loads. `--layer L` targets that layer's copy instead.

- Editor selection: `$VISUAL`, else `$EDITOR`, else `vi`.
- The editor string is run through `sh -c`, so an editor with arguments works:
  `EDITOR="code -w" lele mcp edit api`.
- The temp file holds the entry's **raw JSON object** — no `mcpServers`/name
  wrapper and no `${VAR}` expansion — so it stays valid while you edit.
- On exit the bytes are parsed without expansion and validated; a refused edit
  prints `✗ invalid entry: … — file left unchanged` and writes nothing
  (re-run the command to retry).
- A successful save is canonicalized: compact single-line object, fields in a
  fixed order (`command`, `args`, `env`, `url`, `type`, `headers`,
  `description`), map keys sorted, and `disabled` written only when `true`. If
  the canonical form equals what was on disk, the command prints `✓ no changes`.

```console
$ EDITOR="code -w" lele mcp edit issues
Editing "issues" (global /home/alfredo/.lele/mcp.json) — JSON object for this server. Values are stored raw; ${VAR} is expanded at connect time.
✓ updated "issues" (global /home/alfredo/.lele/mcp.json)
```

Repairing a broken entry adds `✓ "…" is now valid` once the fresh read agrees —
the check is on the **effective** verdict, so an entry that was invalid only
because a `${VAR}` expanded to nothing is announced too. `edit` never *creates*
an entry; a name defined nowhere is a usage error.

### `lele mcp test`

Probes a server out of band: it dials it directly through the production dial
path (transport selection, stdio spawn or HTTP/SSE, the initialize handshake and
`tools/list`), so the probe exercises the same code the runtime dials with —
without going through the manager, which refuses a disabled entry by design.
The entry is resolved the same way a reader would load it: the winning copy,
skipping pure `{"disabled": true}` stubs on the way down (a layer replaces whole
entries, so deleting the stub would expose the definition underneath — that is
the config the question is about). `--timeout` is a Go duration (default `30s`,
must be positive).

- A **disabled** server is probed anyway, after a note naming the layer whose
  copy is disabled: `⊘ "…" is disabled in the agent layer — probing it directly
  (the runtime will not load it)`. A name whose every copy is a bare stub has
  nothing to dial and exits 1 (`… is only a disable stub …`).
- An **invalid** entry is refused before any dial
  (`✗ "…" is invalid: … (fix with: lele mcp edit …)`), so `edit` is the repair
  path. The verdict is the effective one (post-`${VAR}` expansion); an entry
  the runtime rejects with no raw problem still gets a reason, never an empty
  one.
- On success it prints the tool names (first 10, then `+N more`) and, when the
  server reported one, its handshake identity; a failed probe exits 1.

```console
$ lele mcp test smoke
✓ "smoke" connected — 1 tools: echo (server: lele-smoketest 0.1.0)

$ lele mcp test smoke        # the agent layer holds {"smoke": {"disabled": true}}
⊘ "smoke" is disabled in the agent layer — probing it directly (the runtime will not load it)
✓ "smoke" connected — 1 tools: echo (server: lele-smoketest 0.1.0)

$ lele mcp test broken
✗ "broken" is invalid: mixes stdio and remote fields: set either command or url, not both (fix with: lele mcp edit broken)
```

### `lele mcp auth`

Runs the OAuth 2.1 flow of **one remote** server and stores the credential. The
authorization URL is printed **before anything blocks**, alone on its own line:
on a headless host that URL is the only way to finish, and the CLI *is* the
headless path. The command then waits for the browser login to come back to the
loopback listener.

| Flag | Notes |
| --- | --- |
| `--no-browser` | never launch a browser. The URL is still printed — the flag only suppresses the launch. |
| `--manual` | after printing the URL, read **one line** from stdin and submit the pasted authorization result (see below). A refused paste re-prompts on the **same** flow instead of starting over. |
| `--timeout <dur>` | how long to wait for the login (Go duration, default `5m`, must be positive). The engine owns the budget and reports its expiry as `authorization timed out`. |
| `--port <n>` | pin the loopback callback port (default `0` = an ephemeral free port), so a redirect URI registered by hand can be predicted. |

The entry is resolved exactly as `test` resolves it — the winning copy, skipping
pure `{"disabled": true}` stubs — so an **invalid** entry is refused before any
dial (`✗ … is invalid: … (fix with: lele mcp edit …)`) and a **disabled**
server is authorized after a note
(`⊘ "…" is disabled in the agent layer — authorizing it directly …`). A local
(`command`) entry has no `url` to talk to and is refused
(`… is a local (stdio) server — authentication is not supported …`).

Failures are distinguished, never collapsed: a server that needs no login
(`does not require authentication`), one that advertises no registration
endpoint (`authentication is not supported …`), a transport failure
(`transport failure — the server did not complete the handshake`), a cancelled
login, a timed-out login and a store that is unavailable each get their own
line, and every failure exits 1. No token, authorization code, verifier, client
id or client secret is ever printed.

`--manual` submits whatever the pasted line carries, using the **frozen paste
grammar** that the TUI and the WebUI implement — the CLI does not own a copy of
the rule, it calls the shared engine helper `pkg/mcp.ParseAuthPaste`, so the
same paste is accepted, refused or decoded identically on all three surfaces.
The accepted forms are:

1. a **full redirect URL** — `http://127.0.0.1:8080/callback?code=…&state=…`;
2. a **query string** — `?code=…&state=…` or `code=…&state=…`;
3. a **bare authorization code** — `SplxlOBeZQQYbYS6WxSbIA`;
4. **two whitespace-separated tokens** — `<code> <state>`, a terminal
   convenience for authorization servers that print the code and the state on
   separate lines. The CLI reads one line, so both tokens must be on that one
   line.

A URL whose query carries no `code` falls back to its `#fragment`, so a
`response_mode=fragment` redirect still works. Anything else is refused with
`✗ unrecognized input — paste the authorization code or the full redirect URL`
(the same sentence covers a paste that is not an accepted form and a URL/query
that arrived without a code: in both cases there is no code to submit). A paste
longer than 8 KiB is refused whatever its shape. A redirect carrying `error=` is
the authorization server **refusing** the request and is reported as such
(`the authorization server refused the authorization (access_denied)`) instead
of being submitted as a code; the error code is echoed only when it survives the
engine's sanitizer, so a hostile redirect cannot push arbitrary bytes onto the
terminal. The state is optional: a bare code leaves it empty and the engine takes
the callback's word for it.

A **failed** token exchange does not end the flow — the engine keeps it open and
retryable — so `--manual` prints the value-free failure and prompts again,
submitting the next paste on the **same** flow (same authorization URL, same PKCE verifier): mistyping a code costs a retry, not a new login. The loop ends
when the flow completes, when `:q` or `:quit` is entered (reported as
`authorization canceled`, nothing submitted), at end of input (`no input on stdin`),
or after 10 refused pastes (`too many refused pastes`) so a piped stdin with no
EOF cannot spin. Every failure exits 1.

```console
$ lele mcp auth issues
Authorization URL — open it in any browser (printed here for headless hosts):
https://as.example.com/authorize?client_id=lele&state=…&code_challenge=…
✓ "issues" authenticated; the gateway picks it up on the next reload

$ lele mcp auth issues --manual --no-browser   # paste the code the browser landed on
Authorization URL — open it in any browser (printed here for headless hosts):
https://as.example.com/authorize?client_id=lele&state=…&code_challenge=…
http://127.0.0.1:8080/callback?code=SplxlOBeZQQYbYS6WxSbIA&state=…
✓ "issues" authenticated; the gateway picks it up on the next reload
```

### `lele mcp logout`

Forgets everything the lele directory stores about one server: the token **and**
the dynamic registration, so the next dial starts from scratch. It reports
whether anything was actually forgotten but never *what* it was — the store's
only exported questions are yes/no, and a token is never printed.

```console
$ lele mcp logout issues
✓ forgot the stored credentials and registration for "issues" (the gateway drops the session on the next reload)

$ lele mcp logout issues
✓ nothing was stored for "issues" — nothing to forget (a logout that finds nothing is still a success)
```

The name does **not** have to exist in `mcp.json`: the store is keyed by server
name, so a server you already removed can (and should) still be forgotten here.
An empty store is exit 0 — "logout" names a desired state, that state already
holds, and pretending otherwise would make a scripted cleanup fail on its second
run. Only a real store failure is non-zero.

### Secret safety and liveness

- `list` and `show` print env/header **key names only** — never their values,
  and never an expanded `${VAR}`. Same invariant as the management UI. The
  `command`, `args` and `url` fields are shown verbatim, so `${VAR}` there stays
  literal.
- `lele mcp` writes the layer files directly and never talks to a running
  process. `mcp.json` is **not** watched by the config watcher (only
  `config.json` is), so a CLI edit made while a gateway is running is
  out-of-band: unlike the REST API it triggers no wiring pass, and the change
  is picked up lazily — the data plane re-reads the winning entry on next use
  (fingerprint, see [`Lifecycle`](#lifecycle)) and the tool/prompt surface
  catches up on the next config reload. No restart is required, but the change
  is not instantaneous either.

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
- **Remote servers:** a static token still goes in `headers` (values expand,
  so `${ISSUE_TRACKER_TOKEN}` keeps the secret out of the file). A server that
  *demands* a login is handled with the `auth` block, whose credential lands in
  `mcp-auth.json` (`0600`, machine-local, next to `mcp.json`) — never in
  `mcp.json`, and never in a log. `mcp-auth.json` is a secret file: do not commit
  or sync it.

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
