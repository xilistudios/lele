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

### Redaction of tool output (the log redactor)

Tool output is passed through a redactor before it is handed to the model: every *known* keyring value
longer than 8 characters is replaced by its `{{SECRET:<name>}}` placeholder (longest value first). It is a
mitigation, not a guarantee — know its limits:

- **8-character floor.** A shorter value is never redacted, because a 1–7 character string matches ordinary
  text far more often than it identifies a secret. Store secrets of 8+ characters.
- **Exact substring matching, nothing else.** The comparison is a literal `strings.ReplaceAll` per value.
  A secret that reaches the output encoded (base64, URL-escaped, JSON-escaped), split across lines, chunked
  into pieces, hashed, or with any character changed is **not** redacted. So is a secret lele does not carry
  in its own keyring (`{{ENV_*}}` values, a credential in a file the agent reads, a value another process
  set).
- **The placeholder form is not proof of absence.** A model shown `{{SECRET:<name>}}` can still be asked to
  print it, and code that resolves placeholders (the `exec` tool, provider credentials) expands it again.
- **Tool results, and nothing else.** The redactor runs on the content of a tool result on its way to the
  model. A harness template command (`!shell`, `@path`) is a different surface: its output is expanded into
  the *user* message before the turn starts, so it never passes through this redactor and a secret printed
  there reaches the model and the session in the clear — this change hardens tool output only. Treat harness
  output as a paste, not as a tool result.

Availability is handled the other way round from the matching: redaction **fails closed**. If the redactor
cannot do its job — no keyring service wired, the vault cannot be opened, the master key is unreadable, the
secret list cannot be read — the whole message is replaced by

```text
[log message withheld: secret redaction unavailable]
```

and a single WARN (`Secret redaction unavailable; withholding log message content`, carrying the reason) is
emitted at most once per minute, so a broken keyring cannot flood the log. The log event itself still flows:
only its content is withheld. Two cases are deliberately *not* errors:

- **an empty vault** (a keyring that is open, listable, and holds no secrets) passes the content through
  unchanged — there is nothing to redact, so nothing is withheld;
- **`keyring.enabled: false`** is an explicit opt-out, not an availability failure: content passes through
  unredacted, announced once per redactor instance (the gateway builds one for the agent loop and one
  for the subagent manager) with
  `Secret redaction is disabled by config (keyring.enabled=false); tool output is not redacted`. Withholding
  there would blank every tool result on an install that deliberately runs without a keyring.

The same fail-closed rule is not applied to one narrower case, on purpose and visibly: a **secret that the
store refuses to decrypt** while the rest of the list reads fine. That secret is skipped, so its value is
not redacted while every other value still is; a WARN naming the secrets (never the values) is emitted at
most once a minute. Withholding every message because one entry is unreadable would make the keyring a
denial-of-service on the agent. With the only production store (`FileStore`, which decrypts all-or-nothing)
this is unreachable; it is a seam for other `Service` implementations.

A **nil `*Redactor`** passes content through as well: it means redaction was not wired into that component
at all, which is a wiring state, not a keyring that went down — which is why `Redact` on a nil redactor
returns the content while a wired redactor with no service withholds it.

### The master key sits beside the vault

The file backend keeps the vault and its master key in the same directory:

| file | contents | mode |
|---|---|---|
| `<LeleDir>/keyring.enc` | the encrypted vault (`keyring.path` overrides the path) | `0600` |
| `<LeleDir>/keyring.key` | the raw master key that opens it | `0600` |

This is the standard threat model of an encrypted store for a single-user install: the vault is unusable
without the key, but anyone who can read *both* files can decrypt it. `0600` plus the user's home directory
is therefore the boundary. It protects against a copied or backed-up vault, a shared host with other users,
and a leaked archive — **not** against code running as the same user (including the agent itself, which runs
as that user).

Hardening that needs no migration:

- back up the vault **without** `keyring.key` (a backup of both is a backup of the plaintext), and keep the
  key off remote or cloud backups;
- full-disk encryption on the host, and a `$HOME` that is not inside a world-readable path;
- prefer a keychain backend where one is available (`keyring.backend: "auto"` uses it when present), so the
  master key lives in the OS secret service instead of the filesystem;
- rotate on suspicion: delete `keyring.key` after re-entering the stored secrets, and treat anything that
  ever read the key as compromised.

> **Follow-up note (not part of this change):** relocating the master key to the OS keychain for the *file*
> backend — or moving it out of `LeleDir` — cannot be done without a migration for existing vaults, so it is
> deliberately left out of the hardening backlog. Such a migration must (1) detect `keyring.enc` +
> `keyring.key`, (2) re-wrap or move the key, (3) keep the old location readable for one release so a
> rollback cannot lose the vault, and (4) only then refuse to open a vault whose key still sits beside it.
> Until then, the layout above is the contract.
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
