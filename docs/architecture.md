# Architecture

This document gives a high-level map of Lele's current runtime architecture.

## Main Pieces

- CLI entrypoints in `cmd/lele`
- agent loop in `pkg/agent`
- provider and model config in `pkg/config`
- tools in `pkg/tools`
- channels in `pkg/channels`
- message bus in `pkg/bus`
- sessions and state in `pkg/session` and `pkg/state`
- heartbeat, cron, health, devices, and logging services

## Runtime Flow

Typical gateway flow:

1. load config
2. initialize logging
3. create message bus
4. create agent loop
5. register tools and subagent managers
6. initialize channels
7. start cron, heartbeat, devices, health server, and channel manager
8. process inbound and outbound events through the bus

## Agent Loop

The agent loop is the core runtime that:

- resolves agents and workspaces
- manages sessions
- runs tool loops
- coordinates subagents
- emits outbound events for channels

## Channels

Channels convert external input/output into the shared internal bus format.

Current channel manager support includes:

- Telegram
- WhatsApp
- Feishu
- Discord
- MaixCam
- QQ
- DingTalk
- Slack
- LINE
- OneBot
- Native

## Native/Web Path

The built-in web UI is served separately by the `web` command, while the native channel provides the local REST + WebSocket API used by the web/native client experience.

## Subagents

Subagents are created through dedicated managers that reuse provider/tool loop infrastructure while running isolated delegated tasks.

## Related Docs

- `docs/SKILL_SUBAGENTS.md`
- `docs/client-api.md`
- `docs/deployment.md`
