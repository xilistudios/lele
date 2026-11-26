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

## Upload Safety

Native/web uploads are:

- size-limited by `max_upload_size_mb`
- stored under `~/.lele/tmp/uploads/`
- cleaned up using `upload_ttl_hours`

## Logs And Secrets

Lele stores logs locally and the editable config API marks secret fields with metadata.

Even so, avoid placing secrets in prompts, memory files, or user-facing session content.

## Recommendations

- keep `restrict_to_workspace` enabled unless you have a specific reason not to
- prefer explicit provider env vars or secret management over hardcoding tokens
- keep `cors_origins` minimal for native/web setups
- review custom deny patterns before disabling built-in ones

## Related Docs

- `docs/tools_configuration.md`
- `docs/client-api.md`
- `docs/troubleshooting.md`
