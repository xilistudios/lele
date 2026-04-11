# Tools Configuration

Lele exposes tool-related runtime settings through the `tools` section in `~/.lele/config.json`.

## Current Structure

```json
{
  "tools": {
    "web": {
      "brave": { "enabled": false, "api_key": "", "max_results": 5 },
      "duckduckgo": { "enabled": true, "max_results": 5 },
      "perplexity": { "enabled": false, "api_key": "", "max_results": 5 }
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

## Environment Variables

Tool settings can be overridden with environment variables using the `LELE_TOOLS_*` prefix.

Examples:

- `LELE_TOOLS_WEB_BRAVE_ENABLED=true`
- `LELE_TOOLS_WEB_PERPLEXITY_API_KEY=...`
- `LELE_TOOLS_EXEC_ENABLE_DENY_PATTERNS=false`
- `LELE_TOOLS_CRON_EXEC_TIMEOUT_MINUTES=10`

## Related Docs

- `docs/agents-models-providers.md`
- `docs/client-api.md`
- `docs/SKILL_SUBAGENTS.md`
- `docs/SYSTEM_SPAWN_IMPLEMENTATION.md`
