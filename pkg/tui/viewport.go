package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// tokenCacheTTL bounds how often the expensive token/context usage is
// recomputed for the sidebar. The value is refreshed immediately whenever the
// history message count changes, so this TTL only throttles recomputation
// during idle renders (cursor blink, mouse moves) within a single turn.
const tokenCacheTTL = 2 * time.Second

// getTokenUsage returns cached token/context usage for the sidebar, refreshing
// the underlying (expensive) backend calls at most once per tokenCacheTTL or
// when the history message count changes. This keeps View() cheap: previously
// GetCurrentContextUsage ran on every render, rebuilding the system prompt from
// disk and estimating tokens over the full history each time.
func (m *Model) getTokenUsage() (current, window, cumInput, cumOutput int) {
	if m.currentKey == "" {
		return 0, 0, 0, 0
	}

	msgCount := m.getHistoryMessageCount()
	cacheKey := fmt.Sprintf("%s:%d", m.currentKey, msgCount)

	if m.tokenCacheKey == cacheKey && time.Since(m.tokenCacheTime) < tokenCacheTTL {
		return m.tokenCacheCurrent, m.tokenCacheWindow, m.tokenCacheCumInput, m.tokenCacheCumOutput
	}

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

func (m *Model) updateViewport() {
	if m.currentKey == "" {
		m.viewport.SetContent("")
		m.renderedBaseValid = false
		m.renderedBaseKey = ""
		return
	}

	// Fetch history ONCE for this entire render cycle. GetHistoryView returns
	// a read-only reference (no copy), so this is cheap — but calling it once
	// instead of 4+ times avoids redundant mutex acquisitions.
	history := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)

	// Clear streaming state if the assistant message is fully saved in history
	m.cleanupStreamingIfCompleteWithHistory(history)

	// Compute message count from the already-fetched history (avoids another
	// GetHistoryView call inside getHistoryMessageCount).
	historyMsgCount := countHistoryMessages(history)

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
			m.msgRenderCacheLines = nil
			m.msgRenderCacheWidth = m.viewport.Width
		}
	}

	// Rebuild base if cache is invalid OR message count changed OR the last
	// message transitioned from Streaming=true to Streaming=false (the count
	// doesn't change but the rendered content does — the streaming message
	// was skipped during processing and must now be included).
	lastMsgStreaming := len(history) > 0 && history[len(history)-1].Streaming
	if !cacheValid || m.renderedBaseMsgCount != historyMsgCount || (m.renderedBaseLastStreaming && !lastMsgStreaming) {
		baseLines := m.buildRenderedHistoryLines(history)
		m.renderedBaseKey = widthCacheKey
		m.renderedBaseMsgCount = historyMsgCount
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
		m.compactFeedback != "" || m.goalFeedback != ""

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
		if m.forceGotoBottom || (m.viewport.AtBottom() && m.viewport.totalLines() > 0 && m.viewport.Height > 0) {
			m.forceGotoBottom = false
			m.viewport.GotoBottom()
		}
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
			overlaySb.WriteString(UserRoleStyle.Render(i18n.T("tui.you")) + "\n")
			overlaySb.WriteString(UserMessageStyle.Render(wrapText(m.pendingUserMessage, m.viewport.Width-4)) + "\n\n")
			lastRole = "user"
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
			overlaySb.WriteString(ToolCallLabel.Render("  ") + ToolCallName.Render(m.currentToolAction) + "\n")
		}
		overlaySb.WriteString("\n")
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

	// Show compaction result feedback
	if m.compactFeedback != "" {
		overlaySb.WriteString(m.compactFeedback + "\n\n")
	}

	// Show /goal command feedback
	if m.goalFeedback != "" {
		overlaySb.WriteString(m.goalFeedback + "\n\n")
	}

	// Check if viewport is at bottom BEFORE updating overlay.
	// This preserves the user's scroll position when they've scrolled up.
	// forceGotoBottom overrides this when switching sessions or creating a new chat.
	wasAtBottom := m.viewport.AtBottom() || m.forceGotoBottom
	m.forceGotoBottom = false

	// Push overlay lines to the viewport. SetOverlayLines is O(overlay_lines)
	// and does NOT trigger the expensive base-line Split/findLongestLineWidth.
	overlayContent := overlaySb.String()
	if overlayContent != "" {
		overlayLines := strings.Split(strings.ReplaceAll(overlayContent, "\r\n", "\n"), "\n")
		m.viewport.SetOverlayLines(overlayLines)
	} else {
		m.viewport.SetOverlayLines(nil)
	}

	if wasAtBottom && m.viewport.totalLines() > 0 && m.viewport.Height > 0 {
		m.viewport.GotoBottom()
	}
}

// countHistoryMessages counts user+assistant messages in the given history slice.
// This is the pure-function version of getHistoryMessageCount that accepts the
// history directly, avoiding a redundant GetHistoryView call.
func countHistoryMessages(history []providers.Message) int {
	count := 0
	for _, msg := range history {
		if msg.Role == "user" || msg.Role == "assistant" {
			count++
		}
	}
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

// buildRenderedHistoryLines renders completed messages from the given
// history slice using a per-message render cache. Returns []string lines
// directly instead of a concatenated string — this avoids the O(n) string
// concatenation + strings.Split roundtrip that the old string-based approach
// required. Only new/changed messages go through glamour (O(k) where k = new).
func (m *Model) buildRenderedHistoryLines(history []providers.Message) []string {
	totalMsgs := len(history)

	// Virtualized rendering: only render the most recent N messages
	// when the conversation is very long.
	startIdx := 0
	if m.maxRenderedMessages > 0 && totalMsgs > m.maxRenderedMessages {
		startIdx = totalMsgs - m.maxRenderedMessages
	}

	m.renderedMsgStartIdx = startIdx
	m.renderedMsgEndIdx = totalMsgs

	// Lazily initialize per-message render cache (stores []string lines)
	if m.msgRenderCacheLines == nil {
		m.msgRenderCacheLines = make(map[string][]string, m.maxRenderedMessages)
		m.msgRenderCacheWidth = m.viewport.Width
	}

	// Pre-allocate result with a reasonable capacity estimate.
	result := make([]string, 0, min(totalMsgs-startIdx, m.maxRenderedMessages)*8)

	if startIdx > 0 {
		header := CommentColorStyle.Render(fmt.Sprintf("  ↑ %d earlier messages (scroll up in session history to view)", startIdx))
		result = append(result, header, "")
	}

	lastRole := ""
	for i := startIdx; i < totalMsgs; i++ {
		msg := history[i]

		// Skip internal context-compaction summaries
		if isCompactionSummary(msg) {
			continue
		}

		// Skip the last message if it's a streaming assistant message during
		// processing — the TUI is already rendering the live stream via currentStream.
		if m.processing && msg.Role == "assistant" && msg.Streaming && i == totalMsgs-1 {
			continue
		}

		// Compute fingerprint for per-message cache
		fp := messageFingerprint(msg, m.viewport.Width)
		if cachedLines, ok := m.msgRenderCacheLines[fp]; ok {
			result = append(result, cachedLines...)
			lastRole = msg.Role
			continue
		}

		// Cache miss — render the message and store in cache
		var msgSb strings.Builder
		if msg.Role == "user" {
			msgSb.WriteString(UserRoleStyle.Render(i18n.T("tui.you")) + "\n")
			msgSb.WriteString(UserMessageStyle.Render(wrapText(msg.Content, m.viewport.Width-4)) + "\n\n")
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

			// Render tool calls from assistant message (compact: tool_name: params)
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
				msgSb.WriteString(ToolCallLabel.Render("  ") + ToolCallName.Render(line) + "\n")
			}
			msgSb.WriteString("\n")
		} else if msg.Role == "tool" {
			summary := truncateToolResult(msg.Content, 150)
			msgSb.WriteString(ToolResultLabel.Render("  → ") + ToolResultBox.Render(summary) + "\n")
		}
		// Skip system messages — they are internal prompts, not user-facing

		rendered := msgSb.String()
		// Split into lines once and cache the lines. This avoids re-splitting
		// on every frame when the viewport needs them.
		msgLines := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
		m.msgRenderCacheLines[fp] = msgLines // cache lines for fast assembly
		result = append(result, msgLines...)
		lastRole = msg.Role
	}

	return result
}

// buildRenderedHistory is the legacy entry point that fetches history internally.
// Kept for callers that don't have the history slice available.
func (m *Model) buildRenderedHistory() []string {
	history := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)
	return m.buildRenderedHistoryLines(history)
}

// renderApprovalPrompt builds the inline approval prompt shown in the viewport
// when a command requires user approval.
func (m *Model) renderApprovalPrompt() string {
	var sb strings.Builder
	sb.WriteString("⚠️  " + i18n.T("tui.approvalRequired") + "\n\n")
	sb.WriteString(fmt.Sprintf("%s\n", m.pendingApprovalCmd))
	if m.pendingApprovalReason != "" {
		sb.WriteString(fmt.Sprintf("\n%s: %s\n", i18n.T("tui.approvalReason"), m.pendingApprovalReason))
	}
	sb.WriteString(fmt.Sprintf("\n[y] %s  [n] %s", i18n.T("tui.approvalApprove"), i18n.T("tui.approvalReject")))
	return ApprovalBox.Render(sb.String()) + "\n\n"
}

// lastHistoryRole returns the role of the last non-system message in history.
func (m *Model) lastHistoryRole() string {
	history := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)
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

		// Turn header: ┌ [label · Layer N · role]
		headerLabel := turn.label
		if headerLabel == "" {
			headerLabel = turn.speaker
		}
		roleDisplay := turn.role
		if roleDisplay == "" {
			roleDisplay = "participant"
		}
		layerLabel := fmt.Sprintf(i18n.T("tui.group.layer"), turn.layer)
		headerText := fmt.Sprintf("┌ [%s · %s · %s]", headerLabel, layerLabel, roleDisplay)
		sb.WriteString(GroupTurnHeader.Render(headerText) + "\n")

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
