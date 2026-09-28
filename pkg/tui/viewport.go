package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// beginRenderFrame opens a render frame. Called once at the top of View():
// the first history read in the frame fills the frame snapshot (historyView)
// and every other consumer reuses it, so a frame performs at most one
// GetHistoryView call.
func (m *Model) beginRenderFrame() {
	m.frameOpen = true
	m.frameValid = false
	m.frameView = nil
	m.frameKey = ""
}

// endRenderFrame closes the render frame opened by beginRenderFrame and drops
// the snapshot. Nothing outside a frame may reuse it: history mutated in place
// (a finalized streaming assistant message) keeps the same length, so serving
// a slice from a previous frame would render stale content.
func (m *Model) endRenderFrame() {
	m.frameOpen = false
	m.frameValid = false
	m.frameView = nil
	m.frameKey = ""
}

// historyView returns the session history for the current render frame,
// performing at most ONE read (GetHistoryView) per frame.
//
// The frame count is taken through the memoized accessor
// (historyMessageCount), so the rebuild that consumes this same snapshot — and
// any later read of an unchanged one — does not scan it again (T12).
//
// The freshness check is O(1) and runs BEFORE the history is touched: inside an
// open frame a snapshot already taken for the same session is returned as-is.
// Outside a frame (Update() routes, tests) every call reads fresh — the same
// behaviour as before the frame snapshot existed.
//
// The returned slice is the immutable copy-on-write snapshot published by
// pkg/session: callers MUST NOT mutate it (nor its messages), and it must not
// be kept beyond the frame that took it (see endRenderFrame).
func (m *Model) historyView() []providers.Message {
	if m.frameOpen && m.frameValid && m.frameKey == m.currentKey {
		return m.frameView
	}

	var history []providers.Message
	if m.currentKey != "" && m.agentLoop != nil {
		if m.onHistoryRead != nil {
			m.onHistoryRead(m.currentKey)
		}
		history = m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)
	}

	m.frameKey = m.currentKey
	m.frameView = history
	m.frameCount = m.historyMessageCount(history)
	// The fingerprint terms come from the same read as the count: they are what
	// distinguishes "the assistant message was finalized in place" (count
	// unchanged, content changed) and "a tool/result message was appended"
	// (frameCount unchanged, nothing else changed) from a genuinely unchanged
	// state — see viewportContentKey.
	m.frameHistoryTerms(history)
	m.frameValid = true
	return history
}

// frameHistoryTerms records the O(1) history terms the viewport fingerprint is
// built from (see viewportContentKey): the total number of messages of the
// snapshot and whether its last message is still streaming.
//
// Both are recorded together, from one slice, so the fingerprint can never
// describe a state the materialized content does not reflect. Every producer of
// the content and of the fingerprint goes through here: the frame's single read
// (historyView) and each rebuild (updateViewportWithHistory).
func (m *Model) frameHistoryTerms(history []providers.Message) {
	m.frameHistoryLen = len(history)
	m.frameLastStreaming = lastMessageStreaming(history)
}

// lastMessageStreaming reports whether the last message of a history snapshot
// is still streaming. It is the only signal that tells "the assistant message
// was finalized in place" — same message count, changed content — apart from
// the streaming state, so the viewport rebuild check and the materialization
// fingerprint both key on it.
func lastMessageStreaming(history []providers.Message) bool {
	return len(history) > 0 && history[len(history)-1].Streaming
}

// historyCount returns the number of user+assistant messages in the current
// frame's history snapshot. It never reads the history on its own: it resolves
// through historyView's O(1) frame check, which is what keeps the two
// getTokenUsage call sites of a frame on the same value.
func (m *Model) historyCount() int {
	if m.currentKey == "" {
		return 0
	}
	m.historyView()
	return m.frameCount
}

// tokenCacheTTL bounds how often the expensive token/context usage is
// recomputed for the sidebar. The value is refreshed immediately whenever the
// history message count changes, so this TTL only throttles recomputation
// during idle renders (cursor blink, mouse moves) within a single turn.
const tokenCacheTTL = 2 * time.Second

// lazyLoadBatchSize is the number of older messages loaded per scroll-up
// batch when the render window expands toward the start of the history.
const lazyLoadBatchSize = 50

// maxHomeExpandIterations caps the number of maybeExpandRenderWindow calls
// the Home key triggers. Without this bound, a session with thousands of
// compacted messages would fire one SQLite page-load + viewport rebuild per
// iteration — the "↑ N earlier messages" banner keeps telling the user there
// is more history above.
const maxHomeExpandIterations = 20

// getTokenUsage returns cached token/context usage for the sidebar, refreshing
// the underlying (expensive) backend calls at most once per tokenCacheTTL or
// when the history message count changes. This keeps View() cheap: previously
// GetCurrentContextUsage ran on every render, rebuilding the system prompt from
// disk and estimating tokens over the full history each time.
//
// msgCount is the caller's hoisted message count for the current frame (the
// frame's single history read, see historyView). It is a parameter on purpose:
// the freshness check below must be O(1) and must NOT touch the history, so
// this function never reads it — it only compares the cheap cache key.
func (m *Model) getTokenUsage(msgCount int) (current, window, cumInput, cumOutput int) {
	if m.currentKey == "" {
		return 0, 0, 0, 0
	}

	cacheKey := fmt.Sprintf("%s:%d", m.currentKey, msgCount)

	if m.tokenCacheKey == cacheKey && time.Since(m.tokenCacheTime) < tokenCacheTTL {
		return m.tokenCacheCurrent, m.tokenCacheWindow, m.tokenCacheCumInput, m.tokenCacheCumOutput
	}

	// Cache miss: only now pay for the expensive backend reads.
	current, window = m.agentLoop.GetProvidable().GetCurrentContextUsage(m.currentKey)
	cumInput, cumOutput, _ = m.agentLoop.GetProvidable().GetTokenCounts(m.currentKey)

	m.tokenCacheKey = cacheKey
	m.tokenCacheTime = time.Now()
	m.tokenCacheCurrent = current
	m.tokenCacheWindow = window
	m.tokenCacheCumInput = cumInput
	m.tokenCacheCumOutput = cumOutput

	return current, window, cumInput, cumOutput
}

// isCompactionSummary reports whether msg is an internal context-compaction
// summary that should not be rendered in the TUI chat history.
func isCompactionSummary(msg providers.Message) bool {
	return msg.Role == "user" &&
		(strings.HasPrefix(msg.Content, "## Summary of Previous Conversation\n\n") ||
			strings.HasPrefix(msg.Content, "[Context compacted"))
}

// updateViewport refreshes the viewport from the current frame's history
// snapshot. Callers on Update() routes (and tests) can call it directly: with
// no frame open it reads the history once, exactly as before.
//
// This is the Update-side rebuild entry point; the render path reaches the
// rebuild only through syncViewportForFrame (see the T3 block below).
func (m *Model) updateViewport() {
	m.updateViewportWithHistory(m.historyView())
}

// updateViewportWithHistory refreshes the viewport (base + overlay) from an
// already-read history snapshot. renderChatLayout passes the frame snapshot it
// hoisted, so a frame reads the history once for everything it draws.
//
// Callers from the render path must go through syncViewportForFrame: this
// function is the O(lines) rebuild itself (see the T3 block below).
func (m *Model) updateViewportWithHistory(history []providers.Message) {
	if m.onViewportRebuild != nil {
		m.onViewportRebuild(m.frameOpen)
	}

	// The fingerprint recorded at the end of this rebuild describes THIS
	// snapshot (its length, its streaming flag), so re-derive those terms here:
	// historyView already recorded them for its own read, but a caller passing a
	// snapshot directly (tests) must not leave a stale stamp behind — the
	// fingerprint would then claim a state the content does not reflect.
	m.frameHistoryTerms(history)

	if m.currentKey == "" {
		m.viewport.SetContent("")
		m.renderedBaseValid = false
		m.renderedBaseKey = ""
		m.noteViewportMaterialized(0)
		return
	}

	// Capture whether the viewport is currently at the bottom BEFORE any
	// base/overlay rebuild. The rebuild can change maxYOffset (e.g. a new
	// message is appended to baseLines), which would make a post-rebuild
	// AtBottom() check return false even though the user was at the bottom a
	// moment ago. Auto-scroll must reach the new bottom only when the user was
	// already there; otherwise the user's scroll position is preserved.
	// forceGotoBottom forces a scroll to bottom regardless (session switch,
	// new chat, freshly created content).
	wasAtBottom := m.viewport.AtBottom() || m.forceGotoBottom

	// history arrives as the render frame's single snapshot. pkg/session
	// publishes the session history as an immutable copy-on-write snapshot: on
	// the hot path the read is O(1) with no allocation and no copy while that
	// snapshot is current, and the caller MUST NOT mutate the slice or any
	// message in it (the same backing array is shared by every concurrent
	// reader). The frame hoists the read once (see historyView) so the viewport
	// rebuild, the token/context readouts and the sidebar all consume the same
	// slice instead of each reading it back.

	// Reset the lazy-load render window when switching sessions.
	if m.renderWindowSessionKey != m.currentKey {
		m.renderWindowSessionKey = m.currentKey
		m.renderStartIdx = -1
	}

	// Clear streaming state if the assistant message is fully saved in history
	m.cleanupStreamingIfCompleteWithHistory(history)

	// Compute message count from the already-fetched frame snapshot (no
	// further GetHistoryView call). Uses the combined count (archived prefix +
	// resident) so a prefix change triggers rebuild. The resident half is
	// reused for the materialization fingerprint below (same scan, so the
	// render path's msgCount and this one are always the same number).
	//
	// historyMessageCount is the memoized accessor: the frame's read above
	// (historyView) already counted THIS snapshot, so the rebuild reuses that
	// result instead of running the rolescan a second time (T12).
	residentCount := m.historyMessageCount(history)
	historyMsgCount := m.displayMessageCountFrom(residentCount)

	// Determine if the rendered base cache is still valid.
	// Invalidated when session key or viewport width changes.
	// NOT invalidated on message count change — the per-message render cache
	// handles incremental updates, so only new/changed messages are re-rendered.
	widthCacheKey := fmt.Sprintf("%s:%d", m.currentKey, m.viewport.Width)
	cacheValid := m.renderedBaseKey == widthCacheKey && m.renderedBaseValid

	if !cacheValid {
		// Width or session changed — clear per-message render cache
		if m.msgRenderCacheWidth != m.viewport.Width {
			m.msgRenderCacheLines = nil
			m.msgRenderCacheWidth = m.viewport.Width
		}
	}

	// Rebuild base if cache is invalid OR message count changed OR the last
	// message transitioned from Streaming=true to Streaming=false (the count
	// doesn't change but the rendered content does — the streaming message
	// was skipped during processing and must now be included) OR the archived
	// prefix moved OR the history grew in a way the counted total cannot see
	// (an appended tool result, say).
	//
	// The archive needs its own term: widthCacheKey must NOT carry it, because
	// that key also decides whether msgRenderCacheLines is dropped (wiping it on
	// every scroll-up page would re-run glamour over the whole window). And the
	// message-count term alone is not enough: archivedVisibleCount only counts
	// user/assistant rows, so an eviction that lands on other roles — or one
	// that is offset by a same-tick append — can leave the combined count
	// identical while the rendered archived rows and the "↑ N earlier messages"
	// banner both change. Keyed on the prefix identity, not just its size, so a
	// reset + reload of the same length still invalidates.
	//
	// Every term here is mirrored in the viewport fingerprint (viewportContentKey
	// adds len(history) and the streaming flag): the fingerprint decides whether
	// the rebuild runs at all, so a term it cannot see is a term that never
	// reaches this check for a View()-only state change.
	lastMsgStreaming := lastMessageStreaming(history)
	archiveKey := m.archivedCacheKey()
	if !cacheValid || m.renderedBaseMsgCount != historyMsgCount ||
		m.renderedBaseHistoryLen != len(history) ||
		m.renderedBaseArchiveKey != archiveKey ||
		(m.renderedBaseLastStreaming && !lastMsgStreaming) {
		baseLines := m.buildRenderedHistoryLines(history)
		m.renderedBaseKey = widthCacheKey
		m.renderedBaseArchiveKey = archiveKey
		m.renderedBaseMsgCount = historyMsgCount
		m.renderedBaseHistoryLen = len(history)
		m.renderedBaseValid = len(baseLines) > 0
		m.renderedBaseLastStreaming = lastMsgStreaming
		// Push the new base lines to the viewport — O(1) pointer swap.
		// No more strings.Split on a giant concatenated string.
		m.viewport.SetBaseLines(baseLines)
	}

	// ------------------------------------------------------------------
	// FAST PATH: if there's no overlay content to show, skip the overlay
	// build entirely. On idle frames (no streaming, no pending messages,
	// no approvals, no feedback), this returns immediately after the base
	// check above — O(1) per frame.
	// ------------------------------------------------------------------
	hasStreaming := m.processing && (m.currentStream != "" || m.currentThinking != "" || m.currentToolAction != "")
	hasOverlay := hasStreaming ||
		m.pendingUserMessage != "" ||
		m.pendingApprovalID != "" || m.approvalResult != "" ||
		m.activeGroupID != "" ||
		m.compactFeedback != "" ||
		m.statusFeedback != "" ||
		m.hasSubagentProgressOverlay()

	if !hasOverlay && !m.selecting {
		// Nothing ephemeral to show — ensure overlay is cleared.
		if len(m.viewport.overlayLines) > 0 {
			m.viewport.SetOverlayLines(nil)
		}
		// Even with no overlay, if the viewport was at the bottom (or a
		// forced scroll is pending), scroll to bottom so new base content
		// is visible. Without this, newly arrived messages that don't
		// produce an overlay (e.g. a completed assistant response after
		// streaming ends) would not trigger auto-scroll.
		if m.forceGotoBottom || (wasAtBottom && m.viewport.totalLines() > 0 && m.viewport.Height > 0) {
			m.forceGotoBottom = false
			m.viewport.GotoBottom()
		}
		m.noteViewportMaterialized(residentCount)
		return
	}

	// Build the ephemeral overlay (streaming, approvals, feedback).
	// This is small — typically a few lines. We always rebuild it because
	// it's cheap and avoids complex dirty-tracking.
	var overlaySb strings.Builder
	lastRole := lastHistoryRoleFromHistory(history)

	// Show pending user message immediately (before agent responds)
	if m.pendingUserMessage != "" {
		// Search from the end since the message is most likely recent
		alreadyInHistory := false
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == "user" && history[i].Content == m.pendingUserMessage {
				alreadyInHistory = true
				break
			}
			// Optimization: stop searching after going back 10 messages
			// since the pending message should be very recent
			if len(history)-i > 10 {
				break
			}
		}
		if !alreadyInHistory {
			// Defensive: if an assistant message already exists AFTER the
			// last user message, the turn is already underway (mid-turn).
			// The pending message is stale (e.g. it never matched history
			// due to content normalization) — clear it and keep lastRole
			// as-is so the agent title is not re-emitted before every
			// tool call.
			midTurn := false
			for i := len(history) - 1; i >= 0; i-- {
				if history[i].Role == "assistant" {
					midTurn = true
					break
				}
				if history[i].Role == "user" {
					break
				}
				if len(history)-i > 10 {
					break
				}
			}
			if midTurn {
				m.pendingUserMessage = ""
			} else {
				overlaySb.WriteString(UserRoleStyle.Render(i18n.T("tui.you")) + "\n")
				overlaySb.WriteString(UserMessageStyle.Render(wrapText(sanitizeDisplayText(m.pendingUserMessage), m.viewport.Width-4)) + "\n\n")
				lastRole = "user"
			}
		} else {
			m.pendingUserMessage = ""
		}
	}

	if m.processing && (m.currentStream != "" || m.currentThinking != "" || m.currentToolAction != "") {
		// Only show agent name when coming from user (start of a turn)
		if lastRole == "" || lastRole == "user" || lastRole == "system" {
			agentID := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
			agentInfo, ok := m.agentLoop.GetProvidable().GetAgentInfo(agentID)
			agentName := agentID
			if ok && agentInfo.Name != "" {
				agentName = agentInfo.Name
			}
			overlaySb.WriteString(AssistantRoleStyle.Render(agentName) + "\n")
		}

		if m.currentThinking != "" {
			rendered := m.getRenderedThinking(m.viewport.Width - 8)
			overlaySb.WriteString(ThinkingContentStyle.Render(rendered) + "\n")
		}
		if m.currentStream != "" {
			rendered := m.getRenderedStream(m.viewport.Width - 6)
			overlaySb.WriteString(rendered + "\n")
		}
		// Show the currently executing tool call (cleared when stream resumes or completes)
		if m.currentToolAction != "" {
			overlaySb.WriteString(renderToolCallRow(sanitizeDisplayText(m.currentToolAction), m.viewport.Width) + "\n")
		}
		overlaySb.WriteString("\n")
	}

	// Show per-task progress of running subagents (TUI-M4): latest action of
	// each subagent spawned by the current parent session. Rendered under the
	// streaming overlay, and also on its own when the parent has nothing
	// streaming (e.g. the turn is waiting on wait_for_subagent) so progress
	// stays visible for the whole duration of the subagent work. Suppressed
	// while the chat sidebar is visible, since the sidebar already lists
	// subagent statuses and the inline block would be redundant.
	if progressBlock := m.renderSubagentProgress(); progressBlock != "" {
		overlaySb.WriteString(progressBlock + "\n")
	}

	// Show pending command approval prompt
	if m.pendingApprovalID != "" {
		overlaySb.WriteString(m.renderApprovalPrompt())
	}

	// Show brief approval result feedback (after user decision, before tool result)
	if m.approvalResult != "" {
		overlaySb.WriteString(m.approvalResult + "\n\n")
	}

	// Show group chat turns (Mixture of Agents) when a group is active
	if m.activeGroupID != "" {
		if turns, ok := m.groupTranscripts[m.activeGroupID]; ok && len(turns) > 0 {
			overlaySb.WriteString(m.renderGroupTurns(turns, m.viewport.Width))
			overlaySb.WriteString("\n")
		}
	}

	// Show compaction result feedback (sanitized at render as defense in
	// depth: the assignment site sanitizes too, but presentation state must
	// never carry controls/bidi into the frame regardless of writer).
	if m.compactFeedback != "" {
		overlaySb.WriteString(sanitizeDisplayText(m.compactFeedback) + "\n\n")
	}

	// Show /status report feedback
	if m.statusFeedback != "" {
		overlaySb.WriteString(m.statusFeedback + "\n\n")
	}

	// Check if viewport is at bottom BEFORE updating overlay.
	// This preserves the user's scroll position when they've scrolled up.
	// forceGotoBottom overrides this when switching sessions or creating a new chat.
	// wasAtBottom was captured at the very start of updateViewport, before any
	// base/overlay rebuild, so it reflects the user's true pre-update position.
	m.forceGotoBottom = false

	// Push overlay lines to the viewport. SetOverlayLines is O(overlay_lines)
	// and does NOT trigger the expensive base-line Split/findLongestLineWidth.
	overlayContent := overlaySb.String()
	if overlayContent != "" {
		overlayLines := strings.Split(strings.ReplaceAll(overlayContent, "\r\n", "\n"), "\n")
		// Overlay lines are rebuilt on every viewport update (streaming, tool
		// status, approvals), so collapse their SGR churn here too: the merge
		// is O(bytes) once per update, while the lines are read by
		// lineViewport.View()/paintFrame/reapplyBackground every frame.
		mergeLines(overlayLines)
		m.viewport.SetOverlayLines(overlayLines)
	} else {
		m.viewport.SetOverlayLines(nil)
	}

	if wasAtBottom && m.viewport.totalLines() > 0 && m.viewport.Height > 0 {
		m.viewport.GotoBottom()
	}

	// Record the state this rebuild materialized so View() can recognise it as
	// fresh (O(1) fingerprint compare) instead of rebuilding again.
	m.noteViewportMaterialized(residentCount)
}

// ---------------------------------------------------------------------------
// Render-path freshness (T3): View() does not rebuild the viewport.
//
// bubbletea calls View() after EVERY Update — every streaming chunk, spinner
// tick, mouse move and keypress — so rebuilding the viewport there made the
// streaming throttle pointless (32 ms window defeated on every frame) and put
// O(history) work on every frame of a long chat.
//
// The contract now is:
//
//   - the viewport content (base lines + ephemeral overlay) is MATERIALIZED by
//     the Update-driven paths: updateViewport() from event handlers and
//     throttledUpdateViewport() for streaming chunks (immediate leading edge,
//     then at most one rebuild per streamThrottleInterval);
//   - View() only runs the O(1) freshness checks it owns — "is what is on
//     screen what this state would produce" (the fingerprint) and "was it
//     wrapped for the current layout dimensions" (the geometry, visible only
//     here because View() owns the viewport dimensions) — and rebuilds only
//     when one of them fails. A failed content check is deferred while the
//     streaming throttle's pending window is open (that is the throttle); a
//     failed geometry check never is, and neither is a content check older
//     than streamThrottleInterval (see viewportRebuildDeferred);
//   - the O(lines) rebuild lives in updateViewportWithHistory and is reachable
//     from the render path ONLY through syncViewportForFrame below.
//
// Unchanged by this: the archived-history loads (refreshArchivedHistory,
// loadOlderArchivedPage) still run exclusively from Update routes — never from
// View(). But nothing here reads the history beyond the frame's single
// snapshot, so the T2 invariant (one GetHistoryView per frame) still holds.
// ---------------------------------------------------------------------------

// syncViewportForFrame is the only viewport-materialization entry point
// reachable from View(). It runs the render path's O(1) checks and delegates to
// updateViewportWithHistory only when a rebuild is genuinely required.
//
// history is the render frame's snapshot (already read by renderChatLayout) and
// msgCount the resident user+assistant count of that same snapshot, so this
// call adds no history read.
func (m *Model) syncViewportForFrame(history []providers.Message, msgCount int) {
	if m.viewportContentUpToDate(msgCount) {
		return
	}
	// The streaming throttle may be holding a scheduled rebuild (≤ 32 ms away):
	// serving the content its leading edge materialized is the whole point of
	// the throttle. Geometry is never deferred — a base wrapped for other
	// dimensions is visibly wrong, not 32 ms stale.
	if !m.viewportGeometryChanged() && m.viewportRebuildDeferred() {
		return
	}
	m.updateViewportWithHistory(history)
}

// viewportContentUpToDate reports whether the materialized viewport content
// already is what the current model state would produce. O(1): one fingerprint
// compare plus two field reads — never a scan of the history or of the rendered
// lines.
//
// msgCount must be the resident user+assistant count of the frame's history
// snapshot (historyCount); viewportContentKey folds in the same value the
// Update-side skip guard uses, so both agree by construction.
func (m *Model) viewportContentUpToDate(msgCount int) bool {
	// "" means "not materialized yet" or "explicitly invalidated" (several
	// settings paths clear it to force a repaint).
	if m.lastViewportKey == "" {
		return false
	}
	// renderedBaseValid doubles as the base-invalidation signal: theme
	// switches (invalidateRenderCache), window resizes, session switches and
	// compaction clear it, and those paths rely on the render path rebuilding.
	// A session with nothing to render keeps it false, which only costs a
	// trivially cheap rebuild (empty base) per frame.
	if !m.renderedBaseValid {
		return false
	}
	return m.lastViewportKey == m.viewportContentKey(msgCount)
}

// viewportGeometryChanged reports whether the base lines on screen were
// materialized for different layout dimensions than the current ones. View()
// recalculates m.viewport.Width/Height from the rendered bands on every frame
// (they depend on the status line, queue row and input bar heights), so this is
// the one stale state only the render path can see.
func (m *Model) viewportGeometryChanged() bool {
	return m.viewportBuiltWidth != m.viewport.Width || m.viewportBuiltHeight != m.viewport.Height
}

// viewportRebuildDeferred reports whether the streaming throttle owns the
// pending rebuild for this frame, i.e. chunks arrived after the last
// materialization and the coalesced rebuild is scheduled within
// streamThrottleInterval (throttledUpdateViewport's leading edge materialized
// the first chunk of the burst immediately).
//
// The window bound is what keeps this safe: if the scheduled tick is ever lost
// (early-returned handler, dropped command), the render path rebuilds anyway
// once the interval elapses, so content can never be older than
// streamThrottleInterval.
func (m *Model) viewportRebuildDeferred() bool {
	if !m.streamPendingUpdate || m.streamThrottleInterval <= 0 {
		return false
	}
	return time.Since(m.viewportBuiltAt) < m.streamThrottleInterval
}

// noteViewportMaterialized records the state the viewport content was just
// materialized from. It is called at the END of every updateViewportWithHistory
// exit path — after that function's own state fixes (pending user message
// cleared, streaming state reconciled, forceGotoBottom consumed) — so the
// stored fingerprint describes the state as it is now on screen and the next
// frame can skip the rebuild with a single compare.
func (m *Model) noteViewportMaterialized(residentCount int) {
	m.lastViewportKey = m.viewportContentKey(residentCount)
	m.viewportBuiltWidth = m.viewport.Width
	m.viewportBuiltHeight = m.viewport.Height
	m.viewportBuiltAt = time.Now()
	// Whatever the throttle was waiting to render is on screen now: the stream
	// state is materialized as one accumulated string, so a pending flag can
	// never be left pointing at content this rebuild did not already include.
	m.streamPendingUpdate = false
}

// viewportContentKey returns the O(1) fingerprint of every piece of model state
// the viewport content (base lines + ephemeral overlay) depends on. It is the
// single freshness predicate of the viewport: the render path
// (syncViewportForFrame) and the Update path (shouldSkipViewportUpdate) compare
// the live fingerprint against the one recorded by noteViewportMaterialized.
//
// msgCount is the caller's hoisted resident count (never derived here: the
// fingerprint must stay O(1) and must not touch the history).
func (m *Model) viewportContentKey(msgCount int) string {
	// Fields the Update-side skip guard has always used (unchanged values, same
	// order as the original fingerprint).
	content := fmt.Sprintf("%s|%d|%d|%d|%s|%s|%s|%s|%s|%v|%v|%v|%d|%s",
		m.currentKey,
		m.viewport.Width,
		msgCount,
		len(m.currentStream)+len(m.currentThinking),
		m.currentToolAction,
		m.pendingUserMessage,
		m.pendingApprovalID,
		m.approvalResult,
		m.activeGroupID,
		m.processing,
		m.compactFeedback != "",
		m.statusFeedback != "",
		m.renderStartIdx,
		// Full archived fingerprint, not just the prefix length: see
		// archivedCacheKey. Both cache layers share it so they cannot drift.
		m.archivedCacheKey(),
	)
	// Render-path half (T3): the frame-owned dimensions plus the overlay inputs
	// whose event handlers always rebuilt directly (so the Update-side guard
	// never needed them) but whose direct mutation — tests, session restore —
	// must still be observed by the render path.
	// frameHistoryLen/frameLastStreaming are the O(1) terms of the same history
	// snapshot that produced msgCount (see frameHistoryTerms), so they can never
	// describe a different snapshot: the length catches an appended tool/result
	// message (invisible to msgCount) and the flag catches the assistant message
	// being finalized in place.
	extra := fmt.Sprintf("|%d|%d|%d|%v|%v|%v|%s|%v|%d|%d|%d|%d|%d|%v|%v",
		m.viewport.Height,
		m.frameHistoryLen,
		m.maxRenderedMessages,
		m.selecting,
		m.frameLastStreaming,
		// A pending forced scroll is consumed BY the rebuild (and by nothing
		// else), so it belongs to the fingerprint: without it the frame could
		// consider itself up to date and leave the jump-to-bottom waiting for an
		// unrelated change.
		m.forceGotoBottom,
		m.pendingApprovalCmd,
		m.approvalShowFull,
		len(m.groupTranscripts[m.activeGroupID]),
		len(m.groupMeta[m.activeGroupID].synthesis),
		int(m.modalMode),
		len(m.subagentProgress),
		m.subagentProgressDigest(),
		// Sidebar visibility (isChatSidebarVisible) moves the subagent progress
		// block in and out of the viewport overlay, so the two flags that can
		// change it have to be part of the fingerprint as well (modalMode is
		// already above).
		m.showWelcome,
		m.onboardingActive,
	)
	return content + extra
}

// subagentProgressDigest returns an order-independent fingerprint of the
// subagent progress overlay inputs (task ID + latest action). XOR-folded
// per-entry FNV-64a hashes: map iteration order is random, so an
// order-dependent fold would make the fingerprint differ on every frame and
// defeat the render-path guard. Bounded by subagentProgressCap entries.
func (m *Model) subagentProgressDigest() uint64 {
	var digest uint64
	for id, action := range m.subagentProgress {
		h := fnv64aWriteField(fnv64aWriteField(fnv64aOffset, id), action)
		digest ^= h
	}
	return digest
}

// maybeExpandRenderWindow expands the lazy-load render window backwards.
// When renderStartIdx > 0, it shifts the in-memory window back by
// lazyLoadBatchSize. When the in-memory window is fully expanded
// (renderStartIdx == 0) but archived messages are not yet fully loaded, it
// loads one older archived page from SQLite. Returns true if the window was
// expanded. This method MUST only be called from Update() routes (not View())
// since it may perform SQLite I/O.
func (m *Model) maybeExpandRenderWindow() bool {
	if m.currentKey == "" || !m.viewport.AtTop() {
		return false
	}
	if m.renderStartIdx < 0 {
		return false // uninitialized
	}

	oldTotal := m.viewport.totalLines()
	expanded := false

	if m.renderStartIdx > 0 {
		// In-memory window: shift backwards by lazyLoadBatchSize.
		newStart := m.renderStartIdx - lazyLoadBatchSize
		if newStart < 0 {
			newStart = 0
		}
		m.renderStartIdx = newStart
		expanded = true
	} else if m.archivedHasOlder || m.archivedHiddenCount() > 0 {
		// In-memory window is fully expanded: try loading one more archived
		// page from SQLite (Update path — this is never called from View()).
		expanded = m.loadOlderArchivedPage() > 0
	}

	if !expanded {
		return false
	}

	// Force a base rebuild on the next updateViewport pass.
	m.renderedBaseValid = false
	m.renderedBaseKey = ""
	m.renderedBaseMsgCount = -1
	m.renderedBaseHistoryLen = -1

	m.updateViewport()

	// Compensate scroll position: the prepended lines shifted content down,
	// so move YOffset down by the same amount to keep the same line at top.
	if delta := m.viewport.totalLines() - oldTotal; delta > 0 {
		m.viewport.YOffset = delta
		m.viewport.clampOffset()
	}
	return true
}

// countHistoryMessages counts user+assistant messages in the given history
// slice. This is the pure-function version of getHistoryMessageCount that
// accepts the history directly, avoiding a redundant GetHistoryView call; it is
// what the render frame uses to derive the count from its single snapshot.
//
// It is the DEFINITION of what is counted (roles user/assistant, i.e. the
// resident display messages — tool/system rows are not counted) and stays the
// fallback of the memoized accessor below. Callers on the hot path go through
// Model.historyMessageCount so one snapshot is scanned once.
func countHistoryMessages(history []providers.Message) int {
	count := 0
	for _, msg := range history {
		if msg.Role == "user" || msg.Role == "assistant" {
			count++
		}
	}
	return count
}

// historyMessageCount returns countHistoryMessages(history), memoized on the
// IDENTITY of the slice it is asked about (head element address + length).
//
// Why that key is sound — the count is a pure function of the roles and the
// length of the slice, and pkg/session hands out an immutable copy-on-write
// snapshot (GetHistoryView) whose identity changes whenever the resident message
// set can change:
//
//   - every structural mutation (append, in-place replacement, delete,
//     truncate, eviction, exclusion) republishes a freshly cloned slice
//     (publishViewLocked), so the head address and/or len change;
//   - a streaming chunk mutates msg.Content in place and deliberately leaves the
//     published snapshot stale, so the next read rebuilds the copy — a new
//     backing array again (fallback of the epoch check in GetHistoryView).
//     Chunks never change a role nor the length without a republish (see
//     pkg/session/streaming.go), and the count depends on nothing else, so the
//     identity key cannot hide a changed count.
//
// The head pointer is stored as a *providers.Message on purpose: keeping the
// backing array reachable also keeps its address unique, so the GC can never
// recycle it into a false hit for a different snapshot. Taking the address of
// history[0] only computes it — it reads nothing, mutates nothing and does not
// race with the writers that published the snapshot.
//
// The scan is therefore O(1) amortized: the frame's read (historyView) and the
// rebuild that consumes it (updateViewportWithHistory) resolve the same
// snapshot and share one scan, and a frame over an unchanged snapshot pays none.
func (m *Model) historyMessageCount(history []providers.Message) int {
	if len(history) == 0 {
		// No identity to key on (there is no history[0]) and the count of an
		// empty history is 0 by definition — no scan, nothing worth memoizing.
		return 0
	}
	if memo := &m.countMemo; memo.valid && memo.n == len(history) && memo.head == &history[0] {
		return memo.count
	}
	if m.onHistoryCountScan != nil {
		m.onHistoryCountScan()
	}
	count := countHistoryMessages(history)
	m.countMemo = historyCountMemo{valid: true, head: &history[0], n: len(history), count: count}
	return count
}

// lastHistoryRoleFromHistory returns the role of the last non-system message
// from an already-fetched history slice.
func lastHistoryRoleFromHistory(history []providers.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "system" {
			return history[i].Role
		}
	}
	return ""
}

// defaultRenderStartIdx returns the default index of the first message to
// render for a history of msgCount messages. When the history fits within the
// max-rendered-messages window it starts at 0; otherwise it starts at the
// oldest message that still fits within the window (the most recent
// maxRenderedMessages messages). A maxRenderedMessages of 0 (unlimited,
// backward compat) always starts at 0.
func (m *Model) defaultRenderStartIdx(msgCount int) int {
	if m.maxRenderedMessages <= 0 {
		return 0
	}
	if msgCount <= m.maxRenderedMessages {
		return 0
	}
	return msgCount - m.maxRenderedMessages
}

// buildRenderedHistoryLines renders completed messages from the given
// history slice combined with the display-only archived prefix. Archived
// messages are prepended before the resident history so the user sees a
// continuous transcript after /compact. The archived prefix NEVER enters
// the LLM context — it is purely for display.
func (m *Model) buildRenderedHistoryLines(history []providers.Message) []string {
	archived := m.archivedForCurrentSession() // read-only, no I/O
	nArchived := len(archived)
	totalMsgs := nArchived + len(history)

	// Virtualized rendering: only render the most recent N messages
	// when the conversation is very long. The render window start index is
	// persisted in m.renderStartIdx so it can be expanded (lazy loading of
	// older messages) on scroll-up. A value of -1 means uninitialized (falls
	// back to the default window); 0 means all messages are rendered.
	startIdx := m.renderStartIdx
	if startIdx < 0 || startIdx >= totalMsgs {
		startIdx = m.defaultRenderStartIdx(totalMsgs)
		m.renderStartIdx = startIdx
	}

	m.renderedMsgStartIdx = startIdx
	m.renderedMsgEndIdx = totalMsgs

	// Lazily initialize per-message render cache (stores []string lines)
	if m.msgRenderCacheLines == nil {
		m.msgRenderCacheLines = make(map[string][]string, m.maxRenderedMessages)
		m.msgRenderCacheWidth = m.viewport.Width
	}

	// Bound the cache (hallazgo P5): this is a rebuild, so prune it to the
	// fingerprints actually used by this render window. Entries are rebuilt
	// incrementally across message-count changes (hits below are copied into
	// the new map, misses are rendered and stored), so the incremental-hit
	// semantics are unchanged — but orphans left behind by compaction,
	// eviction or history edits drop out here instead of living forever.
	// Cost: N pointer copies per rebuild (N ≤ render window, ~200), and the
	// fast path (shouldSkipViewportUpdate / renderedBaseValid) skips rebuilds
	// entirely, so idle frames do no pruning work.
	liveCache := make(map[string][]string, totalMsgs-startIdx)

	// Pre-allocate result with a reasonable capacity estimate.
	result := make([]string, 0, min(totalMsgs-startIdx, m.maxRenderedMessages)*8)

	if startIdx > 0 || m.archivedHiddenCount() > 0 {
		hiddenEarlier := startIdx + m.archivedHiddenCount()
		header := CommentColorStyle.Render("  " + fmt.Sprintf(i18n.T("tui.earlierMessages"), hiddenEarlier))
		result = append(result, header, "")
	}

	lastRole := ""
	for i := startIdx; i < totalMsgs; i++ {
		var msg providers.Message
		if i < nArchived {
			msg = archived[i]
		} else {
			msg = history[i-nArchived]
		}

		// Skip internal context-compaction summaries
		if isCompactionSummary(msg) {
			continue
		}

		// Skip the last message if it's a streaming assistant message during
		// processing — the TUI is already rendering the live stream via currentStream.
		if m.processing && msg.Role == "assistant" && msg.Streaming && i == totalMsgs-1 {
			continue
		}

		// Suppression state of this message, computed here (not inside the
		// assistant branch) because it also decides whether the render may be
		// cached below: while the last message is executing, its tool-call rows
		// are suppressed — the active tool call is already shown in the overlay
		// (m.currentToolAction via the "tool.executing" event), so painting
		// msg.ToolCalls here too produces a visible duplicate — and that render
		// is a transient variant of the very same fingerprint.
		isExecutingMessage := m.currentToolAction != "" && m.processing && msg.Role == "assistant" && i == totalMsgs-1

		// Compute fingerprint for per-message cache
		fp := messageFingerprint(msg, m.viewport.Width)
		if cachedLines, ok := m.msgRenderCacheLines[fp]; ok {
			liveCache[fp] = cachedLines // keep in the pruned cache
			result = append(result, cachedLines...)
			lastRole = msg.Role
			continue
		}

		// Cache miss — render the message and store in cache
		var msgSb strings.Builder
		if msg.Role == "user" {
			msgSb.WriteString(UserRoleStyle.Render(i18n.T("tui.you")) + "\n")
			msgSb.WriteString(UserMessageStyle.Render(wrapText(sanitizeDisplayText(msg.Content), m.viewport.Width-4)) + "\n\n")
		} else if msg.Role == "assistant" {
			// Only show agent name when coming from user (start of a turn)
			if lastRole == "" || lastRole == "user" || lastRole == "system" {
				agentID := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
				agentInfo, ok := m.agentLoop.GetProvidable().GetAgentInfo(agentID)
				agentName := agentID
				if ok && agentInfo.Name != "" {
					agentName = agentInfo.Name
				}
				msgSb.WriteString(AssistantRoleStyle.Render(agentName) + "\n")
			}

			if msg.ReasoningContent != "" {
				rendered := m.renderMarkdown(msg.ReasoningContent, m.viewport.Width-8)
				msgSb.WriteString(ThinkingContentStyle.Render(rendered) + "\n")
			}

			if msg.Content != "" {
				rendered := m.renderMarkdown(msg.Content, m.viewport.Width-6)
				msgSb.WriteString(rendered + "\n")
			}

			// Render tool calls from assistant message (compact: tool_name: params).
			// Suppressed when this is the currently-executing message (see
			// isExecutingMessage above): the call is already in the overlay.
			// Once the tool completes (tool.result/stream clears
			// currentToolAction), the committed tool calls render here normally.
			if !isExecutingMessage {
				for _, tc := range msg.ToolCalls {
					toolName := tc.Name
					if toolName == "" && tc.Function != nil {
						toolName = tc.Function.Name
					}
					args := formatToolCallArgsCompact(tc)
					line := toolName
					if args != "" {
						line += ": " + args
					}
					msgSb.WriteString(renderToolCallRow(line, m.viewport.Width) + "\n")
				}
			}
			msgSb.WriteString("\n")
		} else if msg.Role == "tool" {
			summary := truncateToolResult(msg.Content, 150)
			msgSb.WriteString(renderToolResultBlock(summary, m.viewport.Width) + "\n")
		}
		// Skip system messages — they are internal prompts, not user-facing

		rendered := msgSb.String()
		// Split into lines once and cache the lines. This avoids re-splitting
		// on every frame when the viewport needs them.
		msgLines := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
		// Collapse the redundant SGR churn glamour/chroma emit per syntax
		// token (−82..88% bytes, −95% SGR, cell-identical output) ONCE at
		// cache-build time so every downstream per-frame stage (viewport
		// slice, paintFrame, reapplyBackground, Place, AppContainer) reads
		// already-merged lines. See mergeAdjacentSGR.
		mergeLines(msgLines)
		// Cache only renders the fingerprint fully describes. While this
		// message is the executing one, msgLines is a transient variant of the
		// same fingerprint (its tool-call rows were suppressed above), so
		// storing it poisons the entry the moment the message stops being the
		// last one — e.g. a message appended while the tool is still running
		// (the /compact window does exactly that): the hit path below would
		// keep serving the tool-row-less render until a width/session/theme
		// change. Not caching it costs one re-render of that single message
		// (the frame still shows the suppressed lines, which is the point) and
		// keeps every cached entry state-independent.
		if !isExecutingMessage {
			liveCache[fp] = msgLines // cache lines for fast assembly
		}
		result = append(result, msgLines...)
		lastRole = msg.Role
	}

	// Swap in the pruned cache (see the bound comment above): it now holds
	// exactly the fingerprints used by this render window.
	m.msgRenderCacheLines = liveCache

	return result
}

// buildRenderedHistory is the legacy entry point that fetches history internally.
// Kept for callers that don't have the history slice available: it resolves
// through the frame snapshot, so it never adds a second read inside a frame.
func (m *Model) buildRenderedHistory() []string {
	history := m.historyView()
	return m.buildRenderedHistoryLines(history)
}

// approvalInnerWidth returns the usable text width inside the approval box for
// the current viewport. ApprovalBox adds 2 border cells and 4 horizontal
// padding cells, so the content budget is viewport.Width-6. Small/zero widths
// (pre-layout renders) fall back to a sane minimum so the box never collapses.
func (m *Model) approvalInnerWidth() int {
	w := m.chatColumnWidth() - 6
	if w < approvalMinInnerWidth {
		return approvalMinInnerWidth
	}
	return w
}

// chatColumnWidth returns the width of the chat column (the viewport column).
// It mirrors the layout math performed in View so overlay content is sized
// correctly even when it is built before View runs — m.viewport.Width can be
// stale at that point (e.g. it still holds its default 80 right after a
// WindowSizeMsg), which would make boxes wider than the column and wrap their
// borders.
func (m *Model) chatColumnWidth() int {
	leftWidth := int(float64(m.width) * leftColumnRatio)
	if w := leftWidth - 2; w > 0 {
		return w
	}
	return m.viewport.Width
}

// approvalMinInnerWidth keeps the prompt readable in extremely narrow
// terminals; the box is still clipped by the viewport, which is the same
// behaviour as every other overlay block.
const approvalMinInnerWidth = 20

// singleLine collapses every whitespace run (including newlines) into single
// spaces so a multi-line command renders as one preview line.
func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// renderApprovalPrompt builds the inline approval prompt shown in the viewport
// when a command requires user approval.
//
// The command is rendered as a single truncated preview line by default. This
// is a correctness requirement, not cosmetics: the viewport clips every line to
// its width, so an unwrapped long command used to overflow the box and destroy
// its right border (and the surrounding layout). Pressing "v" swaps the preview
// for the full command, wrapped to the box width.
func (m *Model) renderApprovalPrompt() string {
	inner := m.approvalInnerWidth()
	cmd := sanitizeDisplayText(m.pendingApprovalCmd)

	var sb strings.Builder
	sb.WriteString("⚠️  " + i18n.T("tui.approvalRequired") + "\n\n")
	sb.WriteString(i18n.T("tui.approvalCommandLabel") + "\n")
	if m.approvalShowFull {
		sb.WriteString(wrapText(cmd, inner) + "\n")
	} else {
		sb.WriteString(truncateRightCells(singleLine(cmd), inner) + "\n")
	}
	if m.pendingApprovalReason != "" {
		reason := wrapText(i18n.T("tui.approvalReason")+": "+sanitizeDisplayText(m.pendingApprovalReason), inner)
		sb.WriteString("\n" + reason + "\n")
	}
	sb.WriteString("\n" + m.renderApprovalKeys(inner))
	return ApprovalBox.Width(inner+approvalBoxChrome).Render(sb.String()) + "\n\n"
}

// approvalBoxChrome is the horizontal cost of ApprovalBox beyond its content:
// 2 border cells plus 2x2 padding cells. lipgloss.Width sets content+padding
// (borders excluded), so box.Width = inner+4 yields an outer width of inner+6,
// exactly the chat column. Kept explicit so the width math and the style stay
// in one place.
const approvalBoxChrome = 4

// renderApprovalKeys lays out the approval shortcuts, wrapped to the box's
// inner width. Wrapping matters: lipgloss grows a box to fit its widest line,
// so an unwrapped key row would push ApprovalBox past the chat column and
// break the surrounding layout. The [v] label describes the action the key
// performs: while the full command is shown it offers the summary back, and
// vice versa.
func (m *Model) renderApprovalKeys(inner int) string {
	viewKey := i18n.T("tui.approvalViewFull")
	if m.approvalShowFull {
		viewKey = i18n.T("tui.approvalViewShort")
	}
	keys := []string{
		fmt.Sprintf("[y] %s", i18n.T("tui.approvalApprove")),
		fmt.Sprintf("[n] %s", i18n.T("tui.approvalReject")),
		fmt.Sprintf("[v] %s", viewKey),
		fmt.Sprintf("[w] %s", i18n.T("tui.approvalWhitelist")),
	}
	return wrapText(strings.Join(keys, "  "), inner)
}

// lastHistoryRole returns the role of the last non-system message in history.
func (m *Model) lastHistoryRole() string {
	history := m.historyView()
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "system" {
			return history[i].Role
		}
	}
	return ""
}

// renderGroupTurns renders all turns of a group chat, organized by layer with
// layer separators. Each turn is rendered as a labeled block with a distinct
// style that differentiates it from normal assistant messages.
func (m *Model) renderGroupTurns(turns []groupTurn, viewportWidth int) string {
	var sb strings.Builder
	prevLayer := -1

	for _, turn := range turns {
		// Insert layer separator when the layer changes
		if turn.layer != prevLayer {
			if prevLayer >= 0 {
				sb.WriteString("\n")
			}
			layerLabel := fmt.Sprintf(i18n.T("tui.group.layer"), turn.layer)
			sepText := fmt.Sprintf("── %s ──", layerLabel)
			sb.WriteString(GroupLayerSeparator.Width(viewportWidth-4).Render(sepText) + "\n")
			prevLayer = turn.layer
		}

		// Turn header: ┌ [label · Layer N · role]. label/speaker/role arrive
		// from group.turn event metadata (LLM-controlled): sanitize them so
		// control/bidi chars cannot desync paint vs measured width, and wrap
		// the composed header so an over-wide label cannot exceed the
		// viewport budget and be re-wrapped by lineViewport at render time
		// (the frame-corruption cascade). Continuation rows keep the "┌ "
		// column via space padding, same pattern as renderToolCallRow.
		headerLabel := sanitizeDisplayText(turn.label)
		if headerLabel == "" {
			headerLabel = sanitizeDisplayText(turn.speaker)
		}
		roleDisplay := sanitizeDisplayText(turn.role)
		if roleDisplay == "" {
			roleDisplay = "participant"
		}
		layerLabel := fmt.Sprintf(i18n.T("tui.group.layer"), turn.layer)
		const headerPrefix = "┌ "
		budget := viewportWidth - 4 - ansi.StringWidth(headerPrefix)
		if budget < 1 {
			budget = 1
		}
		body := fmt.Sprintf("[%s · %s · %s]", headerLabel, layerLabel, roleDisplay)
		for i, row := range strings.Split(wrapText(body, budget), "\n") {
			if i == 0 {
				sb.WriteString(GroupTurnHeader.Render(headerPrefix+row) + "\n")
			} else {
				sb.WriteString(GroupTurnHeader.Render(strings.Repeat(" ", ansi.StringWidth(headerPrefix))+row) + "\n")
			}
		}

		// Turn content with left border
		content := turn.content
		if content != "" {
			rendered := m.renderMarkdown(content, viewportWidth-8)
			sb.WriteString(GroupTurnBorder.Render(rendered) + "\n")
		}
	}

	// Render final synthesis if available
	if meta, ok := m.groupMeta[m.activeGroupID]; ok && meta.synthesis != "" {
		sb.WriteString("\n")
		synthLabel := i18n.T("tui.group.synthesis")
		sb.WriteString(GroupSynthesisLabel.Render("┌ "+synthLabel) + "\n")
		rendered := m.renderMarkdown(meta.synthesis, viewportWidth-8)
		sb.WriteString(GroupSynthesisBorder.Render(rendered) + "\n")
	}

	return sb.String()
}
