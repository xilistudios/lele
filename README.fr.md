<div align="center">
  <img src="assets/logo.png" alt="Lele" width="320">
  <img src="assets/tui.png" alt="TUI" width="650">

  <h1>Lele</h1>

  <p>Assistant personnel IA léger en Go — binaire unique, empreinte réduite, TUI rapide.</p>
  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/binary-~57%20MB-blue" alt="Binary size">
    <img src="https://img.shields.io/badge/TUI%20RSS-~42%20MB-success" alt="TUI RSS">
    <img src="https://img.shields.io/badge/startup-~25%20ms-success" alt="Startup">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  </p>

  [中文](README.zh.md) | [日本語](README.ja.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | **Français** | [Español](README.es.md) | [English](README.md)
</div>

---

Lele est un assistant IA centré sur l'espace de travail, conçu pour les hôtes à longue durée de vie : chat CLI et TUI, passerelle multi-canal, interface web, API client native, skills, cron et sous-agents — sans runtime JS ni pile TUI multi-processus.

## Démarrage rapide

```bash
# Linux / macOS / BSD
curl -fsSL https://raw.githubusercontent.com/xilistudios/lele/main/install.sh | sh

# Windows (PowerShell)
irm https://raw.githubusercontent.com/xilistudios/lele/main/install.ps1 | iex
```
```bash
# Depuis les sources
git clone https://github.com/xilistudios/lele.git && cd lele
make deps && make build && make install
```
```
# Configuration initiale
lele onboard
# Interface agent CLI
lele agent -m "Que peux-tu faire ?"
# TUI
lele tui
```

Les installateurs vérifient le SHA256 et installent par défaut dans `~/.local/bin`.

## Benchmarks

Mesuré sur Apple M1 / macOS arm64. Sessions TUI au repos sous PTY, sans appels de modèle. Le démarrage est la moyenne de 3 exécutions de `--version`. Le RSS est la mémoire de pointe de l'arbre de processus ~6 s après le premier rendu.

| Outil | Runtime | Taille d'installation | Démarrage | RSS TUI au repos | Processus |
| --- | --- | --- | --- | --- | --- |
| **lele** | Go + Bubble Tea | **57 Mo** binaire unique | **~25 ms** | **~42 Mo** | **1** |
| Claude Code 2.1.267 | Bun-compiled TS | 191 Mo binaire | ~13 ms | ~170–187 Mo | 1 |
| pi 0.85.1 | Bun-compiled TS + pi-tui | 71 Mo binaire | ~233 ms | ~173 Mo | 1 |
| OpenCode 1.17.8 | Bun + OpenTUI | 123 Mo binaire | ~284 ms | ~1,0 Go pic | 1 |
| Hermes 0.14.0 | Python + Ink | ~1 Go d'arbre | ~236 ms | ~374 Mo | 3–4 |

Chemin de rendu (benches Go dans le dépôt, image 200×50) : `View()` ≈ 3,4 ms · `paintFrame` ≈ 0,5 ms.

L'empreinte terminal reste discrète : alt-screen Bubble Tea classique + souris + collage entre crochets, sans spam de capacités ni OSC produit.

**Pourquoi c'est important :** un binaire statique unique, ~42 Mo au repos, premier rendu en moins de 100 ms — dimensionné pour les passerelles et les hôtes multi-sessions.

```bash
time lele --version
go test ./pkg/tui/ -bench='PaintFrame|View' -benchmem -count=1 -benchtime=50x
```

## Fonctionnalités

**Runtime d'agent**
- Boucle d'agent avec outils et limite d'itérations
- Agents nommés, secours de modèles, `thinking_level` par agent (surcharge via `/think`)
- Persistance des sessions (SQLite) et sessions éphémères optionnelles
- Pièces jointes dans les flux natifs/web

**Interfaces**
- CLI (`lele agent`) et TUI Bubble Tea complète (`lele tui`)
- Interface web intégrée + client REST/WebSocket natif avec appariement par PIN
- Passerelle pour canaux de chat (Telegram, Discord, Slack, WhatsApp, Feishu, Line, QQ, DingTalk, …)

**Automatisation**
- Jobs planifiés (`lele cron`) et tâches heartbeat via `HEARTBEAT.md`
- Skills et sous-agents asynchrones
- Chat de groupe multi-agents (MoA / round-robin / modérateur / pipeline) — voir `docs/moa-group-chat.md`

**Sécurité**
- Restriction à l'espace de travail, motifs de refus exec, flux d'approbation
- Authentification par jeton pour clients natifs, limites d'upload et TTL

## Captures d'écran

### TUI


![Chat TUI Lele en cours](assets/tui-chat.png)

```bash
lele tui              # nouvelle session
lele tui -s <id>      # reprendre une session
```

Chat en streaming, visualisation d'outils, markdown, barre latérale des sessions, stats de contexte. Thèmes : `dracula` (par défaut), `nord`, `catppuccin`, `gruvbox`, `tokyo-night`, `solarized-light` — à changer dans **Settings → Interface**, stockés dans `~/.lele/tui.json`. Langue via `LELE_LANG` ou `/lang`.

### Interface web

![Interface web Lele](assets/webui.png)

![Chat interface web Lele en cours](assets/webui-chat.png)

1. `lele onboard` — activer l'interface web, obtenir un PIN d'appariement  
2. `lele gateway` — sert `/`, `/api/v1/*`, WebSocket sur le port `18790`  
3. Ouvrir le navigateur et appairer avec le PIN  

API client complète : `docs/client-api.md`.


## Configuration

La configuration se trouve dans `~/.lele/config.json` (modèle : `config/config.example.json`).

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

Sections principales : `agents.defaults`, `session`, `channels`, `providers`, `tools`, `heartbeat`, `gateway`, `logs`, `devices`.

**Fournisseurs :** `anthropic`, `openai`, `openrouter`, `groq`, `zhipu`, `gemini`, `vllm`, `nvidia`, `ollama`, `moonshot`, `deepseek`, `github_copilot`, plus des backends compatibles OpenAI nommés (`model`, `context_window`, `vision`, `reasoning`, …).

**Espace de travail** (`~/.lele/workspace/`) : `sessions/`, `memory/`, `state/`, `cron/`, `skills/`, `AGENT.md`, `HEARTBEAT.md`, `IDENTITY.md`, `SOUL.md`, `USER.md`.

## CLI

| Commande | Description |
| --- | --- |
| `lele onboard` | Initialiser config et espace de travail |
| `lele agent` / `lele agent -m "..."` | Agent interactif ou one-shot |
| `lele tui` / `lele tui -s <session>` | Interface terminal |
| `lele gateway` | Passerelle de messagerie + interface web |
| `lele auth login` | Authentifier les fournisseurs |
| `lele status` | État du runtime |
| `lele cron list` / `lele cron add ...` | Jobs planifiés |
| `lele skills list` | Skills installés |
| `lele client pin` / `lele client list` | Appariement de clients natifs |
| `lele version` | Informations de version |

## Développement

Nécessite Go 1.25+, [Bun](https://bun.sh/) (interface web) et Make.

```bash
make deps web-build build   # binaire + web
make test fmt vet           # vérifications
make build-all              # cross-compilation
```

## Licence

MIT
