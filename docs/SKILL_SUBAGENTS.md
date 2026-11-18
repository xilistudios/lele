# Subagents In Lele

Subagents are independent agent executions that can work in parallel with the main agent.

Lele supports two related tools:

- `spawn`: run a subagent asynchronously in the background
- `subagent`: run a subagent synchronously and return the result directly

Once a task exists there are three more tools to work with it: `list_active_subagents` (the session's task ledger), `wait_for_subagent` (block for a result) and `cancel_subagent` (stop a task).

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
| `model` | no | Model for the subagent (e.g. `anthropic:claude-opus`); defaults to the agent's configured model |
| `dependencies` | no | Task IDs that must complete before this task starts |
| `max_retries` | no | Retries for transient failures; omitted or non-positive falls back to `agents.defaults.subagent_max_retries` (default 2) |

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
| `agent_id` | no | Optional target agent |

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

### `list_active_subagents`

Lists the session's subagent tasks, newest first. This is the session's **ledger**, not a view of live memory: it
merges the tasks the manager is still tracking with the tasks recovered from their persisted subagent sessions,
so a task stays listed after retention reaped it (finished tasks are kept for
`agents.defaults.subagent_retention_minutes`, 5 minutes by default) and after a restart. That is what makes it
useful after context compaction, which drops the `spawn` tool results but not the sessions.

Parameters:

| Parameter | Required | Description |
| --- | --- | --- |
| `status` | no | Filter applied before pagination. Default `active`. See the values below |
| `limit` | no | Maximum tasks per page. Default `20`, clamped to `1..100` |
| `offset` | no | Number of tasks to skip. Default `0` |
| `include_completed` | no | Deprecated alias for `status=all`. Only applies when `status` is omitted |

`status` accepts:

| Value | Meaning |
| --- | --- |
| `active` | Default. `running`, `pending` or `needs_context` — everything that can still produce a result |
| `all` | No filtering: the full history |
| `running` | Executing now |
| `pending` | Waiting for its `dependencies` to complete |
| `needs_context` | Paused, waiting for guidance |
| `completed` | Finished successfully |
| `failed` | Failed with an error |
| `cancelled` | Cancelled by `cancel_subagent` or a stop |
| `not_done` | Could not complete with the current constraints |
| `unknown` | The outcome was never recorded: the task was interrupted (a restart), or its live row now lives in another agent's manager |

An unknown `status` value is an error, not a silent `all` — a typo must not widen the filter. An explicit `status`
always wins over `include_completed`.

Each row is one line:

```text
- subagent-3 [running] audit-auth (agent: coder): scanning pkg/auth [progress: 4/9 files] (running 12s)
- subagent-2 [completed] (persisted) config-review: found 3 risky settings
```

`(persisted)` marks a row recovered from disk rather than a live task. `(running <duration>)` appears only for a
live, still-running task. The label and summary are model-authored, so each is flattened to a single line and
capped (200 bytes for the label and the summary, 120 for the progress and the context request).

Pagination: when the filtered set does not fit in one page the header gains the window and a footer carries the
cursor:

```text
All subagent tasks 37 total, showing 1-20 (newest first)
- subagent-37 [completed] (persisted) config-review
…

Showing 1-20 of 37 · page 1 of 2 · next page: offset=20&limit=20
```

The `next page` clause is omitted on the last page, so you can stop paging on the footer alone. An `offset` past
the end is not an error: it renders `Showing 0 of 37 · offset 40 is past the end` and no cursor. The order is
total (creation time, then task ID), so consecutive pages do not duplicate or skip rows — with one caveat: if a
task is reaped between two page fetches it moves from the in-memory row to the session row, whose creation
timestamp can differ by a few milliseconds, so it can shift across a page boundary once. The filter is applied
before the window, so `total`, the page and the footer all describe the same filtered set.

### `wait_for_subagent`

Blocks until a task reaches a terminal state (`completed`, `failed`, `not_done`, `cancelled`) or pauses
(`needs_context`), then returns its result. This is how you collect an async result inside the current turn
instead of relying on the callback.

| Parameter | Required | Description |
| --- | --- | --- |
| `task_id` | yes | Task ID, e.g. `subagent-1` |
| `timeout_seconds` | no | Default `120`, clamped to `1..600` |

The result carries the status, label, agent, summary, any context request and the task's details. A
`needs_context` task is returned with a hint to continue it with `spawn` (`task_id` plus `guidance`). On timeout
the call fails with the current status; if the task turns terminal while the call is giving up, the result is
returned instead of a timeout error.

`wait_for_subagent` only sees the tasks the manager is still tracking in memory. For a task that was reaped or
that predates a restart, `list_active_subagents` still lists it but `wait_for_subagent` answers
`Subagent task not found`.

### `cancel_subagent`

Cancels a task that is `running`, `pending` or `needs_context`.

| Parameter | Required | Description |
| --- | --- | --- |
| `task_id` | yes | Task ID, e.g. `subagent-1` |

A task that is already terminal is not cancelled; the call reports
`Subagent task <id> is already in terminal status (<status>) and cannot be cancelled.` A cancelled task's status
becomes `cancelled` and its summary becomes `Task cancelled`.

## Session And Result Semantics

- Async subagents receive task IDs such as `subagent-1`
- Related WebSocket/native integrations expose subagent session keys in the form `subagent:<task-id>`
- Async results are reported back through the parent flow instead of independently messaging users by default
- The subagent **session** is the durable record. Each task is stored under the session key
  `<origin-session-key>:<task-id>`, where the origin key is `<channel>:<chatID>`; the in-memory task is the
  authoritative row while it is alive, and the ledger described above is rebuilt from the session rows once the
  task is gone

## The `## Subagent Tasks` Prompt Section

When a session has subagent tasks, its system prompt ends with a `## Subagent Tasks` section rendered from the
same ledger, because the prompt is rebuilt every turn and therefore survives compaction:

```text
## Subagent Tasks

Subagent tasks spawned from this session (newest first). This ledger survives context compaction: finished tasks stay listed here even when the original tool results are no longer in context.

- subagent-3 [running] coder "audit-auth": scanning pkg/auth
- subagent-2 [completed] (persisted) "config-review"
… 5 older task(s) not shown.

Use `list_active_subagents` (status/limit/offset) to page through the full history, and `wait_for_subagent` for the full result of a task that is still alive.
```

The section is bounded: at most 8 rows, with the label and summary of each row capped at 200 bytes, and an
`… N older task(s) not shown.` line when the ledger is longer. A persisted row shows its label but not its
summary — fetching that would be a store query on every turn — so the tool page is where you read it. No elapsed
time is rendered, so the section is byte-identical from one turn to the next. A session without subagent tasks
gets no section at all: its prompt is exactly the prompt it would have without this feature.

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
- history is recovered from the session metadata and the session store, so a task is only as durable as its
  session row. A deployment with no session store keeps the in-memory half only
- the executor of a row written before the session row carried `agent_id` comes from the spawn-time pin recorded
  at spawn; when neither exists the row names no agent
- a reaped row whose status was never persisted reads `unknown`, not `completed`. The session row is created when
  the task starts and the status is written only on a terminal transition, so an interrupted task (restart) or one
  whose live row now belongs to another agent's manager has no status to report
- `wait_for_subagent` and `cancel_subagent` only reach tasks the manager still tracks in memory; the ledger is the
  only view that includes reaped and pre-restart tasks
- a persisted row shows its label but not its summary, because fetching the summary on every turn would be a
  store query; the tool page carries it
- paging is stable within a call, but a task that flips from memory to disk between two page fetches can shift by
  the few milliseconds between the two creation timestamps

## Related Docs

- `docs/SYSTEM_SPAWN_IMPLEMENTATION.md`
- `docs/tools_configuration.md`
- `docs/client-api.md`
