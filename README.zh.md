<div align="center">
  <img src="assets/logo.png" alt="Lele" width="320">

  <h1>Lele</h1>

  <p>轻量高效的 Go 语言个人 AI 助手。</p>

  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  </p>

  [English](README.md) | **中文** | [日本語](README.ja.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | [Español](README.es.md)
</div>

---

Lele 是一个独立的开源项目，致力于打造一个实用的 AI 助手——体积小巧、启动迅速、部署简单。

如今，Lele 已不再是一个简单的 CLI 机器人。它包含了可配置的智能体运行时、多通道网关、Web UI、原生客户端 API、定时任务、子智能体（subagent）以及以工作区为核心的自动化模型。

## 为什么选择 Lele

- 基于 Go 语言的轻量实现，运行资源占用小
- 效率出众，可轻松在配置较低的 Linux 机器和开发板上运行
- 一个项目涵盖 CLI、聊天通道、Web UI 和本地客户端集成
- 可配置的提供器路由，支持直连和 OpenAI 兼容的后端
- 以工作区为核心的设计，内置技能、记忆、定时任务和沙箱控制

## 当前能力

### 智能体运行时（Agent Runtime）

- 通过 `lele agent` 进行 CLI 交互
- 可配置迭代次数的工具调用循环
- 在原生/Web 流程中支持文件附件
- 会话持久化及可选的临时会话
- 命名智能体、绑定关系及模型回退机制

### 接口

- 通过 CLI 在终端中使用
- 网关模式用于聊天通道
- 内置 Web UI
- 原生客户端通道，提供 REST + WebSocket API 及 PIN 配对

### 自动化

- 通过 `lele cron` 管理定时任务
- 基于 `HEARTBEAT.md` 的心跳周期任务
- 用于委派工作的异步子智能体
- 可复用的技能（skills）系统

### 安全与运维

- 工作区限制支持
- 危险命令拒绝模式（exec 工具）
- 敏感操作的审批流程
- 日志、状态命令和配置管理

## 项目状态

Lele 是一个持续演进的独立项目。

当前代码库已支持：

- 生产级别的网关流程
- Web/原生客户端路径
- 可配置的多提供器路由
- 多种消息通道
- 技能、子智能体和定时自动化

之前的主要文档缺口在于旧版 README 仍描述的是早期分支的身份，与当前功能集不匹配。本 README 反映了项目的当前状态。

## 快速开始

### 从源码安装

```bash
git clone https://github.com/xilistudios/lele.git
cd lele
make deps
make build
```

编译后的二进制文件位于 `build/lele`。

### 初始设置

```bash
lele onboard
```

`onboard` 命令会创建基础配置和工作区模板，并可选项启用 Web UI 以及为原生/Web 客户端流程生成配对 PIN。

### 基本 CLI 用法

```bash
lele agent -m "你能做什么？"
```

## Web UI 与原生客户端流程

Lele 现已包含本地 Web UI 和原生客户端通道。

典型使用流程：

1. 运行 `lele onboard`
2. 在提示时启用 Web UI
3. 生成配对 PIN
4. 通过 `lele gateway` 和 `lele web start` 启动服务
5. 在浏览器中打开 Web 应用并使用 PIN 配对

原生通道暴露了 REST 和 WebSocket 端点，供桌面客户端和本地集成使用。

完整 API 文档请参阅 `docs/client-api.md`。

## 配置

主配置文件：

```text
~/.lele/config.json
```

配置模板示例：

```text
config/config.example.json
```

可配置的核心区域：

- `agents.defaults`：工作区、提供器、模型、Token 限制、工具限制
- `session`：临时会话行为和身份链接
- `channels`：网关和消息集成
- `providers`：直连提供器和命名的 OpenAI 兼容后端
- `tools`：网络搜索、cron、exec 安全设置
- `heartbeat`：周期性任务执行
- `gateway`、`logs`、`devices`

### 最小化配置示例

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

## 提供器（Providers）

Lele 同时支持内置提供器和自定义提供器定义。

当前配置/运行时中已包含的内置提供器系列：

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

项目还支持通过命名方式添加 OpenAI 兼容的提供器条目，支持以下按模型级别的设置：

- `model`
- `context_window`
- `max_tokens`
- `temperature`
- `vision`
- `reasoning`

## 通道（Channels）

网关目前支持配置以下通道：

- `telegram`
- `discord`
- `whatsapp`
- `feishu`（飞书）
- `slack`
- `line`
- `onebot`
- `qq`
- `dingtalk`（钉钉）
- `maixcam`
- `native`
- `web`

部分通道仅需 Token 即可运行，而另一些则需要 Webhook 或桥接设置。

## 工作区布局

默认工作区路径：

```text
~/.lele/workspace/
```

典型目录结构：

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

这种以工作区为中心的设计是 Lele 保持实用和高效的关键：状态、提示、技能和自动化都存放在可预测的位置。

## 定时任务、技能与子智能体

### 定时任务

使用 `lele cron` 创建一次性或周期性任务。

示例：

```bash
lele cron list
lele cron add --name reminder --message "检查备份" --every "2h"
```

### 心跳（Heartbeat）

Lele 可以周期性地从工作区读取 `HEARTBEAT.md` 并自动执行其中定义的任务。

### 技能（Skills）

可通过以下命令管理内置和自定义技能：

```bash
lele skills list
lele skills search
lele skills install <skill>
```

### 子智能体（Subagents）

Lele 支持通过子智能体进行异步委派工作。这对于长时间运行或可并行化的任务非常有用。

详情请参阅 `docs/SKILL_SUBAGENTS.md`。

## 安全模型

Lele 可以将智能体的文件和命令访问限制在已配置的工作区内。

关键控制项包括：

- `restrict_to_workspace`
- exec 拒绝模式
- 敏感操作审批流程
- 原生客户端的 Token 认证
- 原生文件上传的大小限制和 TTL

操作详情请参阅 `docs/tools_configuration.md` 和 `docs/client-api.md`。

## CLI 命令参考

| 命令 | 描述 |
| --- | --- |
| `lele onboard` | 初始化配置和工作区 |
| `lele agent` | 启动交互式智能体会话 |
| `lele agent -m "..."` | 运行一次性问答 |
| `lele gateway` | 启动消息网关 |
| `lele web start` | 启动内置 Web UI |
| `lele web status` | 显示 Web UI 状态 |
| `lele auth login` | 认证支持的提供器 |
| `lele status` | 显示运行时状态 |
| `lele cron list` | 列出已调度的任务 |
| `lele cron add ...` | 添加调度任务 |
| `lele skills list` | 列出已安装的技能 |
| `lele client pin` | 生成配对 PIN |
| `lele client list` | 列出已配对的原生客户端 |
| `lele version` | 显示版本信息 |

## 其他文档

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

## 开发

常用构建命令：

```bash
make build
make test
make fmt
make vet
make check
```

## 许可证

MIT
