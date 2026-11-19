# Session And Workspace

Lele is workspace-centered. Prompts, memory, sessions, state, cron jobs, and skills are all anchored to a predictable directory.

## Default Workspace

```text
~/.lele/workspace/
```

The workspace is created during `lele onboard` from embedded templates.

## Typical Layout

```text
~/.lele/workspace/
├── sessions/
├── memory/
├── state/
├── cron/
├── skills/
├── AGENT.md
├── HEARTBEAT.md
└── MEMORY.md
```

An older workspace may additionally contain the **deprecated** persona files
`SOUL.md`, `USER.md` and `IDENTITY.md` — see
[Deprecated context files](#deprecated-context-files).

## What Each File Or Directory Does

### `sessions/`

Stores persisted chat session data.

### `memory/`

Holds long-lived memory files used as part of the agent context.

### `state/`

Stores runtime state such as last active channel and chat information.

### `cron/`

Stores scheduled jobs such as reminders and recurring tasks.

### `skills/`

Contains workspace-local installed skills.

### `AGENT.md`

Primary agent behavior and instructions.

### `MEMORY.md`

Bootstrap memory included in the initial context. The agent updates it with
the `update_memory` tool (`append` adds a fragment, `replace` rewrites the
whole file) instead of writing the file path directly. Every write is capped
at `agents.defaults.memory_max_lines` lines (default `500`) and at a hard
512 KiB byte ceiling independent of the line cap (a single arbitrarily long
line counts as one line, and `MEMORY.md` is inlined into every system
prompt): an over-cap write — line or byte — is rejected and nothing is
written; the agent must compact with `replace`.

### `HEARTBEAT.md`

Periodic tasks read by the heartbeat service.

### Deprecated context files

`SOUL.md` (style, values, personality), `USER.md` (user preferences) and
`IDENTITY.md` (identity of the assistant or installation) are **deprecated**.

- lele no longer creates them: a new workspace gets `AGENT.md`, `MEMORY.md` and
  `HEARTBEAT.md` only, and their default templates were removed.
- They are **not** removed from an existing workspace. If the files are there,
  nothing changes: they are still inlined into the system prompt (in the
  `AGENT.md` → `SOUL.md` → `USER.md` → `IDENTITY.md` → `MEMORY.md` order of
  `pkg/context.BootstrapFiles`) and they are still recognised as context files,
  so the REST/WebUI editors keep reading and writing them
  (`pkg/context.IsContextFile`).
- Put long-lived behaviour into `AGENT.md` (or per-agent `AGENT.md` files) and
  durable facts into `MEMORY.md`; both are still first-class.

## Context Refresh Behavior

Lele rebuilds context from the workspace context files:

- `AGENT.md`
- `MEMORY.md`
- plus `SOUL.md`, `USER.md` and `IDENTITY.md` **when the workspace still has
  them** (deprecated, see above — absent files are skipped)

This is especially visible when sessions are reset or ephemeral mode starts a fresh conversation.

## Ephemeral Sessions

Session behavior is configured under:

```json
{
  "session": {
    "ephemeral": true,
    "ephemeral_threshold": 560
  }
}
```

When enabled, an idle conversation can automatically start fresh after the configured threshold.

## Named-Agent Workspaces

Named agents can use different workspaces. This is useful when you want:

- separate prompts
- separate session history
- separate memory and state
- separate skill installations

Example:

```json
{
  "agents": {
    "list": [
      {
        "id": "coder",
        "workspace": "~/.lele/workspace-coder"
      }
    ]
  }
}
```

## Attachments And Temporary Uploads

Temporary uploads for the native channel live under:

```text
~/.lele/tmp/uploads/
```

They are separate from the workspace and cleaned up by TTL.

## Recommendations

- keep your workspace under versioned backups if it contains important prompts or memory
- use separate workspaces only for real separation, not just cosmetic naming
- keep `AGENT.md` short and stable, and move dynamic facts to `MEMORY.md` or `memory/`

## Related Docs

- `docs/installation-and-onboarding.md`
- `docs/skills-authoring.md`
- `docs/security-and-sandbox.md`
