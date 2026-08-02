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

		inputView := InputBarContainer.Width(60).Render(m.chatInput.View())
		contentBuilder.WriteString(inputView + "\n\n")

		agentID := ""
		modelName := ""
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

		// Model selector line
		selectorLine := fmt.Sprintf("%s %s  %s %s",
			ModelSelectorLabel.Render(i18n.T("tui.model")),
			ModelSelectorStyle.Render(modelName),
			ModelSelectorLabel.Render(i18n.T("tui.agent")),
			ModelSelectorStyle.Render(agentID),
		)
		contentBuilder.WriteString(selectorLine + "\n")

		// Mode tabs: show available modes, highlighting the active one
		modeTabChat := i18n.T("tui.modeChat")
		modeTabAgent := i18n.T("tui.modeAgent")
		modeTabGroup := i18n.T("tui.modeGroup")
		var modeTabs string
		if m.cfg.Groups.Enabled {
			switch m.currentMode {
			case ModeChat:
				modeTabs = fmt.Sprintf("%s   %s   %s",
					ModelSelectorStyle.Render(modeTabChat),
					ModelSelectorLabel.Render(modeTabAgent),
					ModelSelectorLabel.Render(modeTabGroup),
				)
			case ModeGroup:
				modeTabs = fmt.Sprintf("%s   %s   %s",
					ModelSelectorLabel.Render(modeTabChat),
					ModelSelectorLabel.Render(modeTabAgent),
					ModelSelectorStyle.Render(modeTabGroup),
				)
			default: // ModeAgent
				modeTabs = fmt.Sprintf("%s   %s   %s",
					ModelSelectorLabel.Render(modeTabChat),
					ModelSelectorStyle.Render(modeTabAgent),
					ModelSelectorLabel.Render(modeTabGroup),
				)
			}
		} else {
			// Groups disabled: only show Chat and Agent tabs
			switch m.currentMode {
			case ModeChat:
				modeTabs = fmt.Sprintf("%s   %s",
					ModelSelectorStyle.Render(modeTabChat),
					ModelSelectorLabel.Render(modeTabAgent),
				)
			default: // ModeAgent
				modeTabs = fmt.Sprintf("%s   %s",
					ModelSelectorLabel.Render(modeTabChat),
					ModelSelectorStyle.Render(modeTabAgent),
				)
			}
		}
		contentBuilder.WriteString(modeTabs + "\n")

		// Group mode: show group profile selector
		if m.currentMode == ModeGroup {
			profiles := m.getGroupProfiles()
			if len(profiles) > 0 {
				contentBuilder.WriteString("\n")
				contentBuilder.WriteString(ModelSelectorLabel.Render(i18n.T("tui.groupSelectProfile")) + "\n")
				for i, p := range profiles {
					line := fmt.Sprintf("%s (%s, %d agents)", p.ID, p.Strategy, len(p.Participants))
					if i == m.groupProfileIdx {
						contentBuilder.WriteString(ModelSelectorStyle.Render("> "+line) + "\n")
					} else {
						contentBuilder.WriteString(ModelSelectorLabel.Render("  "+line) + "\n")
					}
				}
				contentBuilder.WriteString(HelpStyle.Render(i18n.T("tui.groupTaskPlaceholder")) + "\n")
			} else {
				contentBuilder.WriteString("\n")
				contentBuilder.WriteString(CommentColorStyle.Render(i18n.T("tui.noGroupProfiles")) + "\n")
			}
		}

		tip := i18n.T("tui.typeMessage")
		contentBuilder.WriteString(WelcomeTip.Render(tip) + "\n")

		// Render modal overlay on welcome screen if active
		if m.modalMode != ModalNone {
			if m.modalMode == ModalBackgroundExecs && m.bgExecViewMode {
				return m.renderBgExecOutput()
			}
			if m.modalMode == ModalCron && m.cronDetailMode {
				return m.renderCronDetail()
			}
			if m.modalMode == ModalSecrets && m.secretsDetailMode {
				return m.renderSecretDetail()
			}
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
			case ModalBackgroundExecs:
				modalTitle = i18n.T("tui.backgroundProcesses")
			case ModalCron:
				modalTitle = i18n.T("tui.cronJobs")
			case ModalSecrets:
				modalTitle = m.secretsHeader()
			case ModalProviders:
				modalTitle = i18n.T("tui.selectProvider")
			case ModalProviderDetail:
				modalTitle = i18n.T("tui.providerDetail")
			case ModalAddProvider:
				modalTitle = i18n.T("tui.addProvider")
			case ModalAddModel:
				modalTitle = i18n.T("tui.addModel")
			case ModalAddSecret:
				modalTitle = i18n.T("tui.secrets")
			}

			if m.modalMode == ModalAddProvider || m.modalMode == ModalAddModel || m.modalMode == ModalAddSecret {
				return m.renderFormModal(modalTitle, m.formStepNames())
			}
			if m.modalMode == ModalSecrets {
				return m.renderSecretsList(modalTitle)
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
	contentHeight := m.height

	agentID := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
	modelName := m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
	thinkLevel := m.agentLoop.GetProvidable().GetThinkLevel(m.currentKey)
	if thinkLevel == "" {
		thinkLevel = "off"
	}

	var statusLine string
	isProcessing := m.isSessionProcessing()
	if m.selectionFeedback {
		statusLine = i18n.T("tui.selectionCopied")
	} else if m.selecting {
		statusLine = i18n.T("tui.selecting")
	} else if m.parentSessionKey != "" {
		// Viewing a subagent chat — show navigation hint
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

	statusLineRendered := StatusLineStyle.Render(statusLine)

	m.chatInput.SetWidth(leftWidth - 4)
	inputBar := InputBarContainer.Width(leftWidth - 2).Render(m.chatInput.View())

	// Use cached token counts — GetCurrentContextUsage is expensive (rebuilds
	// system prompt from disk + estimates tokens over full history). The cache
	// refreshes at most once per 2s or when message count changes.
	currentTokens, contextWindow, cumInput, cumOutput := m.getTokenUsage()

	pct := 0.0
	if contextWindow > 0 {
		pct = float64(currentTokens) / float64(contextWindow) * 100
	}
	tokensText := fmt.Sprintf("%d (%.1f%%)", currentTokens, pct)
	modeBadge := fmt.Sprintf("[%s]", strings.ToUpper(m.currentMode.String()))
	bottomBar := lipgloss.JoinHorizontal(lipgloss.Top,
		BottomBarLeft.Width((leftWidth-2)/2).Render(fmt.Sprintf("%s %s · %s · %s", ModelSelectorStyle.Render(modeBadge), agentID, modelName, thinkLevel)),
		BottomBarRight.Width((leftWidth-2)/2).Align(lipgloss.Right).Render(tokensText),
	)

	m.viewport.Width = leftWidth - 2
	m.viewport.Height = calculateViewportHeight(
		contentHeight,
		lipgloss.Height(statusLineRendered),
		lipgloss.Height(autocompleteView),
		lipgloss.Height(inputBar),
		lipgloss.Height(bottomBar),
	)
	m.updateViewport()

	// Render Left Column (Chat Contents)
	var leftBuilder strings.Builder
	viewportContent := m.viewport.View()
	if m.selecting {
		viewportContent = m.applySelectionHighlight(viewportContent)
	}
	leftBuilder.WriteString(ViewportStyle.Render(viewportContent) + "\n")
	leftBuilder.WriteString(statusLineRendered + "\n")
	if autocompleteView != "" {
		leftBuilder.WriteString(autocompleteView)
	}
	leftBuilder.WriteString(inputBar + "\n")
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
	rightBuilder.WriteString(SidebarLabelValue(i18n.T("tui.totalSent"), formatNumber(cumInput+cumOutput)) + "\n")
	rightBuilder.WriteString(SidebarLabelValue(i18n.T("tui.compactions"), fmt.Sprintf("%d", m.agentLoop.GetProvidable().GetCompactionCount(m.currentKey))) + "\n\n")

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

		// Compute the base height once before the loop. The original code
		// re-rendered the FULL accumulated rightBuilder string on every
		// iteration to measure yStart, making the loop O(n²) with many
		// subagents. Now we render only the single new line per iteration
		// (O(1) each → O(n) total) while preserving exact click-target
		// geometry, including any line wrapping on very narrow terminals.
		baseHeight := lipgloss.Height(lipgloss.NewStyle().Width(contentWidth).Render(rightBuilder.String()))
		currentY := baseHeight

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

			yStart := currentY - 1
			lineStr := fmt.Sprintf(" %s %s (%s)\n", statusDot, label, sa.Status)
			rightBuilder.WriteString(lineStr)
			lineHeight := lipgloss.Height(lipgloss.NewStyle().Width(contentWidth).Render(strings.TrimRight(lineStr, "\n")))
			yEnd := yStart + lineHeight

			// Track this subagent item's position for click handling
			m.subagentClickTargets = append(m.subagentClickTargets, subagentClickTarget{
				yStart: yStart,
				yEnd:   yEnd,
				key:    sa.SessionKey,
			})
			currentY += lineHeight
		}
	}

	rightPane := RightSidebar.Width(rightWidth).Height(contentHeight).Render(rightBuilder.String())

	mainLayout := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

	if m.modalMode != ModalNone {
		if m.modalMode == ModalBackgroundExecs && m.bgExecViewMode {
			return m.renderBgExecOutput()
		}
		if m.modalMode == ModalCron && m.cronDetailMode {
			return m.renderCronDetail()
		}
		if m.modalMode == ModalSecrets && m.secretsDetailMode {
			return m.renderSecretDetail()
		}
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
		case ModalBackgroundExecs:
			modalTitle = i18n.T("tui.backgroundProcesses")
		case ModalCron:
			modalTitle = i18n.T("tui.cronJobs")
		case ModalSecrets:
			modalTitle = m.secretsHeader()
		case ModalProviders:
			modalTitle = i18n.T("tui.selectProvider")
		case ModalProviderDetail:
			modalTitle = i18n.T("tui.providerDetail")
		case ModalAddProvider:
			modalTitle = i18n.T("tui.addProvider")
		case ModalAddModel:
			modalTitle = i18n.T("tui.addModel")
		case ModalAddSecret:
			modalTitle = i18n.T("tui.secrets")
		}

		if m.modalMode == ModalAddProvider || m.modalMode == ModalAddModel || m.modalMode == ModalAddSecret {
			return m.renderFormModal(modalTitle, m.formStepNames())
		}
		if m.modalMode == ModalSecrets {
			return m.renderSecretsList(modalTitle)
		}
		return m.renderModal(modalTitle)
	}

	return AppContainer.Width(m.width).Height(m.height).Render(mainLayout)
}

// calculateViewportHeight reserves every line rendered below the viewport.
// Keeping this budget exact prevents the input and bottom bar from spilling
// past the terminal height and being painted twice by the TUI renderer.
func calculateViewportHeight(contentHeight, statusHeight, autocompleteHeight, inputHeight, bottomHeight int) int {
	otherHeight := 1 + statusHeight + 1 + inputHeight + 1 + bottomHeight
	if autocompleteHeight > 0 {
		otherHeight += autocompleteHeight
	}
	viewportHeight := contentHeight - otherHeight
	if viewportHeight < 3 {
		return 3
	}
	return viewportHeight
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

// renderFormModal renders a multi-step form modal with step indicators,
// an input field for the current step, and optional error display.
func (m *Model) renderFormModal(title string, steps []string) string {
	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(title) + "\n\n")

	for i, step := range steps {
		if i < m.formStepIndex {
			// Completed step: show checkmark and value
			val := ""
			if i < len(m.formValues) {
				val = m.formValues[i]
			}
			sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  ✓ %s: %s", step, val)) + "\n")
		} else if i == m.formStepIndex {
			// Current step: highlighted with input indicator
			val := m.textInput.Value()
			if val == "" {
				val = "…"
			}
			sb.WriteString(ModalItemActive.Render(fmt.Sprintf("  ▶ %s: [%s]", step, val)) + "\n")
		} else {
			// Future step: muted
			sb.WriteString(CommentColorStyle.Render(fmt.Sprintf("  ○ %s", step)) + "\n")
		}
	}

	sb.WriteString("\n")

	// Error display
	if m.formError != "" {
		sb.WriteString(lipgloss.NewStyle().Foreground(PrimaryColor).Render("  ✗ "+m.formError) + "\n\n")
	}

	// Text input field
	m.textInput.Width = 40
	sb.WriteString(InputBarContainer.Width(44).Render(m.textInput.View()) + "\n\n")

	// Hints
	sb.WriteString(HelpStyle.Render("  ENTER: next | ESC: cancel"))

	modalView := ModalContainer.Render(sb.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modalView)
}

// formStepNames returns the step names for the current form modal mode.
func (m *Model) formStepNames() []string {
	switch m.modalMode {
	case ModalAddProvider:
		return []string{"Provider name", "Provider type", "API Key", "API Base URL"}
	case ModalAddModel:
		return []string{"Model alias", "Model name", "Context window", "Max tokens", "Vision (yes/no)"}
	case ModalAddSecret:
		return []string{
			i18n.T("tui.secretName"),
			i18n.T("tui.secretValue"),
			i18n.T("tui.secretDescription"),
			i18n.T("tui.secretTags"),
			i18n.T("tui.secretScope"),
		}
	default:
		return nil
	}
}

// renderBgExecOutput renders the output view for a background process.
func (m *Model) renderBgExecOutput() string {
	// Title with process ID and status
	statusColor := CommentColor
	switch m.bgExecViewStatus {
	case "running":
		statusColor = YellowColor
	case "completed":
		statusColor = SecondaryColor
	case "failed":
		statusColor = PrimaryColor
	}

	titleText := fmt.Sprintf("Background Process: %s", m.bgExecViewID)
	statusText := lipgloss.NewStyle().Foreground(statusColor).Render(fmt.Sprintf("[%s]", m.bgExecViewStatus))

	titleLine := lipgloss.JoinHorizontal(lipgloss.Center,
		TitleStyle.Render(titleText),
		"  ",
		statusText,
	)

	// Output content
	outputContent := m.bgExecViewOutput
	if outputContent == "" {
		outputContent = CommentColorStyle.Render("(no output)")
	}

	// Calculate available height for output
	availableHeight := m.height - 8 // title + borders + hints + padding
	if availableHeight < 3 {
		availableHeight = 3
	}

	// Truncate output to fit available height (show last N lines)
	outputLines := strings.Split(outputContent, "\n")
	if len(outputLines) > availableHeight {
		outputLines = outputLines[len(outputLines)-availableHeight:]
	}
	outputContent = strings.Join(outputLines, "\n")

	// Hints at the bottom
	hintsText := CommentColorStyle.Render(i18n.T("tui.bgOutputHints"))

	// Build the view
	var sb strings.Builder
	sb.WriteString(titleLine + "\n\n")
	sb.WriteString(outputContent + "\n\n")
	sb.WriteString(hintsText)

	outputBox := ModalContainer.Width(m.width - 10).Render(sb.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, outputBox)
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
