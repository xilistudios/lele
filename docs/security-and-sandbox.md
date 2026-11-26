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

## Background Process Visibility

Background processes started with `exec` (and the subagent work attached to a session) carry the session key
that started them, and the `list_background_execs` / `get_background_exec_output` / `stop_background_exec`
tools refuse to touch a process from another session:

- a process whose owner key matches the caller, or whose owner is a **descendant** of the caller
  (`telegram:chat-1:subagent-2` is visible to `telegram:chat-1`), is visible — the `:`-separated hierarchy is
  what lets a parent session reach its subagents;
- sibling subagents are separate sessions and cannot see each other;
- a process with **no** owner key is system-global (started by a cron job that names no session, for example)
  and is visible to every caller;
- a caller with **no** session key sees only the system-global processes. A keyless caller is not a privileged
  caller: it cannot reach a session-owned process, and it cannot read or stop it.

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
- the executed command carries only the reference — `${LELE_SECRET_0}` for POSIX shells,
  `${env:LELE_SECRET_0}` for PowerShell. The braces are part of the emitted form: an unbraced `$NAME`
  would absorb a following word character (`{{SECRET:key}}suffix` must not read the variable
  `LELE_SECRET_0suffix`)
- inherited `LELE_SECRET_*` variables are dropped from the child environment, so a command cannot read a
  value the agent process happens to carry under the same name
- the raw value therefore never lands in `/proc/<pid>/cmdline` (world-readable on most Linux setups); the
  stored and displayed command keeps the `{{SECRET:name}}` placeholder form

Quoting: the placeholder must sit where the shell still expands variables.

| context | example | behavior |
|---|---|---|
| unquoted | `curl -H X-Api-Key:{{SECRET:key}} https://…` | expands; the value is subject to word splitting **and** pathname (glob) expansion |
| double quotes | `curl -H "X-Api-Key: {{SECRET:key}}" https://…` | expands; recommended form |
| single quotes | `echo '{{SECRET:key}}'` | rejected with an actionable error — single quotes disable expansion |
| unquoted heredoc delimiter | `cat <<EOF > f` | expands; the placeholder is replaced by the reference inside the body |
| quoted or escaped heredoc delimiter | `cat <<'EOF'`, `cat <<"EOF"`, `cat <<\EOF` | rejected with an actionable error — a quoted heredoc delimiter disables expansion in the body |
| backslash-escaped (POSIX only) | `echo \{{SECRET:key}}` | left untouched, the literal `{{SECRET:key}}` reaches the command. PowerShell has no backslash escape, so the placeholder still expands there |

Secret values containing shell metacharacters (`` ` ``, `$`, `;`, `|`, `&`, `<`, `>`, quotes, backslashes,
newlines) are rejected outright: store values without shell syntax. Wrap the reference in double quotes so
values containing whitespace survive as a single argument.

Limits worth knowing:

- environment injection removes the **argv** exposure, not the **environment** exposure. The value is
  inherited by the whole child process tree, and `/proc/<pid>/environ` is readable by the process owner
  (mode `0400`): the same user — and root — can still read it, unlike `/proc/<pid>/cmdline`, which is
  world-readable (`0444`).
- any command that prints its environment (`env`, `printenv`, `set -x`, a failing child's diagnostic) writes
  the value into that command's output, and therefore into the tool result and the session. Do not dump the
  environment in a command that references a secret. The log/keyring redactor masks known secret values in
  the copy that reaches the model, but it is a mitigation, not a guarantee: treat such a command as a leak.
- harness template `!shell` commands do **not** resolve `{{SECRET:name}}` placeholders — template expansion
  has no keyring access, so placeholders stay literal there.
- the guard that re-checks the command still runs, and it runs against the value-inlined form of the
  command, exactly as before env injection existed; values are also screened for shell metacharacters. The
  only relaxation is the emitted reference itself: `${LELE_SECRET_<digits>}` is exempt from the generic
  `${…}` deny pattern. Every other `${…}` form stays denied.

## Recommendations

- keep `restrict_to_workspace` enabled unless you have a specific reason not to
- prefer explicit provider env vars or secret management over hardcoding tokens
- keep `cors_origins` minimal for native/web setups
- review custom deny patterns before disabling built-in ones

## Related Docs

- `docs/tools_configuration.md`
- `docs/client-api.md`
- `docs/troubleshooting.md`
