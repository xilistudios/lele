package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

const (
	agentFieldID = iota
	agentFieldName
	agentFieldDescription
	agentFieldWorkspace
	agentFieldModel
	agentFieldTemperature
	agentFieldThinkingLevel
	agentFieldSkills
	agentFieldSubagentsAllow
	agentFieldSubagentsMaxConcurrent
	agentFieldSetDefault
	agentFieldDelete
)

const (
	defaultsFieldProvider = iota
	defaultsFieldModel
	defaultsFieldMaxTokens
	defaultsFieldTemperature
	defaultsFieldThinkingLevel
	defaultsFieldMaxToolIterations
	defaultsFieldMaxReadLines
	defaultsFieldMemoryMaxLines
	defaultsFieldSubagentTimeout
	defaultsFieldSubagentMaxConcurrent
	defaultsFieldLLMLoopTimeout
)

func thinkingLevelSelectorOptions() (labels, values []string) {
	return []string{"(default)", "off", "low", "medium", "high"},
		[]string{"", "off", "low", "medium", "high"}
}

const settingsAgentAddKey = "__add__"

func (m *Model) loadAgentsSettings() {
	m.modalItems = nil
	m.settingsAgentKeys = nil

	m.modalItems = append(m.modalItems, fmt.Sprintf("📋 %s", i18n.T("tui.settings.agentDefaults")))
	m.settingsAgentKeys = append(m.settingsAgentKeys, "")

	for _, agent := range m.cfg.Agents.List {
		label := agent.ID
		if agent.Name != "" {
			label = fmt.Sprintf("%s (%s)", agent.Name, agent.ID)
		}
		if agent.Default {
			label += " ★"
		}
		m.modalItems = append(m.modalItems, label)
		m.settingsAgentKeys = append(m.settingsAgentKeys, agent.ID)
	}

	m.modalItems = append(m.modalItems, fmt.Sprintf("+ %s", i18n.T("tui.settings.addAgent")))
	m.settingsAgentKeys = append(m.settingsAgentKeys, settingsAgentAddKey)
}

func (m *Model) loadAgentDetail(agentID string) {
	m.settingsAgentID = agentID

	if agentID == "" {
		d := m.cfg.Agents.Defaults
		m.modalItems = []string{
			fmt.Sprintf("Provider: %s", valueOr(d.Provider, "default")),
			fmt.Sprintf("Model: %s", valueOr(d.Model, "default")),
			fmt.Sprintf("MaxTokens: %d", d.MaxTokens),
			fmt.Sprintf("Temperature: %s", formatFloatPtr(d.Temperature)),
			fmt.Sprintf("Thinking: %s", thinkingLevelStr(d.ThinkingLevel)),
			fmt.Sprintf("MaxToolIterations: %d", d.MaxToolIterations),
			fmt.Sprintf("MaxReadLines: %d", d.MaxReadLines),
			fmt.Sprintf("MemoryMaxLines: %d", d.MemoryMaxLines),
			fmt.Sprintf("SubagentTimeout: %dm", d.SubagentTimeoutMinutes),
			fmt.Sprintf("SubagentMaxConcurrent: %d", d.SubagentMaxConcurrent),
			fmt.Sprintf("LLMLoopTimeout: %dm", d.LLMLoopTimeoutMinutes),
		}
		return
	}

	agent := m.findAgent(agentID)
	if agent == nil {
		m.modalItems = []string{i18n.T("tui.settings.agentNotFound")}
		return
	}

	modelStr := "default"
	if agent.Model != nil {
		modelStr = valueOr(agent.Model.Primary, "?")
		if len(agent.Model.Fallbacks) > 0 {
			modelStr = fmt.Sprintf("%s (+%s)", modelStr, strings.Join(agent.Model.Fallbacks, ", "))
		}
	}

	tempStr := "default"
	if agent.Temperature != nil {
		tempStr = fmt.Sprintf("%.2f", *agent.Temperature)
	}

	thinkStr := thinkingLevelStr(agent.ThinkingLevel)

	skillsStr := "none"
	if len(agent.Skills) > 0 {
		skillsStr = strings.Join(agent.Skills, ", ")
	}

	subagentsAllowStr := "default"
	subagentsMaxStr := "default"
	if agent.Subagents != nil {
		if len(agent.Subagents.AllowAgents) > 0 {
			subagentsAllowStr = strings.Join(agent.Subagents.AllowAgents, ", ")
		}
		if agent.Subagents.MaxConcurrent > 0 {
			subagentsMaxStr = strconv.Itoa(agent.Subagents.MaxConcurrent)
		}
	}

	defaultMark := ""
	if agent.Default {
		defaultMark = " ★"
	}

	m.modalItems = []string{
		fmt.Sprintf("ID: %s%s", agent.ID, defaultMark),
		fmt.Sprintf("Name: %s", valueOr(agent.Name, agent.ID)),
		fmt.Sprintf("Description: %s", valueOr(agent.Description, "none")),
		fmt.Sprintf("Workspace: %s", valueOr(agent.Workspace, "default")),
		fmt.Sprintf("Model: %s", modelStr),
		fmt.Sprintf("Temperature: %s", tempStr),
		fmt.Sprintf("Thinking: %s", thinkStr),
		fmt.Sprintf("Skills: %s", skillsStr),
		fmt.Sprintf("Subagents Allow: %s", subagentsAllowStr),
		fmt.Sprintf("Subagents MaxConcurrent: %s", subagentsMaxStr),
		fmt.Sprintf("★ %s", i18n.T("tui.settings.setAsDefault")),
		fmt.Sprintf("🗑 %s", i18n.T("tui.settings.deleteAgent")),
	}
}

func (m *Model) handleAgentsEnter() tea.Cmd {
	if m.modalSelectedIdx >= len(m.settingsAgentKeys) {
		return nil
	}
	key := m.settingsAgentKeys[m.modalSelectedIdx]

	if key == settingsAgentAddKey {
		m.settingsEditField = "newAgentID"
		m.textInput.SetValue("")
		m.textInput.Focus()
		return nil
	}

	m.resetModal(ModalSettingsAgentEdit)
	m.settingsAgentID = key
	m.loadAgentDetail(key)
	m.modalSelectedIdx = 0
	m.modalScrollOffset = 0
	return nil
}

func (m *Model) handleAgentEditEnter() tea.Cmd {
	agentID := m.settingsAgentID

	if agentID == "" {
		return m.handleDefaultsEditEnter()
	}

	agent := m.findAgent(agentID)
	if agent == nil {
		return nil
	}

	switch m.modalSelectedIdx {
	case agentFieldID:
		return nil
	case agentFieldName:
		m.settingsEditField = "agentName"
		m.textInput.SetValue(agent.Name)
		m.textInput.Focus()
	case agentFieldDescription:
		m.settingsEditField = "agentDescription"
		m.textInput.SetValue(agent.Description)
		m.textInput.Focus()
	case agentFieldWorkspace:
		m.settingsEditField = "agentWorkspace"
		m.textInput.SetValue(agent.Workspace)
		m.textInput.Focus()
	case agentFieldModel:
		modelStr := ""
		if agent.Model != nil {
			modelStr = agent.Model.Primary
		}
		labels, values := m.modelSelectorOptions(modelStr)
		if len(values) == 0 {
			labels = []string{"(default)"}
			values = []string{""}
		}
		labels = append(labels, "(custom...)")
		values = append(values, "__custom__")
		m.startSettingsSelector("agentModel", modelStr, labels, values)
	case agentFieldTemperature:
		m.settingsEditField = "agentTemperature"
		tempStr := ""
		if agent.Temperature != nil {
			tempStr = fmt.Sprintf("%.2f", *agent.Temperature)
		}
		m.textInput.SetValue(tempStr)
		m.textInput.Focus()
	case agentFieldThinkingLevel:
		current := ""
		if agent.ThinkingLevel != nil {
			current = *agent.ThinkingLevel
		}
		labels, values := thinkingLevelSelectorOptions()
		m.startSettingsSelector("agentThinkingLevel", current, labels, values)
	case agentFieldSkills:
		return nil
	case agentFieldSubagentsAllow:
		m.startSubagentPicker(agent)
	case agentFieldSubagentsMaxConcurrent:
		m.settingsEditField = "agentSubagentsMaxConcurrent"
		maxStr := ""
		if agent.Subagents != nil && agent.Subagents.MaxConcurrent > 0 {
			maxStr = strconv.Itoa(agent.Subagents.MaxConcurrent)
		}
		m.textInput.SetValue(maxStr)
		m.textInput.Focus()
	case agentFieldSetDefault:
		m.setAgentDefault(agentID)
	case agentFieldDelete:
		m.settingsEditField = "confirmDelete"
		m.formError = i18n.T("tui.settings.confirmDelete")
		m.textInput.SetValue("")
		m.textInput.Focus()
	}
	return nil
}

func (m *Model) handleDefaultsEditEnter() tea.Cmd {
	d := &m.cfg.Agents.Defaults
	switch m.modalSelectedIdx {
	case defaultsFieldProvider:
		labels, values := m.providerSelectorOptions(d.Provider)
		if len(values) == 0 {
			m.settingsEditField = "defaultProvider"
			m.textInput.SetValue(d.Provider)
			m.textInput.Focus()
		} else {
			m.startSettingsSelector("defaultProvider", d.Provider, labels, values)
		}
	case defaultsFieldModel:
		labels, values := m.modelSelectorOptions(d.Model)
		if len(values) == 0 {
			labels = []string{"(default)"}
			values = []string{""}
		}
		labels = append(labels, "(custom...)")
		values = append(values, "__custom__")
		m.startSettingsSelector("defaultModel", d.Model, labels, values)
	case defaultsFieldMaxTokens:
		m.settingsEditField = "defaultMaxTokens"
		m.textInput.SetValue(strconv.Itoa(d.MaxTokens))
		m.textInput.Focus()
	case defaultsFieldTemperature:
		m.settingsEditField = "defaultTemperature"
		m.textInput.SetValue(formatFloatPtr(d.Temperature))
		m.textInput.Focus()
	case defaultsFieldThinkingLevel:
		current := ""
		if d.ThinkingLevel != nil {
			current = *d.ThinkingLevel
		}
		labels, values := thinkingLevelSelectorOptions()
		m.startSettingsSelector("defaultThinkingLevel", current, labels, values)
	case defaultsFieldMaxToolIterations:
		m.settingsEditField = "defaultMaxToolIterations"
		m.textInput.SetValue(strconv.Itoa(d.MaxToolIterations))
		m.textInput.Focus()
	case defaultsFieldMaxReadLines:
		m.settingsEditField = "defaultMaxReadLines"
		m.textInput.SetValue(strconv.Itoa(d.MaxReadLines))
		m.textInput.Focus()
	case defaultsFieldMemoryMaxLines:
		m.settingsEditField = "defaultMemoryMaxLines"
		m.textInput.SetValue(strconv.Itoa(d.MemoryMaxLines))
		m.textInput.Focus()
	case defaultsFieldSubagentTimeout:
		m.settingsEditField = "defaultSubagentTimeout"
		m.textInput.SetValue(strconv.Itoa(d.SubagentTimeoutMinutes))
		m.textInput.Focus()
	case defaultsFieldSubagentMaxConcurrent:
		m.settingsEditField = "defaultSubagentMaxConcurrent"
		m.textInput.SetValue(strconv.Itoa(d.SubagentMaxConcurrent))
		m.textInput.Focus()
	case defaultsFieldLLMLoopTimeout:
		m.settingsEditField = "defaultLLMLoopTimeout"
		m.textInput.SetValue(strconv.Itoa(d.LLMLoopTimeoutMinutes))
		m.textInput.Focus()
	}
	return nil
}

func (m *Model) handleAgentSettingsInput(value string) {
	value = strings.TrimSpace(value)

	if m.settingsEditField == "newAgentID" {
		if value == "" {
			m.formError = i18n.T("tui.settings.agentIDRequired")
			return
		}
		for _, a := range m.cfg.Agents.List {
			if a.ID == value {
				m.formError = i18n.T("tui.settings.agentIDDuplicate")
				return
			}
		}
		newAgent := config.AgentConfig{
			ID:      value,
			Name:    value,
			Default: len(m.cfg.Agents.List) == 0,
		}
		m.cfg.Agents.List = append(m.cfg.Agents.List, newAgent)
		m.saveConfigToDisk()
		m.applyAgentReload()
		m.settingsEditField = ""
		m.formError = ""
		m.loadAgentsSettings()
		return
	}

	if m.settingsEditField == "confirmDelete" {
		if value == "y" || value == "yes" {
			m.deleteAgent(m.settingsAgentID)
		}
		m.settingsEditField = ""
		m.formError = ""
		return
	}

	agent := m.findAgent(m.settingsAgentID)
	if agent != nil {
		m.handleAgentFieldEdit(agent, value)
		return
	}

	m.handleDefaultsFieldEdit(value)
}

func (m *Model) applyAgentReload() {
	if m.agentLoop != nil {
		m.agentLoop.ReloadRegistry(m.cfg.Snapshot())
	}
}

func (m *Model) handleAgentFieldEdit(agent *config.AgentConfig, value string) {
	switch m.settingsEditField {
	case "agentName":
		agent.Name = value
	case "agentDescription":
		agent.Description = value
	case "agentWorkspace":
		agent.Workspace = value
	case "agentModel":
		if value == "" {
			agent.Model = nil
		} else {
			agent.Model = &config.AgentModelConfig{Primary: value}
		}
	case "agentTemperature":
		if value == "" {
			agent.Temperature = nil
		} else {
			f, err := strconv.ParseFloat(value, 64)
			if err != nil {
				m.formError = i18n.T("tui.settings.invalidNumber")
				return
			}
			agent.Temperature = &f
		}
	case "agentThinkingLevel":
		level, ok := config.NormalizeThinkingLevel(value)
		if !ok {
			m.formError = i18n.T("tui.settings.invalidThinkingLevel")
			return
		}
		if level == "" {
			agent.ThinkingLevel = nil
		} else {
			agent.ThinkingLevel = &level
		}
	case "agentSubagentsMaxConcurrent":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			m.formError = i18n.T("tui.settings.invalidNumber")
			return
		}
		if agent.Subagents == nil {
			agent.Subagents = &config.SubagentsConfig{}
		}
		agent.Subagents.MaxConcurrent = v
	}

	m.formError = ""
	m.saveConfigToDisk()
	m.applyAgentReload()
	m.settingsEditField = ""
	m.loadAgentDetail(m.settingsAgentID)
}

func (m *Model) handleDefaultsFieldEdit(value string) {
	d := &m.cfg.Agents.Defaults
	switch m.settingsEditField {
	case "defaultProvider":
		d.Provider = value
	case "defaultModel":
		d.Model = value
	case "defaultMaxTokens":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			m.formError = i18n.T("tui.settings.invalidNumber")
			return
		}
		d.MaxTokens = v
	case "defaultTemperature":
		if value == "" {
			d.Temperature = nil
		} else {
			f, err := strconv.ParseFloat(value, 64)
			if err != nil {
				m.formError = i18n.T("tui.settings.invalidNumber")
				return
			}
			d.Temperature = &f
		}
	case "defaultThinkingLevel":
		level, ok := config.NormalizeThinkingLevel(value)
		if !ok {
			m.formError = i18n.T("tui.settings.invalidThinkingLevel")
			return
		}
		if level == "" {
			d.ThinkingLevel = nil
		} else {
			d.ThinkingLevel = &level
		}
	case "defaultMaxToolIterations":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			m.formError = i18n.T("tui.settings.invalidNumber")
			return
		}
		d.MaxToolIterations = v
	case "defaultMaxReadLines":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			m.formError = i18n.T("tui.settings.invalidNumber")
			return
		}
		d.MaxReadLines = v
	case "defaultMemoryMaxLines":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			m.formError = i18n.T("tui.settings.invalidNumber")
			return
		}
		d.MemoryMaxLines = v
	case "defaultSubagentTimeout":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			m.formError = i18n.T("tui.settings.invalidNumber")
			return
		}
		d.SubagentTimeoutMinutes = v
	case "defaultSubagentMaxConcurrent":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			m.formError = i18n.T("tui.settings.invalidNumber")
			return
		}
		d.SubagentMaxConcurrent = v
	case "defaultLLMLoopTimeout":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			m.formError = i18n.T("tui.settings.invalidNumber")
			return
		}
		d.LLMLoopTimeoutMinutes = v
	}

	m.formError = ""
	m.saveConfigToDisk()
	m.applyAgentReload()
	m.settingsEditField = ""
	m.loadAgentDetail(m.settingsAgentID)
}

func (m *Model) setAgentDefault(agentID string) {
	for i := range m.cfg.Agents.List {
		m.cfg.Agents.List[i].Default = (m.cfg.Agents.List[i].ID == agentID)
	}
	m.saveConfigToDisk()
	m.applyAgentReload()
	m.loadAgentDetail(agentID)
}

func (m *Model) deleteAgent(agentID string) {
	isDefault := false
	for _, a := range m.cfg.Agents.List {
		if a.ID == agentID && a.Default {
			isDefault = true
			break
		}
	}

	newList := make([]config.AgentConfig, 0, len(m.cfg.Agents.List)-1)
	for _, a := range m.cfg.Agents.List {
		if a.ID != agentID {
			newList = append(newList, a)
		}
	}
	m.cfg.Agents.List = newList

	if isDefault && len(m.cfg.Agents.List) > 0 {
		m.cfg.Agents.List[0].Default = true
	}

	m.saveConfigToDisk()
	m.applyAgentReload()

	m.settingsAgentID = ""
	m.loadAgentsSettings()
}

func (m *Model) findAgent(agentID string) *config.AgentConfig {
	for i := range m.cfg.Agents.List {
		if m.cfg.Agents.List[i].ID == agentID {
			return &m.cfg.Agents.List[i]
		}
	}
	return nil
}

func (m *Model) startSubagentPicker(agent *config.AgentConfig) {
	m.subagentPickerActive = true
	m.subagentPickerIdx = 0
	m.modalScrollOffset = 0
	m.subagentPickerItems = nil
	m.subagentPickerLabels = nil
	m.subagentPickerSelected = make(map[int]bool)

	allowed := make(map[string]bool)
	if agent.Subagents != nil {
		for _, id := range agent.Subagents.AllowAgents {
			allowed[id] = true
		}
	}

	for _, a := range m.cfg.Agents.List {
		if a.ID == m.settingsAgentID {
			continue
		}
		idx := len(m.subagentPickerItems)
		m.subagentPickerItems = append(m.subagentPickerItems, a.ID)
		label := a.ID
		if a.Name != "" {
			label = fmt.Sprintf("%s (%s)", a.Name, a.ID)
		}
		m.subagentPickerLabels = append(m.subagentPickerLabels, label)
		m.subagentPickerSelected[idx] = allowed[a.ID]
	}
}

func (m *Model) toggleSubagentPicker() {
	if !m.subagentPickerActive || m.subagentPickerIdx >= len(m.subagentPickerItems) {
		return
	}
	m.subagentPickerSelected[m.subagentPickerIdx] = !m.subagentPickerSelected[m.subagentPickerIdx]
}

func (m *Model) saveSubagentPicker() {
	if !m.subagentPickerActive {
		return
	}

	agent := m.findAgent(m.settingsAgentID)
	if agent != nil {
		selected := make([]string, 0, len(m.subagentPickerSelected))
		for i, isSel := range m.subagentPickerSelected {
			if isSel && i < len(m.subagentPickerItems) {
				selected = append(selected, m.subagentPickerItems[i])
			}
		}
		if len(selected) == 0 {
			if agent.Subagents != nil && agent.Subagents.MaxConcurrent == 0 &&
				agent.Subagents.TimeoutMin == 0 && agent.Subagents.MaxIterations == 0 &&
				agent.Subagents.Model == nil {
				agent.Subagents = nil
			} else if agent.Subagents != nil {
				agent.Subagents.AllowAgents = nil
			}
		} else {
			if agent.Subagents == nil {
				agent.Subagents = &config.SubagentsConfig{}
			}
			agent.Subagents.AllowAgents = selected
		}
		m.saveConfigToDisk()
		m.applyAgentReload()
	}

	m.cancelSubagentPicker()
	m.loadAgentDetail(m.settingsAgentID)
}

func (m *Model) cancelSubagentPicker() {
	m.subagentPickerActive = false
	m.subagentPickerItems = nil
	m.subagentPickerLabels = nil
	m.subagentPickerSelected = nil
	m.subagentPickerIdx = 0
	m.modalScrollOffset = 0
}

func formatFloatPtr(f *float64) string {
	if f == nil {
		return "default"
	}
	return fmt.Sprintf("%.2f", *f)
}

func thinkingLevelStr(s *string) string {
	if s == nil || *s == "" {
		return "default"
	}
	return *s
}
