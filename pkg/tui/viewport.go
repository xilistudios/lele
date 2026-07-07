package tui

import (
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func (m *Model) updateViewport() {
	if m.currentKey == "" {
		m.viewport.SetContent("")
		m.renderedBase = ""
		m.renderedBaseKey = ""
		return
	}

	// Determine if the rendered base cache is still valid.
	// Invalidated when session key or viewport width changes.
	// During processing, always rebuild to pick up tool calls and other
	// history changes that arrive while the stream is still active.
	cacheKey := fmt.Sprintf("%s:%d", m.currentKey, m.viewport.Width)
	cacheValid := m.renderedBaseKey == cacheKey && !m.processing && !m.hasRunningSubagents()

	if !cacheValid {
		// Rebuild the base content from session history
		m.renderedBase = m.buildRenderedHistory()
		m.renderedBaseKey = cacheKey
	}

	var sb strings.Builder
	sb.WriteString(m.renderedBase)
	lastRole := m.lastHistoryRole()

	// Show pending user message immediately (before agent responds)
	if m.pendingUserMessage != "" {
		sb.WriteString(UserRoleStyle.Render(i18n.T("tui.you")) + "\n")
		sb.WriteString(UserMessageStyle.Render(wrapText(m.pendingUserMessage, m.viewport.Width-4)) + "\n\n")
		lastRole = "user"
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
			rendered := m.renderMarkdown(m.currentThinking, m.viewport.Width-8)
			sb.WriteString(ThinkingContentStyle.Render(rendered) + "\n")
		}
		if m.currentStream != "" {
			rendered := m.renderMarkdown(m.currentStream, m.viewport.Width-6)
			sb.WriteString(rendered + "\n")
		}
		// Show the currently executing tool call (cleared when stream resumes or completes)
		if m.currentToolAction != "" {
			sb.WriteString(ToolCallLabel.Render("  ") + ToolCallName.Render(m.currentToolAction) + "\n")
		}
		sb.WriteString("\n")
	}

	// Show real-time status of running subagents in the parent chat.
	// These updates arrive while the parent agent is NOT streaming (it's waiting
	// for the async spawn tool to return), so we display them unconditionally
	// whenever there is at least one active subagent with a known last action.
	if len(m.subagentProgress) > 0 {
		for taskID, action := range m.subagentProgress {
			if action == "" {
				continue
			}
			label := taskID + ": " + action
			sb.WriteString(SubagentProgressLabel.Render("  ⟳ ") + SubagentProgressStyle.Render(label) + "\n")
		}
		sb.WriteString("\n")
	}

	m.viewport.SetContent(sb.String())
	if sb.Len() > 0 && m.viewport.Height > 0 {
		m.viewport.GotoBottom()
	}
}

// buildRenderedHistory renders all completed messages from session history.
// The result is cached in renderedBase to avoid re-rendering on every streaming chunk.
func (m *Model) buildRenderedHistory() string {
	history := m.agentLoop.GetProvidable().GetSessionHistory(m.currentKey)

	var sb strings.Builder
	lastRole := ""
	for i, msg := range history {
		// Skip the last message if it's a streaming assistant message during
		// processing — the TUI is already rendering the live stream via currentStream.
		if m.processing && msg.Role == "assistant" && msg.Streaming && i == len(history)-1 {
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

// lastHistoryRole returns the role of the last non-system message in history.
func (m *Model) lastHistoryRole() string {
	history := m.agentLoop.GetProvidable().GetSessionHistory(m.currentKey)
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "system" {
			return history[i].Role
		}
	}
	return ""
}
