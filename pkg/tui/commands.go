package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func (m *Model) executeCommand(cmd string) tea.Cmd {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}
	parts[0] = normalizeCommandKey(parts[0])

	if m.isKnownCommand(parts[0]) {
		m.cmdUsage.Record(parts[0])
	}

	switch parts[0] {
	case "/sessions":
		m.resetModal(ModalSessions)
		allSessions := m.freshSessionListing()
		for _, s := range allSessions {
			if isSubagentSessionKey(s.Key) {
				continue
			}
			sessionMode := s.Mode
			if sessionMode == "" {
				sessionMode = "agent"
			}
			if sessionMode != m.currentMode.String() {
				continue
			}
			name := s.Name
			if name == "" {
				name = i18n.T("tui.newChatDefault")
			}

			isProcessing := m.agentLoop.GetProvidable().IsSessionProcessing(s.Key)

			item := name
			if isProcessing {
				item = fmt.Sprintf("[%s] %s", i18n.T("tui.loading"), item)
			}
			m.modalItems = append(m.modalItems, item)
			m.modalSessionKeys = append(m.modalSessionKeys, s.Key)
		}
		return nil

	case "/new":
		m.createNewChat()
		m.showWelcome = true
		m.reloadSessions()
		return nil

	case "/agents":
		m.resetModal(ModalAgent)
		m.modalItems = m.agentLoop.GetProvidable().ListAvailableAgentIDs()
		if len(m.modalItems) == 0 {
			m.modalItems = append(m.modalItems, i18n.T("tui.noneConfigured"))
		}
		return nil

	case "/mode":
		m.handleModeCommand(parts[1:])
		return nil

	case "/models":
		m.resetModal(ModalModel)
		cfgSnapshot := m.agentLoop.GetProvidable().GetConfigSnapshot()
		if cfgSnapshot != nil && cfgSnapshot.Providers != nil {
			providersMap := cfgSnapshot.Providers.ListNamed()
			var pNames []string
			for k := range providersMap {
				pNames = append(pNames, k)
			}
			sort.Strings(pNames)
			for _, pName := range pNames {
				pCfg := providersMap[pName]
				var aliases []string
				for mAlias := range pCfg.Models {
					aliases = append(aliases, mAlias)
				}
				sort.Strings(aliases)
				for _, mAlias := range aliases {
					m.modalItems = append(m.modalItems, fmt.Sprintf("%s:%s", pName, mAlias))
				}
			}
		}
		if len(m.modalItems) == 0 {
			m.modalItems = append(m.modalItems, i18n.T("tui.noneConfigured"))
		}
		if m.currentKey != "" {
			curModel := m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
			if curModel != "" && cfgSnapshot != nil && cfgSnapshot.Providers != nil {
				for i, item := range m.modalItems {
					if cfgSnapshot.Providers.ResolveModelAlias(item, "") == curModel {
						m.modalSelectedIdx = i
						break
					}
				}
			}
		}
		return nil

	case "/clear":
		m.agentLoop.GetProvidable().ClearSession(m.currentKey)
		if m.pendingModel != "" && m.currentKey != "" {
			m.agentLoop.GetProvidable().SetSessionModel(m.currentKey, m.pendingModel)
		}
		m.resetArchivedHistory()
		m.reloadSessions()
		return nil

	case "/clearq":
		n := m.queueDepth()
		m.clearQueue()
		m.queueFeedback = fmt.Sprintf(i18n.T("tui.queue.dropped"), n)
		return nil

	case "/flushq":
		return m.forceSendNextQueued()

	case "/think":
		m.resetModal(ModalThink)
		m.modalItems = append([]string(nil), config.ThinkingLevels...)
		return nil

	case "/lang":
		m.resetModal(ModalLang)
		m.modalItems, m.modalLangCodes = buildLanguageModalItems(m.cfg.Language)
		return nil

	case "/subagents":
		m.resetModal(ModalSubagents)
		subagentQueryKey := m.currentKey
		if !strings.HasPrefix(subagentQueryKey, "native:") {
			subagentQueryKey = "native:" + subagentQueryKey
		}
		subagents := m.agentLoop.GetProvidable().GetSessionSubagents(subagentQueryKey)
		if len(subagents) == 0 {
			m.modalItems = append(m.modalItems, i18n.T("tui.noSubagents"))
		} else {
			sortSubagents(subagents)
			for _, sa := range subagents {
				label := sa.Label
				if label == "" {
					label = sa.TaskID
				}
				m.modalItems = append(m.modalItems, fmt.Sprintf("%s [%s] %s", sa.TaskID, sa.Status, label))
				m.modalSubagentKeys = append(m.modalSubagentKeys, sa.SessionKey)
			}
		}
		return nil

	case "/bg":
		m.resetModal(ModalBackgroundExecs)
		procs := m.agentLoop.GetProvidable().GetBackgroundExecs(true)
		if len(procs) == 0 {
			m.modalItems = append(m.modalItems, i18n.T("tui.noBgProcesses"))
		} else {
			for _, p := range procs {
				elapsed := time.Duration(p.Elapsed) * time.Millisecond
				item := fmt.Sprintf("%s [%s] %s (%s)", p.ID, p.Status, p.Command, elapsed.Round(time.Second))
				m.modalItems = append(m.modalItems, item)
				m.bgExecModalKeys = append(m.bgExecModalKeys, p.ID)
			}
		}
		return nil

	case "/cron":
		m.resetModal(ModalCron)
		m.loadCronJobs()
		return nil

	case "/secrets":
		m.resetModal(ModalSecrets)
		m.loadSecrets()
		return nil

	case "/skills":
		m.resetModal(ModalSkills)
		m.skillsModalKeys = nil
		m.skillsSelectedMap = nil
		m.skillsScanResults = nil
		m.skillsScanRepo = ""
		m.skillsFeedback = ""
		m.loadSkillsList()
		return nil

	case "/mcp":
		m.resetModal(ModalMCP)
		m.loadMCPList()
		return nil

	case "/commands":
		m.resetModal(ModalCommands)
		m.commandsModalKeys = nil
		m.commandsRows = nil
		m.commandsFeedback = ""
		m.loadCommandsList()
		return nil

	case "/settings":
		m.resetModal(ModalSettings)
		m.modalItems = []string{
			i18n.T("tui.settings.agents"),
			i18n.T("tui.settings.system"),
			i18n.T("tui.settings.interface"),
		}
		return nil

	case "/providers":
		m.resetModal(ModalProviders)
		m.providerModalKeys = nil
		m.providerSelectedName = ""
		m.providerEditMode = false
		m.formStepIndex = 0
		m.formValues = nil
		m.formError = ""
		m.formConfirmMode = false
		providers := m.listProviders()
		if len(providers) == 0 {
			m.modalItems = append(m.modalItems, i18n.T("tui.noProviders"))
		} else {
			for _, name := range providers {
				models := m.listProviderModels(name)
				item := fmt.Sprintf("%s (%d models)", name, len(models))
				m.modalItems = append(m.modalItems, item)
				m.providerModalKeys = append(m.providerModalKeys, name)
			}
		}
		m.modalItems = append(m.modalItems, "---")
		m.modalItems = append(m.modalItems, i18n.T("tui.connectAction"))
		return nil

	case "/connect":
		m.startConnectFlow(nil)
		return nil

	case "/add-model":
		m.resetModal(ModalAddModel)
		m.formStepIndex = 0
		m.formValues = make([]string, addModelStepCount)
		m.formError = ""
		m.formConfirmMode = false
		m.textInput.SetValue("")
		m.textInput.Placeholder = "Model alias (e.g. gpt-4o)"
		return nil

	case "/compact":
		if m.currentKey == "" {
			return nil
		}
		m.chatInput.SetValue("")
		m.compactFeedback = ""
		m.processing = true
		m.startTime = time.Now()
		m.elapsedTime = 0
		m.currentStream = ""
		m.currentThinking = ""
		m.currentToolAction = i18n.T("tui.compacting")
		m.reloadSessions()
		key := m.currentKey
		compactCmd := func() tea.Msg {
			result := m.agentLoop.GetProvidable().CompactSession(key)
			return compactResultMsg{result: result, sessionKey: key}
		}
		return tea.Batch(compactCmd, m.tickCmd())

	case "/status":
		if m.currentKey == "" {
			m.createNewChat()
			m.showWelcome = false
		}
		m.compactFeedback = ""
		m.statusFeedback = m.buildStatusReport()
		m.updateViewport()
		return nil

	case "/goal":
		if m.currentKey == "" {
			m.createNewChat()
			m.showWelcome = false
		}
		m.compactFeedback = ""
		result := m.agentLoop.HandleGoalCommand(m.currentKey, parts[1:])
		m.updateViewport()

		if isGoalSetCommand(parts[1:]) {
			goalText := strings.Join(parts[1:], " ")
			if idx := strings.Index(goalText, "--turns"); idx > 0 {
				goalText = strings.TrimSpace(goalText[:idx])
			}
			m.currentToolAction = "🎯 Goal set: " + sanitizeDisplayText(goalText)
			m.processing = true
			m.startTime = time.Now()
			m.elapsedTime = 0
			m.currentStream = ""
			m.currentThinking = ""
			return m.tickCmd()
		}
		m.currentToolAction = sanitizeDisplayText(result)
		return nil

	case "/quit":
		m.cancel()
		return tea.Quit
	}

	if m.isCustomCommand(parts[0]) {
		content := strings.TrimSpace(cmd)
		if m.processing {
			return m.enqueueCurrentInputWhileBusy(content)
		}
		return m.publishUserMessage(content)
	}
	return nil
}

func (m *Model) startConnectFlow(preset *providerPreset) {
	m.resetModal(ModalAddProvider)
	m.providerEditMode = false
	m.providerSelectedName = ""
	m.providerSavedInFlow = false
	m.formError = ""
	m.formConfirmMode = false
	m.providerTypePicker = false
	m.providerTypePickerIdx = 0
	m.providerTypePickerMax = 0
	m.connectSuccess = false
	m.providerTypeFromPreset = false
	m.connectRowPicker = false
	m.connectRowIdx = -1
	m.connectSkipAPIBase = false
	m.connectAlreadyUsable = false
	m.formValues = make([]string, 11)

	if preset != nil {
		m.formValues[0] = preset.label
		m.formValues[1] = preset.typ
		m.formValues[3] = preset.apiBase
		m.providerTypeFromPreset = true
		m.formStepIndex = 2
		m.providerSelectedName = strings.ToLower(strings.TrimSpace(preset.label))
		if preset.typ == "ollama" {
			m.formStepIndex = 3
		}
		m.textInput.SetValue("")
		m.textInput.Placeholder = "API Key"
		if preset.keyHint != "" {
			m.textInput.Placeholder = "API Key (" + preset.keyHint + ")"
		}
		if preset.typ == "ollama" {
			m.textInput.SetValue(preset.apiBase)
			m.textInput.Placeholder = "API Base URL (default for this provider)"
		}
	} else {
		m.formStepIndex = 0
		m.providerTypePicker = true
		m.connectRowPicker = true
		m.providerTypePickerIdx = 0
		m.modalScrollOffset = 0
		m.providerTypePickerMax = len(connectRows())
		m.textInput.SetValue("")
		m.textInput.Placeholder = ""
	}
	m.syncTextInputEcho()
}

func isGoalSetCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "status", "pause", "resume", "clear":
		return false
	}
	return true
}

func (m *Model) buildStatusReport() string {
	current, window, cumInput, cumOutput := m.getTokenUsage(m.getHistoryMessageCount())
	compactions := m.agentLoop.GetProvidable().GetCompactionCount(m.currentKey)

	var sb strings.Builder
	sb.WriteString(i18n.T("tui.context") + "\n")
	sb.WriteString(i18n.T("tui.currentContext") + ": " + formatNumber(current) + "\n")
	sb.WriteString(i18n.T("tui.contextWindow") + ": " + formatNumber(window) + "\n")
	if window > 0 {
		pct := float64(current) / float64(window) * 100
		sb.WriteString(fmt.Sprintf(i18n.T("tui.used"), pct) + "\n")
	}
	sb.WriteString(i18n.T("tui.inputSent") + ": " + formatNumber(cumInput) + "\n")
	sb.WriteString(i18n.T("tui.outputReceived") + ": " + formatNumber(cumOutput) + "\n")
	sb.WriteString(i18n.T("tui.totalSent") + ": " + formatNumber(cumInput+cumOutput) + "\n")
	sb.WriteString(i18n.T("tui.compactions") + ": " + fmt.Sprintf("%d", compactions))

	return sb.String()
}
