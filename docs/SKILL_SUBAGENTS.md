# Subagents In Lele

Subagents are independent agent executions that can work in parallel with the main agent.

Lele supports two related tools:

- `spawn`: run a subagent asynchronously in the background
- `subagent`: run a subagent synchronously and return the result directly

## What Subagents Are Good For

- long-running delegated work
- parallel exploration or analysis
- scheduled tasks that should not block the main agent
- isolated work with a separate tool loop

## Current Behavior

Subagents inherit the parent agent's execution context where relevant, including:

- model/provider runtime
- workspace restrictions
- available tools registry
- tool iteration limits

They do not behave like shared-session clones of the parent. A subagent is an independent execution with its own task lifecycle.

## Available Subagent Tools

### `spawn`

Runs a task in the background and reports status/results back to the parent flow.

Parameters:

| Parameter | Required | Description |
| --- | --- | --- |
| `task` | yes, unless continuing | Task for a new subagent |
| `label` | no | Short display label |
| `agent_id` | no | Optional target agent |
| `task_id` | no | Existing paused subagent task to continue |
| `guidance` | no | Extra guidance when continuing |

Example:

```json
{
  "tool": "spawn",
  "args": {
    "task": "Review the current config and summarize risky settings",
    "label": "config-review"
  }
}
```

### `subagent`

Runs a delegated task synchronously and returns the result directly to the caller.

Parameters:

| Parameter | Required | Description |
| --- | --- | --- |
| `task` | yes | Task to execute |
| `label` | no | Optional display label |

Example:

```json
{
  "tool": "subagent",
  "args": {
    "task": "Summarize the purpose of the files under pkg/channels",
    "label": "channels-summary"
  }
}
```

## Session And Result Semantics

- Async subagents receive task IDs such as `subagent-1`
- Related WebSocket/native integrations expose subagent session keys in the form `subagent:<task-id>`
- Async results are reported back through the parent flow instead of independently messaging users by default

## Security Model

Subagents inherit the same safety boundaries as the parent agent:

- workspace path restrictions
- exec deny patterns
- normal file-size and upload constraints
- agent-specific allowlists when configured

If a parent agent cannot access a path or use a tool, the subagent should be expected to follow the same restriction.

## Practical Use Cases

### Parallel analysis

Use `spawn` to inspect different parts of a repository or different external tasks without blocking the main conversation.

### Background scheduled work

Cron jobs can create subagents through `spawn`, which is useful for periodic checks, maintenance tasks, and research-style jobs.

### Focused synchronous delegation

Use `subagent` when you want an isolated tool loop but still need the result inline during the current turn.

## Known Constraints

- subagents do not share the full live message history of the parent session
- async subagents may pause waiting for more guidance
- results are reported back as task outcomes, not merged automatically into prior messages

## Related Docs

- `docs/SYSTEM_SPAWN_IMPLEMENTATION.md`
- `docs/tools_configuration.md`
- `docs/client-api.md`
