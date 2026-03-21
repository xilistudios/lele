# Tools Configuration

Lele's tools configuration is located in the `tools` field of `config.json`.

## Directory Structure

```json
{
  "tools": {
    "web": { ... },
    "exec": { ... },
    "approval": { ... },
    "cron": { ... }
  }
}
```

## Web Tools

Web tools are used for web search and fetching.

### Brave

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | false | Enable Brave search |
| `api_key` | string | - | Brave Search API key |
| `max_results` | int | 5 | Maximum number of results |

### DuckDuckGo

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | true | Enable DuckDuckGo search |
| `max_results` | int | 5 | Maximum number of results |

### Perplexity

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | false | Enable Perplexity search |
| `api_key` | string | - | Perplexity API key |
| `max_results` | int | 5 | Maximum number of results |

## Exec Tool

The exec tool is used to execute shell commands.

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enable_deny_patterns` | bool | true | Enable default dangerous command blocking |
| `custom_deny_patterns` | array | [] | Custom deny patterns (regular expressions) |

### Functionality

- **`enable_deny_patterns`**: Set to `false` to completely disable the default dangerous command blocking patterns
- **`custom_deny_patterns`**: Add custom deny regex patterns; commands matching these will be blocked

### Default Blocked Command Patterns

By default, Lele blocks the following dangerous commands:

- Delete commands: `rm -rf`, `del /f/q`, `rmdir /s`
- Disk operations: `format`, `mkfs`, `diskpart`, `dd if=`, writing to `/dev/sd*`
- System operations: `shutdown`, `reboot`, `poweroff`
- Command substitution: `$()`, `${}`, backticks
- Pipe to shell: `| sh`, `| bash`
- Privilege escalation: `sudo`, `chmod`, `chown`
- Process control: `pkill`, `killall`, `kill -9`
- Remote operations: `curl | sh`, `wget | sh`, `ssh`
- Package management: `apt`, `yum`, `dnf`, `npm install -g`, `pip install --user`
- Containers: `docker run`, `docker exec`
- Git: `git push`, `git force`
- Other: `eval`, `source *.sh`

### Configuration Example

```json
{
  "tools": {
    "exec": {
      "enable_deny_patterns": true,
      "custom_deny_patterns": [
        "\\brm\\s+-r\\b",
        "\\bkillall\\s+python"
      ],
    }
  }
}
```

## Approval Tool

The approval tool controls permissions for dangerous operations.

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | true | Enable approval functionality |
| `write_file` | bool | true | Require approval for file writes |
| `edit_file` | bool | true | Require approval for file edits |
| `append_file` | bool | true | Require approval for file appends |
| `exec` | bool | true | Require approval for command execution |
| `timeout_minutes` | int | 5 | Approval timeout in minutes |

## Cron Tool

The cron tool is used for scheduling periodic tasks.

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `exec_timeout_minutes` | int | 5 | Execution timeout in minutes, 0 means no limit |

### Cron Job Execution Modes

Cron jobs have three execution modes based on the payload configuration:

#### 1. Direct Delivery (`deliver: true`)

Sends the message directly to the channel without agent processing. Useful for simple reminders.

```json
{
  "action": "add",
  "message": "Reminder: Submit your report",
  "cron_expr": "0 9 * * *",
  "deliver": true
}
```

#### 2. Agent Processing (`deliver: false`)

Processes the message as a normal conversation with the main agent. The agent can use tools and perform complex tasks.

```json
{
  "action": "add",
  "message": "Analyze system logs and report issues",
  "cron_expr": "0 9 * * *",
  "deliver": false
}
```

#### 3. Command Execution (`command`)

Executes a shell command directly and sends the output to the channel. Automatically sets `deliver: false`.

```json
{
  "action": "add",
  "message": "Disk usage check",
  "cron_expr": "0 9 * * *",
  "command": "df -h"
}
```

#### 4. Subagent Spawn (`spawn`)

Spawns a subagent to handle the task. Generates a `SYSTEM_SPAWN:` message that delegates to a subagent with specific instructions.

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

**Spawn Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `task` | string | Required. Task description for the subagent |
| `label` | string | Optional. Short identifier |
| `agent_id` | string | Optional. Target agent ID |
| `guidance` | string | Optional. Additional instructions |

**Generated SYSTEM_SPAWN: Message Format:**

```
SYSTEM_SPAWN:
TASK: <task>
LABEL: <label>
AGENT_ID: <agent_id>
GUIDANCE: <guidance>
CONTEXT: <original message>
```

**Behavior Notes:**
- When `spawn` is set, `deliver` is automatically `false`
- `at` jobs (one-time) are deleted after execution
- `every` and `cron` jobs remain active until removed or disabled

## Environment Variables

All configuration options can be overridden via environment variables with the format `LELE_TOOLS_<SECTION>_<KEY>`:

For example:
- `LELE_TOOLS_WEB_BRAVE_ENABLED=true`
- `LELE_TOOLS_EXEC_ENABLE_DENY_PATTERNS=false`
- `LELE_TOOLS_CRON_EXEC_TIMEOUT_MINUTES=10`

Note: Array-type environment variables are not currently supported and must be set via the config file.
