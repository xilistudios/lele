<div align="center">
  <img src="assets/logo.png" alt="Lele" width="320">
  <img src="assets/tui.png" alt="TUI" width="650">

  <h1>Lele</h1>

  <p>Asistente personal de IA ligero en Go — binario único, bajo consumo, TUI rápida.</p>
  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/binary-~57%20MB-blue" alt="Binary size">
    <img src="https://img.shields.io/badge/TUI%20RSS-~42%20MB-success" alt="TUI RSS">
    <img src="https://img.shields.io/badge/render-~3.4%20ms%20%40%20200%C3%9750-success" alt="Render time">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  </p>

  [中文](README.zh.md) | [日本語](README.ja.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | **Español** | [English](README.md)
</div>

---

Lele es un asistente de IA centrado en el espacio de trabajo, pensado para hosts de larga duración: chat CLI y TUI, pasarela multicanal, web UI, API de cliente nativa, skills, cron y subagentes — sin runtime JS ni stack TUI multiproceso.

## Inicio rápido

```bash
# Linux / macOS / BSD
curl -fsSL https://raw.githubusercontent.com/xilistudios/lele/main/install.sh | sh

# Windows (PowerShell)
irm https://raw.githubusercontent.com/xilistudios/lele/main/install.ps1 | iex
```
```bash
# Desde el código fuente
git clone https://github.com/xilistudios/lele.git && cd lele
make deps && make build && make install
```
```
# Configuración inicial
lele onboard
# Interfaz de agente CLI
lele agent -m "¿Qué puedes hacer?"
# TUI
lele tui
```

Los instaladores verifican SHA256 e instalan por defecto en `~/.local/bin`.

## Benchmarks

Medido en Apple M1 / macOS arm64. Sesiones TUI en reposo bajo PTY, sin llamadas al modelo. **Render** es el tiempo total para pintar un frame completo (lele: bench `View()` del repo a 200×50; otros: tiempo desde el inicio del proceso hasta el primer frame TUI completo — init + primer frame). RSS es la memoria pico del árbol de procesos ~6 s tras ese render.

| Herramienta | Runtime | Tamaño de instalación | Render | RSS TUI en reposo | Procesos |
| --- | --- | --- | --- | --- | --- |
| **lele** | Go + Bubble Tea | **57 MB** binario único | **≈ 3.4 ms/frame** (200×50 `View()`) | **~42 MB** | **1** |
| Claude Code 2.1.267 | Bun-compiled TS | 191 MB binario | ~400 ms hasta el primer frame | 170–187 MB | 1 |
| pi 0.85.1 | Bun-compiled TS + pi-tui | 71 MB binario | ~350 ms hasta el primer frame | ~173 MB | 1 |
| OpenCode 1.17.8 | Bun + OpenTUI | 123 MB binario | ~950 ms hasta el primer frame | ~1.0 GB pico | 1 |
| Hermes 0.14.0 | Python + Ink | ~1 GB árbol | ~1.3 s hasta el primer frame | ~374 MB | 3–4 |

### Diferencia de recursos vs otros

Misma máquina, misma sesión TUI en reposo. Multiplicadores aproximados vs lele (~42 MB RSS, ~57 MB binario, **~60 ms** hasta el primer frame TUI):

| vs | Memoria | Disco / instalación | Primer render | Notas |
| --- | --- | --- | --- | --- |
| Claude Code | **~4× menos** | **~3× más pequeño** | **~7× más rápido** | 42 vs ~180 MB · 60 ms vs ~400 ms |
| pi | **~4× menos** | ~igual | **~6× más rápido** | 42 vs ~173 MB · 60 ms vs ~350 ms |
| OpenCode | **~25× menos** | **~2× más pequeño** | **~16× más rápido** | 42 vs ~1 GB · 60 ms vs ~950 ms |
| Hermes | **~9× menos** | **~17× más pequeño** | **~22× más rápido** | 42 vs ~374 MB · 60 ms vs ~1.3 s |

## Características

**Runtime del agente**
- Bucle de agente con herramientas y límite de iteraciones
- Agentes con nombre, fallback de modelos y `thinking_level` por agente (anula con `/think`)
- Persistencia de sesiones (SQLite) y sesiones efímeras opcionales
- Adjuntos de archivo en flujos nativos/web

**Interfaces**
- CLI (`lele agent`) y TUI completa con Bubble Tea (`lele tui`)
- Web UI integrada + cliente REST/WebSocket nativo con emparejamiento por PIN
- Pasarela para canales de chat (Telegram, Discord, Slack, WhatsApp, Feishu, Line, QQ, DingTalk, …)

**Automatización**
- Trabajos programados (`lele cron`) y tareas de heartbeat desde `HEARTBEAT.md`
- Skills y subagentes asíncronos
- Chat de grupo multi-agente (MoA / round-robin / moderador / pipeline) — ver `docs/moa-group-chat.md`

**Seguridad**
- Restricción al espacio de trabajo, patrones de denegación en exec y flujo de aprobación
- Autenticación por token para clientes nativos, límites de subida y TTL

## Capturas de pantalla

### TUI


![Chat TUI de Lele en curso](assets/tui-chat.png)

```bash
lele tui              # nueva sesión
lele tui -s <id>      # reanudar sesión
```

Chat en streaming, visualización de herramientas, markdown, barra lateral de sesiones y estadísticas de contexto. Temas: `dracula` (por defecto), `nord`, `catppuccin`, `gruvbox`, `tokyo-night`, `solarized-light` — cámbialos en **Settings → Interface**, guardados en `~/.lele/tui.json`. Idioma vía `LELE_LANG` o `/lang`.

### Web UI

![Interfaz Web de Lele](assets/webui.png)

![Chat Web UI de Lele en curso](assets/webui-chat.png)

1. `lele onboard` — habilita la web UI y obtén un PIN de emparejamiento  
2. `lele gateway` — sirve `/`, `/api/v1/*` y WebSocket en el puerto `18790`  
3. Abre el navegador y empareja con el PIN  

API completa del cliente: `docs/client-api.md`.


## Configuración

La configuración vive en `~/.lele/config.json` (plantilla: `config/config.example.json`).

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

Principales secciones: `agents.defaults`, `session`, `channels`, `providers`, `tools`, `heartbeat`, `gateway`, `logs`, `devices`.

**Proveedores:** `anthropic`, `openai`, `openrouter`, `groq`, `zhipu`, `gemini`, `vllm`, `nvidia`, `ollama`, `moonshot`, `deepseek`, `github_copilot`, más backends con nombre compatibles con OpenAI (`model`, `context_window`, `vision`, `reasoning`, …).

**Espacio de trabajo** (`~/.lele/workspace/`): `sessions/`, `memory/`, `state/`, `cron/`, `skills/`, `AGENT.md`, `HEARTBEAT.md`, `IDENTITY.md`, `SOUL.md`, `USER.md`.

## CLI

| Comando | Descripción |
| --- | --- |
| `lele onboard` | Inicializar configuración y espacio de trabajo |
| `lele agent` / `lele agent -m "..."` | Agente interactivo o de un solo disparo |
| `lele tui` / `lele tui -s <session>` | Interfaz de terminal |
| `lele gateway` | Pasarela de mensajería + web UI |
| `lele auth login` | Autenticar proveedores |
| `lele status` | Estado del runtime |
| `lele cron list` / `lele cron add ...` | Trabajos programados |
| `lele skills list` | Skills instalados |
| `lele client pin` / `lele client list` | Emparejamiento de clientes nativos |
| `lele version` | Información de versión |

## Desarrollo

Requiere Go 1.25+, [Bun](https://bun.sh/) (web UI) y Make.

```bash
make deps web-build build   # binario + web
make test fmt vet           # comprobaciones
make build-all              # cross-compile
```

## Licencia

MIT
