# Security And Sandbox

Lele defaults to a workspace-first execution model designed to reduce accidental damage from agent tool usage.

## Workspace Restriction

The main control is:

```json
{
  "agents": {
    "defaults": {
      "restrict_to_workspace": true
    }
  }
}
```

When enabled, file and command operations are intended to stay bounded to the configured workspace.

## Exec Tool Protection

Lele also protects `exec` with deny patterns.

Relevant config:

```json
{
  "tools": {
    "exec": {
      "enable_deny_patterns": true,
      "custom_deny_patterns": []
    }
  }
}
```

These patterns are meant to block clearly dangerous operations such as:

- destructive deletes
- raw disk writes
- shutdown and reboot commands
- shell injection patterns
- piping downloaded content into shells

## Approval Flow

Some sensitive actions can surface approval requests through supported channels.

This is especially relevant for:

- guarded command execution
- native/web UI approval prompts

The native channel emits `approval.request` and expects an `approve` response event from clients.

## Native Channel Security

The native channel includes:

- PIN-based pairing
- bearer-token auth
- refresh tokens
- session ownership checks
- CORS origin filtering
- upload limits and TTL cleanup

Clients can only access session keys within their own namespace.

## Upload Safety

Native/web uploads are:

- size-limited by `max_upload_size_mb`
- stored under `~/.lele/tmp/uploads/`
- cleaned up using `upload_ttl_hours`

## Logs And Secrets

Lele stores logs locally and the editable config API marks secret fields with metadata.

Even so, avoid placing secrets in prompts, memory files, or user-facing session content.

## Exec Secrets

The `exec` tool resolves `{{SECRET:name}}` placeholders against the agent-scoped keyring at run time.
Resolution is environment-based, never argv-based:

- every distinct secret referenced by a command is exported to the child process environment as
  `LELE_SECRET_0`, `LELE_SECRET_1`, … in first-appearance order (one variable per secret name, so the
  mapping is deterministic and cannot collide between secrets)
- the executed command carries only the reference — `$LELE_SECRET_0` for POSIX shells, `$env:LELE_SECRET_0`
  for PowerShell
- the raw value therefore never lands in `/proc/<pid>/cmdline` (world-readable on most Linux setups), in
  logs, or in session history; the stored and displayed command keeps the `{{SECRET:name}}` placeholder form

Quoting: the placeholder must sit where the shell still expands variables.

| context | example | behavior |
|---|---|---|
| unquoted | `curl -H X-Api-Key:{{SECRET:key}} https://…` | expands; the value is subject to word splitting |
| double quotes | `curl -H "X-Api-Key: {{SECRET:key}}" https://…` | expands; recommended form |
| single quotes | `echo '{{SECRET:key}}'` | rejected with an actionable error — single quotes disable expansion |
| backslash-escaped | `echo \{{SECRET:key}}` | left untouched, the literal `{{SECRET:key}}` reaches the command |

Secret values containing shell metacharacters (`` ` ``, `$`, `;`, `|`, `&`, `<`, `>`, quotes, backslashes,
newlines) are rejected outright: store values without shell syntax. Wrap the reference in double quotes so
values containing whitespace survive as a single argument.

Limits worth knowing:

- environment injection removes the **argv** exposure, not the **environment** exposure: a child process tree
  can still read the value from `/proc/<pid>/environ` (restricted to the process owner unless `hidepid=0`).
- harness template `!shell` commands do **not** resolve `{{SECRET:name}}` placeholders — template expansion
  has no keyring access, so placeholders stay literal there.
- the guard that re-checks the resolved command still runs, and values are still screened for shell
  metacharacters; env injection is not a relaxation of either check.

## Recommendations

- keep `restrict_to_workspace` enabled unless you have a specific reason not to
- prefer explicit provider env vars or secret management over hardcoding tokens
- keep `cors_origins` minimal for native/web setups
- review custom deny patterns before disabling built-in ones

## Related Docs

- `docs/tools_configuration.md`
- `docs/client-api.md`
- `docs/troubleshooting.md`
