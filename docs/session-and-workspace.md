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
├── IDENTITY.md
├── MEMORY.md
├── SOUL.md
└── USER.md
```

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

### `SOUL.md`

Longer-lived style, values, or personality guidance.

### `USER.md`

User preferences and expectations.

### `IDENTITY.md`

Identity information about the assistant or installation.

### `MEMORY.md`

Bootstrap memory included in the initial context.

### `HEARTBEAT.md`

Periodic tasks read by the heartbeat service.

## Context Refresh Behavior

Lele rebuilds context from workspace files such as:

- `AGENT.md`
- `SOUL.md`
- `USER.md`
- `IDENTITY.md`
- `MEMORY.md`

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
