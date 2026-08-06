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
		m.renderedBase = ""
		m.renderedBaseKey = ""
		return
	}

	// Clear streaming state if the assistant message is fully saved in history
	m.cleanupStreamingIfComplete()

	// Determine if the rendered base cache is still valid.
	// Invalidated when session key, viewport width, or message count changes.
	// During streaming the history doesn't grow (the assistant message is
	// tracked via currentStream), so the cache can stay valid.
	historyMsgCount := m.getHistoryMessageCount()
	cacheKey := fmt.Sprintf("%s:%d:%d", m.currentKey, m.viewport.Width, historyMsgCount)
	cacheValid := m.renderedBaseKey == cacheKey

	if !cacheValid {
		// Rebuild the base content from session history
		m.renderedBase = m.buildRenderedHistory()
		m.renderedBaseKey = cacheKey
		m.renderedBaseMsgCount = historyMsgCount
	}

	// Fast path: nothing changed since the last render (same session, same
	// width, same message count, no streaming/feedback state). Reuse the
	// already-rendered viewport content — this avoids re-running glamour
	// markdown rendering and wrapText over the whole history on every frame
	// of the loading animation, mouse move, or cursor blink. This is the
	// dominant cost for long (>400k token) conversations.
	hasStreaming := m.processing && (m.currentStream != "" || m.currentThinking != "" || m.currentToolAction != "")
	if cacheValid && !hasStreaming &&
		m.pendingUserMessage == "" && m.pendingApprovalID == "" && m.approvalResult == "" &&
		m.activeGroupID == "" && m.compactFeedback == "" && m.goalFeedback == "" {
		if !m.selecting {
			return
		}
		// While selecting we need the updated content for highlight; the base
		// is cached so only the overlay pass runs.
	}

	var sb strings.Builder
	sb.WriteString(m.renderedBase)
	lastRole := m.lastHistoryRole()

	// Show pending user message immediately (before agent responds)
	if m.pendingUserMessage != "" {
		history := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)
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
			sb.WriteString(UserRoleStyle.Render(i18n.T("tui.you")) + "\n")
			sb.WriteString(UserMessageStyle.Render(wrapText(m.pendingUserMessage, m.viewport.Width-4)) + "\n\n")
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
			sb.WriteString(AssistantRoleStyle.Render(agentName) + "\n")
		}

		if m.currentThinking != "" {
			rendered := m.getRenderedThinking(m.viewport.Width - 8)
			sb.WriteString(ThinkingContentStyle.Render(rendered) + "\n")
		}
		if m.currentStream != "" {
			rendered := m.getRenderedStream(m.viewport.Width - 6)
			sb.WriteString(rendered + "\n")
		}
		// Show the currently executing tool call (cleared when stream resumes or completes)
		if m.currentToolAction != "" {
			sb.WriteString(ToolCallLabel.Render("  ") + ToolCallName.Render(m.currentToolAction) + "\n")
		}
		sb.WriteString("\n")
	}

	// Show pending command approval prompt
	if m.pendingApprovalID != "" {
		sb.WriteString(m.renderApprovalPrompt())
	}

	// Show brief approval result feedback (after user decision, before tool result)
	if m.approvalResult != "" {
		sb.WriteString(m.approvalResult + "\n\n")
	}

	// Show group chat turns (Mixture of Agents) when a group is active
	if m.activeGroupID != "" {
		if turns, ok := m.groupTranscripts[m.activeGroupID]; ok && len(turns) > 0 {
			sb.WriteString(m.renderGroupTurns(turns, m.viewport.Width))
			sb.WriteString("\n")
		}
	}

	// Show compaction result feedback
	if m.compactFeedback != "" {
		sb.WriteString(m.compactFeedback + "\n\n")
	}

	// Show /goal command feedback
	if m.goalFeedback != "" {
		sb.WriteString(m.goalFeedback + "\n\n")
	}

	// Check if viewport is at bottom BEFORE updating content.
	// This preserves the user's scroll position when they've scrolled up.
	// forceGotoBottom overrides this when switching sessions or creating a new chat.
	wasAtBottom := m.viewport.AtBottom() || m.forceGotoBottom
	m.forceGotoBottom = false

	m.viewport.SetContent(sb.String())
	if wasAtBottom && sb.Len() > 0 && m.viewport.Height > 0 {
		m.viewport.GotoBottom()
	}
}

// buildRenderedHistory renders completed messages from session history.
// For long conversations, it only renders the most recent maxRenderedMessages
// messages to bound memory usage. The result is cached in renderedBase.
func (m *Model) buildRenderedHistory() string {
	history := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)

	totalMsgs := len(history)

	// Virtualized rendering: only render the most recent N messages
	// when the conversation is very long. This prevents the renderedBase
	// string from growing unbounded with ANSI-heavy content.
	startIdx := 0
	if m.maxRenderedMessages > 0 && totalMsgs > m.maxRenderedMessages {
		startIdx = totalMsgs - m.maxRenderedMessages
	}

	m.renderedMsgStartIdx = startIdx
	m.renderedMsgEndIdx = totalMsgs

	var sb strings.Builder
	if startIdx > 0 {
		sb.WriteString(CommentColorStyle.Render(fmt.Sprintf("  ↑ %d earlier messages (scroll up in session history to view)\n\n", startIdx)))
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

		if msg.Role == "user" {
			sb.WriteString(UserRoleStyle.Render(i18n.T("tui.you")) + "\n")
			sb.WriteString(UserMessageStyle.Render(wrapText(msg.Content, m.viewport.Width-4)) + "\n\n")
		} else if msg.Role == "assistant" {
			// Only show agent name when coming from user (start of a turn)
			if lastRole == "" || lastRole == "user" || lastRole == "system" {
				agentID := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
				agentInfo, ok := m.agentLoop.GetProvidable().GetAgentInfo(agentID)
				agentName := agentID
				if ok && agentInfo.Name != "" {
					agentName = agentInfo.Name
				}
				sb.WriteString(AssistantRoleStyle.Render(agentName) + "\n")
			}

			if msg.ReasoningContent != "" {
				rendered := m.renderMarkdown(msg.ReasoningContent, m.viewport.Width-8)
				sb.WriteString(ThinkingContentStyle.Render(rendered) + "\n")
			}

			if msg.Content != "" {
				rendered := m.renderMarkdown(msg.Content, m.viewport.Width-6)
				sb.WriteString(rendered + "\n")
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
				sb.WriteString(ToolCallLabel.Render("  ") + ToolCallName.Render(line) + "\n")
			}
			sb.WriteString("\n")
		} else if msg.Role == "tool" {
			summary := truncateToolResult(msg.Content, 150)
			sb.WriteString(ToolResultLabel.Render("  → ") + ToolResultBox.Render(summary) + "\n")
		}
		// Skip system messages — they are internal prompts, not user-facing
		lastRole = msg.Role
	}

	return sb.String()
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
