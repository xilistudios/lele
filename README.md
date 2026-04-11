<div align="center">
  <img src="assets/logo.png" alt="Lele" width="320">

  <h1>Lele</h1>

  <p>Lightweight and efficient personal AI assistant in Go.</p>

  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  </p>

  [中文](README.zh.md) | [日本語](README.ja.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | [Español](README.es.md) | **English**
</div>

---

Lele is an independent project focused on delivering a practical AI assistant with a small footprint, fast startup, and a straightforward deployment model.

Today the project is more than a minimal CLI bot. It includes a configurable agent runtime, multi-channel gateway, web UI, native client API, scheduled tasks, subagents, and a workspace-centered automation model.

## Why Lele

- Lightweight Go implementation with a small operational footprint
- Efficient enough to run comfortably on modest Linux machines and boards
- One project for CLI, chat channels, web UI, and local client integrations
- Configurable provider routing with support for direct and OpenAI-compatible backends
- Workspace-first design with skills, memory, scheduled jobs, and sandbox controls

## Current Capabilities

### Agent Runtime

- CLI chat with `lele agent`
- Tool-using agent loop with configurable iteration limits
- File attachments in native/web flows
- Session persistence and optional ephemeral sessions
- Named agents, bindings, and model fallbacks

### Interfaces

- Terminal usage through the CLI
- Gateway mode for chat channels
- Built-in web UI
- Native client channel with REST + WebSocket API and PIN pairing

### Automation

- Scheduled jobs with `lele cron`
- Heartbeat-based periodic tasks from `HEARTBEAT.md`
- Async subagents for delegated work
- Skills system for reusable workflows

### Safety And Operations

- Workspace restriction support
- Dangerous command deny patterns for exec tools
- Approval flow for sensitive actions
- Logs, status commands, and configuration management

## Project Status

Lele is an actively evolving standalone project.

The current codebase already supports:

- production-style gateway flows
- a web/native client path
- configurable multi-provider routing
- multiple messaging channels
- skills, subagents, and scheduled automation

The main documentation gap was that the old README still described an earlier fork identity and did not match the current feature set. This README reflects the project as it exists now.

## Quick Start

### Install From Source

```bash
git clone https://github.com/xilistudios/lele.git
cd lele
make deps
make build
```

The binary is written to `build/lele`.

### Initial Setup

```bash
lele onboard
```

`onboard` creates the base config, workspace templates, and can optionally enable the web UI and generate a pairing PIN for the native/web client flow.

### Minimal CLI Usage

```bash
lele agent -m "What can you do?"
```

## Web UI And Native Client Flow

Lele now includes a local web UI plus a native client channel.

Typical flow:

1. Run `lele onboard`
2. Enable the Web UI when prompted
3. Generate a pairing PIN
4. Start the services with `lele gateway` and `lele web start`
5. Open the web app in your browser and pair with the PIN

The native channel exposes REST and WebSocket endpoints for desktop clients and local integrations.

See `docs/client-api.md` for the full API.

## Configuration

Main config file:

```text
~/.lele/config.json
```

Example config template:

```text
config/config.example.json
```

Core areas you can configure:

- `agents.defaults`: workspace, provider, model, token limits, tool limits
- `session`: ephemeral session behavior and identity links
- `channels`: gateway and messaging integrations
- `providers`: direct providers and named OpenAI-compatible backends
- `tools`: web search, cron, exec safety settings
- `heartbeat`: periodic task execution
- `gateway`, `logs`, `devices`

### Minimal Example

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.lele/workspace",
      "restrict_to_workspace": true,
      "model": "glm-4.7",
      "max_tokens": 8192,
      "max_tool_iterations": 20
    }
  },
  "providers": {
    "openrouter": {
      "type": "openrouter",
      "api_key": "YOUR_API_KEY"
    }
  }
}
```

## Providers

Lele supports both built-in providers and named provider definitions.

Built-in provider families currently represented in config/runtime include:

- `anthropic`
- `openai`
- `openrouter`
- `groq`
- `zhipu`
- `gemini`
- `vllm`
- `nvidia`
- `ollama`
- `moonshot`
- `deepseek`
- `github_copilot`

The project also supports named OpenAI-compatible provider entries with per-model settings such as:

- `model`
- `context_window`
- `max_tokens`
- `temperature`
- `vision`
- `reasoning`

## Channels

The gateway currently includes configuration for:

- `telegram`
- `discord`
- `whatsapp`
- `feishu`
- `slack`
- `line`
- `onebot`
- `qq`
- `dingtalk`
- `maixcam`
- `native`
- `web`

Some channels are simple token-based integrations, while others require webhook or bridge setup.

## Workspace Layout

Default workspace:

```text
~/.lele/workspace/
```

Typical contents:

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
├── SOUL.md
└── USER.md
```

This workspace-centered layout is part of what keeps Lele practical and efficient: state, prompts, skills, and automation live in a predictable place.

## Scheduling, Skills, And Subagents

### Scheduled Tasks

Use `lele cron` to create one-shot or recurring jobs.

Examples:

```bash
lele cron list
lele cron add --name reminder --message "Check backups" --every "2h"
```

### Heartbeat

Lele can periodically read `HEARTBEAT.md` from the workspace and execute tasks automatically.

### Skills

Built-in and custom skills can be managed with:

```bash
lele skills list
lele skills search
lele skills install <skill>
```

### Subagents

Lele supports delegated async work through subagents. This is useful for long-running or parallelizable tasks.

See `docs/SKILL_SUBAGENTS.md` for details.

## Security Model

Lele can restrict agent file and command access to the configured workspace.

Key controls include:

- `restrict_to_workspace`
- exec deny patterns
- approval flow for sensitive actions
- token-based auth for native clients
- upload limits and TTL for native file uploads

See `docs/tools_configuration.md` and `docs/client-api.md` for operational details.

## CLI Reference

| Command | Description |
| --- | --- |
| `lele onboard` | Initialize config and workspace |
| `lele agent` | Start interactive agent session |
| `lele agent -m "..."` | Run a one-shot prompt |
| `lele gateway` | Start messaging gateway |
| `lele web start` | Start the built-in web UI |
| `lele web status` | Show web UI status |
| `lele auth login` | Authenticate supported providers |
| `lele status` | Show runtime status |
| `lele cron list` | List scheduled jobs |
| `lele cron add ...` | Add a scheduled job |
| `lele skills list` | List installed skills |
| `lele client pin` | Generate a pairing PIN |
| `lele client list` | List paired native clients |
| `lele version` | Show version information |

## Additional Docs

- `docs/agents-models-providers.md`
- `docs/architecture.md`
- `docs/channel-setup.md`
- `docs/cli-reference.md`
- `docs/config-reference.md`
- `docs/client-api.md`
- `docs/deployment.md`
- `docs/examples.md`
- `docs/installation-and-onboarding.md`
- `docs/logging-and-observability.md`
- `docs/model-routing.md`
- `docs/security-and-sandbox.md`
- `docs/session-and-workspace.md`
- `docs/skills-authoring.md`
- `docs/tools_configuration.md`
- `docs/troubleshooting.md`
- `docs/web-ui.md`
- `docs/SKILL_SUBAGENTS.md`
- `docs/SYSTEM_SPAWN_IMPLEMENTATION.md`

## Development

Useful targets:

```bash
make build
make test
make fmt
make vet
make check
```

## License

MIT
