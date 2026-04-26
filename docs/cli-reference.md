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
lele auth logout --provider openai
lele auth status
```

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

- `docs/installation-and-onboarding.md`
- `docs/channel-setup.md`
