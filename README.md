<div align="center">
  <img src="assets/logo.png" alt="Lele" width="320">
  <img src="assets/tui.png" alt="TUI" width="650">

  <h1>Lele</h1>

  <p>Lightweight personal AI assistant in Go — single binary, small footprint, fast TUI.</p>
  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/binary-~57%20MB-blue" alt="Binary size">
    <img src="https://img.shields.io/badge/TUI%20RSS-~42%20MB-success" alt="TUI RSS">
    <img src="https://img.shields.io/badge/render-~3.4%20ms%20%40%20200%C3%9750-success" alt="Render time">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  </p>

  [中文](README.zh.md) | [日本語](README.ja.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | [Español](README.es.md) | **English**
</div>

---

Lele is a workspace-first AI assistant built for long-running hosts: CLI and TUI chat, a multi-channel gateway, web UI, native client API, skills, cron, and subagents — without a JS runtime or multi-process TUI stack.

## Quick Start

```bash
# Linux / macOS / BSD
curl -fsSL https://raw.githubusercontent.com/xilistudios/lele/main/install.sh | sh

# Windows (PowerShell)
irm https://raw.githubusercontent.com/xilistudios/lele/main/install.ps1 | iex
```
```bash
# From source
git clone https://github.com/xilistudios/lele.git && cd lele
make deps && make build && make install
```
```
# Initial setup
lele onboard
# CLI agent interface
lele agent -m "What can you do?"
# TUI
lele tui
```

Installers verify SHA256 and install to `~/.local/bin` by default.

## Benchmarks

Measured on Apple M1 / macOS arm64. Idle TUI sessions under a PTY, no model calls. **Render** is wall time to paint one full frame (lele: in-repo `View()` bench at 200×50; others: time from process start to first complete TUI paint — init + first frame). RSS is peak process-tree memory ~6s after that paint.

| Tool | Runtime | Install size | Render | Idle TUI RSS | Processes |
| --- | --- | --- | --- | --- | --- |
| **lele** | Go + Bubble Tea | **57 MB** single binary | **≈ 3.4 ms/frame** (200×50 `View()`) | **~42 MB** | **1** |
| Claude Code 2.1.267 | Bun-compiled TS | 191 MB binary | ~400 ms to first paint | 170–187 MB | 1 |
| pi 0.85.1 | Bun-compiled TS + pi-tui | 71 MB binary | ~350 ms to first paint | ~173 MB | 1 |
| OpenCode 1.17.8 | Bun + OpenTUI | 123 MB binary | ~950 ms to first paint | ~1.0 GB peak | 1 |
| Hermes 0.14.0 | Python + Ink | ~1 GB tree | ~1.3 s to first paint | ~374 MB | 3–4 |

### Resource difference vs others

Same machine, same idle TUI session. Rough multipliers vs lele (~42 MB RSS, ~57 MB binary, **~60 ms** to first TUI paint):

| vs | Memory | Disk / install | First render | Notes |
| --- | --- | --- | --- | --- |
| Claude Code | **~4× less** | **~3× smaller** | **~7× faster** | 42 vs ~180 MB · 60 ms vs ~400 ms |
| pi | **~4× less** | ~equal | **~6× faster** | 42 vs ~173 MB · 60 ms vs ~350 ms |
| OpenCode | **~25× less** | **~2× smaller** | **~16× faster** | 42 vs ~1 GB · 60 ms vs ~950 ms |
| Hermes | **~9× less** | **~17× smaller** | **~22× faster** | 42 vs ~374 MB · 60 ms vs ~1.3 s |

## Features

**Agent runtime**
- Tool-using agent loop with iteration limits
- Named agents, model fallbacks, per-agent `thinking_level` (override with `/think`)
- Session persistence (SQLite) and optional ephemeral sessions
- File attachments in native/web flows

**Interfaces**
- CLI (`lele agent`) and full Bubble Tea TUI (`lele tui`)
- Built-in web UI + native REST/WebSocket client with PIN pairing
- Gateway for chat channels (Telegram, Discord, Slack, WhatsApp, Feishu, Line, QQ, DingTalk, …)

**Automation**
- Scheduled jobs (`lele cron`) and `HEARTBEAT.md` heartbeat tasks
- Skills and async subagents
- Multi-agent group chat (MoA / round-robin / moderator / pipeline) — see `docs/moa-group-chat.md`

**Safety**
- Workspace restriction, exec deny patterns, approval flow
- Token auth for native clients, upload limits/TTL

## Screenshots

### TUI


![Lele TUI chat in progress](assets/tui-chat.png)

```bash
lele tui              # new session
lele tui -s <id>      # resume session
```

Streaming chat, tool visualization, markdown, session sidebar, context stats. Themes: `dracula` (default), `nord`, `catppuccin`, `gruvbox`, `tokyo-night`, `solarized-light` — switch in **Settings → Interface**, stored in `~/.lele/tui.json`. Language via `LELE_LANG` or `/lang`.

### Web UI

![Lele Web UI chat interface](assets/webui.png)

![Lele Web UI chat in progress](assets/webui-chat.png)

1. `lele onboard` — enable web UI, get a pairing PIN  
2. `lele gateway` — serves `/`, `/api/v1/*`, WebSocket on port `18790`  
3. Open the browser and pair with the PIN  

Full client API: `docs/client-api.md`.


## Configuration

Config lives at `~/.lele/config.json` (template: `config/config.example.json`).

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

Main sections: `agents.defaults`, `session`, `channels`, `providers`, `tools`, `heartbeat`, `gateway`, `logs`, `devices`.

**Providers:** `anthropic`, `openai`, `openrouter`, `groq`, `zhipu`, `gemini`, `vllm`, `nvidia`, `ollama`, `moonshot`, `deepseek`, `github_copilot`, plus hermes-agent ports (`xai`, `nous`, `lmstudio`, `minimax`, `vercel`, `opencode`, `huggingface`, `novita`, `xiaomi`, …), Qwen Cloud / Alibaba Token Plan (`alibaba_token_plan`, `alibaba_token_plan_cn`), and named OpenAI-compatible backends (`model`, `context_window`, `vision`, `reasoning`, …). A curated model catalog (`catalog/` in-repo, cached under `~/.lele/cache/catalog/`) with context/vision/thinking metadata powers WebUI/TUI autocomplete when adding models.

**Workspace** (`~/.lele/workspace/`): `sessions/`, `memory/`, `state/`, `cron/`, `skills/`, `AGENT.md`, `HEARTBEAT.md`, `IDENTITY.md`, `SOUL.md`, `USER.md`.

## CLI

| Command | Description |
| --- | --- |
| `lele onboard` | Initialize config and workspace |
| `lele agent` / `lele agent -m "..."` | Interactive or one-shot agent |
| `lele tui` / `lele tui -s <session>` | Terminal UI |
| `lele gateway` | Messaging gateway + web UI |
| `lele auth login` | Authenticate providers |
| `lele status` | Runtime status |
| `lele cron list` / `lele cron add ...` | Scheduled jobs |
| `lele skills list` | Installed skills |
| `lele client pin` / `lele client list` | Native client pairing |
| `lele version` | Version info |

## Development

Requires Go 1.25+, [Bun](https://bun.sh/) (web UI), and Make.

```bash
make deps web-build build   # build binary + web
make test fmt vet           # checks
make build-all              # cross-compile
```

## License

MIT
