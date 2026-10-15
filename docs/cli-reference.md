# CLI Reference

This page summarizes the current top-level CLI commands and their main subcommands.

## Top-Level Commands

| Command | Description |
| --- | --- |
| `lele onboard` | Initialize config and workspace |
| `lele agent` | Interact with the agent directly |
| `lele auth` | Manage provider authentication |
| `lele gateway` | Start the gateway runtime |
| `lele web` | Manage the web server |
| `lele status` | Show runtime/config status |
| `lele cron` | Manage scheduled jobs |
| `lele migrate` | Migrate from OpenClaw |
| `lele skills` | Manage skills |
| `lele client` | Manage native channel clients |
| `lele mcp` | Manage MCP servers (see `docs/mcp.md`) |
| `lele version` | Show version information |

## `lele agent`

Common usage:

```bash
lele agent
lele agent -m "What changed in this repo?"
```

## `lele auth`

Subcommands:

- `login`
- `logout`
- `status`

Examples:

```bash
lele auth login --provider openai
lele auth login --provider openai --device-code
lele auth login --provider anthropic
lele auth login --provider claude
lele auth login --provider claude --paste-code
lele auth login --provider gemini
lele auth login --provider gemini --paste-code
lele auth logout --provider openai
lele auth status
```

Providers accepted by `login` (`claude` is an alias of `anthropic`, `codex` of
`openai`, `gemini-cli` of `gemini`, `antigravity` of `google-antigravity`):

- `openai` — browser flow; `--device-code` uses the headless device flow.
- `google-antigravity` — browser flow (Google Cloud Code Assist).
- `anthropic` — paste-token flow (unchanged).
- `claude` — Claude.ai OAuth browser flow, with a paste-code fallback when the
  loopback callback cannot run; stores the credential under the canonical key
  `anthropic` and sets `providers.anthropic.auth_method = "oauth"`.
- `gemini` — Google Code Assist OAuth browser flow (the flow of the official
  `@google/gemini-cli`), with a paste-code fallback when the loopback callback
  cannot run; stores the credential under the canonical key `gemini` and sets
  `providers.gemini.auth_method = "oauth"`. `gemini-cli` is an alias of
  `gemini` and normalizes to it; there is no `google` alias for this flow.

`--device-code` is only available for `openai`; any other provider rejects it.
`--paste-code` runs the headless Claude or Gemini flow (reads the callback
code/URL from stdin — the equivalent of jcode's `JCODE_CLAUDE_AUTH_CODE` for
Claude, the hosted `https://codeassist.google.com/authcode` redirect for
Gemini) and is only available for the Claude and Gemini OAuth flows
(`--provider claude`, `--provider gemini`, or the `gemini-cli` alias); any other
provider rejects it with
`--paste-code is only supported for the Claude and Gemini OAuth flows; use: lele
auth login --provider claude --paste-code`. Both `--provider anthropic`
(paste-token) and `--provider claude` (OAuth) act on the same `anthropic`
credential slot. The `gemini` OAuth login and a Gemini developer API-key setup
coexist under the same provider key: the Code Assist transport is selected only
when `providers.gemini.auth_method` is `oauth` and no API key is configured, so
an existing API-key configuration keeps working unchanged. `lele auth logout
--provider gemini` (or `--provider gemini-cli`) removes the credential and
clears `providers.gemini.auth_method`, leaving an API key or models on that
entry intact.
Names are case-insensitive and surrounding whitespace is ignored.

## `lele gateway`

Starts the main runtime:

- agent loop
- channels
- cron service
- heartbeat service
- device service
- config watcher
- health endpoints

Debug mode:

```bash
lele gateway --debug
```

## `lele web`

The web UI is served by the gateway server. Use `lele gateway` to start it.

## `lele cron`

Subcommands:

- `list`
- `add`
- `remove`
- `enable`
- `disable`

Examples:

```bash
lele cron list
lele cron add --name reminder --message "Check backups" --every 7200
lele cron add --name daily --message "Summarize errors" --cron "0 9 * * *"
lele cron remove <job_id>
lele cron disable <job_id>
lele cron enable <job_id>
```

## `lele skills`

Subcommands:

- `list`
- `install`
- `remove`
- `install-builtin`
- `list-builtin`
- `search`
- `show`

Examples:

```bash
lele skills list
lele skills search
lele skills install sipeed/lele-skills/weather
lele skills show weather
lele skills remove weather
```

## `lele client`

Common native channel management commands:

```bash
lele client pin
lele client pin --device "Desktop"
lele client list
lele client pending
lele client remove <client_id>
lele client status
```

## `lele mcp`

Manage the layered `mcp.json` files (list, show, add, remove, edit, test).

Subcommands:

- `list`
- `show`
- `add`
- `remove`
- `edit`
- `test`

Examples:

```bash
lele mcp list
lele mcp show filesystem
lele mcp add filesystem --command npx --arg -y --arg @modelcontextprotocol/server-filesystem --arg /tmp
lele mcp remove filesystem --all
lele mcp edit api
lele mcp test api
```

Full reference: `docs/mcp.md` ("Managing servers from the CLI").

## `lele update`

Self-update from GitHub Releases (stable channel only).

```bash
lele update                    # Check and install the latest version (with confirmation)
lele update --check            # Only check if an update is available
lele update --yes              # Install without confirmation
lele update --version vX.Y.Z   # Install a specific version
lele update --rollback         # Restore the previous binary from backup
lele update --no-restart       # Do not restart the service after installing
lele update --force            # Allow updating a dev/local build
```

Options:

- `-c, --check` — check only, do not install
- `-y, --yes` — assume yes to prompts
- `-v, --version` — install a specific version
- `-f, --force` — force update of dev builds
- `--no-restart` — skip service restart after install
- `--rollback` — roll back to the previous version (from `~/.lele/backups`)

Notes:

- Downloads are verified against the release `checksums.txt` before install.
- The previous binary is kept in `~/.lele/backups` for rollback.
- Restart uses the detected supervisor (systemd user service, or self-exec).
- Dev builds (`version == "dev"`) require `--force`.
- Can be disabled with `updates.enabled: false` in config.

## `lele migrate`

Options:

- `--dry-run`
- `--refresh`
- `--config-only`
- `--workspace-only`
- `--force`
- `--openclaw-home`
- `--lele-home`

Examples:

```bash
lele migrate
lele migrate --dry-run
lele migrate --refresh
lele migrate --force
```

## Related Docs

- `docs/mcp.md`
- `docs/installation-and-onboarding.md`
- `docs/channel-setup.md`
