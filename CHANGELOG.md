# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0] - 2026-07-07

### Added

#### TUI (Terminal User Interface)
- Complete terminal-based user interface for interactive agent sessions
- Internationalization (i18n) support with Spanish (default), English, and Portuguese
- Runtime language switching via `/lang` command
- Tool call rendering with visual feedback
- Markdown rendering in terminal
- Optimized startup performance (~150ms)
- Viewport management for long conversations
- Command system for TUI interactions

#### WebUI Improvements
- Native clients settings page for managing paired devices
- UI/UX improvements across the application
- Enhanced chat composer with better attachment handling
- Improved settings navigation and organization
- Subagent sidebar navigation fixes and animation improvements

#### Backend
- Anthropic Messages API support (`anthropic_messages` provider)
- AWS Bedrock integration with Anthropic provider routing
- Native client REST authentication with PIN pairing
- Subagent retry mechanism and tool loop improvements
- Model reference format migration from `/` to `:` separator (provider:model)

### Fixed

- Preserve `anthropic.` prefix for AWS Bedrock endpoints
- Temperature and thinking config handling for Anthropic reasoning models
- Provider prefix stripping in anthropic_messages provider
- Chat header display issues in WebUI
- Subagent sidebar animation cleanup and navigation validation
- Pagination query key mismatch for subagent history
- Model normalization simplification and test isolation
- Linter errors in TUI style module

### Changed

- Refactored model normalization to use `StripProviderPrefix` upstream
- Migrated model reference separator from `/` to `:` (e.g., `openai:gpt-4`)
- Updated thinking config to Anthropic's new adaptive format
- Modularized TUI codebase for better maintainability

## [0.1.0] - 2025-04-11

### Added

- Initial release
- Agent runtime with tool-using loop
- Multi-channel gateway (Telegram, Discord, WhatsApp, Slack, etc.)
- Web UI for chat and configuration
- Native client channel with REST + WebSocket API
- Skills system for reusable workflows
- Scheduled tasks with cron
- Subagents for delegated async work
- Workspace-centered automation model
- Configuration management
- Security controls and sandbox restrictions
