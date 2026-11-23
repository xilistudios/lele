# Tools Configuration

Lele exposes tool-related runtime settings through the `tools` section in `~/.lele/config.json`.

## Current Structure

```json
{
  "tools": {
    "web": {
      "brave": { "enabled": false, "api_key": "", "max_results": 5 },
      "duckduckgo": { "enabled": true, "max_results": 5 },
      "perplexity": { "enabled": false, "api_key": "", "max_results": 5 },
      "searxng": { "enabled": false, "instance_url": "", "categories": "general", "language": "auto", "safesearch": 0, "max_results": 5 }
    },
    "cron": {
      "exec_timeout_minutes": 5
    },
    "exec": {
      "enable_deny_patterns": true,
      "custom_deny_patterns": []
    }
  }
}
```

## Web Tools

These settings control search and web retrieval backends used by the agent.

### Brave

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable Brave Search |
| `api_key` | string | empty | Brave API key |
| `max_results` | int | `5` | Result limit |

### DuckDuckGo

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `true` | Enable DuckDuckGo fallback |
| `max_results` | int | `5` | Result limit |

### Perplexity

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable Perplexity search |
| `api_key` | string | empty | Perplexity API key |
| `max_results` | int | `5` | Result limit |

### SearXNG

[SearXNG](https://docs.searxng.org/) is a privacy-respecting metasearch engine that aggregates results from 70+ search engines. It can be self-hosted, requires no API keys, and is ideal for local/private search.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable SearXNG search |
| `instance_url` | string | empty | SearXNG instance URL (e.g. `https://search.example.com`) |
| `categories` | string | `general` | Search categories: `general`, `images`, `news`, `science`, `it`, etc. |
| `language` | string | `auto` | Search language code (`en`, `es`, `auto`, etc.) |
| `safesearch` | int | `0` | Safe search filter: `0` (off), `1` (moderate), `2` (strict) |
| `max_results` | int | `5` | Result limit |

#### SearXNG Instance Requirements

The SearXNG instance must have the JSON output format enabled. In the instance's `settings.yml`:

```yaml
search:
  formats:
    - html
    - json
```

Without this, the API returns `403 Forbidden`. The tool will show a clear error message if this is the case.

#### Example

```json
{
  "tools": {
    "web": {
      "searxng": {
        "enabled": true,
        "instance_url": "https://search.mapachoshome.com",
        "categories": "general",
        "language": "auto",
        "safesearch": 0,
        "max_results": 5
      }
    }
  }
}
```

#### Search Provider Priority

When multiple providers are enabled, the priority order is:

1. **Perplexity** — AI-powered search (API key required)
2. **Brave** — independent search index (API key required)
3. **SearXNG** — self-hosted metasearch (no API key)
4. **DuckDuckGo** — HTML scraping fallback (fragile, may break)

SearXNG is positioned above DuckDuckGo because it aggregates multiple engines and is more stable than scraping DuckDuckGo's HTML.

## Exec Tool

The exec tool is protected by deny-pattern filtering.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `enable_deny_patterns` | bool | `true` | Enable built-in dangerous-command blocking |
| `custom_deny_patterns` | []string | `[]` | Additional regex patterns to block |

### Example

```json
{
  "tools": {
    "exec": {
      "enable_deny_patterns": true,
      "custom_deny_patterns": [
        "\\brm\\s+-r\\b",
        "\\bkillall\\s+python"
      ]
    }
  }
}
```

### What The Built-In Filter Is For

The built-in deny patterns are intended to block obviously dangerous operations such as:

- destructive deletes
- disk formatting and raw device writes
- shutdown and reboot commands
- shell injection patterns
- command piping into shells
- privilege escalation attempts

The exact blocked patterns are implementation details and may evolve over time.

## Cron Tool

The cron tool controls scheduled task execution timeouts.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `exec_timeout_minutes` | int | `5` | Timeout for agent-style cron executions. `0` disables the timeout. |

### Cron Execution Modes

Cron jobs can be used in several ways:

1. Direct delivery to a channel
2. Normal agent processing
3. Command execution
4. Subagent delegation via `spawn`

### Spawn Example

```json
{
  "action": "add",
  "message": "Daily health check",
  "cron_expr": "0 9 * * *",
  "spawn": {
    "task": "Perform heartbeat check and report status",
    "label": "heartbeat",
    "agent_id": "coder",
    "guidance": "Report only if there are problems"
  }
}
```

When `spawn` is present, the scheduled task is delegated to a subagent instead of being executed inline by the main flow.

## Subagent Tools

`spawn`, `subagent`, `list_active_subagents`, `wait_for_subagent` and `cancel_subagent` are registered per agent,
each bound to that agent's own subagent manager. They have no `config.json` settings of their own — the parameters
below are the tool-call arguments the model sends, listed here because they are the only knobs these tools expose.
See `docs/SKILL_SUBAGENTS.md` for the lifecycle and the ledger semantics.

### `list_active_subagents`

Lists the session's subagent tasks, newest first, from the durable ledger (live tasks merged with tasks recovered
from their persisted sessions). It is the only subagent view that includes reaped and pre-restart tasks.

| Argument | Type | Default | Description |
| --- | --- | --- | --- |
| `status` | string | `active` | Filter applied before pagination. One of `active`, `all`, `running`, `pending`, `needs_context`, `completed`, `failed`, `cancelled`, `not_done`, `unknown`. Any other value is an error |
| `limit` | int | `20` | Maximum tasks per page. Values above `100` are clamped to `100`; a non-positive value falls back to `20` |
| `offset` | int | `0` | Number of tasks to skip, for paging |
| `include_completed` | bool | `false` | Deprecated alias for `status=all`. Ignored when `status` is given |

`active` means `running`, `pending` or `needs_context` — the tasks that can still produce a result. `unknown` is
a recovered row whose outcome was never recorded: the task was interrupted (a restart), or its live row now lives
in another agent's manager. Filtering happens before pagination, so `limit`/`offset` page over the filtered set.

### `wait_for_subagent`

Blocks until a task finishes or pauses, then returns its result.

| Argument | Type | Default | Description |
| --- | --- | --- | --- |
| `task_id` | string | — (required) | Subagent task ID, e.g. `subagent-1` |
| `timeout_seconds` | int | `120` | Maximum wait. Clamped to `1`–`600` |

Only tasks the manager still tracks in memory can be waited on; a reaped or pre-restart task answers
`Subagent task not found` even though `list_active_subagents` still lists it.

### `cancel_subagent`

Cancels a task that is `running`, `pending` or `needs_context`.

| Argument | Type | Default | Description |
| --- | --- | --- | --- |
| `task_id` | string | — (required) | Subagent task ID, e.g. `subagent-1` |

A task that is already terminal is not cancelled; the call reports its status instead.

## MCP Tool Discovery

`list_mcp_tools` and `get_mcp_tool` expose MCP servers configured in layered
`mcp.json` files (see `docs/mcp.md`). They have no `config.json` settings of
their own and are offered to a session automatically **only** while at least
one active server is configured. Both are **session-scoped**: the discovery
tools and the `mcp_*` tools they register live in a per-session layer over the
agent's tool registry, so one chat's load never shows up in another chat's
context.

| Tool | Arguments | Description |
| --- | --- | --- |
| `list_mcp_tools` | `server` (string, required), `page` (integer, default 1), `page_size` (integer, default 20, max 100) | One page of the server's tool names — no schemas — ordered most-used first |
| `get_mcp_tool` | `server` (string, required), `tool` (string) or `tools` (array of strings) | Loads full descriptions + input schemas and registers exactly those tools |

### Behavior

- Before discovery, the model sees only server names and descriptions (the
  prompt section) — never tool schemas.
- `list_mcp_tools` is schema-free and registers nothing: names, one trimmed
  line of description and a `(used N times)` marker. Ordering is usage
  frequency (most used first, ties alphabetical); an out-of-range `page` is
  clamped to the last page and an oversized `page_size` to 100.
- `get_mcp_tool` fetches the server's specs and registers **only the requested
  tools** under `mcp_<server>_<tool>` names **in the calling session's layer**;
  the result lists each tool with its input schema. Ask for `tool` (one) or
  `tools` (several, deduplicated); a partial batch is a success with a trailing
  note naming the tools that were not found.
- Repeated calls are idempotent: the registry count never changes and the
  specs are rendered again.
- Another session of the same agent sees none of it: its tool definitions are
  unchanged and a call to a tool it did not load reports `tool "…" not found`.
  Clearing the session (`/clear`, `/new`) or retiring the agent's MCP manager
  drops the layer, and the session has to load again.
- An unknown server fails with a clean error that lists the known servers
  (`known servers: …`); an unknown tool points back at `list_mcp_tools`.
- Loaded tools then run like built-ins: calls are forwarded to the server and
  a crashed server is reconnected once per call.

### Allowlist

The two tools are gated individually (`tools` in `config.json`). The legacy
name `load_mcp_tools` is kept as an **alias that enables both**, so a config
written for the old bulk loader keeps working — no tool registers under that
name any more. With neither tool allowed the session layers are dropped while
the `## MCP Servers` prompt section keeps listing the servers (configuration is
not a tool), and the section's instructions only ever name the tools the
allowlist keeps — an agent allowed only `list_mcp_tools` is never told to call
`get_mcp_tool`. Subagents receive neither tool.

### Example

```text
list_mcp_tools
{"server": "files"}

get_mcp_tool
{"server": "files", "tools": ["read_file", "write_file"]}
```

Server configuration — `mcp.json` layers, `${VAR}` expansion, transports,
usage counters (`<LeleDir>/mcp_usage.json`), security notes and limitations — is
documented in `docs/mcp.md`.

## Skill Discovery

Skills come from three catalogs — the agent workspace, the global
`<LeleDir>/skills` directory and the builtin skills bundled with lele — and the
prompt inlines only the **5 most used** for the agent's ID (usage lives in
`<LeleDir>/skills_usage.json`). Until usage accumulates that window is
alphabetical, so a fresh install shows the first five skills by name. Three
tools cover the rest of the catalog:

| Tool | Arguments | Description |
| --- | --- | --- |
| `search_skill` | `query` (string, required), `limit` (integer, default 10, max 50) | Keyword search over names, descriptions and SKILL.md bodies, ranked by relevance |
| `list_skills` | `page` (integer, default 1), `page_size` (integer, default 20, max 100) | One page of the catalog, no bodies, ordered most-used first |
| `load_skill` | `skill` (string) or `skills` (array of strings), `unload` (boolean) | Loads the full SKILL.md instructions of those skills, or unpins them when `unload` is true |

### Behavior

- `search_skill` ranks name matches above description matches and both above
  SKILL.md body matches, then breaks score ties by usage frequency and finally
  alphabetically; the result carries names, one trimmed description line and an
  absolute `location:` per skill — never a body.
- `list_skills` paginates the catalog (20 per page, `page_size` up to 100);
  an out-of-range `page` is clamped to the last page. Entries carry a
  `(used N times)` marker once the skill has been loaded at least once.
- Both listings exclude what is already in **this session's** context: the
  top-5 window plus the skills pinned by this session. An exclusion is only
  reported for a skill that will actually render, so a skill disabled or
  deleted since it was loaded neither renders nor hides from `list_skills`.
- `load_skill` pins the loaded skill to the **calling session** and returns the
  bodies in the tool result (the system prompt is fixed at turn start, so a
  mid-turn load is only usable same-turn through the result). Another chat of
  the same agent sees nothing, and `/new` drops only the retired session's pins.
- A session pins at most **8 skills / 32 KiB of SKILL.md text**. A load that
  would exceed either cap is refused with a message naming the skills and the
  unload call to make room; a partially accepted batch loads what fits.
- Loading a skill that is already pinned is a no-op: no second copy of the body
  and no extra usage count. Each newly pinned skill increments its usage
  counter once.
- `{"skill": "x", "unload": true}` (or `"skills": [...]`) removes a pin and
  never touches the usage counters — they record how often a skill was loaded,
  not how long it stays pinned. Unloading something that is not pinned is
  reported, not an error.
- Subagent runs receive the bodies and the usage count but never pin: a
  subagent prompt renders no pins, so pinning there would inflate the session
  that spawned it while telling the subagent nothing. The result says so.

### Allowlist

The three tools are gated individually by `tools` in `config.json`, and the
`# Skills` prompt section only names the tools the allowlist keeps — an agent
allowed only `search_skill` is never told about `list_skills` or `load_skill`,
mirroring the MCP discovery tools. With the skill tools removed the prompt still
lists the top-5 skills (they are context, not tools). The agent's `skills`
filter and the workspace enabled/disabled settings apply to all three.

### Example

```text
search_skill
{"query": "dagu", "limit": 5}

list_skills
{"page": 2, "page_size": 10}

load_skill
{"skills": ["dagu", "lele"]}

load_skill
{"skill": "dagu", "unload": true}
```

## Environment Variables

Tool settings can be overridden with environment variables using the `LELE_TOOLS_*` prefix.

Examples:

- `LELE_TOOLS_WEB_BRAVE_ENABLED=true`
- `LELE_TOOLS_WEB_PERPLEXITY_API_KEY=...`
- `LELE_TOOLS_WEB_SEARXNG_ENABLED=true`
- `LELE_TOOLS_WEB_SEARXNG_INSTANCE_URL=https://search.example.com`
- `LELE_TOOLS_EXEC_ENABLE_DENY_PATTERNS=false`
- `LELE_TOOLS_CRON_EXEC_TIMEOUT_MINUTES=10`

## Related Docs

- `docs/agents-models-providers.md`
- `docs/client-api.md`
- `docs/mcp.md`
- `docs/SKILL_SUBAGENTS.md`
- `docs/SYSTEM_SPAWN_IMPLEMENTATION.md`
