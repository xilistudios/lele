package tui

import (
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/tui/i18n"
	"github.com/xilistudios/lele/pkg/tui/theme"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

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

	// --------------------------------------------------------------------------
	// WELCOME HOME SCREEN LAYOUT
	// --------------------------------------------------------------------------
	if m.showWelcome {
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
			case ModalSkills:
				modalTitle = i18n.T("tui.skills")
			case ModalSkillInstall:
				modalTitle = i18n.T("tui.installSkill")
			case ModalSkillPicker:
				modalTitle = i18n.T("tui.selectSkills")
			case ModalSettings, ModalSettingsAgents, ModalSettingsAgentEdit,
				ModalSettingsSystem, ModalSettingsSystemEdit, ModalSettingsTUI:
				modalTitle = i18n.T("tui.settings.title")
				if m.modalMode == ModalSettingsAgents {
					modalTitle = i18n.T("tui.settings.title") + " › " + i18n.T("tui.settings.agents")
				} else if m.modalMode == ModalSettingsAgentEdit {
					agentLabel := i18n.T("tui.settings.agentDefaults")
					if m.settingsAgentID != "" {
						agentLabel = m.settingsAgentID
					}
					modalTitle = i18n.T("tui.settings.title") + " › " + i18n.T("tui.settings.agents") + " › " + agentLabel
				} else if m.modalMode == ModalSettingsSystem {
					modalTitle = i18n.T("tui.settings.title") + " › " + i18n.T("tui.settings.system")
				} else if m.modalMode == ModalSettingsSystemEdit {
					modalTitle = i18n.T("tui.settings.title") + " › " + i18n.T("tui.settings.system") + " › " + m.systemSettingsTitle()
				} else if m.modalMode == ModalSettingsTUI {
					modalTitle = i18n.T("tui.settings.title") + " › " + i18n.T("tui.settings.interface")
				}
			}

			if m.modalMode == ModalAddProvider || m.modalMode == ModalAddModel || m.modalMode == ModalAddSecret {
				return m.renderFormModal(modalTitle, m.formStepNames())
			}
			if m.modalMode == ModalSecrets {
				return m.renderSecretsList(modalTitle)
			}
			if m.modalMode == ModalSkillInstall {
				return m.renderFormModal(modalTitle, []string{"GitHub Repository"})
			}
			if m.modalMode == ModalSkillPicker {
				return m.renderSkillPicker(modalTitle)
			}
			if m.modalMode == ModalSettingsTUI {
				return m.renderTUISettings(modalTitle)
			}
			if m.modalMode == ModalSettingsAgents {
				return m.renderModal(modalTitle)
			}
			if m.modalMode == ModalSettingsAgentEdit {
				if m.subagentPickerActive {
					return m.renderSubagentPicker(modalTitle)
				}
				if m.settingsSelectorActive {
					return m.renderSettingsSelector(modalTitle)
				}
				if m.settingsEditField != "" {
					return m.renderAgentEditInput()
				}
				return m.renderModal(modalTitle)
			}
			if m.modalMode == ModalSettingsSystemEdit {
				if m.settingsSelectorActive {
					return m.renderSettingsSelector(modalTitle)
				}
				if m.settingsEditField != "" {
					return m.renderSystemSettingsEdit(modalTitle)
				}
				return m.renderModal(modalTitle)
			}
			return m.renderModal(modalTitle)
		}

		// Center the entire welcome content block in the terminal
		return m.paintFrame(contentBuilder.String())
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

	// Client-side queue indicator: shows how many messages are waiting to be
	// auto-submitted for this session (see queue.go). Appended before the goal
	// badge so both fit within the width clamp below.
	if qs := m.queueStatusLine(); qs != "" {
		statusLine = fmt.Sprintf("%s · %s", statusLine, qs)
	}

	// Append goal badge to the right of the status line.
	// Orange = goal in progress, Green = goal completed.
	if m.currentKey != "" && m.agentLoop != nil {
		if goal := m.agentLoop.GoalManager().Get(m.currentKey); goal != nil {
			statusWidth := lipgloss.Width(statusLine)
			remaining := (leftWidth - 2) - statusWidth - 2
			if remaining > 8 {
				// Budget the label by display cells: "🎯 " is 3 cells wide.
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

	// Ensure status line does not exceed available column width. The line may
	// contain ANSI sequences (bouncing dots, goal badge); truncating by runes
	// would cut mid-escape and collapse the visible width, so clamp by cells.
	if maxSW := leftWidth - 2; maxSW > 0 && lipgloss.Width(statusLine) > maxSW {
		statusLine = truncateRightCells(statusLine, maxSW)
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

	var statusLineRendered string
	if contentHeight < 20 {
		statusLineRendered = lipgloss.NewStyle().Foreground(CommentColor).Render(statusLine)
	} else {
		statusLineRendered = StatusLineStyle.Render(statusLine)
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

	leftPane := LeftColumnStyle.Width(leftWidth).Height(contentHeight).MaxHeight(contentHeight).Render(leftBuilder.String())

	// Render Right Column (Sidebar Panel)
	var rightBuilder strings.Builder

	contentWidth := rightWidth - 4
	if contentWidth < 1 {
		contentWidth = 1
	}

	sessionName := m.sessionMgr.GetName(m.currentKey)
	if sessionName == "" {
		sessionName = i18n.T("tui.newChatDefault")
	}
	// Show subagent indicator in sidebar title when viewing a subagent chat
	if m.parentSessionKey != "" {
		sessionName = "⇗ " + sessionName
	}
	// Clamp by display cells (ANSI- and wide-char-aware) instead of runes.
	if ansi.StringWidth(sessionName) > contentWidth {
		sessionName = truncateRightCells(sessionName, contentWidth)
	}
	rightBuilder.WriteString(SidebarTitle.Render(sessionName) + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.context")) + "\n")

	// Current context usage (history + system prompt) — cached above
	rightBuilder.WriteString(SidebarLabelValue(i18n.T("tui.currentContext"), formatNumber(currentTokens)) + "\n")
	rightBuilder.WriteString(SidebarLabelValue(i18n.T("tui.contextWindow"), formatNumber(contextWindow)) + "\n")

	// On medium/large heights, show detailed cumulative token counts
	if contentHeight >= 20 {
		rightBuilder.WriteString(SidebarLabelValue(i18n.T("tui.inputSent"), formatNumber(cumInput)) + "\n")
		rightBuilder.WriteString(SidebarLabelValue(i18n.T("tui.outputReceived"), formatNumber(cumOutput)) + "\n")
		rightBuilder.WriteString(SidebarLabelValue(i18n.T("tui.totalSent"), formatNumber(cumInput+cumOutput)) + "\n")
		rightBuilder.WriteString(SidebarLabelValue(i18n.T("tui.compactions"), fmt.Sprintf("%d", m.agentLoop.GetProvidable().GetCompactionCount(m.currentKey))) + "\n")
	}
	rightBuilder.WriteString("\n")

	if contentHeight >= 16 {
		rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.workspace")) + "\n")
		wsPath := m.workspacePath
		// Keep the tail of the path (directory name matters most); budget
		// contentWidth-1 cells as before, but measure in display cells.
		if ansi.StringWidth(wsPath) > contentWidth-1 {
			wsPath = truncateLeftCells(wsPath, contentWidth-1)
		}
		rightBuilder.WriteString(SidebarValue.Render(wsPath) + "\n")
		branch := m.gitBranch
		if ansi.StringWidth(branch) > contentWidth-1 {
			branch = truncateRightCells(branch, contentWidth-1)
		}
		rightBuilder.WriteString(SidebarValue.Render(branch) + "\n\n")
	}

	if contentHeight >= 14 {
		rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.status")) + "\n")
		rightBuilder.WriteString(SidebarValue.Render(SidebarConnectedDot.Render("●")+" Lele "+agent.GatewayVersion()) + "\n\n")
	}

	// Get session subagents
	subagentQueryKey := m.currentKey
	if m.parentSessionKey != "" {
		subagentQueryKey = m.parentSessionKey
	}
	if !strings.HasPrefix(subagentQueryKey, "native:") {
		subagentQueryKey = "native:" + subagentQueryKey
	}
	subagents := m.getSessionSubagentsCached(subagentQueryKey)

	// Reset subagent click targets for fresh tracking
	m.subagentClickTargets = nil

	if len(subagents) > 0 {
		// Sort by appearance (most recent first)
		sortSubagents(subagents)

		currentSidebarHeight := lipgloss.Height(lipgloss.NewStyle().Width(contentWidth).Render(rightBuilder.String()))
		availableLines := contentHeight - currentSidebarHeight - 1 // -1 for Subagents header

		if availableLines > 0 {
			rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.sidebar.subagents")) + "\n")
			currentY := currentSidebarHeight + 1

			maxItems := len(subagents)
			hasMore := false
			if maxItems > availableLines {
				maxItems = availableLines - 1
				hasMore = true
			}
			if maxItems < 0 {
				maxItems = 0
			}

			for i := 0; i < maxItems; i++ {
				sa := subagents[i]
				label := sa.Label
				if label == "" {
					label = sa.TaskID
				}

				// The printed line has a layout of " [statusDot] [label] ([status])\n"
				// Truncate label so line NEVER wraps across multiple rows.
				maxLabelWidth := contentWidth - (6 + len(sa.Status))
				if maxLabelWidth < 4 {
					maxLabelWidth = 4
				}
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

				lineStr := fmt.Sprintf(" %s %s (%s)\n", statusDot, label, sa.Status)
				rightBuilder.WriteString(lineStr)

				// Track this subagent item's position for click handling
				m.subagentClickTargets = append(m.subagentClickTargets, subagentClickTarget{
					yStart: currentY,
					yEnd:   currentY + 1,
					key:    sa.SessionKey,
				})
				currentY++
			}

			if hasMore {
				remainingCount := len(subagents) - maxItems
				moreStr := fmt.Sprintf(" +%d more", remainingCount)
				rightBuilder.WriteString(CommentColorStyle.Render(moreStr) + "\n")
			}
		}
	}

	rightPane := RightSidebar.Width(rightWidth).Height(contentHeight).MaxHeight(contentHeight).Render(rightBuilder.String())

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
		case ModalSettings, ModalSettingsAgents, ModalSettingsAgentEdit,
			ModalSettingsSystem, ModalSettingsSystemEdit, ModalSettingsTUI:
			modalTitle = i18n.T("tui.settings.title")
			if m.modalMode == ModalSettingsAgents {
				modalTitle = i18n.T("tui.settings.title") + " › " + i18n.T("tui.settings.agents")
			} else if m.modalMode == ModalSettingsAgentEdit {
				agentLabel := i18n.T("tui.settings.agentDefaults")
				if m.settingsAgentID != "" {
					agentLabel = m.settingsAgentID
				}
				modalTitle = i18n.T("tui.settings.title") + " › " + i18n.T("tui.settings.agents") + " › " + agentLabel
			} else if m.modalMode == ModalSettingsSystem {
				modalTitle = i18n.T("tui.settings.title") + " › " + i18n.T("tui.settings.system")
			} else if m.modalMode == ModalSettingsSystemEdit {
				modalTitle = i18n.T("tui.settings.title") + " › " + i18n.T("tui.settings.system") + " › " + m.systemSettingsTitle()
			} else if m.modalMode == ModalSettingsTUI {
				modalTitle = i18n.T("tui.settings.title") + " › " + i18n.T("tui.settings.interface")
			}
		}

		if m.modalMode == ModalAddProvider || m.modalMode == ModalAddModel || m.modalMode == ModalAddSecret {
			return m.renderFormModal(modalTitle, m.formStepNames())
		}
		if m.modalMode == ModalSecrets {
			return m.renderSecretsList(modalTitle)
		}
		if m.modalMode == ModalSettingsTUI {
			return m.renderTUISettings(modalTitle)
		}
		if m.modalMode == ModalSettingsAgents {
			return m.renderModal(modalTitle)
		}
		if m.modalMode == ModalSettingsAgentEdit {
			if m.subagentPickerActive {
				return m.renderSubagentPicker(modalTitle)
			}
			if m.settingsSelectorActive {
				return m.renderSettingsSelector(modalTitle)
			}
			if m.settingsEditField != "" {
				return m.renderAgentEditInput()
			}
			return m.renderModal(modalTitle)
		}
		if m.modalMode == ModalSettingsSystemEdit {
			if m.settingsSelectorActive {
				return m.renderSettingsSelector(modalTitle)
			}
			if m.settingsEditField != "" {
				return m.renderSystemSettingsEdit(modalTitle)
			}
			return m.renderModal(modalTitle)
		}
		return m.renderModal(modalTitle)
	}

	return m.paintFrame(mainLayout)
}

// renderOnboarding renders the first-run onboarding wizard based on the
// current onboardingStep. Each step builds its own centered layout.
func (m *Model) renderOnboarding() string {
	var b strings.Builder
	width := m.width
	if width == 0 {
		width = 80
	}

	switch m.onboardingStep {
	case obWelcome:
		b.WriteString(m.renderObWelcome(width))
	case obLanguage:
		b.WriteString(m.renderObLanguage(width))
	case obTheme:
		b.WriteString(m.renderObTheme(width))
	case obProviderPicker:
		b.WriteString(m.renderObProviderPicker(width))
	case obConnect:
		b.WriteString(m.renderObConnect(width))
	case obVerify:
		b.WriteString(m.renderObVerify(width))
	case obDone:
		b.WriteString(m.renderObDone(width))
	default:
		// Placeholder for future steps.
		b.WriteString("Coming soon...")
	}

	return b.String()
}

// renderObConnect renders the guided-connect step (step 5 of 6). It delegates
// to the shared /connect form-modal content so the flow stays in sync with
// ModalAddProvider, and wraps it with the onboarding progress dots. The
// renderOnboarding caller's outer paintFrame frames the whole step, so we use
// the unframed form content (renderFormModal paints its own full frame, which
// would double-frame here).
func (m *Model) renderObConnect(width int) string {
	var inner strings.Builder
	inner.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		CommentColorStyle.Render(fmt.Sprintf(i18n.T("tui.onboard.progress"), 5, 6))+"\n"+m.renderProgressDots(5)) + "\n\n")
	inner.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		m.renderFormModalContent(i18n.T("tui.addProvider"), m.formStepNames())))
	return inner.String()
}

// renderObWelcome renders the first onboarding step: the lele ASCII logo, the
// welcome message, progress dots and the keyboard hints. When the user has
// pressed Esc a skip-confirmation is overlaid on top instead.
func (m *Model) renderObWelcome(width int) string {
	var b strings.Builder

	// lele ASCII logo (same as the regular welcome screen).
	logo := "  _      ______ _      ______\n" +
		" | |    |  ____| |    |  ____|\n" +
		" | |    | |__  | |    | |__   \n" +
		" | |    |  __| | |    |  __|\n" +
		" | |____| |____| |____| |____\n" +
		" |______|______|______|______|"
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, WelcomeLogo.Render(logo)) + "\n\n")

	// Welcome message
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, i18n.T("tui.onboard.welcome")) + "\n\n")

	// Progress dots (step 1 of 6)
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		CommentColorStyle.Render(fmt.Sprintf(i18n.T("tui.onboard.progress"), 1, 6))+"\n"+m.renderProgressDots(1)) + "\n\n")

	// Hints
	hint := i18n.T("tui.onboard.pressEnter") + " · " + i18n.T("tui.onboard.escSkip")
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, HelpStyle.Render(hint)) + "\n")

	// Skip confirmation overlay
	if m.obSkipConfirm {
		skip := m.renderSkipConfirm(width)
		var inner strings.Builder
		inner.WriteString(b.String())
		inner.WriteString("\n\n" + skip)
		return inner.String()
	}

	return b.String()
}

// renderSkipConfirm renders the two-option skip confirmation list, using
// obSelectedPreset to highlight the active choice ("Yes, skip" = 0 / "No,
// continue" = 1).
func (m *Model) renderSkipConfirm(width int) string {
	var sb strings.Builder
	sb.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, i18n.T("tui.onboard.skipConfirm")) + "\n\n")

	options := []string{i18n.T("tui.onboard.skipYes"), i18n.T("tui.onboard.skipNo")}
	for i, opt := range options {
		var line string
		if i == m.obSelectedPreset {
			line = ModalItemActive.Render(fmt.Sprintf("> %s", opt))
		} else {
			line = ModalItemInactive.Render(fmt.Sprintf("  %s", opt))
		}
		sb.WriteString(line + "\n")
	}

	return lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, ModalContainer.Render(sb.String()))
}

// renderObLanguage renders the language picker step (step 2 of 6). The list
// mirrors the /lang modal and uses modalSelectedIdx for navigation.
func (m *Model) renderObLanguage(width int) string {
	var b strings.Builder

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, TitleStyle.Render(i18n.T("tui.onboard.language"))) + "\n\n")

	// Progress dots (step 2 of 6)
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		CommentColorStyle.Render(fmt.Sprintf(i18n.T("tui.onboard.progress"), 2, 6))+"\n"+m.renderProgressDots(2)) + "\n\n")

	langs := []string{
		"English",
		"Español",
		"Português",
	}
	var listSb strings.Builder
	for i, lang := range langs {
		if i == m.modalSelectedIdx {
			listSb.WriteString(ModalItemActive.Render(fmt.Sprintf("> %s", lang)) + "\n")
		} else {
			listSb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  %s", lang)) + "\n")
		}
	}
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, ModalContainer.Width(60).Render(listSb.String())) + "\n\n")

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render(i18n.T("tui.onboard.pressEnter")+" · "+i18n.T("tui.onboard.escSkip"))) + "\n")

	return b.String()
}

// renderObTheme renders the theme picker step (step 3 of 6). The list shows
// all built-in themes; the current theme is pre-selected.
func (m *Model) renderObTheme(width int) string {
	var b strings.Builder

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, TitleStyle.Render(i18n.T("tui.onboard.theme"))) + "\n\n")

	// Progress dots (step 3 of 6)
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		CommentColorStyle.Render(fmt.Sprintf(i18n.T("tui.onboard.progress"), 3, 6))+"\n"+m.renderProgressDots(3)) + "\n\n")

	names := theme.Builtins()
	// Find current theme index for default selection
	var listSb strings.Builder
	for i, name := range names {
		if i == m.modalSelectedIdx {
			listSb.WriteString(ModalItemActive.Render(fmt.Sprintf("> %s", name)) + "\n")
		} else {
			listSb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  %s", name)) + "\n")
		}
	}
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, ModalContainer.Width(60).Render(listSb.String())) + "\n\n")

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render(i18n.T("tui.onboard.themeHint"))) + "\n")

	return b.String()
}

// renderObProviderPicker renders the provider preset selection step (step 4 of
// 6). It lists all providerPresets, an "Other / custom" entry and a "Skip for
// now" entry. modalSelectedIdx tracks the highlighted row; each preset shows a
// hint about the expected API key (or "no API key needed" for local models).
func (m *Model) renderObProviderPicker(width int) string {
	var b strings.Builder

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, TitleStyle.Render(i18n.T("tui.onboard.pickProvider"))) + "\n\n")

	// Progress dots (step 4 of 6)
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		CommentColorStyle.Render(fmt.Sprintf(i18n.T("tui.onboard.progress"), 4, 6))+"\n"+m.renderProgressDots(4)) + "\n\n")

	var listSb strings.Builder
	total := len(providerPresets) + 2 // presets + other/custom + skip
	for i := 0; i < total; i++ {
		var label, hint string
		switch {
		case i < len(providerPresets):
			label = providerPresets[i].label
			if strings.EqualFold(providerPresets[i].typ, "ollama") {
				hint = i18n.T("tui.onboard.noKeyNeeded")
			} else {
				hint = fmt.Sprintf(i18n.T("tui.onboard.keyFormat"), providerPresets[i].keyHint)
			}
		case i == len(providerPresets):
			label = i18n.T("tui.onboard.otherCustom")
		default:
			label = i18n.T("tui.onboard.skipForNow")
		}

		line := fmt.Sprintf("> %s", label)
		if hint != "" {
			line += "   " + CommentColorStyle.Render(hint)
		}
		if i == m.modalSelectedIdx {
			listSb.WriteString(ModalItemActive.Render(line) + "\n")
		} else {
			listSb.WriteString(ModalItemInactive.Render(line) + "\n")
		}
	}
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, ModalContainer.Width(60).Render(listSb.String())) + "\n\n")

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render(i18n.T("tui.onboard.pickProviderHint"))) + "\n")

	return b.String()
}

// renderObVerify renders the verification step (step 6 of 6). While the async
// key validation runs it shows a spinner; on failure it shows a warning (the
// key can still be used) and, when skipped via Esc, an explainer that
// verification was skipped.
func (m *Model) renderObVerify(width int) string {
	var b strings.Builder

	// Progress dots (step 6 of 6)
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		CommentColorStyle.Render(fmt.Sprintf(i18n.T("tui.onboard.progress"), 6, 6))+"\n"+m.renderProgressDots(6)) + "\n\n")

	switch {
	case m.obVerifying:
		// Spinner + verifying message.
		spinner := m.getBouncingDots()
		b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
			spinner+"  "+i18n.T("tui.onboard.verifying")) + "\n\n")
	case m.obVerifyFailed:
		// Warning — key may still work.
		b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
			lipgloss.NewStyle().Foreground(YellowColor).Render("⚠  "+i18n.T("tui.onboard.verifyFailed"))) + "\n\n")
		b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
			HelpStyle.Render(i18n.T("tui.onboard.pressEnter"))) + "\n")
	default:
		// Esc-skipped verification.
		b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
			i18n.T("tui.onboard.verifying")) + "\n\n")
		b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
			HelpStyle.Render(i18n.T("tui.onboard.pressEnter"))) + "\n")
	}

	return b.String()
}

// renderObDone renders the success screen (after obVerify). It shows a green
// checkmark, a summary box of the configured provider/model/key, a warning if
// verification failed, and a quick-tips cheat sheet. Setup is otherwise
// complete, so this is the last onboarding step.
func (m *Model) renderObDone(width int) string {
	var b strings.Builder

	// Green checkmark headline.
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		SuccessStyle.Render("✓  "+i18n.T("tui.onboard.done"))) + "\n\n")

	// Progress dots (step 6 of 6)
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		CommentColorStyle.Render(fmt.Sprintf(i18n.T("tui.onboard.progress"), 6, 6))+"\n"+m.renderProgressDots(6)) + "\n\n")

	// Summary box.
	if m.obProviderName != "" {
		var sb strings.Builder
		if m.obProviderName != "" {
			sb.WriteString(CommentColorStyle.Render(i18n.T("tui.onboard.doneProvider")) + ": " + m.obProviderName + "\n")
		}
		if m.obModelName != "" {
			sb.WriteString(CommentColorStyle.Render(i18n.T("tui.onboard.doneModel")) + ": " + m.obModelName + "\n")
		}
		if m.obMaskedKey != "" {
			sb.WriteString(CommentColorStyle.Render(i18n.T("tui.onboard.doneKey")) + ": " + m.obMaskedKey + "\n")
		}
		b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, ModalContainer.Width(60).Render(sb.String())) + "\n\n")
	}

	// Warning if verification failed.
	if m.obVerifyFailed {
		b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
			lipgloss.NewStyle().Foreground(YellowColor).Render("⚠  "+i18n.T("tui.onboard.verifyFailed"))) + "\n\n")
	}

	// Quick-tips cheat sheet.
	var tips strings.Builder
	tips.WriteString(CommentColorStyle.Render(i18n.T("tui.onboard.tips")) + "\n")
	tips.WriteString(HelpStyle.Render(i18n.T("tui.onboard.tipSend")) + "\n")
	tips.WriteString(HelpStyle.Render(i18n.T("tui.onboard.tipModels")) + "\n")
	tips.WriteString(HelpStyle.Render(i18n.T("tui.onboard.tipAgents")) + "\n")
	tips.WriteString(HelpStyle.Render(i18n.T("tui.onboard.tipChats")) + "\n")
	tips.WriteString(HelpStyle.Render(i18n.T("tui.onboard.tipConnect")) + "\n")
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, tips.String()) + "\n\n")

	// Bottom hint.
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		SuccessStyle.Render(i18n.T("tui.onboard.pressEnterStart"))) + "\n")

	return b.String()
}

// renderProgressDots renders the onboarding progress indicator: filled dots
// (●) for completed steps and empty dots (○) for upcoming steps.
func (m *Model) renderProgressDots(step int) string {
	const total = 6
	dots := make([]rune, total)
	for i := 0; i < total; i++ {
		if i < step {
			dots[i] = '●'
		} else {
			dots[i] = '○'
		}
	}
	return SuccessStyle.Render(string(dots))
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
	if viewportHeight < 1 {
		return 1
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
		// When the theme picker is active, use structured items to render
		// section headers differently from selectable items.
		if m.themePickerActive && i < len(m.themePickerItems) {
			tpItem := m.themePickerItems[i]
			if tpItem.kind == "header" {
				// Section headers: centered, dimmed, no selection prefix
				modalSb.WriteString(CommentColorStyle.Render(tpItem.label) + "\n")
				continue
			}
			if tpItem.kind == "loading" || tpItem.kind == "error" {
				// Status messages: dimmed, no selection prefix
				modalSb.WriteString(CommentColorStyle.Render(tpItem.label) + "\n")
				continue
			}
		}
		// Regular selectable items
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

	// Theme picker: show navigation hint at the bottom
	if m.themePickerActive {
		modalSb.WriteString("\n" + HelpStyle.Render("  "+i18n.T("tui.settings.themePickerHint")) + "\n")
	}

	modalView := ModalContainer.Render(modalSb.String())
	return m.paintFrame(modalView)
}

// renderTUISettings renders the Interface settings modal. When an inline edit
// is active (settingsEditField set) it shows the text input for the field;
// otherwise it renders the list of toggleable/editable settings.
func (m *Model) renderTUISettings(modalTitle string) string {
	if m.themePickerActive {
		return m.renderModal(i18n.T("tui.settings.themePickerTitle"))
	}
	if m.settingsEditField != "" {
		var sb strings.Builder
		sb.WriteString(TitleStyle.Render(modalTitle) + "\n\n")

		label := ""
		switch m.settingsEditField {
		case "maxMessages":
			label = i18n.T("tui.settings.maxMessages")
		case "streamThrottle":
			label = i18n.T("tui.settings.streamThrottle")
		}
		m.textInput.Width = 40
		sb.WriteString(ModalItemActive.Render(fmt.Sprintf("  %s: %s", label, m.textInputView())) + "\n")

		if m.formError != "" {
			sb.WriteString("\n" + lipgloss.NewStyle().Foreground(PrimaryColor).Render("  ✗ "+m.formError) + "\n")
		}
		sb.WriteString("\n" + HelpStyle.Render("  "+i18n.T("tui.settings.editHint")))

		modalView := ModalContainer.Render(sb.String())
		return m.paintFrame(modalView)
	}
	// List mode — reuse the standard scrollable modal renderer.
	return m.renderModal(modalTitle)
}

// systemSettingsTitle returns the localized title for the active system
// settings sub-view, or the generic System title when none is active.
func (m *Model) systemSettingsTitle() string {
	switch m.settingsSection {
	case sysSubViewName(sysGroupSession):
		return i18n.T("tui.settings.session")
	case sysSubViewName(sysGroupTools):
		return i18n.T("tui.settings.tools")
	case sysSubViewName(sysGroupLogs):
		return i18n.T("tui.settings.logs")
	case sysSubViewName(sysGroupLanguage):
		return i18n.T("tui.settings.language")
	case sysSubViewName(sysGroupGoal):
		return i18n.T("tui.settings.goal")
	case sysSubViewName(sysGroupUpdates):
		return i18n.T("tui.settings.updates")
	}
	return i18n.T("tui.settings.system")
}

// renderSystemSettingsEdit renders a system settings inline-edit view for the
// currently editing field (settingsEditField). It shows the text input plus an
// optional validation error.
func (m *Model) renderSystemSettingsEdit(title string) string {
	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(title) + "\n\n")

	label := m.settingsEditField
	m.textInput.Width = 40
	sb.WriteString(ModalItemActive.Render(fmt.Sprintf("  %s: %s", label, m.textInputView())) + "\n")

	if m.formError != "" {
		sb.WriteString("\n" + lipgloss.NewStyle().Foreground(PrimaryColor).Render("  ✗ "+m.formError) + "\n")
	}
	sb.WriteString("\n" + HelpStyle.Render("  "+i18n.T("tui.settings.editHint")))

	modalView := ModalContainer.Render(sb.String())
	return m.paintFrame(modalView)
}

// renderAgentEditInput renders the inline edit view for the currently editing
// agent field (settingsEditField set). For delete confirmation it shows the
// confirm prompt instead of a text input. Returns to the list once committed.
func (m *Model) renderAgentEditInput() string {
	title := i18n.T("tui.settings.agents")
	if m.settingsAgentID != "" {
		title = m.settingsAgentID
	}

	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(title) + "\n\n")

	if m.settingsEditField == "confirmDelete" {
		sb.WriteString(ModalItemActive.Render("  "+m.formError) + "\n")
		sb.WriteString(HelpStyle.Render("  " + i18n.T("tui.settings.confirmDeleteHint")))
	} else {
		label := m.settingsEditField
		fieldLabels := map[string]string{
			"agentName":                   "Name",
			"agentDescription":            "Description",
			"agentWorkspace":              "Workspace",
			"agentModel":                  "Model",
			"agentTemperature":            "Temperature",
			"agentSubagentsMaxConcurrent": "Subagents MaxConcurrent",
			"newAgentID":                  "Agent ID",
			"confirmDelete":               "", // handled separately
		}
		if l, ok := fieldLabels[m.settingsEditField]; ok && l != "" {
			label = l
		}
		m.textInput.Width = 60
		sb.WriteString(ModalItemActive.Render(fmt.Sprintf("  %s: %s", label, m.textInputView())) + "\n")
		if m.formError != "" {
			sb.WriteString("\n" + lipgloss.NewStyle().Foreground(PrimaryColor).Render("  ✗ "+m.formError) + "\n")
		}
		sb.WriteString("\n" + HelpStyle.Render("  "+i18n.T("tui.settings.editHint")))
	}

	modalView := ModalContainer.Render(sb.String())
	return m.paintFrame(modalView)
}

// renderFormModal renders a multi-step form modal with step indicators,
// an input field for the current step, and optional error display. It frames
// the modal content with paintFrame.
func (m *Model) renderFormModal(title string, steps []string) string {
	return m.paintFrame(m.renderFormModalContent(title, steps))
}

// renderFormModalContent builds the ModalContainer-wrapped (but unframed)
// content for a form modal. renderFormModal paints it into a full frame; the
// onboarding wizard renders it inline inside its own frame via renderObConnect.
func (m *Model) renderFormModalContent(title string, steps []string) string {
	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(title) + "\n\n")

	// ── Success screen: show what was saved and how to continue ──
	if m.connectSuccess {
		sb.WriteString(SuccessStyle.Render("  "+i18n.T("tui.connectModelSaved")) + "\n\n")

		providerName := ""
		providerType := ""
		modelAlias := ""
		if len(m.formValues) > 0 {
			providerName = m.formValues[0]
		}
		if len(m.formValues) > 1 {
			providerType = m.formValues[1]
		}
		if len(m.formValues) > 4 {
			modelAlias = m.formValues[4]
		}
		sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  %s: %s", i18n.T("tui.connectReviewProvider"), providerName)) + "\n")
		if providerType != "" {
			sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  Type: %s", providerType)) + "\n")
		}
		if modelAlias != "" {
			sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  %s: %s", i18n.T("tui.connectReviewModel"), modelAlias)) + "\n")
		}
		sb.WriteString("\n")
		sb.WriteString(HelpStyle.Render("  " + i18n.T("tui.connectSuccessHint")))
		return ModalContainer.Render(sb.String())
	}

	// ── Provider-type picker: list of known presets ──
	if m.modalMode == ModalAddProvider && m.providerTypePicker {
		sb.WriteString(ModalItemInactive.Render("  "+i18n.T("tui.connectPickType")) + "\n\n")
		max := m.providerTypePickerMax
		if max <= 0 {
			max = len(providerPresets) + 1
		}
		for i := 0; i < max; i++ {
			if i < len(providerPresets) {
				p := providerPresets[i]
				label := p.label
				if p.apiBase != "" {
					label += "  ·  " + p.apiBase
				}
				if i == m.providerTypePickerIdx {
					sb.WriteString(ModalItemActive.Render("  > "+label) + "\n")
				} else {
					sb.WriteString(ModalItemInactive.Render("    "+label) + "\n")
				}
			} else {
				// Last entry: "custom"
				label := i18n.T("tui.connectCustomType")
				if i == m.providerTypePickerIdx {
					sb.WriteString(ModalItemActive.Render("  > "+label) + "\n")
				} else {
					sb.WriteString(ModalItemInactive.Render("    "+label) + "\n")
				}
			}
		}
		sb.WriteString("\n")
		sb.WriteString(HelpStyle.Render("  " + i18n.T("tui.connectPickerHint")))
		return ModalContainer.Render(sb.String())
	}

	isReviewStep := m.modalMode == ModalAddProvider && m.formStepIndex == 9 && m.providerSavedInFlow

	for i, step := range steps {
		// In review mode, show provider steps (0-3) and model steps (4-8) as
		// completed with their values, and the review step (9) as the current
		// active item with a confirmation prompt.
		if isReviewStep && i < 9 {
			val := ""
			if i < len(m.formValues) {
				val = m.formValues[i]
			}
			// Mask secrets (API key) for display — audit M2. Uses the same
			// predicate as the completed-step list so every secret step is
			// covered for both form modals.
			if m.isSecretFormValue(i) {
				val = maskSecretDisplay(val)
			}
			// Add a section header before model steps
			if i == 4 {
				sb.WriteString("\n" + SidebarHeader.Render(i18n.T("tui.connectReviewModel")) + "\n")
			}
			sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  ✓ %s: %s", step, val)) + "\n")
			continue
		}
		if i < m.formStepIndex {
			// Completed step: show checkmark and value
			val := ""
			if i < len(m.formValues) {
				val = m.formValues[i]
			}
			// Audit M2: never render a collected secret in clear text.
			if m.isSecretFormValue(i) {
				val = maskSecretDisplay(val)
			}
			sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  ✓ %s: %s", step, val)) + "\n")
		} else if i == m.formStepIndex {
			if isReviewStep {
				sb.WriteString(ModalItemActive.Render(fmt.Sprintf("  ▶ %s", step)) + "\n")
			} else {
				// Current step: highlighted with input indicator
				val := m.textInput.Value()
				// Audit M2: mask before display — the widget echoes dots but
				// this line prints the raw value. Empty wins the "…"
				// placeholder (masking "" would stay "").
				if m.isSecretInputStep() {
					val = maskSecretDisplay(val)
				}
				if val == "" {
					val = "…"
				}
				sb.WriteString(ModalItemActive.Render(fmt.Sprintf("  ▶ %s: [%s]", step, val)) + "\n")
			}
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

	// Text input field (hidden on review step)
	if !isReviewStep {
		m.textInput.Width = 40
		sb.WriteString(InputBarContainer.Width(44).Render(m.textInputView()) + "\n\n")
	}

	// Contextual step hint (optional fields)
	if m.modalMode == ModalAddProvider && !m.providerSavedInFlow && !m.providerTypePicker && !m.connectSuccess {
		switch m.formStepIndex {
		case 2:
			sb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.connectAPIKeyOptional")) + "\n\n")
		case 3:
			if m.providerTypeFromPreset {
				sb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.connectAPIBasePrefilled")) + "\n\n")
			} else {
				sb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.connectAPIBaseRequired")) + "\n\n")
			}
		}
	}

	// Hints
	if isReviewStep {
		sb.WriteString(HelpStyle.Render("  " + i18n.T("tui.connectReviewHint")))
	} else if m.providerSavedInFlow {
		sb.WriteString(HelpStyle.Render("  " + i18n.T("tui.connectModelStepsHint")))
	} else {
		sb.WriteString(HelpStyle.Render("  " + i18n.T("tui.formEnter")))
	}

	modalView := ModalContainer.Render(sb.String())
	return modalView
}

// formStepNames returns the step names for the current form modal mode.
func (m *Model) formStepNames() []string {
	switch m.modalMode {
	case ModalAddProvider:
		return []string{
			"Provider name", "Provider type", "API Key", "API Base URL",
			"Model alias", "Model name", "Context window", "Max tokens", "Vision (yes/no)",
			i18n.T("tui.connectReview"),
		}
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
	outputContent = sanitizeDisplayText(outputContent)

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
	return m.paintFrame(outputBox)
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
			sb.WriteString(bouncingDotChar)
		} else {
			sb.WriteRune(' ')
		}
	}
	sb.WriteRune(']')
	return sb.String()
}

// renderSkillPicker renders a multi-select modal for choosing skills to install.
func (m *Model) renderSkillPicker(modalTitle string) string {
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

	// Show repo name
	if m.skillsScanRepo != "" {
		modalSb.WriteString(CommentColorStyle.Render("  Repo: "+m.skillsScanRepo) + "\n")
	}
	modalSb.WriteString("\n")

	// Initialize selection map if needed
	if m.skillsSelectedMap == nil {
		m.skillsSelectedMap = make(map[int]bool)
		// Pre-select all by default
		for i := range m.skillsScanResults {
			m.skillsSelectedMap[i] = true
		}
	}

	// Render only the visible window of items
	endIdx := m.modalScrollOffset + maxVisible
	if endIdx > len(m.skillsScanResults) {
		endIdx = len(m.skillsScanResults)
	}

	for i := m.modalScrollOffset; i < endIdx; i++ {
		if i >= len(m.skillsScanResults) {
			break
		}
		skill := m.skillsScanResults[i]
		selected := m.skillsSelectedMap[i]
		item := formatPickerItem(skill.Name, skill.Description, selected)

		if i == m.modalSelectedIdx {
			modalSb.WriteString(ModalItemActive.Render("> "+item) + "\n")
		} else {
			modalSb.WriteString(ModalItemInactive.Render("  "+item) + "\n")
		}
	}

	// Show error if any
	if m.formError != "" {
		modalSb.WriteString("\n" + lipgloss.NewStyle().Foreground(PrimaryColor).Render("  ✗ "+m.formError) + "\n")
	}

	// Hints
	modalSb.WriteString("\n" + CommentColorStyle.Render("  [Space] Toggle  [Enter] Install  [Esc] Back") + "\n")

	modalView := ModalContainer.Render(modalSb.String())
	return m.paintFrame(modalView)
}
