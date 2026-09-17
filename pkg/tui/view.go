package tui

import (
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/tui/i18n"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// View is the top-level render function called by bubbletea after every
// Update. It delegates to either the welcome screen or the split-column
// chat layout, and overlays modals on top of either.
func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return i18n.T("tui.initializing")
	}

	// Audit M2: universal render-side sync — bubbletea always calls View()
	// after Update(), so re-deriving the echo mode here guarantees every
	// renderer that paints the text input via m.textInputView() (form modals,
	// settings edit fields) shows the correct mode even when an Enter/ESC
	// transition returned before the Update-side sync could run.
	m.syncTextInputEcho()

	if m.showWelcome {
		return m.renderWelcome()
	}
	return m.renderChatLayout()
}

// renderWelcome renders the welcome/home screen with the lele ASCII logo,
// input bar, model/agent selector, mode tabs, and optional modal overlay.
func (m *Model) renderWelcome() string {
	// First-run onboarding wizard rendering
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

	// Autocomplete overlay
	if m.showAutocomplete && len(m.autocompleteItems) > 0 {
		contentBuilder.WriteString(m.renderAutocompleteBlock(60) + "\n")
	}

	contentBuilder.WriteString(InputBarContainer.Width(60).Render(m.chatInput.View()) + "\n\n")

	// Model/agent selector line
	agentID, modelName := m.resolveWelcomeAgentModel()
	selectorLine := fmt.Sprintf("%s %s  %s %s",
		ModelSelectorLabel.Render(i18n.T("tui.model")),
		ModelSelectorStyle.Render(modelName),
		ModelSelectorLabel.Render(i18n.T("tui.agent")),
		ModelSelectorStyle.Render(agentID),
	)
	contentBuilder.WriteString(selectorLine + "\n")

	// Mode tabs
	contentBuilder.WriteString(m.renderModeTabs() + "\n")

	// Group mode: show group profile selector
	if m.currentMode == ModeGroup {
		contentBuilder.WriteString(m.renderGroupProfileSelector())
	}

	contentBuilder.WriteString(WelcomeTip.Render(i18n.T("tui.typeMessage")) + "\n")

	// Modal overlay on welcome screen
	if m.modalMode != ModalNone {
		return m.renderActiveModal()
	}

	return m.paintFrame(contentBuilder.String())
}

// resolveWelcomeAgentModel returns the agent ID and model name for the
// welcome screen, considering pending overrides and session state.
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

// renderModeTabs renders the Chat/Agent/Group mode tabs for the welcome
// screen, highlighting the active mode.
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
		default: // ModeAgent
			return fmt.Sprintf("%s   %s   %s",
				ModelSelectorLabel.Render(modeTabChat),
				ModelSelectorStyle.Render(modeTabAgent),
				ModelSelectorLabel.Render(modeTabGroup),
			)
		}
	}

	// Groups disabled: only show Chat and Agent tabs
	switch m.currentMode {
	case ModeChat:
		return fmt.Sprintf("%s   %s",
			ModelSelectorStyle.Render(modeTabChat),
			ModelSelectorLabel.Render(modeTabAgent),
		)
	default: // ModeAgent
		return fmt.Sprintf("%s   %s",
			ModelSelectorLabel.Render(modeTabChat),
			ModelSelectorStyle.Render(modeTabAgent),
		)
	}
}

// renderGroupProfileSelector renders the group profile picker for the
// welcome screen when in Group mode.
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

// renderAutocompleteBlock renders the autocomplete suggestion list at the
// given width.
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

// renderChatLayout renders the split-column conversational layout with the
// chat viewport on the left and the sidebar panel on the right.
func (m *Model) renderChatLayout() string {
	leftWidth := int(float64(m.width) * leftColumnRatio)
	rightWidth := m.width - leftWidth - chatSidebarGutter - 3
	if rightWidth < 1 {
		rightWidth = 1
	}
	contentHeight := m.height

	agentID := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
	modelName := m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
	thinkLevel := m.agentLoop.GetProvidable().GetEffectiveThinkLevel(m.currentKey)

	// ── Status line ──
	statusLine := m.renderStatusLine(leftWidth)

	// ── Queue row ──
	// The pending-message band sits between the status line and the composer.
	// It is resolved here because it both costs a line in the viewport budget
	// and is rendered into the left column. Empty when the queue is idle, so
	// the common case pays nothing.
	queueRow := m.queueRowLine(leftWidth - 2)

	// ── Autocomplete ──
	var autocompleteView string
	if m.showAutocomplete && len(m.autocompleteItems) > 0 {
		autocompleteView = m.renderAutocompleteBlock(leftWidth - 4)
	}

	// ── Status line rendered ──
	var statusLineRendered string
	if contentHeight < 20 {
		statusLineRendered = lipgloss.NewStyle().Foreground(CommentColor).Render(statusLine)
	} else {
		statusLineRendered = StatusLineStyle.Render(statusLine)
	}

	// ── Queue row rendered ──
	// Styled only when non-empty: lipgloss.Height("") is 1, so passing a bare
	// empty string to the budget would silently reserve a phantom line.
	var queueRowRendered string
	if queueRow != "" {
		queueRowRendered = QueueRowStyle.Render(queueRow)
	}

	// ── Input bar ──
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

	// ── Token usage ──
	currentTokens, contextWindow, _, _ := m.getTokenUsage()

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

	// ── Viewport ──
	// lipgloss.Height("") reports 1, so every optional band is measured from
	// its own rendered string: a band that renders as "" passes 0 and costs no
	// line, while a non-empty one reserves exactly what it draws.
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
	m.updateViewport()

	// ── Left Column (Chat Contents) ──
	leftPane := m.renderLeftColumn(leftWidth, contentHeight, statusLineRendered, queueRowRendered, autocompleteView, inputBar, bottomBar)

	// ── Right Column (Sidebar Panel) ──
	rightPane := m.renderRightColumn(rightWidth, contentWidth(rightWidth), contentHeight)

	// ── Final layout ──
	leftPane = clampPaneLines(leftPane, leftWidth)
	rightPane = clampPaneLines(rightPane, rightWidth+1) // +1 left border

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

// contentWidth returns the usable content width inside the right sidebar.
func contentWidth(rightWidth int) int {
	cw := rightWidth - 4
	if cw < 1 {
		return 1
	}
	return cw
}

// renderStatusLine builds the status line shown above the input bar. It
// includes processing indicators, selection feedback, queue count, and
// the goal badge.
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
	} else if m.lastDuration > 0 {
		statusLine = fmt.Sprintf(i18n.T("tui.doneIn"), m.lastDuration.Seconds())
	} else {
		statusLine = i18n.T("tui.ready")
	}

	// Queue indicator
	if qs := m.queueStatusLine((leftWidth - 2) - lipgloss.Width(statusLine)); qs != "" {
		statusLine = fmt.Sprintf("%s · %s", statusLine, qs)
	}

	// Goal badge
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

	// Clamp by cells to avoid cutting mid-ANSI
	if maxSW := leftWidth - 2; maxSW > 0 && lipgloss.Width(statusLine) > maxSW {
		statusLine = truncateRightCells(statusLine, maxSW)
	}

	return statusLine
}

// renderLeftColumn assembles the left pane of the split-column layout:
// viewport, status line, queue row, autocomplete, input bar, and bottom bar.
// queueRow may be empty, in which case it contributes no line at all.
func (m *Model) renderLeftColumn(leftWidth, contentHeight int, statusLineRendered, queueRow, autocompleteView, inputBar, bottomBar string) string {
	var leftBuilder strings.Builder

	viewportContent := m.viewport.View()
	if m.selecting {
		viewportContent = m.applySelectionHighlight(viewportContent)
	}
	leftBuilder.WriteString(ViewportStyle.Render(viewportContent) + "\n")
	leftBuilder.WriteString(statusLineRendered + "\n")
	if queueRow != "" {
		leftBuilder.WriteString(queueRow + "\n")
	}
	if autocompleteView != "" {
		leftBuilder.WriteString(autocompleteView + "\n")
	}
	leftBuilder.WriteString(inputBar + "\n")
	leftBuilder.WriteString(bottomBar)

	return LeftColumnStyle.Width(leftWidth).Height(contentHeight).MaxHeight(contentHeight).Render(leftBuilder.String())
}

// renderRightColumn assembles the right sidebar pane with session info,
// token usage, workspace path, subagent list, and click targets.
func (m *Model) renderRightColumn(rightWidth, cw, contentHeight int) string {
	var rightBuilder strings.Builder

	writeSidebarRow := func(row string) {
		rightBuilder.WriteString(clampSidebarRow(row, cw) + "\n")
	}

	// Session name
	rightBuilder.WriteString(m.renderSidebarSessionName(cw) + "\n\n")

	// Context section
	rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.context")) + "\n")
	currentTokens, contextWindow, cumInput, cumOutput := m.getTokenUsage()
	writeSidebarRow(SidebarLabelValue(i18n.T("tui.currentContext"), formatNumber(currentTokens)))
	writeSidebarRow(SidebarLabelValue(i18n.T("tui.contextWindow"), formatNumber(contextWindow)))

	if contentHeight >= 20 {
		writeSidebarRow(SidebarLabelValue(i18n.T("tui.inputSent"), formatNumber(cumInput)))
		writeSidebarRow(SidebarLabelValue(i18n.T("tui.outputReceived"), formatNumber(cumOutput)))
		writeSidebarRow(SidebarLabelValue(i18n.T("tui.totalSent"), formatNumber(cumInput+cumOutput)))
		writeSidebarRow(SidebarLabelValue(i18n.T("tui.compactions"), fmt.Sprintf("%d", m.agentLoop.GetProvidable().GetCompactionCount(m.currentKey))))
	}
	rightBuilder.WriteString("\n")

	// Workspace section
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

	// Status section
	if contentHeight >= 14 {
		rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.status")) + "\n")
		versionLine := " " + SidebarConnectedDot.Render("●") +
			lipgloss.NewStyle().Foreground(Foreground).Render(" Lele "+agent.GatewayVersion())
		rightBuilder.WriteString(clampSidebarRow(versionLine, cw) + "\n\n")
	}

	// Subagents section
	m.renderSidebarSubagents(&rightBuilder, cw, contentHeight)

	return RightSidebar.Width(rightWidth).Height(contentHeight).MaxHeight(contentHeight).Render(rightBuilder.String())
}

// renderSidebarSessionName returns the formatted session name for the
// sidebar title, with a subagent indicator prefix when applicable.
func (m *Model) renderSidebarSessionName(cw int) string {
	sessionName := m.sessionMgr.GetName(m.currentKey)
	if sessionName == "" {
		sessionName = i18n.T("tui.newChatDefault")
	}
	if m.parentSessionKey != "" {
		sessionName = "⇗ " + sessionName
	}
	if ansi.StringWidth(sessionName) > cw {
		sessionName = truncateRightCells(sessionName, cw)
	}
	return SidebarTitle.Render(sessionName)
}

// renderSidebarSubagents appends the subagent list to the sidebar builder
// and tracks click targets for mouse handling.
func (m *Model) renderSidebarSubagents(rightBuilder *strings.Builder, cw, contentHeight int) {
	subagentQueryKey := m.currentKey
	if m.parentSessionKey != "" {
		subagentQueryKey = m.parentSessionKey
	}
	if !strings.HasPrefix(subagentQueryKey, "native:") {
		subagentQueryKey = "native:" + subagentQueryKey
	}
	subagents := m.getSessionSubagentsCached(subagentQueryKey)
	m.subagentClickTargets = nil

	if len(subagents) == 0 {
		return
	}

	sortSubagents(subagents)

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
	// currentSidebarHeight is lipgloss.Height(prefix) == (rendered rows)+1
	// because the prefix ends with a newline. The header fills that trailing
	// slot (row index Height-1) and the first item starts at row index Height,
	// so currentY must be currentSidebarHeight, not currentSidebarHeight+1.
	// The previous +1 shifted every click target one row below its item row.
	currentY := currentSidebarHeight

	for i := 0; i < maxItems; i++ {
		sa := subagents[i]
		label := sa.Label
		if label == "" {
			label = sa.TaskID
		}

		maxLabelWidth := cw - (6 + len(sa.Status))
		if maxLabelWidth < 4 {
			maxLabelWidth = 4
		}
		label = truncateRightCells(label, maxLabelWidth)

		var statusDot string
		switch sa.Status {
		case "running", "needs_context", "not_done":
			statusDot = StatusRunning.Render("●")
		case "completed":
			statusDot = StatusCompleted.Render("●")
		case "failed", "cancelled":
			statusDot = StatusFailed.Render("●")
		default:
			statusDot = "○"
		}

		rightBuilder.WriteString(fmt.Sprintf(" %s %s (%s)\n", statusDot, label, sa.Status))

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
