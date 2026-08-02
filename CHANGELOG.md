# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.4.0] - 2026-08-01

### Added

#### Agent
- `/goal` command — persistent goal system inspired by Hermes Agent. The agent works autonomously toward a goal across multiple turns until achieved or the turn budget is exhausted. Includes a state machine (active/paused/done/blocked), disk persistence, and an LLM-based goal judge for completion evaluation. Subcommands: `status`, `pause`, `resume`, `clear`. Supports `--turns N` to set the budget.

#### WebUI
- Cron job management page with full CRUD: list, create, edit, delete, enable/disable, and run-now. Includes `useCronJobs` hook with react-query integration, sidebar navigation entry, and i18n support (en/es/pt).
- Double-click confirmation for session deletion — requires two clicks on the trash button before deleting, with confirmation state styling and blur-to-reset.

#### TUI
- `/cron` command opens a modal listing all scheduled jobs with detail view (schedule, payload, state, next/last run). Keyboard actions: E (enable/disable), R (run now), D (delete), ENTER (detail). Uses a read/manage-only CronService instance that never fires jobs from the TUI.

#### Backend
- REST endpoints for cron management in the native channel: `GET/POST/PUT/DELETE /api/v1/cron/*`.
- CronService extended with `ListJobs`, `GetJob`, `EnableJob`, `RemoveJob`, `RunJobNow`.

### Fixed

#### WebUI
- Streaming assistant messages no longer replace older completed assistants in-place during iterative tool calls — actively streaming assistants are always appended as new messages, preserving chronological order (post-tool-call response now correctly appears after the tool call).
- Duplicated/overlapping streaming text after WebSocket reconnection — backend now skips `message.stream` and `message.thinking` events when flushing buffered events during reconnect (content already included in `in_progress_messages`); frontend migrates `restore-{sessionKey}` placeholder messages to the real `message_id` when live chunks arrive.
- Hardened streaming/HTTP message reconciliation: extracted `computeAssistantInsertIndex()` as a pure shared function for the 3-case ordering rule; unified React Query keys with `buildChatHistoryQueryKey` for subagent sessions; guarded against empty content prefix in `handleHistoryUpdated`; scoped 4s reconciliation polling to the current `sessionKey`.

## [0.3.4] - 2026-07-29

### Fixed

#### Agent & Providers
- Agent execution no longer terminates on transient LLM errors (500/502/503, overloaded, timeout) — `runLLMIteration` now retries up to 3 attempts with 5s/15s/30s backoff within the same execution instead of returning the error and forcing the user to resend. Non-retriable errors (auth, billing, format) and context cancellation still stop the run immediately.
- Single-provider agents no longer block for ~5 minutes per attempt — `executeWithRetry` caps internal retries to 3 when no fallback candidates exist, letting the temporal layer (agent loop) handle persistence. Multi-fallback behavior is unchanged.
- Added `IsRetriableError` helper to classify (possibly wrapped) fallback errors as transient or not.

#### Session Management
- Fixed `concurrent map writes` panic in `SessionManager.Save()` — the method was acquiring a read lock (`RLock`) but calling `saveUnlocked()`, which writes to the `sessionMeta` map and `indexDirty`. Two concurrent saves (e.g. parallel session processing) could crash the process. Now correctly uses write lock.

### Changed

#### Providers
- Removed explicit `temperature` parameter from Anthropic provider requests (`anthropic`, `anthropic_messages`, and Bedrock) — Anthropic reasoning models have deprecated the temperature parameter; omitting it avoids API errors and lets the provider use its defaults.

## [0.3.3] - 2026-07-29

### Fixed

#### Providers
- Agent no longer gets stuck in cooldown when no fallback models are configured — a transient provider error (rate limit, timeout, 5xx) was applying a cooldown (1m → 5m → 25m → 1h) even with a single candidate, blocking the agent on subsequent turns since there was no alternative candidate to fail over to. Cooldown is now only applied when fallback candidates exist; a single-provider agent keeps retrying the same provider on the next turn. Multi-fallback behavior is unchanged.

## [0.3.2] - 2026-07-28

### Added

#### TUI
- In-app text selection with click+drag in the viewport: drag to select lines (blue highlight), text is copied to clipboard on release; status bar shows "Selecting..." during drag and "Copied!" confirmation for 2 seconds
- Mouse capture now starts ON by default so scroll-wheel and sidebar work out of the box; selection only activates in the viewport area (not sidebar/input)
- i18n strings for selection states (en, es, pt)

### Fixed

#### Context & Session Management
- Context compaction not working between turns — five root causes fixed:
  - **Critical**: `forceCompression` now builds a basic local summary from excluded messages when LLM summarization fails, preventing total context loss
  - **High**: `processSystemMessage` now uses `EnableSummary=true` so subagent results (50K+ chars) trigger post-turn compaction when the session exceeds 75% of the context window
  - **Medium**: Intra-loop compaction (`CompactLoopMessages`) now syncs state to session storage, ensuring post-turn summarization sees the reduced history
  - **Low**: `summarizeSessionCore` only un-excludes the most recent summary message instead of all historical ones, preventing stale summaries from accumulating
  - **Low**: `EstimateTokens` now counts Media attachments (images) and ContentParts for multimodal messages, fixing token undercounting

#### Code Quality
- Aligned struct fields in `types.go` for goimports compliance

## [0.3.0] - 2026-07-23

### Added

#### TUI
- Provider management: `/providers`, `/connect`, `/add-model` commands with multi-step forms to list, add, and delete providers/models; all changes persist to config
- Background process viewer (`/bg`): list, inspect real-time output, and stop background exec processes
- Command approval system: inline prompt for dangerous shell commands matched by `denyPatterns`; user can approve (y) or reject (n/ESC) with 5-minute timeout
- Clipboard copy (Ctrl+Y): copies last assistant message via OSC 52 with xclip/xsel/wl-copy/pbcopy fallback
- Mouse capture starts OFF by default so native terminal text selection works out of the box (toggle with Ctrl+T)
- i18n strings for all new features (en, es, pt)

#### WebUI
- Background process page with expandable cards, color-coded status badges, terminal-style output viewer, stop button, and SSE real-time streaming
- Compaction events rendered as tool cards instead of plain text (tool.executing + tool.result)

#### Backend
- Better provider cooldown logging

### Fixed

#### TUI
- Forward keystrokes to textInput in form modals so text input works inside multi-step forms
- Correct indentation and brace placement for textInput forwarding
- Restore persisted session model before falling back to agent default, preventing model drift on session reload

#### Subagents
- Use configured `subagents.model` override instead of always inheriting the parent agent's model

#### Performance & Stability
- Cap RAM usage for subagents with background execs: 1MB ring buffer per process, max 3 concurrent subagents, 20 process limit, 50K char tool result truncation
- Compaction no longer fails on reasoning models that have deprecated the temperature parameter; options are now built from the session's model config

#### Configuration
- Respect `-c`/`--config-dir` flag in logger, auth store, Telegram offset file, and config defaults (5 locations previously hardcoded `~/.lele`)

### Changed

#### Performance
- TUI startup is 386x faster on subsequent launches via `_index.json` metadata cache (~3ms vs ~1.2s); first startup is 10x faster via parallel loading (min(NumCPU, 16) workers)
- Background session cleanup goroutine evicts idle sessions every 5 min and sweeps orphaned subagent metadata
- Lazy session loading: only metadata loads on startup, full messages load on-demand
- LRU session eviction: max 50 sessions in memory with 30-min idle TTL
- Prune excluded messages from RAM after summarization/compaction (kept on disk)
- Reduce background exec retention from 10 min to 5 min; truncate stdout/stderr to 64 KB on completion
- Pool strings.Builder in SSE stream parsers (Anthropic + OpenAI compat)
- Virtualized TUI rendering: only last 200 messages rendered

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
- WebUI: chat route sync no longer interferes with non-chat pages (settings, agents)
- WebUI: agent files page route accepts optional fileName parameter

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
