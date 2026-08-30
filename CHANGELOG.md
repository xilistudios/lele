# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.7.9] - 2026-08-21

### Fixed

#### TUI
- Add-agent flow in settings not working (#214) — Enter was routed to `handleAgentsEnter()` instead of the text input handler because `modalMode` stayed as `ModalSettingsAgents` during the add-agent flow, and keystrokes were never forwarded to the text input. Both fixed by checking `settingsEditField` first and adding `ModalSettingsAgents` to the forwarding condition.

#### Session
- Seq rebase after `LoadEvictedMessages` (#215) — loading evicted rows back into memory after `PruneExcluded` left gapped seqs, breaking the `seq = firstInMemorySeq + sliceIndex` invariant so the next save wrote content onto the wrong rows (role/JSON mismatch corruption). Rows are now re-numbered contiguously via a full rewrite after loading, with a regression test reproducing the evict→prune→load→save sequence.
- Epoch-guard all save paths to prevent lost concurrent mutations (#215) — every save path releases the session mutex during disk I/O; a mutation in flight was silently dropped from post-I/O bookkeeping. A save epoch counter is bumped on every logical mutation and re-checked after I/O: idempotent paths skip bookkeeping on mismatch so the mutation is re-persisted, and destructive paths force a clean full rewrite. `DeleteLastMessage` was also replaced with a seq-watermark delete so a stale delete cannot wipe a concurrent append.

## [0.7.8] - 2026-08-28

### Added

#### Agent
- Subagent context compaction with reactive recovery and session sync (#216) — when the LLM API rejects a subagent prompt for exceeding the model's context window, old messages are summarized and the request retried instead of failing the whole task. Uses a data-driven per-provider overflow classifier (`IsContextOverflowError`) covering OpenAI, Anthropic, Gemini, Bedrock, and Mistral patterns, with a budget of 3 reactive compaction attempts between successful responses to prevent compact-fail loops. Proactive compaction threshold is configurable (`CompactionThresholdPercent`, default 75%) with an optional dedicated `CompactionModel` for summarization, and compact events are published to the message bus for TUI visibility.
- Subagent compaction synced to persisted sessions (#216) — new `SessionManager.CompactSession` stores the summary, excludes all but the last keepCount messages from context (preserving the original request at index 0), persists, and optionally evicts excluded messages. Both proactive and reactive subagent compaction paths sync to the persisted session, so history no longer balloons back after a restart. The main agent loop now uses the shared overflow classifier too, including Bedrock `InvalidParameter` total-token errors, instead of a broad substring heuristic that misfired on unrelated errors like max_tokens limits.

#### WebUI
- Validation error extraction in settings (#215) — validation errors from 422 API responses are extracted and displayed; the `ApiError` class gained a `validationErrors` field, with tests for HTTP client parsing.
- `max_tool_iterations=0` is now allowed (#215) — config validation accepts 0 to mean unlimited tool iterations instead of rejecting it.

### Fixed

#### TUI
- Input focus indicator shown only when the input is active (#217) — the chat input cursor kept blinking even when a modal, pending approval, or onboarding wizard was on top. A `syncChatInputFocus` helper now aligns textarea focus with app state after every message, and the blink command is only returned on the blurred→focused transition so the cursor restarts cleanly without spamming blink timers.

## [0.7.7] - 2026-08-20

### Added

#### TUI
- Color themes with live preview, onboarding integration, and 12 built-in themes (#211) — user-selectable color themes (dracula, nord, catppuccin, gruvbox, tokyo-night, solarized-light, one-dark, monokai, github-dark, rose-pine, dracula-pro, blood-moon) with live preview on navigation in Settings → Interface and during first-run onboarding. Selection is persisted in `~/.lele/tui.json`; custom partial themes are supported with Dracula fallbacks. Package-level style vars are rebuilt via `ApplyTheme()` so all 11 semantic colors propagate instantly.
- Community themes support (#212) — community themes are fetched from the [awesome-lele](https://github.com/xilistudios/awesome-lele) repo and can be installed directly from the TUI Settings → Interface → Theme picker. Two sections (Built-in + Community) with async fetch, install/uninstall, and live preview for installed themes.
- Unified `/settings` UI for agents, system, and interface config (#206) — single settings modal with tabbed sections for agent defaults, system preferences, and interface configuration, replacing scattered config commands.

#### WebUI
- Agent description viewing and editing (#207) — dedicated UI for viewing and editing agent descriptions directly from the WebUI agent management page.

#### Tools
- Redact secret values from tool results (#205) — `secret get` now returns first 3 chars + `****` instead of plaintext value. `ForUser` message includes `SECRET` name usage hint. `secret list` includes exec placeholder examples. Raw secret values are no longer exposed in chat history, forcing agents to use `exec` with `{{SECRET:name}}` placeholders for secure injection.

### Fixed

#### WebUI
- Show correct tool names in history instead of generic 'Action' label (#210) — tool call names are now resolved from the message's `tool_calls` array and displayed in the history view.
- Optimize session/model/agent loading performance (#209) — parallelized data fetching and reduced redundant API calls for faster page loads.

#### Tools
- Subagent data race in tool coordinator (#205) — fixed a race condition in the subagent tool coordinator that could cause panics under concurrent access.

### Changed

#### TUI
- Removed message count feature from TUI and WebUI (#208) — the message count display was removed from both interfaces as part of a UI simplification pass.

## [0.7.5] - 2026-08-14

### Fixed

#### Agent
- `read_image` no longer hidden when a fallback model lacks vision — the tool was filtered out if ANY model in the fallback chain didn't support images, so a vision-capable primary model lost `read_image` just because a fallback couldn't see. The tool is now exposed based on the primary model only; when the chain fails over to a non-vision model, image content parts are stripped per-candidate in `callWithFallback` so the fallback never receives image data it would reject.
- System prompt vision flag syncs with the session model — the flag was set once at instance creation, so switching models at runtime (`/model`, TUI picker, REST) left the tools section of the system prompt hiding/showing `read_image` based on a stale value. It is now re-derived from the session's current model at the start of each agent loop.

#### Tools
- Background exec session isolation — background shell processes are now scoped to the session that started them. `list_background_execs`, `get_background_exec_output`, and `stop_background_exec` only see processes owned by the acting session (plus those of subagents it spawned, since subagent session keys extend the parent key). Foreign processes are reported as "not found", so one session can no longer read or kill another session's background commands. Operator views (TUI `/bg`, WebUI) still see all processes.

#### WebUI
- Tool call names in history (#202) — streaming finalization with tool_calls was dropped by `saveIncrementalUnlocked` (only updated last msg); now tracks `modifiedFrom` index + batch `UpdateMessages`; `saveUnlocked` chains save paths instead of early-returning.

## [0.7.4] - 2026-08-13

### Added
- Release workflow with GoReleaser — automated release pipeline using GitHub Actions and GoReleaser for consistent binary builds across platforms.

## [0.7.2] - 2026-08-12

### Fixed
- Nameless tool_calls strip (#177) — tool calls without names were not being stripped from messages, causing API errors.
- LLM loop timeout (#178) — the agent loop could hang indefinitely waiting for LLM responses; added configurable timeout.

## [0.7.1] - 2026-08-11

### Added
- Goal evaluator — agents can now evaluate whether a conversation goal has been achieved.
- TUI lazy load (#175) — messages are now lazy-loaded in the TUI for better performance with large sessions.

### Fixed
- Panic prevention (#174) — added safeguards to prevent panics in edge cases during message processing.
