package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/locales"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

const (
	sysGroupSession  = 0
	sysGroupTools    = 1
	sysGroupLogs     = 2
	sysGroupLanguage = 3
	sysGroupGoal     = 4
	sysGroupUpdates  = 5
	sysGroupGateway  = 6
	sysGroupKeyring  = 7
)

const (
	sessionFieldEphemeral = iota
	sessionFieldThreshold
	sessionFieldCompactionPercent
	sessionFieldCompactionModel
	sessionFieldEvictExcluded
	sessionFieldDurableInbound
	sessionFieldDurableOutbound
	sessionFieldResume
	sessionFieldDMScope
)

const (
	goalFieldJudgeMode = iota
	goalFieldJudgeAgent
)

const (
	logsFieldEnabled = iota
	logsFieldPath
	logsFieldMaxDays
	logsFieldRotation
)

const (
	toolsFieldExecTimeout = iota
	toolsFieldDenyPatterns
	toolsFieldCustomDenyPatterns
	toolsFieldWhitelistCommands
	toolsFieldCronExecTimeout
)

const (
	updatesFieldEnabled = iota
	updatesFieldChannel
	updatesFieldRepo
)

const (
	gatewayFieldHost = iota
	gatewayFieldPort
)

const (
	keyringFieldEnabled = iota
	keyringFieldPath
	keyringFieldBackend
	keyringFieldAuditLogSize
	keyringFieldAllowAgentSet
	keyringFieldAllowAgentDelete
)

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func checkMark(cond bool) string {
	if cond {
		return "✓"
	}
	return ""
}

func sysSubViewName(idx int) string {
	return fmt.Sprintf("sys_%d", idx)
}

func sysGroupFromSection(section string) int {
	if !strings.HasPrefix(section, "sys_") {
		return -1
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(section, "sys_"))
	if err != nil {
		return -1
	}
	return idx
}

func (m *Model) loadSystemSettings() {
	m.modalItems = []string{
		i18n.T("tui.settings.session"),
		i18n.T("tui.settings.tools"),
		i18n.T("tui.settings.logs"),
		i18n.T("tui.settings.language"),
		i18n.T("tui.settings.goal"),
		i18n.T("tui.settings.updates"),
		i18n.T("tui.settings.gateway"),
		i18n.T("tui.settings.keyring"),
	}
}

func (m *Model) loadSessionSettings() {
	m.modalItems = []string{
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.ephemeral"), boolStatus(m.cfg.Session.Ephemeral)),
		fmt.Sprintf("%s: %ds", i18n.T("tui.settings.ephemeralThreshold"), m.cfg.Session.EphemeralThreshold),
		fmt.Sprintf("%s: %d%%", i18n.T("tui.settings.compactionPercent"), m.cfg.Session.CompactionThresholdPercent),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.compactionModel"), valueOr(m.cfg.Session.CompactionModel, "default")),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.evictExcluded"), boolStatus(m.cfg.Session.EvictExcludedFromMemory)),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.durableInbound"), boolStatus(m.cfg.Session.DurableInboundEnabled())),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.durableOutbound"), boolStatus(m.cfg.Session.DurableOutboundEnabled())),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.resume"), boolStatus(m.cfg.Session.ResumeEnabled())),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.dmScope"), valueOr(m.cfg.Session.DMScope, "default")),
	}
}

func (m *Model) loadToolsSettings() {
	m.modalItems = []string{
		fmt.Sprintf("%s: %ds", i18n.T("tui.settings.execTimeout"), m.cfg.Tools.Exec.TimeoutSeconds),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.denyPatterns"), boolStatus(m.cfg.Tools.Exec.EnableDenyPatterns)),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.customDenyPatterns"), valueOr(strings.Join(m.cfg.Tools.Exec.CustomDenyPatterns, ", "), "none")),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.whitelistCommands"), valueOr(strings.Join(m.cfg.Tools.Exec.WhitelistCommands, ", "), "none")),
		fmt.Sprintf("%s: %dm", i18n.T("tui.settings.cronExecTimeout"), m.cfg.Tools.Cron.ExecTimeoutMinutes),
	}
}

func (m *Model) loadLogsSettings() {
	m.modalItems = []string{
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.logsEnabled"), boolStatus(m.cfg.Logs.Enabled)),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.logsPath"), valueOr(m.cfg.Logs.Path, "default")),
		fmt.Sprintf("%s: %d", i18n.T("tui.settings.logsMaxDays"), m.cfg.Logs.MaxDays),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.logsRotation"), valueOr(m.cfg.Logs.Rotation, "daily")),
	}
}

const languageDownloadMoreItem = "tui.languages.downloadMore"

func buildLanguageModalItems(current string) (labels []string, codes []string) {
	langs := i18n.InstalledLanguages()
	for _, code := range langs {
		labels = append(labels, i18n.FormatLanguageOption(code, ""))
		codes = append(codes, code)
	}
	labels = append(labels, "⬇ "+i18n.T("tui.languages.downloadMore"))
	codes = append(codes, "")
	return labels, codes
}

func remoteLanguageLabels(statuses []locales.Status) (labels []string, codes []string) {
	installed := map[string]bool{}
	for _, c := range i18n.InstalledLanguages() {
		installed[c] = true
	}
	for _, s := range statuses {
		if s.Installed || installed[s.Code] {
			continue
		}
		name := s.NativeName
		if name == "" {
			name = s.Name
		}
		if name == "" {
			name = s.Code
		}
		labels = append(labels, fmt.Sprintf("%s (%s) — %s", name, s.Code, i18n.T("tui.languages.install")))
		codes = append(codes, s.Code)
	}
	return labels, codes
}

func (m *Model) loadLanguageSettings() {
	m.modalItems = nil
	m.modalLangCodes = nil
	for _, code := range i18n.InstalledLanguages() {
		m.modalItems = append(m.modalItems, i18n.FormatLanguageOption(code, checkMark(m.cfg.Language == code)))
		m.modalLangCodes = append(m.modalLangCodes, code)
	}
	m.modalItems = append(m.modalItems, "⬇ "+i18n.T("tui.languages.downloadMore"))
	m.modalLangCodes = append(m.modalLangCodes, "")
}

func (m *Model) loadGoalSettings() {
	m.modalItems = []string{
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.goalJudgeMode"), valueOr(m.cfg.Goal.Judge.Mode, "inline")),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.goalJudgeAgent"), valueOr(m.cfg.Goal.Judge.Agent, "default")),
	}
}

func (m *Model) loadUpdatesSettings() {
	m.modalItems = []string{
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.updatesEnabled"), boolStatus(m.cfg.Updates.Enabled)),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.updatesChannel"), valueOr(m.cfg.Updates.Channel, "stable")),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.updatesRepo"), valueOr(m.cfg.Updates.Repo, "default")),
	}
}

func (m *Model) loadGatewaySettings() {
	m.modalItems = []string{
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.gatewayHost"), valueOr(m.cfg.Gateway.Host, "default")),
		fmt.Sprintf("%s: %d", i18n.T("tui.settings.gatewayPort"), m.cfg.Gateway.Port),
	}
}

func (m *Model) loadKeyringSettings() {
	m.modalItems = []string{
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.keyringEnabled"), boolStatus(m.cfg.Keyring.Enabled)),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.keyringPath"), valueOr(m.cfg.Keyring.Path, "default")),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.keyringBackend"), valueOr(m.cfg.Keyring.Backend, "default")),
		fmt.Sprintf("%s: %d", i18n.T("tui.settings.keyringAuditLogSize"), m.cfg.Keyring.AuditLogSize),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.keyringAllowAgentSet"), boolStatus(m.cfg.Keyring.AllowAgentSet)),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.keyringAllowAgentDelete"), boolStatus(m.cfg.Keyring.AllowAgentDelete)),
	}
}

func (m *Model) handleSystemSubEnter() tea.Cmd {
	switch m.settingsSection {
	case sysSubViewName(sysGroupSession):
		return m.handleSessionEnter()
	case sysSubViewName(sysGroupTools):
		return m.handleToolsEnter()
	case sysSubViewName(sysGroupLogs):
		return m.handleLogsEnter()
	case sysSubViewName(sysGroupLanguage):
		return m.handleLanguageEnter()
	case sysSubViewName(sysGroupGoal):
		return m.handleGoalEnter()
	case sysSubViewName(sysGroupUpdates):
		return m.handleUpdatesEnter()
	case sysSubViewName(sysGroupGateway):
		return m.handleGatewayEnter()
	case sysSubViewName(sysGroupKeyring):
		return m.handleKeyringEnter()
	}
	return nil
}

func (m *Model) handleSessionEnter() tea.Cmd {
	s := &m.cfg.Session
	switch m.modalSelectedIdx {
	case sessionFieldEphemeral:
		s.Ephemeral = !s.Ephemeral
		m.persistSystemSettings()
	case sessionFieldThreshold:
		m.startNumberEdit("ephemeralThreshold", s.EphemeralThreshold)
	case sessionFieldCompactionPercent:
		m.settingsEditField = "compactionPercent"
		m.textInput.SetValue(strconv.Itoa(s.CompactionThresholdPercent))
		m.textInput.Focus()
	case sessionFieldCompactionModel:
		m.startCompactionModelEdit()
	case sessionFieldEvictExcluded:
		s.EvictExcludedFromMemory = !s.EvictExcludedFromMemory
		m.persistSystemSettings()
	case sessionFieldDurableInbound:
		enabled := !s.DurableInboundEnabled()
		s.DurableInbound = &enabled
		m.persistSystemSettings()
	case sessionFieldDurableOutbound:
		enabled := !s.DurableOutboundEnabled()
		s.DurableOutbound = &enabled
		m.persistSystemSettings()
	case sessionFieldResume:
		enabled := !s.ResumeEnabled()
		s.Resume = &enabled
		m.persistSystemSettings()
	case sessionFieldDMScope:
		labels := []string{"(default)", "main", "per-peer", "per-channel-peer", "per-account-channel-peer"}
		values := []string{"", "main", "per-peer", "per-channel-peer", "per-account-channel-peer"}
		m.startSettingsSelector("dmScope", s.DMScope, labels, values)
	}
	return nil
}

func (m *Model) startCompactionModelEdit() {
	labels, values := m.modelSelectorOptions(m.cfg.Session.CompactionModel)
	if len(values) == 0 {
		m.settingsEditField = "compactionModel"
		m.textInput.SetValue(m.cfg.Session.CompactionModel)
		m.textInput.Focus()
		return
	}
	m.startSettingsSelector("compactionModel", m.cfg.Session.CompactionModel, labels, values)
}

func (m *Model) persistSystemSettings() {
	m.formError = ""
	m.saveConfigToDisk()
	m.reloadSystemSubView()
}

func (m *Model) handleToolsEnter() tea.Cmd {
	tools := &m.cfg.Tools
	switch m.modalSelectedIdx {
	case toolsFieldExecTimeout:
		m.startNumberEdit("execTimeout", tools.Exec.TimeoutSeconds)
	case toolsFieldDenyPatterns:
		tools.Exec.EnableDenyPatterns = !tools.Exec.EnableDenyPatterns
		m.persistSystemSettings()
	case toolsFieldCustomDenyPatterns:
		m.settingsEditField = "customDenyPatterns"
		m.textInput.SetValue(strings.Join(tools.Exec.CustomDenyPatterns, ", "))
		m.textInput.Focus()
	case toolsFieldWhitelistCommands:
		m.settingsEditField = "whitelistCommands"
		m.textInput.SetValue(strings.Join(tools.Exec.WhitelistCommands, ", "))
		m.textInput.Focus()
	case toolsFieldCronExecTimeout:
		m.startNumberEdit("cronExecTimeout", tools.Cron.ExecTimeoutMinutes)
	}
	return nil
}

func (m *Model) handleLogsEnter() tea.Cmd {
	logs := &m.cfg.Logs
	switch m.modalSelectedIdx {
	case logsFieldEnabled:
		logs.Enabled = !logs.Enabled
		m.persistSystemSettings()
	case logsFieldPath:
		m.settingsEditField = "logsPath"
		m.textInput.SetValue(logs.Path)
		m.textInput.Focus()
	case logsFieldMaxDays:
		m.startNumberEdit("logsMaxDays", logs.MaxDays)
	case logsFieldRotation:
		labels := []string{"daily", "weekly"}
		m.startSettingsSelector("logsRotation", logs.Rotation, labels, labels)
	}
	return nil
}

func (m *Model) handleLanguageEnter() tea.Cmd {
	if m.modalSelectedIdx >= len(m.modalLangCodes) {
		return nil
	}
	code := m.modalLangCodes[m.modalSelectedIdx]
	if code == "" {
		return m.openRemoteLanguageBrowser()
	}
	m.cfg.Language = code
	m.saveConfigToDisk()
	i18n.SetLanguage(code)
	m.loadLanguageSettings()
	return nil
}

func (m *Model) handleGoalEnter() tea.Cmd {
	switch m.modalSelectedIdx {
	case goalFieldJudgeMode:
		labels := []string{"inline", "subagent"}
		m.startSettingsSelector("goalJudgeMode", m.cfg.Goal.Judge.Mode, labels, labels)
	case goalFieldJudgeAgent:
		labels := []string{"(default)"}
		values := []string{""}
		for _, a := range m.cfg.Agents.List {
			label := a.ID
			if a.Name != "" {
				label = fmt.Sprintf("%s (%s)", a.Name, a.ID)
			}
			labels = append(labels, label)
			values = append(values, a.ID)
		}
		m.startSettingsSelector("goalJudgeAgent", m.cfg.Goal.Judge.Agent, labels, values)
	}
	return nil
}

func (m *Model) handleUpdatesEnter() tea.Cmd {
	updates := &m.cfg.Updates
	switch m.modalSelectedIdx {
	case updatesFieldEnabled:
		updates.Enabled = !updates.Enabled
		m.persistSystemSettings()
	case updatesFieldChannel:
		labels := []string{"stable"}
		m.startSettingsSelector("updatesChannel", updates.Channel, labels, labels)
	case updatesFieldRepo:
		m.settingsEditField = "updatesRepo"
		m.textInput.SetValue(updates.Repo)
		m.textInput.Focus()
	}
	return nil
}

func (m *Model) handleGatewayEnter() tea.Cmd {
	gw := &m.cfg.Gateway
	switch m.modalSelectedIdx {
	case gatewayFieldHost:
		m.settingsEditField = "gatewayHost"
		m.textInput.SetValue(gw.Host)
		m.textInput.Focus()
	case gatewayFieldPort:
		m.startNumberEdit("gatewayPort", gw.Port)
	}
	return nil
}

func (m *Model) handleKeyringEnter() tea.Cmd {
	kr := &m.cfg.Keyring
	switch m.modalSelectedIdx {
	case keyringFieldEnabled:
		kr.Enabled = !kr.Enabled
		m.persistSystemSettings()
	case keyringFieldPath:
		m.settingsEditField = "keyringPath"
		m.textInput.SetValue(kr.Path)
		m.textInput.Focus()
	case keyringFieldBackend:
		labels := []string{"(default)", "auto", "file", "keychain"}
		values := []string{"", "auto", "file", "keychain"}
		m.startSettingsSelector("keyringBackend", kr.Backend, labels, values)
	case keyringFieldAuditLogSize:
		m.startNumberEdit("keyringAuditLogSize", kr.AuditLogSize)
	case keyringFieldAllowAgentSet:
		kr.AllowAgentSet = !kr.AllowAgentSet
		m.persistSystemSettings()
	case keyringFieldAllowAgentDelete:
		kr.AllowAgentDelete = !kr.AllowAgentDelete
		m.persistSystemSettings()
	}
	return nil
}

func (m *Model) handleSystemSettingsInput(value string) {
	value = strings.TrimSpace(value)

	switch m.settingsEditField {
	case "ephemeralThreshold":
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
			return
		}
		m.cfg.Session.EphemeralThreshold = v
	case "compactionPercent":
		v, ok := m.parsePercentField(value)
		if !ok {
			return
		}
		m.cfg.Session.CompactionThresholdPercent = v
	case "compactionModel":
		m.cfg.Session.CompactionModel = value
	case "dmScope":
		m.cfg.Session.DMScope = value
	case "execTimeout":
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
			return
		}
		m.cfg.Tools.Exec.TimeoutSeconds = v
	case "customDenyPatterns":
		m.cfg.Tools.Exec.CustomDenyPatterns = parseCSVList(value)
	case "whitelistCommands":
		m.cfg.Tools.Exec.WhitelistCommands = parseCSVList(value)
	case "cronExecTimeout":
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
			return
		}
		m.cfg.Tools.Cron.ExecTimeoutMinutes = v
	case "logsPath":
		m.cfg.Logs.Path = value
	case "logsMaxDays":
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
			return
		}
		m.cfg.Logs.MaxDays = v
	case "logsRotation":
		m.cfg.Logs.Rotation = value
	case "goalJudgeMode":
		m.cfg.Goal.Judge.Mode = value
	case "goalJudgeAgent":
		m.cfg.Goal.Judge.Agent = value
	case "updatesChannel":
		m.cfg.Updates.Channel = value
	case "updatesRepo":
		m.cfg.Updates.Repo = value
	case "gatewayHost":
		m.cfg.Gateway.Host = value
	case "gatewayPort":
		v, ok := m.parseIntField(value, 1, 65535)
		if !ok {
			return
		}
		m.cfg.Gateway.Port = v
	case "keyringPath":
		m.cfg.Keyring.Path = value
	case "keyringBackend":
		m.cfg.Keyring.Backend = value
	case "keyringAuditLogSize":
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
			return
		}
		m.cfg.Keyring.AuditLogSize = v
	default:
		return
	}

	m.formError = ""
	m.saveConfigToDisk()
	m.settingsEditField = ""
	m.reloadSystemSubView()
}

func (m *Model) reloadSystemSubView() {
	switch m.settingsSection {
	case sysSubViewName(sysGroupSession):
		m.loadSessionSettings()
	case sysSubViewName(sysGroupTools):
		m.loadToolsSettings()
	case sysSubViewName(sysGroupLogs):
		m.loadLogsSettings()
	case sysSubViewName(sysGroupLanguage):
		m.loadLanguageSettings()
	case sysSubViewName(sysGroupGoal):
		m.loadGoalSettings()
	case sysSubViewName(sysGroupUpdates):
		m.loadUpdatesSettings()
	case sysSubViewName(sysGroupGateway):
		m.loadGatewaySettings()
	case sysSubViewName(sysGroupKeyring):
		m.loadKeyringSettings()
	}
}
