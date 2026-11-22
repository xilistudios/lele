package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func (m *Model) beginRenderFrame() {
	m.frameOpen = true
	m.frameValid = false
	m.frameView = nil
	m.frameKey = ""
}

func (m *Model) endRenderFrame() {
	m.frameOpen = false
	m.frameValid = false
	m.frameView = nil
	m.frameKey = ""
}

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
	m.frameHistoryTerms(history)
	m.frameValid = true
	return history
}

func (m *Model) frameHistoryTerms(history []providers.Message) {
	m.frameHistoryLen = len(history)
	m.frameLastStreaming = lastMessageStreaming(history)
}

func lastMessageStreaming(history []providers.Message) bool {
	return len(history) > 0 && history[len(history)-1].Streaming
}

func (m *Model) historyCount() int {
	if m.currentKey == "" {
		return 0
	}
	m.historyView()
	return m.frameCount
}

const tokenCacheTTL = 2 * time.Second

const lazyLoadBatchSize = 50

const maxHomeExpandIterations = 20

func (m *Model) getTokenUsage(msgCount int) (current, window, cumInput, cumOutput int) {
	if m.currentKey == "" {
		return 0, 0, 0, 0
	}

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

func isCompactionSummary(msg providers.Message) bool {
	return msg.Role == "user" &&
		(strings.HasPrefix(msg.Content, "## Summary of Previous Conversation\n\n") ||
			strings.HasPrefix(msg.Content, "[Context compacted"))
}

func userDisplayContent(msg providers.Message) string {
	if msg.DisplayContent != "" {
		return msg.DisplayContent
	}
	return msg.Content
}

func isPendingUserMessage(msg providers.Message, pending string) bool {
	return msg.Role == "user" && pending != "" && strings.TrimSpace(userDisplayContent(msg)) == pending
}

func (m *Model) updateViewport() {
	m.updateViewportWithHistory(m.historyView())
}

func (m *Model) updateViewportWithHistory(history []providers.Message) {
	if m.onViewportRebuild != nil {
		m.onViewportRebuild(m.frameOpen)
	}

	m.frameHistoryTerms(history)

	if m.currentKey == "" {
		m.viewport.SetContent("")
		m.renderedBaseValid = false
		m.renderedBaseKey = ""
		m.noteViewportMaterialized(0)
		return
	}

	wasAtBottom := m.viewport.AtBottom() || m.forceGotoBottom

	if m.renderWindowSessionKey != m.currentKey {
		m.renderWindowSessionKey = m.currentKey
		m.renderStartIdx = -1
	}

	m.cleanupStreamingIfCompleteWithHistory(history)

	residentCount := m.historyMessageCount(history)
	historyMsgCount := m.displayMessageCountFrom(residentCount)

	widthCacheKey := fmt.Sprintf("%s:%d", m.currentKey, m.viewport.Width)
	cacheValid := m.renderedBaseKey == widthCacheKey && m.renderedBaseValid

	if !cacheValid {
		if m.msgRenderCacheWidth != m.viewport.Width {
			m.msgRenderCacheLines = nil
			m.msgRenderCacheWidth = m.viewport.Width
		}
	}

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
		m.viewport.SetBaseLines(baseLines)
	}

	hasStreaming := m.processing && (m.currentStream != "" || m.currentThinking != "" || m.currentToolAction != "")
	hasOverlay := hasStreaming ||
		m.pendingUserMessage != "" ||
		m.pendingApprovalID != "" || m.approvalResult != "" ||
		m.activeGroupID != "" ||
		m.compactFeedback != "" ||
		m.statusFeedback != "" ||
		m.hasSubagentProgressOverlay()

	if !hasOverlay && !m.selecting {
		if len(m.viewport.overlayLines) > 0 {
			m.viewport.SetOverlayLines(nil)
		}
		if m.forceGotoBottom || (wasAtBottom && m.viewport.totalLines() > 0 && m.viewport.Height > 0) {
			m.forceGotoBottom = false
			m.viewport.GotoBottom()
		}
		m.noteViewportMaterialized(residentCount)
		return
	}

	var overlaySb strings.Builder
	lastRole := lastHistoryRoleFromHistory(history)

	if m.pendingUserMessage != "" {
		alreadyInHistory := false
		for i := len(history) - 1; i >= 0; i-- {
			if isPendingUserMessage(history[i], m.pendingUserMessage) {
				alreadyInHistory = true
				break
			}
			if len(history)-i > 10 {
				break
			}
		}
		if !alreadyInHistory {
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
		if m.currentToolAction != "" {
			overlaySb.WriteString(renderToolCallRow(sanitizeDisplayText(m.currentToolAction), m.viewport.Width) + "\n")
		}
		overlaySb.WriteString("\n")
	}

	if progressBlock := m.renderSubagentProgress(); progressBlock != "" {
		overlaySb.WriteString(progressBlock + "\n")
	}

	if m.pendingApprovalID != "" {
		overlaySb.WriteString(m.renderApprovalPrompt())
	}

	if m.approvalResult != "" {
		overlaySb.WriteString(m.approvalResult + "\n\n")
	}

	if m.activeGroupID != "" {
		if turns, ok := m.groupTranscripts[m.activeGroupID]; ok && len(turns) > 0 {
			overlaySb.WriteString(m.renderGroupTurns(turns, m.viewport.Width))
			overlaySb.WriteString("\n")
		}
	}

	if m.compactFeedback != "" {
		overlaySb.WriteString(sanitizeDisplayText(m.compactFeedback) + "\n\n")
	}

	if m.statusFeedback != "" {
		overlaySb.WriteString(m.statusFeedback + "\n\n")
	}

	m.forceGotoBottom = false

	overlayContent := overlaySb.String()
	if overlayContent != "" {
		overlayLines := strings.Split(strings.ReplaceAll(overlayContent, "\r\n", "\n"), "\n")
		mergeLines(overlayLines)
		m.viewport.SetOverlayLines(overlayLines)
	} else {
		m.viewport.SetOverlayLines(nil)
	}

	if wasAtBottom && m.viewport.totalLines() > 0 && m.viewport.Height > 0 {
		m.viewport.GotoBottom()
	}

	m.noteViewportMaterialized(residentCount)
}

func (m *Model) syncViewportForFrame(history []providers.Message, msgCount int) {
	if m.viewportContentUpToDate(msgCount) {
		return
	}
	if !m.viewportGeometryChanged() && m.viewportRebuildDeferred() {
		return
	}
	m.updateViewportWithHistory(history)
}

func (m *Model) viewportContentUpToDate(msgCount int) bool {
	if m.lastViewportKey == "" {
		return false
	}
	if !m.renderedBaseValid {
		return false
	}
	return m.lastViewportKey == m.viewportContentKey(msgCount)
}

func (m *Model) viewportGeometryChanged() bool {
	return m.viewportBuiltWidth != m.viewport.Width || m.viewportBuiltHeight != m.viewport.Height
}

func (m *Model) viewportRebuildDeferred() bool {
	if !m.streamPendingUpdate || m.streamThrottleInterval <= 0 {
		return false
	}
	return time.Since(m.viewportBuiltAt) < m.streamThrottleInterval
}

func (m *Model) noteViewportMaterialized(residentCount int) {
	m.lastViewportKey = m.viewportContentKey(residentCount)
	m.viewportBuiltWidth = m.viewport.Width
	m.viewportBuiltHeight = m.viewport.Height
	m.viewportBuiltAt = time.Now()
	m.streamPendingUpdate = false
}

func (m *Model) viewportContentKey(msgCount int) string {
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
		m.archivedCacheKey(),
	)
	extra := fmt.Sprintf("|%d|%d|%d|%v|%v|%v|%s|%v|%d|%d|%d|%d|%d|%v|%v",
		m.viewport.Height,
		m.frameHistoryLen,
		m.maxRenderedMessages,
		m.selecting,
		m.frameLastStreaming,
		m.forceGotoBottom,
		m.pendingApprovalCmd,
		m.approvalShowFull,
		len(m.groupTranscripts[m.activeGroupID]),
		len(m.groupMeta[m.activeGroupID].synthesis),
		int(m.modalMode),
		len(m.subagentProgress),
		m.subagentProgressDigest(),
		m.showWelcome,
		m.onboardingActive,
	)
	return content + extra
}

func (m *Model) subagentProgressDigest() uint64 {
	var digest uint64
	for id, action := range m.subagentProgress {
		h := fnv64aWriteField(fnv64aWriteField(fnv64aOffset, id), action)
		digest ^= h
	}
	return digest
}

func (m *Model) maybeExpandRenderWindow() bool {
	if m.currentKey == "" || !m.viewport.AtTop() {
		return false
	}
	if m.renderStartIdx < 0 {
		return false
	}

	oldTotal := m.viewport.totalLines()
	expanded := false

	if m.renderStartIdx > 0 {
		newStart := m.renderStartIdx - lazyLoadBatchSize
		if newStart < 0 {
			newStart = 0
		}
		m.renderStartIdx = newStart
		expanded = true
	} else if m.archivedHasOlder || m.archivedHiddenCount() > 0 {
		expanded = m.loadOlderArchivedPage() > 0
	}

	if !expanded {
		return false
	}

	m.renderedBaseValid = false
	m.renderedBaseKey = ""
	m.renderedBaseMsgCount = -1
	m.renderedBaseHistoryLen = -1

	m.updateViewport()

	if delta := m.viewport.totalLines() - oldTotal; delta > 0 {
		m.viewport.YOffset = delta
		m.viewport.clampOffset()
	}
	return true
}

func countHistoryMessages(history []providers.Message) int {
	count := 0
	for _, msg := range history {
		if msg.Role == "user" || msg.Role == "assistant" {
			count++
		}
	}
	return count
}

func (m *Model) historyMessageCount(history []providers.Message) int {
	if len(history) == 0 {
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

func lastHistoryRoleFromHistory(history []providers.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "system" {
			return history[i].Role
		}
	}
	return ""
}

func (m *Model) defaultRenderStartIdx(msgCount int) int {
	if m.maxRenderedMessages <= 0 {
		return 0
	}
	if msgCount <= m.maxRenderedMessages {
		return 0
	}
	return msgCount - m.maxRenderedMessages
}

func (m *Model) buildRenderedHistoryLines(history []providers.Message) []string {
	archived := m.archivedForCurrentSession()
	nArchived := len(archived)
	totalMsgs := nArchived + len(history)

	startIdx := m.renderStartIdx
	if startIdx < 0 || startIdx >= totalMsgs {
		startIdx = m.defaultRenderStartIdx(totalMsgs)
		m.renderStartIdx = startIdx
	}

	m.renderedMsgStartIdx = startIdx
	m.renderedMsgEndIdx = totalMsgs

	if m.msgRenderCacheLines == nil {
		m.msgRenderCacheLines = make(map[string][]string, m.maxRenderedMessages)
		m.msgRenderCacheWidth = m.viewport.Width
	}

	liveCache := make(map[string][]string, totalMsgs-startIdx)

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

		if isCompactionSummary(msg) {
			continue
		}

		if m.processing && msg.Role == "assistant" && msg.Streaming && i == totalMsgs-1 {
			continue
		}

		isExecutingMessage := m.currentToolAction != "" && m.processing && msg.Role == "assistant" && i == totalMsgs-1

		fp := messageFingerprint(msg, m.viewport.Width)
		if cachedLines, ok := m.msgRenderCacheLines[fp]; ok {
			liveCache[fp] = cachedLines
			result = append(result, cachedLines...)
			lastRole = msg.Role
			continue
		}

		var msgSb strings.Builder
		if msg.Role == "user" {
			if sender, body, isNotice := parseSystemNotice(msg.Content); isNotice {
				msgSb.WriteString(renderSystemNotice(sender, body, m.viewport.Width) + "\n\n")
			} else {
				msgSb.WriteString(UserRoleStyle.Render(i18n.T("tui.you")) + "\n")
				msgSb.WriteString(UserMessageStyle.Render(wrapText(sanitizeDisplayText(userDisplayContent(msg)), m.viewport.Width-4)) + "\n\n")
			}
		} else if msg.Role == "assistant" {
			if msg.ErrorDetail != "" {
				msgSb.WriteString(renderErrorBox(msg.ErrorDetail, m.viewport.Width) + "\n\n")
			} else {
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
			}
		} else if msg.Role == "tool" {
			summary := truncateToolResult(msg.Content, 150)
			msgSb.WriteString(renderToolResultBlock(summary, m.viewport.Width) + "\n")
		}

		rendered := msgSb.String()
		msgLines := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
		mergeLines(msgLines)
		if !isExecutingMessage {
			liveCache[fp] = msgLines
		}
		result = append(result, msgLines...)
		lastRole = msg.Role
	}

	m.msgRenderCacheLines = liveCache

	return result
}

func (m *Model) buildRenderedHistory() []string {
	history := m.historyView()
	return m.buildRenderedHistoryLines(history)
}

func (m *Model) approvalInnerWidth() int {
	w := m.chatColumnWidth() - 6
	if w < approvalMinInnerWidth {
		return approvalMinInnerWidth
	}
	return w
}

func (m *Model) chatColumnWidth() int {
	leftWidth := int(float64(m.width) * leftColumnRatio)
	if w := leftWidth - 2; w > 0 {
		return w
	}
	return m.viewport.Width
}

const approvalMinInnerWidth = 20

func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

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

const approvalBoxChrome = 4

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

func (m *Model) lastHistoryRole() string {
	history := m.historyView()
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "system" {
			return history[i].Role
		}
	}
	return ""
}

func (m *Model) renderGroupTurns(turns []groupTurn, viewportWidth int) string {
	var sb strings.Builder
	prevLayer := -1

	for _, turn := range turns {
		if turn.layer != prevLayer {
			if prevLayer >= 0 {
				sb.WriteString("\n")
			}
			layerLabel := fmt.Sprintf(i18n.T("tui.group.layer"), turn.layer)
			sepText := fmt.Sprintf("── %s ──", layerLabel)
			sb.WriteString(GroupLayerSeparator.Width(viewportWidth-4).Render(sepText) + "\n")
			prevLayer = turn.layer
		}

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

		content := turn.content
		if content != "" {
			rendered := m.renderMarkdown(content, viewportWidth-8)
			sb.WriteString(GroupTurnBorder.Render(rendered) + "\n")
		}
	}

	if meta, ok := m.groupMeta[m.activeGroupID]; ok && meta.synthesis != "" {
		sb.WriteString("\n")
		synthLabel := i18n.T("tui.group.synthesis")
		sb.WriteString(GroupSynthesisLabel.Render("┌ "+synthLabel) + "\n")
		rendered := m.renderMarkdown(meta.synthesis, viewportWidth-8)
		sb.WriteString(GroupSynthesisBorder.Render(rendered) + "\n")
	}

	return sb.String()
}
