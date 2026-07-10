package tui

import (
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/tui/i18n"

	"github.com/charmbracelet/lipgloss"
)

func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return i18n.T("tui.initializing")
	}

	// --------------------------------------------------------------------------
	// WELCOME HOME SCREEN LAYOUT
	// --------------------------------------------------------------------------
	if m.showWelcome {
		var contentBuilder strings.Builder

		logo := "  _      ______ _      ______\n" +
			" | |    |  ____| |    |  ____|\n" +
			" | |    | |__  | |    | |__   \n" +
			" | |    |  __| | |    |  __|\n" +
			" | |____| |____| |____| |____\n" +
			" |______|______|______|______|"
		contentBuilder.WriteString(WelcomeLogo.Render(logo) + "\n\n")

		var autocompleteView string
		if m.showAutocomplete && len(m.autocompleteItems) > 0 {
			var autoSb strings.Builder
			for i, cmd := range m.autocompleteItems {
				line := fmt.Sprintf("%-12s %s", cmd.name, cmd.description)
				if i == m.autocompleteIdx {
					autoSb.WriteString(ModalItemActive.Render(line) + "\n")
				} else {
					autoSb.WriteString(ModalItemInactive.Render(line) + "\n")
				}
			}
			autocompleteView = ModalContainer.Width(60).Render(autoSb.String())
			contentBuilder.WriteString(autocompleteView + "\n")
		}

		inputView := InputBarContainer.Width(60).Render(m.textInput.View())
		contentBuilder.WriteString(inputView + "\n\n")

		agentID := ""
		modelName := ""
		thinkLevel := "off"
		if m.currentKey != "" {
			agentID = m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
			modelName = m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
			tl := m.agentLoop.GetProvidable().GetThinkLevel(m.currentKey)
			if tl != "" {
				thinkLevel = tl
			}
		} else {
			agentID = m.agentLoop.GetProvidable().GetDefaultAgentID()
			if m.pendingModel != "" {
				modelName = m.pendingModel
			}
			if m.pendingAgent != "" {
				agentID = m.pendingAgent
			}
			if m.pendingThink != "" {
				thinkLevel = m.pendingThink
			}
		}
		if modelName == "" {
			if agentInfo, ok := m.agentLoop.GetProvidable().GetAgentInfo(agentID); ok {
				modelName = agentInfo.Model
			}
		}

		// Model selector line
		selectorLine := fmt.Sprintf("%s %s  %s %s",
			ModelSelectorLabel.Render(i18n.T("tui.model")),
			ModelSelectorStyle.Render(modelName),
			ModelSelectorLabel.Render(i18n.T("tui.agent")),
			ModelSelectorStyle.Render(agentID),
		)
		contentBuilder.WriteString(selectorLine + "\n")

		infoText := fmt.Sprintf("%s %s  ·  %s  ·  %s  ·  %s", i18n.T("tui.thinking"), thinkLevel, i18n.T("tui.ctrlModel"), i18n.T("tui.ctrlAgent"), i18n.T("tui.ctrlChats"))
		contentBuilder.WriteString(HelpStyle.Render(infoText) + "\n\n")

		tip := i18n.T("tui.typeMessage")
		contentBuilder.WriteString(WelcomeTip.Render(tip) + "\n")

		// Render modal overlay on welcome screen if active
		if m.modalMode != ModalNone {
			var modalTitle string
			switch m.modalMode {
			case ModalAgent:
				modalTitle = i18n.T("tui.selectAgent")
			case ModalModel:
				modalTitle = i18n.T("tui.selectModel")
			case ModalSessions:
				modalTitle = i18n.T("tui.selectChat")
			case ModalSubagents:
				modalTitle = i18n.T("tui.selectSubagent")
			case ModalThink:
				modalTitle = i18n.T("tui.selectThinkLevel")
			case ModalLang:
				modalTitle = i18n.T("tui.selectLanguage")
			}

			return m.renderModal(modalTitle)
		}

		// Center the entire welcome content block in the terminal
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, contentBuilder.String())
	}

	// --------------------------------------------------------------------------
	// SPLIT COLUMN CONVERSATIONAL LAYOUT
	// --------------------------------------------------------------------------
	leftWidth := int(float64(m.width) * leftColumnRatio)
	rightWidth := m.width - leftWidth - 3
	contentHeight := m.height - 2

	// Render Left Column (Chat Contents)
	var leftBuilder strings.Builder

	agentID := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
	modelName := m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
	thinkLevel := m.agentLoop.GetProvidable().GetThinkLevel(m.currentKey)
	if thinkLevel == "" {
		thinkLevel = "off"
	}

	m.viewport.Width = leftWidth - 2
	m.viewport.Height = contentHeight - 6
	leftBuilder.WriteString(ViewportStyle.Render(m.viewport.View()) + "\n")

	var statusLine string
	isProcessing := m.processing || m.hasRunningSubagents()
	if m.parentSessionKey != "" {
		// Viewing a subagent chat — show navigation hint
		if isProcessing {
			if m.escHint {
				statusLine = fmt.Sprintf("%s %s  ◀ %s", m.getBouncingDots(), i18n.T("tui.pressEscAgain"), i18n.T("tui.backToParent"))
			} else {
				statusLine = fmt.Sprintf("%s %s  ◀ %s", m.getBouncingDots(), i18n.T("tui.processing"), i18n.T("tui.backToParent"))
			}
		} else {
			statusLine = fmt.Sprintf("◄ %s  (ctrl+b)", i18n.T("tui.backToParent"))
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

	var autocompleteView string
	if m.showAutocomplete && len(m.autocompleteItems) > 0 {
		var autoSb strings.Builder
		for i, cmd := range m.autocompleteItems {
			line := fmt.Sprintf("%-12s %s", cmd.name, cmd.description)
			if i == m.autocompleteIdx {
				autoSb.WriteString(ModalItemActive.Render(line) + "\n")
			} else {
				autoSb.WriteString(ModalItemInactive.Render(line) + "\n")
			}
		}
		autocompleteView = ModalContainer.Width(leftWidth-4).Render(autoSb.String()) + "\n"
	}

	leftBuilder.WriteString(StatusLineStyle.Render(statusLine) + "\n")
	if autocompleteView != "" {
		leftBuilder.WriteString(autocompleteView)
	}

	m.textInput.Width = leftWidth - 4
	inputBar := InputBarContainer.Width(leftWidth - 2).Render(m.textInput.View())
	leftBuilder.WriteString(inputBar + "\n")

	// Cache token counts to avoid duplicate API calls
	currentTokens, contextWindow := m.agentLoop.GetProvidable().GetCurrentContextUsage(m.currentKey)
	cumInput, cumOutput, _ := m.agentLoop.GetProvidable().GetTokenCounts(m.currentKey)

	pct := 0.0
	if contextWindow > 0 {
		pct = float64(currentTokens) / float64(contextWindow) * 100
	}
	tokensText := fmt.Sprintf("%d (%.1f%%)", currentTokens, pct)
	bottomBar := lipgloss.JoinHorizontal(lipgloss.Top,
		BottomBarLeft.Width((leftWidth-2)/2).Render(fmt.Sprintf("%s · %s · %s", agentID, modelName, thinkLevel)),
		BottomBarRight.Width((leftWidth-2)/2).Align(lipgloss.Right).Render(fmt.Sprintf("%s | %s | %s", tokensText, i18n.T("tui.ctrlCommands"), func() string {
			if m.mouseEnabled {
				return i18n.T("tui.mouseOn")
			}
			return i18n.T("tui.mouseOff")
		}())),
	)
	leftBuilder.WriteString(bottomBar)

	leftPane := LeftColumnStyle.Width(leftWidth).Render(leftBuilder.String())

	// Render Right Column (Sidebar Panel)
	var rightBuilder strings.Builder

	sessionName := m.sessionMgr.GetName(m.currentKey)
	if sessionName == "" {
		sessionName = i18n.T("tui.newChatDefault")
	}
	// Show subagent indicator in sidebar title when viewing a subagent chat
	if m.parentSessionKey != "" {
		sessionName = "⇗ " + sessionName
	}
	rightBuilder.WriteString(SidebarTitle.Render(sessionName) + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.context")) + "\n")

	// Current context usage (history + system prompt) — cached above
	rightBuilder.WriteString(SidebarLabelValue(i18n.T("tui.currentContext"), formatNumber(currentTokens)) + "\n")
	rightBuilder.WriteString(SidebarLabelValue(i18n.T("tui.contextWindow"), formatNumber(contextWindow)) + "\n")

	// Cumulative token counts for this session — cached above
	rightBuilder.WriteString(SidebarLabelValue(i18n.T("tui.inputSent"), formatNumber(cumInput)) + "\n")
	rightBuilder.WriteString(SidebarLabelValue(i18n.T("tui.outputReceived"), formatNumber(cumOutput)) + "\n")
	rightBuilder.WriteString(SidebarLabelValue(i18n.T("tui.totalSent"), formatNumber(cumInput+cumOutput)) + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.workspace")) + "\n")
	rightBuilder.WriteString(SidebarValue.Render(m.workspacePath) + "\n")
	rightBuilder.WriteString(SidebarValue.Render(m.gitBranch) + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.status")) + "\n")
	rightBuilder.WriteString(SidebarValue.Render(SidebarConnectedDot.Render("●")+" Lele "+agent.GatewayVersion()) + "\n\n")

	// Get session subagents
	subagentQueryKey := m.currentKey
	if m.parentSessionKey != "" {
		subagentQueryKey = m.parentSessionKey
	}
	if !strings.HasPrefix(subagentQueryKey, "native:") {
		subagentQueryKey = "native:" + subagentQueryKey
	}
	subagents := m.agentLoop.GetProvidable().GetSessionSubagents(subagentQueryKey)

	// Reset subagent click targets for fresh tracking
	m.subagentClickTargets = nil

	if len(subagents) > 0 {
		// Sort by appearance (most recent first)
		sortSubagents(subagents)

		rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.sidebar.subagents")) + "\n")

		contentWidth := rightWidth - 4
		if contentWidth < 1 {
			contentWidth = 1
		}

		for _, sa := range subagents {
			label := sa.Label
			if label == "" {
				label = sa.TaskID
			}

			// Truncate label dynamically to prevent word wrapping in the sidebar.
			// The printed line has a layout of " [statusDot] [label] ([status])\n"
			// Status dot is 1 char. Spacing and parentheses add another 5 chars.
			// Status text length is len(sa.Status).
			// RightSidebar has a left border (1), padding left (2), padding right (1), so useful width is rightWidth - 4.
			maxLabelWidth := (rightWidth - 4) - (3 + len(sa.Status) + 3)
			if maxLabelWidth < 5 {
				maxLabelWidth = 5 // Keep a minimum width so it doesn't disappear completely
			}
			// Rune-safe truncation to avoid breaking multi-byte UTF-8 characters
			r := []rune(label)
			if len(r) > maxLabelWidth {
				if maxLabelWidth < 3 {
					label = string(r[:maxLabelWidth])
				} else {
					label = string(r[:maxLabelWidth-3]) + "..."
				}
			}

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

			yStart := lipgloss.Height(lipgloss.NewStyle().Width(contentWidth).Render(rightBuilder.String())) - 1
			lineStr := fmt.Sprintf(" %s %s (%s)\n", statusDot, label, sa.Status)
			rightBuilder.WriteString(lineStr)
			yEnd := yStart + lipgloss.Height(lipgloss.NewStyle().Width(contentWidth).Render(strings.TrimRight(lineStr, "\n")))

			// Track this subagent item's position for click handling
			m.subagentClickTargets = append(m.subagentClickTargets, subagentClickTarget{
				yStart: yStart,
				yEnd:   yEnd,
				key:    sa.SessionKey,
			})
		}
	}

	rightPane := RightSidebar.Width(rightWidth).Height(contentHeight).Render(rightBuilder.String())

	mainLayout := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

	if m.modalMode != ModalNone {
		var modalTitle string
		switch m.modalMode {
		case ModalAgent:
			modalTitle = i18n.T("tui.selectAgent")
		case ModalModel:
			modalTitle = i18n.T("tui.selectModel")
		case ModalSessions:
			modalTitle = i18n.T("tui.selectChat")
		case ModalSubagents:
			modalTitle = i18n.T("tui.selectSubagent")
		case ModalThink:
			modalTitle = i18n.T("tui.selectThinkLevel")
		case ModalLang:
			modalTitle = i18n.T("tui.selectLanguage")
		}

		return m.renderModal(modalTitle)
	}

	return AppContainer.Width(m.width).Height(m.height).Render(mainLayout)
}

// maxModalVisible returns the maximum number of items visible in a modal given terminal height.
func (m *Model) maxModalVisible() int {
	maxVisible := m.height - 8 // room for title, borders, padding
	if maxVisible < 3 {
		maxVisible = 3
	}
	if maxVisible > len(m.modalItems) {
		maxVisible = len(m.modalItems)
	}
	return maxVisible
}

// renderModal renders a modal overlay with scroll support for long lists.
func (m *Model) renderModal(modalTitle string) string {
	maxVisible := m.maxModalVisible()

	// Clamp scroll offset so selected item is always visible
	if m.modalSelectedIdx < m.modalScrollOffset {
		m.modalScrollOffset = m.modalSelectedIdx
	}
	if m.modalSelectedIdx >= m.modalScrollOffset+maxVisible {
		m.modalScrollOffset = m.modalSelectedIdx - maxVisible + 1
	}
	if m.modalScrollOffset < 0 {
		m.modalScrollOffset = 0
	}

	var modalSb strings.Builder
	modalSb.WriteString(TitleStyle.Render(modalTitle) + "\n")

	// Scroll indicator: show ↑ if there are items above
	if m.modalScrollOffset > 0 {
		modalSb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.moreAbove")) + "\n")
	} else {
		modalSb.WriteString("\n")
	}

	// Render only the visible window of items
	endIdx := m.modalScrollOffset + maxVisible
	if endIdx > len(m.modalItems) {
		endIdx = len(m.modalItems)
	}
	for i := m.modalScrollOffset; i < endIdx; i++ {
		item := m.modalItems[i]
		if i == m.modalSelectedIdx {
			modalSb.WriteString(ModalItemActive.Render("> "+item) + "\n")
		} else {
			modalSb.WriteString(ModalItemInactive.Render("  "+item) + "\n")
		}
	}

	// Scroll indicator: show ↓ if there are items below
	if endIdx < len(m.modalItems) {
		modalSb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.moreBelow")) + "\n")
	}

	modalView := ModalContainer.Render(modalSb.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modalView)
}

func (m *Model) getBouncingDots() string {
	width := 12
	pos := m.animationTick % (2 * (width - 3))
	var offset int
	if pos < width-3 {
		offset = pos
	} else {
		offset = 2*(width-3) - pos
	}

	var sb strings.Builder
	sb.WriteRune('[')
	for i := 0; i < width; i++ {
		if i == offset || i == offset+1 || i == offset+2 {
			sb.WriteString(lipgloss.NewStyle().Foreground(SecondaryColor).Render("●"))
		} else {
			sb.WriteRune(' ')
		}
	}
	sb.WriteRune(']')
	return sb.String()
}
