# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.2] - 2026-07-22

### Added

#### Subagent Architecture
- Split monolithic `subagent.go` into modular files: `subagent_types.go`, `subagent_manager.go`, `subagent_runner.go`, `subagent_task.go`, `subagent_tool.go`, `subagent_prompt.go`, `subagent_utils.go`
- Context sanitization for subagent LLM calls to prevent sensitive data leakage
- Session exclusion management in session manager (`manager_exclude_test.go`)
- Subagent-aware context handling in `llm_runner.go`

### Fixed

- Subagent notification subsystem now delivers events correctly
- Eliminated redundant subagent notifications
- TUI cancel action no longer causes panics
- Replaced stray `fmt.Println` calls with context-aware logger

### Changed

- Improved `tool_coordinator.go` with subagent context awareness
- Document config types extracted into `document_types.go`, `document_convert.go`, `document_defaults.go`
- Fallback provider logic refined for better error handling

## [0.2.1] - 2026-07-15

### Added

#### Harness Module
- New `pkg/context/harness.go` for building harness context from workspace files
- Comprehensive test suite (`harness_test.go`, `parse_skill_test.go`)
- Automatic AGENT.md, SOUL.md, USER.md, IDENTITY.md, MEMORY.md, and skills context assembly

#### TUI Enhancements
- Mouse click support for subagent items in sidebar navigation
- Enhanced markdown rendering with syntax highlighting
- New i18n strings for subagent interaction

#### Context & Session Management
- `EstimateTokens` now counts Content + ReasoningContent + ToolCalls + overhead
- Configurable `CompactionThresholdPercent` (default 75%)
- Race condition guard for `summarizeSession` (sync.Map)
- UTF-8-safe truncation in session manager summary prompts

### Fixed

- `findMatchingBrace()` is now string-aware to avoid counting braces inside JSON strings
- TUI viewport rendering issues
- Deduplicated `summarizeSession`/`summarizeSessionWithError` into `summarizeSessionCore`
- Removed dead code (`summarizeBatch`)

### Changed

- Centralized `DecodeToolCallArguments()` to handle nil/empty/double-encoded JSON
- Replaced 6 duplicated JSON decode patterns across providers
- Improved onboard configuration flow

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
