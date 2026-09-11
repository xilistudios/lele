<div align="center">
  <img src="assets/logo.png" alt="Lele" width="320">
  <img src="assets/tui.png" alt="TUI" width="650">

  <h1>Lele</h1>

  <p>Assistente pessoal de IA leve em Go — binário único, footprint reduzido, TUI rápida.</p>
  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/binary-~57%20MB-blue" alt="Binary size">
    <img src="https://img.shields.io/badge/TUI%20RSS-~42%20MB-success" alt="TUI RSS">
    <img src="https://img.shields.io/badge/render-~3.4%20ms%20%40%20200%C3%9750-success" alt="Render time">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  </p>

  [中文](README.zh.md) | [日本語](README.ja.md) | **Português** | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | [Español](README.es.md) | [English](README.md)
</div>

---

Lele é um assistente de IA com foco em workspace, pensado para hosts de longa duração: chat CLI e TUI, gateway multicanal, web UI, API nativa de cliente, skills, cron e subagentes — sem runtime JS nem stack TUI multiprocesso.

## Início rápido

```bash
# Linux / macOS / BSD
curl -fsSL https://raw.githubusercontent.com/xilistudios/lele/main/install.sh | sh

# Windows (PowerShell)
irm https://raw.githubusercontent.com/xilistudios/lele/main/install.ps1 | iex
```
```bash
# A partir do código-fonte
git clone https://github.com/xilistudios/lele.git && cd lele
make deps && make build && make install
```
```
# Configuração inicial
lele onboard
# Interface do agente CLI
lele agent -m "O que você pode fazer?"
# TUI
lele tui
```

Os instaladores verificam o SHA256 e instalam em `~/.local/bin` por padrão.

## Benchmarks

Medido em Apple M1 / macOS arm64. Sessões TUI ociosas sob PTY, sem chamadas de modelo. **Render** é o tempo total para pintar um frame completo (lele: bench `View()` do repo a 200×50; outros: tempo do início do processo até o primeiro frame TUI completo — init + primeiro frame). RSS é a memória de pico da árvore de processos ~6s após esse render.

| Ferramenta | Runtime | Tamanho do install | Render | RSS TUI ocioso | Processos |
| --- | --- | --- | --- | --- | --- |
| **lele** | Go + Bubble Tea | **57 MB** binário único | **≈ 3.4 ms/frame** (200×50 `View()`) | **~42 MB** | **1** |
| Claude Code 2.1.267 | Bun-compiled TS | 191 MB binário | ~400 ms até o primeiro frame | 170–187 MB | 1 |
| pi 0.85.1 | Bun-compiled TS + pi-tui | 71 MB binário | ~350 ms até o primeiro frame | ~173 MB | 1 |
| OpenCode 1.17.8 | Bun + OpenTUI | 123 MB binário | ~950 ms até o primeiro frame | ~1.0 GB pico | 1 |
| Hermes 0.14.0 | Python + Ink | ~1 GB árvore | ~1.3 s até o primeiro frame | ~374 MB | 3–4 |

### Diferença de recursos vs outros

Mesma máquina, mesma sessão TUI ociosa. Multiplicadores aproximados vs lele (~42 MB RSS, ~57 MB binário, **~60 ms** até o primeiro frame TUI):

| vs | Memória | Disco / install | Primeiro render | Notas |
| --- | --- | --- | --- | --- |
| Claude Code | **~4× menos** | **~3× menor** | **~7× mais rápido** | 42 vs ~180 MB · 60 ms vs ~400 ms |
| pi | **~4× menos** | ~igual | **~6× mais rápido** | 42 vs ~173 MB · 60 ms vs ~350 ms |
| OpenCode | **~25× menos** | **~2× menor** | **~16× mais rápido** | 42 vs ~1 GB · 60 ms vs ~950 ms |
| Hermes | **~9× menos** | **~17× menor** | **~22× mais rápido** | 42 vs ~374 MB · 60 ms vs ~1.3 s |

## Funcionalidades

**Runtime do agente**
- Loop de agente com ferramentas e limite de iterações
- Agentes nomeados, fallback de modelos e `thinking_level` por agente (sobrescreva com `/think`)
- Persistência de sessões (SQLite) e sessões efêmeras opcionais
- Anexos de arquivo em fluxos nativos/web

**Interfaces**
- CLI (`lele agent`) e TUI completa com Bubble Tea (`lele tui`)
- Web UI nativa + cliente REST/WebSocket com pareamento por PIN
- Gateway para canais de chat (Telegram, Discord, Slack, WhatsApp, Feishu, Line, QQ, DingTalk, …)

**Automação**
- Jobs agendados (`lele cron`) e tarefas de heartbeat via `HEARTBEAT.md`
- Skills e subagentes assíncronos
- Chat em grupo multi-agente (MoA / round-robin / moderador / pipeline) — veja `docs/moa-group-chat.md`

**Segurança**
- Restrição ao workspace, padrões de deny em exec e fluxo de aprovação
- Autenticação por token para clientes nativos, limites de upload e TTL

## Capturas de tela

### TUI


![Chat TUI do Lele em andamento](assets/tui-chat.png)

```bash
lele tui              # nova sessão
lele tui -s <id>      # retomar sessão
```

Chat em streaming, visualização de ferramentas, markdown, barra lateral de sessões e estatísticas de contexto. Temas: `dracula` (padrão), `nord`, `catppuccin`, `gruvbox`, `tokyo-night`, `solarized-light` — alterne em **Settings → Interface**, salvos em `~/.lele/tui.json`. Idioma via `LELE_LANG` ou `/lang`.

### Web UI

![Interface Web do Lele](assets/webui.png)

![Chat Web UI do Lele em andamento](assets/webui-chat.png)

1. `lele onboard` — ative a web UI e obtenha um PIN de pareamento  
2. `lele gateway` — serve `/`, `/api/v1/*` e WebSocket na porta `18790`  
3. Abra o navegador e pareie com o PIN  

API completa do cliente: `docs/client-api.md`.


## Configuração

A configuração fica em `~/.lele/config.json` (template: `config/config.example.json`).

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

Principais seções: `agents.defaults`, `session`, `channels`, `providers`, `tools`, `heartbeat`, `gateway`, `logs`, `devices`.

**Providers:** `anthropic`, `openai`, `openrouter`, `groq`, `zhipu`, `gemini`, `vllm`, `nvidia`, `ollama`, `moonshot`, `deepseek`, `github_copilot`, além de backends nomeados compatíveis com OpenAI (`model`, `context_window`, `vision`, `reasoning`, …).

**Workspace** (`~/.lele/workspace/`): `sessions/`, `memory/`, `state/`, `cron/`, `skills/`, `AGENT.md`, `HEARTBEAT.md`, `IDENTITY.md`, `SOUL.md`, `USER.md`.

## CLI

| Comando | Descrição |
| --- | --- |
| `lele onboard` | Inicializar config e workspace |
| `lele agent` / `lele agent -m "..."` | Agente interativo ou one-shot |
| `lele tui` / `lele tui -s <session>` | Interface de terminal |
| `lele gateway` | Gateway de mensagens + web UI |
| `lele auth login` | Autenticar providers |
| `lele status` | Status do runtime |
| `lele cron list` / `lele cron add ...` | Jobs agendados |
| `lele skills list` | Skills instaladas |
| `lele client pin` / `lele client list` | Pareamento de clientes nativos |
| `lele version` | Informações de versão |

## Desenvolvimento

Requer Go 1.25+, [Bun](https://bun.sh/) (web UI) e Make.

```bash
make deps web-build build   # binário + web
make test fmt vet           # verificações
make build-all              # cross-compile
```

## Licença

MIT
