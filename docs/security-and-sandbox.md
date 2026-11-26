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

When enabled — it is on by default (`pkg/config/config.go:1118`) — `restrict_to_workspace` bounds two
specific things: the `working_dir` argument of the `exec` tool, and the paths the file tools accept. It is
**not** a sandbox around commands: nothing resolves or contains what a command touches, so any command that
gets past the lexer described below can `cd`, read and write anywhere the operating system lets the lele user
go.

### What `exec` enforces: the working directory, nothing else

Before a command runs, `exec` resolves the directory it will start in — the `working_dir` argument, or the
agent's workspace when the argument is omitted (`pkg/tools/shell.go:607-617`) — and rejects it when it falls
outside the configured workspace (`pkg/tools/shell.go:621-633`, using `isWithinWorkspace` from
`pkg/tools/filesystem.go:75-78`):

```text
access denied: working_dir is outside the workspace
```

That directory is then handed to the shell as its starting directory (`pkg/tools/shell.go:690-692`). Beyond
it, the tool knows nothing about what the command will touch:

- a command that starts can leave the workspace immediately: `cd ~ && pwd` prints the home directory,
  `cd / && ls etc` lists the system `/etc`, and `cd $HOME && echo hi > probe` writes there. Which spelling
  gets that far is decided by the lexer below; what the command can then touch is decided by the operating
  system, and by the user lele runs as.
- the tool does not resolve anything inside the command, so `~`, `$HOME`, globs, variables, `$(…)`,
  symlinks and `cd`-relative paths are invisible to it.

The *paths inside* the command string are inspected, and only lexically and only while
`restrict_to_workspace` is on (`pkg/tools/shell.go:893-921`). That pass is a text heuristic and it is wrong
in both directions. The deny patterns are a separate pass that does **not** depend on the flag — it runs
whenever `enable_deny_patterns` is on (`pkg/tools/shell.go:450`, `pkg/tools/shell.go:874-878`), so turning
`restrict_to_workspace` off does not remove it:

| lexical check | anchor | consequence |
|---|---|---|
| the substring `../` or `..\` anywhere in the command blocks it (`path traversal detected`) | `pkg/tools/shell.go:894-896` | over-blocks — `echo a../b` is refused; a command that never spells those two characters passes |
| every `/…` fragment (or `C:\…` drive fragment) is resolved **from the filesystem root**, so only absolute paths that really are inside the working directory survive (`path outside working dir`) | `pkg/tools/shell.go:903-920` | over-blocks — `cat sub/x.txt` is refused even though it is inside the workspace (the `/x.txt` fragment is read as `/x.txt`); under-blocks — `cd / && ls etc` runs, because `ls etc` contains no `/` |
| deny patterns | `pkg/tools/shell.go:874-878` | name-based only, independent of `restrict_to_workspace`; see [Exec Tool Protection](#exec-tool-protection) |

These are accident guards against obviously dangerous one-liners, not a security boundary. Real command
containment needs an OS-level sandbox — namespaces, seccomp, a container, or a separate unprivileged
user — which lele does not implement.

### What the file tools enforce: containment, with symlinks resolved

Every file-tool path goes through `validatePath` (`pkg/tools/filesystem.go:12-60`): a relative path is
joined to the workspace, the result is checked with `isWithinWorkspace`, and — unlike `exec` — symlinks
are resolved with `filepath.EvalSymlinks` for both the workspace and the target, so a symlink that leads
out of the workspace is rejected (`pkg/tools/filesystem.go:38-44`):

```text
access denied: symlink resolves outside workspace
```

A path that does not exist yet cannot sidestep that check: the nearest existing ancestor is resolved instead
(`pkg/tools/filesystem.go:46-53` with `pkg/tools/filesystem.go:62-73`), so a new file cannot be created
through a symlinked directory that escapes the workspace.

### What is and isn't enforced

Every `enforced` cell below holds **with `restrict_to_workspace` on** — and, for the file-tool row, with a
non-empty workspace configured; the deny-pattern row is the one entry that is independent of the flag.

| surface | what is actually enforced | anchor | verdict |
|---|---|---|---|
| `exec` `working_dir` argument | must be inside the configured workspace, otherwise the call fails | `pkg/tools/shell.go:621-633` | enforced |
| `exec` command string | lexical `../` substring and `/…` fragment scan; no path resolution, no containment | `pkg/tools/shell.go:893-921` | best-effort: refuses some harmless commands, evaded by any path without a `/` |
| `exec` deny patterns | regex match on the command before it runs | `pkg/tools/shell.go:874-878` | enforced as far as the patterns reach |
| approval flow | deny-pattern hits become an `ApprovalRequired` result when the agent is wired to an approval manager; the lexical path checks block outright | `pkg/tools/shell.go:635-658`, `pkg/agent/tool_coordinator.go:575-576` | enforced |
| file tools (`read_file`, `write_file`, `edit_file`, …) | workspace containment **with** `EvalSymlinks` on the target and on the nearest existing ancestor | `pkg/tools/filesystem.go:12-60` | enforced |
| harness `!shell` and `@path` | their own gates only; the `exec` tool, its deny patterns, its approvals and its working-directory check are all bypassed — see [Harness command surfaces](#harness-command-surfaces-shell-path) | `pkg/harness/expand.go:113-118`, `pkg/harness/expand.go:169-193` | trusted-user surface, reachable by the agent itself |
| the workspace boundary as a whole | nothing stops a command from leaving the workspace: `cd / && ls etc` lists `/etc`, `cd $HOME && echo hi > probe` writes in the home directory — a `cd` plus a slash-free path is all it takes | `pkg/tools/shell.go:690-692` | **not a sandbox** |

### Harness command surfaces (`!shell`, `@path`)

Slash-command templates expand while the inbound message is being prepared — before the agent turn is even
built (`pkg/agent/message_processor.go:86`, `pkg/agent/harness_command.go:172-186`). Whatever such a template
runs therefore happens **outside** the `exec` tool: no deny pattern, no approval request, no
`restrict_to_workspace` working-directory check. The command is authored by the operator, but that makes it a
*trusted* surface, **not** a human-only one: nothing checks who produced the message, and the agent can reach
this surface by itself. Its own `cron` tool is registered on the agent loop (`cmd/lele/gateway.go:822-830`)
and takes the message text verbatim from the agent (`pkg/tools/cron.go:207-210`,
`pkg/tools/cron.go:293-302`); a scheduled job whose message starts with `/name` is fed back through the
message processor (`pkg/tools/cron.go:488-494`) and expanded exactly like a typed one
(`pkg/agent/message_processor.go:86`). The `system`-channel diversion does not apply — the job carries the
session's own channel, not `system` (`pkg/agent/message_processor.go:45-47`) — and `deliver: false` only
suppresses the outbound notification. That route reaches `@path` as well, including the ungated relative
form. Keep `allow_shell` and `allow_absolute_files` off unless you also accept it. Each expansion form has
its own gate:

| form | what it does | gate | anchor |
|---|---|---|---|
| `` !`command` `` | runs the command through `sh -c` with the workspace as its working directory, a 15 s timeout (`pkg/harness/expand.go:18`) and output capped at 32 KiB (`DefaultMaxShellOutput`, `pkg/harness/expand.go:15`), then pastes the output into the prompt | `harness.allow_shell` or the command's own `allow_shell`; **off by default**, and without it the placeholder expands to `[shell disabled]` | `pkg/harness/expand.go:113-118`, `pkg/harness/expand.go:141-163`, `pkg/harness/manager.go:147-158` |
| `@path` (relative) | reads the file (capped at 64 KiB, `DefaultMaxFileOutput`, `pkg/harness/expand.go:16`) and pastes it into the prompt | none beyond the lexical check below | `pkg/harness/expand.go:169-193` |
| `@/absolute/path` | reads any absolute path, inside the workspace or not | `harness.allow_absolute_files` or the command's own `allow_absolute_files`; **off by default**, and while it is off the placeholder becomes `[blocked: absolute path]` | `pkg/harness/expand.go:170-178`, `pkg/harness/manager.go:180-193` |

The template command inherits lele's own environment unchanged — `os.Environ()` plus four non-interactive
shell variables (`pkg/harness/expand.go:148-153`) — so anything lele carries in its environment (provider
keys exported as env vars, for instance) and any `LELE_SECRET_*` variable already present in the parent
process are readable from it. That is not the environment the `exec` tool builds, which drops inherited
`LELE_SECRET_*` and injects only the secrets the command references (`pkg/tools/shell.go:111-124`).

The `@path` containment is lexical and symlink-following, so it is weaker than the file tools: the path is
joined to the workspace and compared with `filepath.Rel` (`pkg/harness/expand.go:180-188`, message
`[outside workspace: @path]`), and there is no `EvalSymlinks` anywhere in the path. A symlink inside the
workspace that points outside it therefore passes the check and its target is read
(`pkg/harness/expand.go:189-192` follows the link with `os.Stat`/`os.ReadFile`). Treat workspace symlinks as
trusted content: a repository can ship one.

Knobs, in `config.json`: `harness.allow_shell` and `harness.allow_absolute_files` set the defaults, and each
command definition can override them with `allow_shell` and `allow_absolute_files`
(`pkg/config/config.go:150-162`). Per-command runtime overrides live in the harness manager
(`pkg/harness/manager.go:147-158`, `pkg/harness/manager.go:180-193`).

`{{SECRET:name}}` placeholders are **not** resolved in a harness `!shell` command: template expansion has no
keyring access, so the placeholder reaches the shell literally. See [Exec Secrets](#exec-secrets), which
covers the `exec` tool only.

Whatever a template prints is **prompt content, not a tool result**: the expansion is spliced into the user
message before the turn is built, so it never passes through the tool-output redactor described in
[Redaction of tool output](#redaction-of-tool-output-the-log-redactor). A secret printed by a `!shell`
command, or read out of a pasted `@path`, reaches the model and the session in the clear — treat harness
output as a paste, not as a tool result. Only the tool-result paths are redacted
(`pkg/tools/toolloop.go:554`, `pkg/agent/llm_runner.go:794`, `pkg/agent/group_turn.go:212`,
`pkg/agent/group_turn.go:221`).

## Exec Tool Protection

Lele also protects `exec` with deny patterns. They are regexes matched against the lowercased,
whitespace-trimmed command after the emitted secret references (`${LELE_SECRET_<n>}`) are masked to a neutral
token (`pkg/tools/shell.go:866-878`, mask at `pkg/tools/shell.go:107-109`). A match blocks the command
before anything runs — it does not inspect what the command would actually touch, so it is a name-based
filter, not a boundary.

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

Both shortcuts match a **normalized** key, not the literal text: the command is lowercased and its runs of
whitespace collapsed to one space (`NormalizeWhitelistKey`, `pkg/tools/shell.go:44-46`), so the exempt set
is wider than the spelling that was written down.

- a command listed in `tools.exec.whitelist_commands` (`pkg/config/config.go:1104`, loaded at
  `pkg/tools/shell.go:487-489`) skips the guard entirely, for that normalized key
  (`pkg/tools/shell.go:870-872`);
- when an operator approves a blocked command, the re-run is executed with the guard bypassed, again for
  that normalized key only (`pkg/agent/tool_executor.go:203-210` with `pkg/tools/base.go:62-73`). The
  bypass is never stored: the next `exec` call of the same command is guarded again, and the `working_dir`
  check at `pkg/tools/shell.go:621-633` runs first and unconditionally in either case.

If you need a guarantee rather than a filter, run lele as a dedicated unprivileged user, or inside a
container whose filesystem the agent is allowed to damage.

## Approval Flow

Some sensitive actions can surface approval requests through supported channels.

This is especially relevant for:

- guarded command execution
- native/web UI approval prompts

Approval applies to **deny-pattern hits only**: those are the blockable cases (`pkg/tools/shell.go:874-878`
returns `true` for blockable, and `pkg/tools/shell.go:638-657` turns them into an `ApprovalRequired` result
when the agent has an approval manager, `pkg/agent/tool_coordinator.go:575-576`). The allowlist check, the
`../` traversal check and the `/…` fragment check are non-blockable, so they always fail outright and
there is nothing to approve (`pkg/tools/shell.go:880-891`, `pkg/tools/shell.go:893-921`).

The native channel emits `approval.request` and expects an `approve` response event from clients.

### Who may resolve a request

An approval is owned by the peer that requested it, and the two kinds of surface state that ownership
differently:

- **Requester surfaces** (`telegram`) record the requesting user, as `<numeric user id>` or
  `<numeric user id>|<username>`. Only that user may approve, reject or view the request, in any chat of the
  session — the callbacks carry the user id, so the username part is never what grants authority and a
  username change does not lock the requester out. Anything else gets a requester-mismatch notice and the
  request stays pending for its owner.
- **Session surfaces** (native/WebUI, REST, WebSocket, TUI, CLI) have no per-user requester: the client that
  owns the session key approves its own tool calls. Such a turn records an **empty** requester, so the
  requester check cannot lock the card out; the session-ownership check is the whole authority.

A callback also has to match the approval's session: the key is built with the same route builder the turn
used, and a key that is empty on either side matches nothing (fail closed). Anonymous group admins all arrive
as Telegram's `GroupAnonymousBot`, so for them per-requester authority degrades to "any anonymous admin".

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
  (`telegram:chat-1:subagent-2` is visible to `telegram:chat-1`), is visible — the `:`-separated
  hierarchy is what lets a parent session reach its subagents;
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
> backend — or moving it out of `LeleDir` — cannot be done without a migration for existing vaults, so it
> is deliberately left out of the hardening backlog. Such a migration must (1) detect `keyring.enc` +
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
  has no keyring access, so placeholders stay literal there (see
  [Harness command surfaces](#harness-command-surfaces-shell-path)).
- the guard that re-checks the command still runs, and it runs against the value-inlined form of the
  command, exactly as before env injection existed; values are also screened for shell metacharacters. The
  only relaxation is the emitted reference itself: `${LELE_SECRET_<digits>}` is exempt from the generic
  `${…}` deny pattern. Every other `${…}` form stays denied.

## Recommendations

- keep `restrict_to_workspace` enabled unless you have a specific reason not to — knowing what it does: it
  keeps `exec` starting inside the workspace and keeps the file tools inside it (with symlinks resolved). It
  does not bound a command's own reads and writes, so the real containment layer is the OS user lele runs as:
  a dedicated unprivileged account, or a container with only the workspace mounted writable
- leave `harness.allow_shell` off unless you author the templates yourself: a `!shell` template runs without
  the exec guard, and `@path` follows workspace symlinks out of the workspace
- prefer explicit provider env vars or secret management over hardcoding tokens
- keep `cors_origins` minimal for native/web setups
- review custom deny patterns before disabling built-in ones — they are a name-based filter, not a sandbox

## Related Docs

- `docs/tools_configuration.md`
- `docs/client-api.md`
- `docs/troubleshooting.md`
