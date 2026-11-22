package tui

import (
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/tui/i18n"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return i18n.T("tui.initializing")
	}

	m.syncTextInputEcho()

	m.beginRenderFrame()
	defer m.endRenderFrame()

	m.hasBackToParentClick = false

	if m.showWelcome {
		m.noteFrameLayout(m.width, m.height, 0, 0, 0)
		return m.renderWelcome()
	}
	return m.renderChatLayout()
}

func (m *Model) renderWelcome() string {
	if m.onboardingActive {
		return m.paintFrame(m.renderOnboarding())
	}

	var contentBuilder strings.Builder

	logo := "  _      ______ _      ______\n" +
		" | |    |  ____| |    |  ____|\n" +
		" | |    | |__  | |    | |__   \n" +
		" | |    |  __| | |    |  __|\n" +
		" | |____| |____| |____| |____\n" +
		" |______|______|______|______|"
	contentBuilder.WriteString(WelcomeLogo.Render(logo) + "\n\n")

	if m.showAutocomplete && len(m.autocompleteItems) > 0 {
		contentBuilder.WriteString(m.renderAutocompleteBlock(60) + "\n")
	}

	contentBuilder.WriteString(InputBarContainer.Width(60).Render(m.chatInput.View()) + "\n\n")

	agentID, modelName := m.resolveWelcomeAgentModel()
	selectorLine := fmt.Sprintf("%s %s  %s %s",
		ModelSelectorLabel.Render(i18n.T("tui.model")),
		ModelSelectorStyle.Render(modelName),
		ModelSelectorLabel.Render(i18n.T("tui.agent")),
		ModelSelectorStyle.Render(agentID),
	)
	contentBuilder.WriteString(selectorLine + "\n")

	contentBuilder.WriteString(m.renderModeTabs() + "\n")

	if m.currentMode == ModeGroup {
		contentBuilder.WriteString(m.renderGroupProfileSelector())
	}

	contentBuilder.WriteString(WelcomeTip.Render(i18n.T("tui.typeMessage")) + "\n")
	contentBuilder.WriteString(WelcomeTip.Render(i18n.T("tui.tabHint")) + "\n")

	if m.modalMode != ModalNone {
		return m.renderActiveModal()
	}

	return m.paintFrame(contentBuilder.String())
}

func (m *Model) resolveWelcomeAgentModel() (agentID, modelName string) {
	if m.currentKey != "" {
		agentID = m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
		modelName = m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
	} else {
		agentID = m.agentLoop.GetProvidable().GetDefaultAgentID()
		if m.pendingModel != "" {
			modelName = m.pendingModel
		}
		if m.pendingAgent != "" {
			agentID = m.pendingAgent
		}
	}
	if modelName == "" {
		if agentInfo, ok := m.agentLoop.GetProvidable().GetAgentInfo(agentID); ok {
			modelName = agentInfo.Model
		}
	}
	return
}

func (m *Model) renderModeTabs() string {
	modeTabChat := i18n.T("tui.modeChat")
	modeTabAgent := i18n.T("tui.modeAgent")
	modeTabGroup := i18n.T("tui.modeGroup")

	if m.cfg.Groups.Enabled {
		switch m.currentMode {
		case ModeChat:
			return fmt.Sprintf("%s   %s   %s",
				ModelSelectorStyle.Render(modeTabChat),
				ModelSelectorLabel.Render(modeTabAgent),
				ModelSelectorLabel.Render(modeTabGroup),
			)
		case ModeGroup:
			return fmt.Sprintf("%s   %s   %s",
				ModelSelectorLabel.Render(modeTabChat),
				ModelSelectorLabel.Render(modeTabAgent),
				ModelSelectorStyle.Render(modeTabGroup),
			)
		default:
			return fmt.Sprintf("%s   %s   %s",
				ModelSelectorLabel.Render(modeTabChat),
				ModelSelectorStyle.Render(modeTabAgent),
				ModelSelectorLabel.Render(modeTabGroup),
			)
		}
	}

	switch m.currentMode {
	case ModeChat:
		return fmt.Sprintf("%s   %s",
			ModelSelectorStyle.Render(modeTabChat),
			ModelSelectorLabel.Render(modeTabAgent),
		)
	default:
		return fmt.Sprintf("%s   %s",
			ModelSelectorLabel.Render(modeTabChat),
			ModelSelectorStyle.Render(modeTabAgent),
		)
	}
}

func (m *Model) renderGroupProfileSelector() string {
	var sb strings.Builder
	profiles := m.getGroupProfiles()
	if len(profiles) > 0 {
		sb.WriteString("\n")
		sb.WriteString(ModelSelectorLabel.Render(i18n.T("tui.groupSelectProfile")) + "\n")
		for i, p := range profiles {
			line := fmt.Sprintf("%s (%s, %d agents)", p.ID, p.Strategy, len(p.Participants))
			if i == m.groupProfileIdx {
				sb.WriteString(ModelSelectorStyle.Render("> "+line) + "\n")
			} else {
				sb.WriteString(ModelSelectorLabel.Render("  "+line) + "\n")
			}
		}
		sb.WriteString(HelpStyle.Render(i18n.T("tui.groupTaskPlaceholder")) + "\n")
	} else {
		sb.WriteString("\n")
		sb.WriteString(CommentColorStyle.Render(i18n.T("tui.noGroupProfiles")) + "\n")
	}
	return sb.String()
}

func (m *Model) renderAutocompleteBlock(width int) string {
	var sb strings.Builder
	for i, cmd := range m.autocompleteItems {
		line := fmt.Sprintf("%-12s %s", cmd.name, cmd.description)
		if i == m.autocompleteIdx {
			sb.WriteString(ModalItemActive.Render(line) + "\n")
		} else {
			sb.WriteString(ModalItemInactive.Render(line) + "\n")
		}
	}
	return ModalContainer.Width(width).Render(sb.String())
}

func (m *Model) renderChatLayout() string {
	leftWidth := int(float64(m.width) * leftColumnRatio)
	rightWidth := m.width - leftWidth - chatSidebarGutter - 3
	if rightWidth < 1 {
		rightWidth = 1
	}
	contentHeight := m.height

	history := m.historyView()
	msgCount := m.historyCount()

	agentID := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
	modelName := m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
	thinkLevel := m.agentLoop.GetProvidable().GetEffectiveThinkLevel(m.currentKey)

	statusLine := m.renderStatusLine(leftWidth)

	queueRow := m.queueRowLine(leftWidth - 2)

	var autocompleteView string
	if m.showAutocomplete && len(m.autocompleteItems) > 0 {
		autocompleteView = m.renderAutocompleteBlock(leftWidth - 4)
	}

	var statusLineRendered string
	if contentHeight < 20 {
		statusLineRendered = lipgloss.NewStyle().Foreground(CommentColor).Render(statusLine)
	} else {
		statusLineRendered = StatusLineStyle.Render(statusLine)
	}

	var queueRowRendered string
	if queueRow != "" {
		queueRowRendered = QueueRowStyle.Render(queueRow)
	}

	m.chatInput.SetWidth(leftWidth - 4)
	var inputBar string
	if contentHeight < 16 {
		inputBar = lipgloss.NewStyle().
			Background(InputBgColor).
			Padding(0, 1).
			Width(leftWidth - 2).
			Render(m.chatInput.View())
	} else {
		inputBar = InputBarContainer.Width(leftWidth - 2).Render(m.chatInput.View())
	}

	currentTokens, contextWindow, _, _ := m.getTokenUsage(msgCount)

	pct := 0.0
	if contextWindow > 0 {
		pct = float64(currentTokens) / float64(contextWindow) * 100
	}
	tokensText := fmt.Sprintf("%d (%.1f%%)", currentTokens, pct)
	modeBadge := fmt.Sprintf("[%s]", strings.ToUpper(m.currentMode.String()))

	availWidth := leftWidth - 2
	if availWidth < 10 {
		availWidth = 10
	}
	tokensWidth := lipgloss.Width(tokensText)
	availRight := tokensWidth
	if availRight > availWidth/2 {
		availRight = availWidth / 2
	}
	availLeft := availWidth - availRight
	if availLeft < 10 {
		availLeft = 10
	}

	leftInfoRaw := fmt.Sprintf("%s · %s · %s", agentID, modelName, thinkLevel)
	modeBadgeRendered := ModelSelectorStyle.Render(modeBadge)
	badgeWidth := lipgloss.Width(modeBadgeRendered) + 1
	leftTextBudget := availLeft - badgeWidth
	if leftTextBudget < 5 {
		leftTextBudget = 5
	}
	if lipgloss.Width(leftInfoRaw) > leftTextBudget {
		r := []rune(leftInfoRaw)
		if leftTextBudget > 3 && len(r) > leftTextBudget {
			leftInfoRaw = string(r[:leftTextBudget-3]) + "..."
		} else if len(r) > leftTextBudget {
			leftInfoRaw = string(r[:leftTextBudget])
		}
	}
	leftBarText := fmt.Sprintf("%s %s", modeBadgeRendered, leftInfoRaw)

	bottomBar := lipgloss.JoinHorizontal(lipgloss.Top,
		BottomBarLeft.Width(availLeft).MaxWidth(availLeft).Render(leftBarText),
		BottomBarRight.Width(availRight).MaxWidth(availRight).Align(lipgloss.Right).Render(tokensText),
	)

	autocompleteHeight := 0
	if autocompleteView != "" {
		autocompleteHeight = lipgloss.Height(autocompleteView)
	}
	queueRowHeight := 0
	if queueRowRendered != "" {
		queueRowHeight = lipgloss.Height(queueRowRendered)
	}
	m.viewport.Width = leftWidth - 2
	m.viewport.Height = calculateViewportHeight(
		contentHeight,
		lipgloss.Height(statusLineRendered),
		queueRowHeight,
		autocompleteHeight,
		lipgloss.Height(inputBar),
		lipgloss.Height(bottomBar),
	)
	m.noteFrameLayout(m.width, m.height, m.viewport.Height, queueRowHeight, autocompleteHeight)
	m.syncViewportForFrame(history, msgCount)

	leftPane := m.renderLeftColumn(leftWidth, contentHeight, statusLineRendered, queueRowRendered, autocompleteView, inputBar, bottomBar)

	rightPane := m.renderRightColumn(rightWidth, contentWidth(rightWidth), contentHeight, msgCount)

	leftPane = clampPaneLines(leftPane, leftWidth)
	rightPane = clampPaneLines(rightPane, rightWidth+1)

	gutterPane := lipgloss.NewStyle().
		Width(chatSidebarGutter).
		Height(contentHeight).
		MaxHeight(contentHeight).
		Background(BgColor).
		Render("")

	mainLayout := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, gutterPane, rightPane)

	if m.modalMode != ModalNone {
		return m.renderActiveModal()
	}

	return m.paintFrame(mainLayout)
}

func contentWidth(rightWidth int) int {
	cw := rightWidth - 4
	if cw < 1 {
		return 1
	}
	return cw
}

func (m *Model) renderStatusLine(leftWidth int) string {
	isProcessing := m.isSessionProcessing()

	var statusLine string
	if m.selectionFeedback {
		statusLine = i18n.T("tui.selectionCopied")
	} else if m.selecting {
		statusLine = i18n.T("tui.selecting")
	} else if m.parentSessionKey != "" {
		if isProcessing {
			if m.escHint {
				statusLine = fmt.Sprintf("%s %s  ◀ %s", m.getBouncingDots(), i18n.T("tui.pressEscAgain"), i18n.T("tui.backToParent"))
			} else {
				statusLine = fmt.Sprintf("%s %s  ◀ %s", m.getBouncingDots(), i18n.T("tui.processing"), i18n.T("tui.backToParent"))
			}
		} else {
			statusLine = fmt.Sprintf("◄ %s", i18n.T("tui.backToParent"))
		}
	} else if isProcessing {
		if m.escHint {
			statusLine = fmt.Sprintf("%s %s", m.getBouncingDots(), i18n.T("tui.pressEscAgain"))
		} else {
			statusLine = fmt.Sprintf("%s %s", m.getBouncingDots(), i18n.T("tui.processing"))
		}
		if tok := formatTokPerSec(m.streamTokPerSec); tok != "" {
			statusLine = fmt.Sprintf("%s · %s", statusLine, tok)
		}
	} else if !m.isSubagentSession(m.currentKey) && m.hasRunningSubagents() {
		statusLine = fmt.Sprintf(i18n.T("tui.subagentsRunning"), m.subagentsCache.running)
	} else if m.lastDuration > 0 {
		statusLine = fmt.Sprintf(i18n.T("tui.doneIn"), m.lastDuration.Seconds())
		if tok := formatTokPerSec(m.lastTokPerSec); tok != "" {
			statusLine = fmt.Sprintf("%s · %s", statusLine, tok)
		}
	} else {
		statusLine = i18n.T("tui.ready")
	}

	if m.currentKey != "" && m.agentLoop != nil {
		if goal := m.agentLoop.GoalManager().Get(m.currentKey); goal != nil {
			statusWidth := lipgloss.Width(statusLine)
			remaining := (leftWidth - 2) - statusWidth - 2
			if remaining > 8 {
				goalLabel := truncateGoalLabel(goal.Text, remaining)
				goalColor := OrangeColor
				if goal.Status == agent.GoalDone {
					goalColor = SecondaryColor
				}
				goalBadge := lipgloss.NewStyle().
					Foreground(goalColor).
					Bold(true).
					Width(remaining).
					MaxWidth(remaining).
					Align(lipgloss.Right).
					Render("🎯 " + goalLabel)
				statusLine = lipgloss.JoinHorizontal(lipgloss.Top, statusLine, goalBadge)
			}
		}
	}

	if maxSW := leftWidth - 2; maxSW > 0 && lipgloss.Width(statusLine) > maxSW {
		statusLine = truncateRightCells(statusLine, maxSW)
	}

	return statusLine
}

func (m *Model) renderLeftColumn(leftWidth, contentHeight int, statusLineRendered, queueRow, autocompleteView, inputBar, bottomBar string) string {
	var leftBuilder strings.Builder

	viewportContent := m.viewport.View()
	if m.selecting {
		viewportContent = m.applySelectionHighlight(viewportContent)
	}
	leftBuilder.WriteString(ViewportStyle.Render(viewportContent) + "\n")
	if queueRow != "" {
		leftBuilder.WriteString(queueRow + "\n")
	}
	if m.parentSessionKey != "" {
		y := lipgloss.Height(leftBuilder.String()) - 1 + leadingBlankRows(statusLineRendered)
		m.backToParentClick = subagentClickTarget{yStart: y, yEnd: y + 1, key: m.parentSessionKey}
		m.hasBackToParentClick = true
	}
	leftBuilder.WriteString(statusLineRendered + "\n")
	if autocompleteView != "" {
		leftBuilder.WriteString(autocompleteView + "\n")
	}
	leftBuilder.WriteString(inputBar + "\n")
	leftBuilder.WriteString(bottomBar)

	return LeftColumnStyle.Width(leftWidth).Height(contentHeight).MaxHeight(contentHeight).Render(leftBuilder.String())
}

func (m *Model) renderRightColumn(rightWidth, cw, contentHeight, msgCount int) string {
	var rightBuilder strings.Builder

	writeSidebarRow := func(row string) {
		rightBuilder.WriteString(clampSidebarRow(row, cw) + "\n")
	}

	rightBuilder.WriteString(m.renderSidebarSessionName(cw) + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.context")) + "\n")
	currentTokens, contextWindow, cumInput, cumOutput := m.getTokenUsage(msgCount)
	writeSidebarRow(SidebarLabelValue(i18n.T("tui.currentContext"), formatNumber(currentTokens)))
	writeSidebarRow(SidebarLabelValue(i18n.T("tui.contextWindow"), formatNumber(contextWindow)))

	if contentHeight >= 20 {
		writeSidebarRow(SidebarLabelValue(i18n.T("tui.inputSent"), formatNumber(cumInput)))
		writeSidebarRow(SidebarLabelValue(i18n.T("tui.outputReceived"), formatNumber(cumOutput)))
		writeSidebarRow(SidebarLabelValue(i18n.T("tui.totalSent"), formatNumber(cumInput+cumOutput)))
		writeSidebarRow(SidebarLabelValue(i18n.T("tui.compactions"), fmt.Sprintf("%d", m.agentLoop.GetProvidable().GetCompactionCount(m.currentKey))))
	}
	rightBuilder.WriteString("\n")

	if contentHeight >= 16 {
		rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.workspace")) + "\n")
		wsPath := m.workspacePath
		if ansi.StringWidth(wsPath) > cw-1 {
			wsPath = truncateLeftCells(wsPath, cw-1)
		}
		rightBuilder.WriteString(SidebarValue.Render(wsPath) + "\n")
		branch := m.gitBranch
		if branch != "" {
			if ansi.StringWidth(branch) > cw-1 {
				branch = truncateRightCells(branch, cw-1)
			}
			rightBuilder.WriteString(SidebarValue.Render(branch) + "\n")
		}
		rightBuilder.WriteString("\n")
	}

	if contentHeight >= 14 {
		rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.status")) + "\n")
		versionLine := " " + SidebarConnectedDot.Render("●") +
			lipgloss.NewStyle().Foreground(Foreground).Render(" Lele "+agent.GatewayVersion())
		rightBuilder.WriteString(clampSidebarRow(versionLine, cw) + "\n\n")
	}

	m.renderSidebarSubagents(&rightBuilder, cw, contentHeight)

	return RightSidebar.Width(rightWidth).Height(contentHeight).MaxHeight(contentHeight).Render(rightBuilder.String())
}

func (m *Model) renderSidebarSessionName(cw int) string {
	sessionName := m.sessionMgr.GetName(m.currentKey)
	if sessionName == "" {
		sessionName = i18n.T("tui.newChatDefault")
	}
	sessionName = sanitizeDisplayText(sessionName)
	if m.parentSessionKey != "" {
		sessionName = "⇗ " + sessionName
	}
	if ansi.StringWidth(sessionName) > cw {
		sessionName = truncateRightCells(sessionName, cw)
	}
	return SidebarTitle.Render(sessionName)
}

func (m *Model) renderSidebarSubagents(rightBuilder *strings.Builder, cw, contentHeight int) {
	subagents := m.cachedSessionSubagents()
	m.subagentClickTargets = nil

	if len(subagents) == 0 {
		return
	}

	currentSidebarHeight := lipgloss.Height(lipgloss.NewStyle().Width(cw).Render(rightBuilder.String()))
	availableLines := contentHeight - currentSidebarHeight - 1

	if availableLines <= 0 {
		return
	}

	maxItems := len(subagents)
	hasMore := false
	if maxItems > availableLines {
		if availableLines <= 1 {
			rightBuilder.WriteString(CommentColorStyle.Render(
				fmt.Sprintf(" %s: +%d", i18n.T("tui.sidebar.subagents"), len(subagents))) + "\n")
			return
		}
		maxItems = availableLines - 1
		hasMore = true
	}

	rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.sidebar.subagents")) + "\n")
	currentY := currentSidebarHeight

	for i := 0; i < maxItems; i++ {
		sa := subagents[i]
		label := sa.Label
		if label == "" {
			label = sa.TaskID
		}
		label = sanitizeDisplayText(label)
		status := sanitizeDisplayText(sa.Status)

		maxLabelWidth := cw - (6 + len(status))
		if maxLabelWidth < 4 {
			maxLabelWidth = 4
		}
		label = truncateRightCells(label, maxLabelWidth)

		var statusDot string
		switch status {
		case "running", "needs_context", "not_done":
			statusDot = StatusRunning.Render("●")
		case "completed":
			statusDot = StatusCompleted.Render("●")
		case "failed", "cancelled":
			statusDot = StatusFailed.Render("●")
		default:
			statusDot = "○"
		}

		rightBuilder.WriteString(fmt.Sprintf(" %s %s (%s)\n", statusDot, label, status))

		m.subagentClickTargets = append(m.subagentClickTargets, subagentClickTarget{
			yStart: currentY,
			yEnd:   currentY + 1,
			key:    sa.SessionKey,
		})
		currentY++
	}

	if hasMore {
		remainingCount := len(subagents) - maxItems
		rightBuilder.WriteString(CommentColorStyle.Render(fmt.Sprintf(" +%d more", remainingCount)) + "\n")
	}
}
