package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func (m *Model) executeCommand(cmd string) tea.Cmd {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	switch parts[0] {
	case "/sessions":
		m.resetModal(ModalSessions)
		allSessions := m.sessionMgr.ListSessions()
		// Batch-fetch message counts once (single SQLite query for cold
		// sessions) instead of calling GetTotalMessageCount per session.
		msgCounts := m.sessionMgr.AllTotalMessageCounts()
		for _, s := range allSessions {
			// Exclude subagent sessions from the session list — they have
			// their own navigation via /subagents and are not top-level chats.
			if isSubagentSessionKey(s.Key) {
				continue
			}
			// Filter by current mode (empty mode = "agent" for backward compat).
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
			count := msgCounts[s.Key]

			// Check if session is currently processing
			isProcessing := m.agentLoop.GetProvidable().IsSessionProcessing(s.Key)

			// Format session item with message count
			item := name
			if count > 0 {
				item = fmt.Sprintf("%s (%d msgs)", name, count)
			}
			// Prefix with loading indicator if session is currently processing
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
		// Pre-select the current session's model so the cursor lands on it.
		// GetSessionModel returns the resolved provider:modelID value, while
		// modalItems are provider:alias entries, so resolve each item before
		// comparing.
		if m.currentKey != "" {
			curModel := m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
			if curModel != "" && cfgSnapshot != nil && cfgSnapshot.Providers != nil {
				for i, item := range m.modalItems {
					if cfgSnapshot.Providers.ResolveModelAlias(item, "") == curModel {
						m.modalSelectedIdx = i
						// Keep scroll offset at 0; the view layer already
						// clamps the visible window around the selection.
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
		m.reloadSessions()
		return nil

	case "/think":
		m.resetModal(ModalThink)
		m.modalItems = []string{"off", "low", "medium", "high"}
		return nil

	case "/lang":
		m.resetModal(ModalLang)
		// Show language names with codes
		m.modalItems = []string{
			"Español (es)",
			"English (en)",
			"Português (pt)",
		}
		return nil

	case "/subagents":
		m.resetModal(ModalSubagents)
		// Subagent tasks store OriginSessionKey as "native:<chatID>", so we
		// must pass the prefixed key for the lookup to match correctly.
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
		m.resetModal(ModalAddProvider)
		m.providerEditMode = false
		m.providerSelectedName = ""
		m.providerSavedInFlow = false
		m.formStepIndex = 0
		m.formValues = make([]string, 10) // 0-3: provider, 4-8: model, 9: review
		m.formError = ""
		m.formConfirmMode = false
		m.providerTypePicker = false
		m.providerTypePickerIdx = 0
		m.providerTypePickerMax = 0
		m.connectSuccess = false
		m.providerTypeFromPreset = false
		m.textInput.SetValue("")
		m.textInput.Placeholder = "Provider name (e.g. openai)"
		return nil

	case "/add-model":
		m.resetModal(ModalAddModel)
		m.formStepIndex = 0
		m.formValues = make([]string, 5) // alias, model_name, context_window, max_tokens, vision
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

	case "/goal":
		// If we're on the welcome screen with no session, create one now —
		// the same behavior as submitMessage and submitGroupStart.
		if m.currentKey == "" {
			m.createNewChat()
			m.showWelcome = false
		}
		m.compactFeedback = ""
		result := m.agentLoop.HandleGoalCommand(m.currentKey, parts[1:])
		m.updateViewport()

		// Setting a new goal (as opposed to status/pause/resume/clear) kicks
		// off an autonomous agent turn via the message bus. Mark the session
		// as processing and start the tick chain so the loading indicator
		// appears and stays in sync with the backend.
		if isGoalSetCommand(parts[1:]) {
			goalText := strings.Join(parts[1:], " ")
			// Strip --turns flag from display
			if idx := strings.Index(goalText, "--turns"); idx > 0 {
				goalText = strings.TrimSpace(goalText[:idx])
			}
			m.currentToolAction = "🎯 Goal set: " + goalText
			m.processing = true
			m.startTime = time.Now()
			m.elapsedTime = 0
			m.currentStream = ""
			m.currentThinking = ""
			return m.tickCmd()
		}
		// For status/pause/resume/clear, show result as tool action briefly
		m.currentToolAction = result
		return nil

	case "/quit":
		m.cancel()
		return tea.Quit
	}
	return nil
}

// isGoalSetCommand reports whether the /goal arguments set a new goal
// (free-form goal text) as opposed to invoking a subcommand
// (status/pause/resume/clear) or showing usage. Only a new goal triggers
// the autonomous kickoff turn, so only then should the TUI enter the
// processing state.
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
