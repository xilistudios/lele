package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func (m *Model) executeCommand(cmd string) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return
	}

	switch parts[0] {
	case "/sessions":
		m.resetModal(ModalSessions)
		allSessions := m.sessionMgr.ListSessions()
		for _, s := range allSessions {
			name := s.Name
			if name == "" {
				name = i18n.T("tui.newChatDefault")
			}
			count := len(s.Messages)

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

	case "/new":
		m.createNewChat()
		m.showWelcome = true
		m.reloadSessions()

	case "/agents":
		m.resetModal(ModalAgent)
		m.modalItems = m.agentLoop.GetProvidable().ListAvailableAgentIDs()

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

	case "/clear":
		m.agentLoop.GetProvidable().ClearSession(m.currentKey)
		if m.pendingModel != "" && m.currentKey != "" {
			m.agentLoop.GetProvidable().SetSessionModel(m.currentKey, m.pendingModel)
		}
		m.reloadSessions()

	case "/think":
		m.resetModal(ModalThink)
		m.modalItems = []string{"off", "low", "medium", "high"}

	case "/lang":
		m.resetModal(ModalLang)
		// Show language names with codes
		m.modalItems = []string{
			"Español (es)",
			"English (en)",
			"Português (pt)",
		}

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

	case "/quit":
		m.printSessionSummary()
		m.cancel()
		os.Exit(0)
	}
}
