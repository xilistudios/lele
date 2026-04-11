# SYSTEM_SPAWN Implementation

Lele supports delegating scheduled work to subagents through a `SYSTEM_SPAWN:` control message.

## Purpose

This mechanism lets cron and other internal flows trigger asynchronous subagent execution without tightly coupling scheduling logic to subagent internals.

## High-Level Flow

1. A cron job includes a `spawn` block
2. The cron execution generates a `SYSTEM_SPAWN:` message
3. The main message processor detects that prefix
4. The agent delegates the work through the `spawn` tool
5. The subagent runs independently and reports its outcome back

## Spawn Payload Example

```json
{
  "action": "add",
  "message": "Daily database backup",
  "cron_expr": "0 2 * * *",
  "spawn": {
    "task": "Create a PostgreSQL backup and upload it to object storage",
    "agent_id": "coder",
    "label": "backup-daily",
    "guidance": "Only report if the backup fails or storage upload is slow"
  }
}
```

## Generated Control Message Shape

```text
SYSTEM_SPAWN:
TASK: <task>
LABEL: <label>
AGENT_ID: <agent_id>
GUIDANCE: <guidance>
CONTEXT: <original message>
```

Not every field is required, but `TASK` is expected for a new delegated run.

## Why This Design Exists

- keeps cron logic loosely coupled
- allows the normal message processing path to own spawning behavior
- makes internal delegation easier to test
- preserves the same execution model used by normal agent-triggered subagents

## Runtime Effect

When a spawned task starts, the parent flow returns quickly while the subagent continues in the background.

Downstream integrations such as the native channel can surface:

- `tool.executing`
- `tool.result`
- `subagent.result`

with `subagent_session_key` metadata when available.

## Related Docs

- `docs/SKILL_SUBAGENTS.md`
- `docs/tools_configuration.md`
