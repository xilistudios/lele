<div align="center">
  <img src="assets/logo.png" alt="Lele" width="320">
  <img src="assets/tui.png" alt="TUI" width="650">

  <h1>Lele</h1>

  <p>轻量级 Go 个人 AI 助手——单二进制、体积小、TUI 快速响应。</p>
  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/binary-~57%20MB-blue" alt="Binary size">
    <img src="https://img.shields.io/badge/TUI%20RSS-~42%20MB-success" alt="TUI RSS">
    <img src="https://img.shields.io/badge/render-~3.4%20ms%20%40%20200%C3%9750-success" alt="Render time">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  </p>

  [中文](README.zh.md) | [日本語](README.ja.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | [Español](README.es.md) | [English](README.md)
</div>

---

Lele 是一个以工作区为核心的 AI 助手，面向长时间运行的宿主环境：CLI 与 TUI 聊天、多通道网关、Web UI、原生客户端 API、技能、cron 与子智能体——无需 JS 运行时或多进程 TUI 技术栈。

## 快速开始

```bash
# Linux / macOS / BSD
curl -fsSL https://raw.githubusercontent.com/xilistudios/lele/main/install.sh | sh

# Windows (PowerShell)
irm https://raw.githubusercontent.com/xilistudios/lele/main/install.ps1 | iex
```
```bash
# 从源码安装
git clone https://github.com/xilistudios/lele.git && cd lele
make deps && make build && make install
```
```
# 初始设置
lele onboard
# CLI 智能体接口
lele agent -m "你能做什么？"
# TUI
lele tui
```

安装脚本会校验 SHA256，默认安装到 `~/.local/bin`。

## 基准测试

在 Apple M1 / macOS arm64 上测量。空闲 TUI 会话（PTY 下，无模型调用）。**Render** 为绘制一整帧的墙钟时间（lele：仓库内 `View()` bench 200×50；其余：从进程启动到首次完整 TUI 绘制的时间——init + 首帧）。RSS 为首帧绘制后约 6 秒的进程树峰值内存。

| 工具 | 运行时 | 安装体积 | Render | 空闲 TUI RSS | 进程数 |
| --- | --- | --- | --- | --- | --- |
| **lele** | Go + Bubble Tea | **57 MB** 单二进制 | **≈ 3.4 ms/frame** (200×50 `View()`) | **~42 MB** | **1** |
| Claude Code 2.1.267 | Bun-compiled TS | 191 MB 二进制 | ~400 ms 至首帧 | 170–187 MB | 1 |
| pi 0.85.1 | Bun-compiled TS + pi-tui | 71 MB 二进制 | ~350 ms 至首帧 | ~173 MB | 1 |
| OpenCode 1.17.8 | Bun + OpenTUI | 123 MB 二进制 | ~950 ms 至首帧 | ~1.0 GB 峰值 | 1 |
| Hermes 0.14.0 | Python + Ink | ~1 GB 目录树 | ~1.3 s 至首帧 | ~374 MB | 3–4 |

### 与其他工具的资源差异

同一机器、同一空闲 TUI 会话。相对 lele（~42 MB RSS、~57 MB 二进制、**~60 ms** 首次 TUI 绘制）的大致倍率：

| vs | 内存 | 磁盘 / 安装 | 首次渲染 | 备注 |
| --- | --- | --- | --- | --- |
| Claude Code | **~4× 更少** | **~3× 更小** | **~7× 更快** | 42 vs ~180 MB · 60 ms vs ~400 ms |
| pi | **~4× 更少** | ~相当 | **~6× 更快** | 42 vs ~173 MB · 60 ms vs ~350 ms |
| OpenCode | **~25× 更少** | **~2× 更小** | **~16× 更快** | 42 vs ~1 GB · 60 ms vs ~950 ms |
| Hermes | **~9× 更少** | **~17× 更小** | **~22× 更快** | 42 vs ~374 MB · 60 ms vs ~1.3 s |

## 功能特性

**智能体运行时**
- 带迭代上限的工具调用循环
- 命名智能体、模型回退、按智能体的 `thinking_level`（可用 `/think` 覆盖）
- 会话持久化（SQLite）及可选临时会话
- 原生/Web 流程中的文件附件

**接口**
- CLI（`lele agent`）与完整 Bubble Tea TUI（`lele tui`）
- 内置 Web UI + 原生 REST/WebSocket 客户端与 PIN 配对
- 消息通道网关（Telegram、Discord、Slack、WhatsApp、飞书、Line、QQ、钉钉……）

**自动化**
- 定时任务（`lele cron`）与 `HEARTBEAT.md` 心跳任务
- 技能与异步子智能体
- 多智能体群聊（MoA / 轮询 / 主持人 / 流水线）——见 `docs/moa-group-chat.md`

**安全**
- 工作区限制、exec 拒绝模式、审批流程
- 原生客户端 Token 认证、上传限制/TTL

## 截图

### TUI


![Lele TUI 聊天进行中](assets/tui-chat.png)

```bash
lele tui              # 新会话
lele tui -s <id>      # 恢复会话
```

流式聊天、工具可视化、Markdown、会话侧边栏、上下文统计。主题：`dracula`（默认）、`nord`、`catppuccin`、`gruvbox`、`tokyo-night`、`solarized-light`——在 **Settings → Interface** 中切换，存储于 `~/.lele/tui.json`。语言通过 `LELE_LANG` 或 `/lang` 设置。

### Web UI

![Lele Web UI 界面](assets/webui.png)

![Lele Web UI 聊天进行中](assets/webui-chat.png)

1. `lele onboard` — 启用 Web UI，获取配对 PIN  
2. `lele gateway` — 在端口 `18790` 上提供 `/`、`/api/v1/*`、WebSocket  
3. 在浏览器中打开并使用 PIN 配对  

完整客户端 API：`docs/client-api.md`。


## 配置

配置位于 `~/.lele/config.json`（模板：`config/config.example.json`）。

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

主要分区：`agents.defaults`、`session`、`channels`、`providers`、`tools`、`heartbeat`、`gateway`、`logs`、`devices`。

**提供器：** `anthropic`、`openai`、`openrouter`、`groq`、`zhipu`、`gemini`、`vllm`、`nvidia`、`ollama`、`moonshot`、`deepseek`、`github_copilot`，以及命名的 OpenAI 兼容后端（`model`、`context_window`、`vision`、`reasoning` 等）。

**工作区**（`~/.lele/workspace/`）：`sessions/`、`memory/`、`state/`、`cron/`、`skills/`、`AGENT.md`、`HEARTBEAT.md`、`IDENTITY.md`、`SOUL.md`、`USER.md`。

## CLI

| 命令 | 描述 |
| --- | --- |
| `lele onboard` | 初始化配置和工作区 |
| `lele agent` / `lele agent -m "..."` | 交互式或一次性智能体 |
| `lele tui` / `lele tui -s <session>` | 终端 UI |
| `lele gateway` | 消息网关 + Web UI |
| `lele auth login` | 认证提供器 |
| `lele status` | 运行时状态 |
| `lele cron list` / `lele cron add ...` | 定时任务 |
| `lele skills list` | 已安装技能 |
| `lele client pin` / `lele client list` | 原生客户端配对 |
| `lele version` | 版本信息 |

## 开发

需要 Go 1.25+、[Bun](https://bun.sh/)（Web UI）和 Make。

```bash
make deps web-build build   # 构建二进制 + web
make test fmt vet           # 检查
make build-all              # 交叉编译
```

## 许可证

MIT
