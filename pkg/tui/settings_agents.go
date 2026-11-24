package tui

import (
	"fmt"
	"math"
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
	defaultsFieldWorkspace
	defaultsFieldRestrictToWorkspace
	defaultsFieldModelFallbacks
	defaultsFieldImageModel
	defaultsFieldImageModelFallbacks
	defaultsFieldMaxToolIterations
	defaultsFieldMaxReadLines
	defaultsFieldMemoryMaxLines
	defaultsFieldSubagentTimeout
	defaultsFieldSubagentMaxConcurrent
	defaultsFieldSubagentMaxRetries
	defaultsFieldSubagentMaxIterations
	defaultsFieldSubagentRetention
	defaultsFieldLLMLoopTimeout
	defaultsFieldPromptCacheEnabled
	defaultsFieldPromptCacheTTL
)

func thinkingLevelSelectorOptions() (labels, values []string) {
	return append([]string{"(default)"}, config.ThinkingLevels...),
		append([]string{""}, config.ThinkingLevels...)
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
			fmt.Sprintf("Workspace: %s", valueOr(d.Workspace, "default")),
			fmt.Sprintf("RestrictToWorkspace: %s", boolStatus(d.RestrictToWorkspace)),
			fmt.Sprintf("ModelFallbacks: %s", valueOr(strings.Join(d.ModelFallbacks, ", "), "none")),
			fmt.Sprintf("ImageModel: %s", valueOr(d.ImageModel, "default")),
			fmt.Sprintf("ImageModelFallbacks: %s", valueOr(strings.Join(d.ImageModelFallbacks, ", "), "none")),
			fmt.Sprintf("MaxToolIterations: %d", d.MaxToolIterations),
			fmt.Sprintf("MaxReadLines: %d", d.MaxReadLines),
			fmt.Sprintf("MemoryMaxLines: %d", d.MemoryMaxLines),
			fmt.Sprintf("SubagentTimeout: %dm", d.SubagentTimeoutMinutes),
			fmt.Sprintf("SubagentMaxConcurrent: %d", d.SubagentMaxConcurrent),
			fmt.Sprintf("SubagentMaxRetries: %d", d.SubagentMaxRetries),
			fmt.Sprintf("SubagentMaxIterations: %d", d.SubagentMaxIterations),
			fmt.Sprintf("SubagentRetention: %dm", d.SubagentRetentionMinutes),
			fmt.Sprintf("LLMLoopTimeout: %dm", d.LLMLoopTimeoutMinutes),
			fmt.Sprintf("PromptCache: %s", boolStatus(d.PromptCache.Enabled)),
			fmt.Sprintf("PromptCacheTTL: %s", d.PromptCache.NormalizedTTL()),
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
		m.startModelFieldSelector("agentModel", modelStr)
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
		m.startModelFieldSelector("defaultModel", d.Model)
	case defaultsFieldMaxTokens:
		m.startNumberEdit("defaultMaxTokens", d.MaxTokens)
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
	case defaultsFieldWorkspace:
		m.settingsEditField = "defaultWorkspace"
		m.textInput.SetValue(d.Workspace)
		m.textInput.Focus()
	case defaultsFieldRestrictToWorkspace:
		d.RestrictToWorkspace = !d.RestrictToWorkspace
		m.persistDefaultsEdit()
	case defaultsFieldModelFallbacks:
		m.settingsEditField = "defaultModelFallbacks"
		m.textInput.SetValue(strings.Join(d.ModelFallbacks, ", "))
		m.textInput.Focus()
	case defaultsFieldImageModel:
		m.startModelFieldSelector("defaultImageModel", d.ImageModel)
	case defaultsFieldImageModelFallbacks:
		m.settingsEditField = "defaultImageModelFallbacks"
		m.textInput.SetValue(strings.Join(d.ImageModelFallbacks, ", "))
		m.textInput.Focus()
	case defaultsFieldMaxToolIterations:
		m.startNumberEdit("defaultMaxToolIterations", d.MaxToolIterations)
	case defaultsFieldMaxReadLines:
		m.startNumberEdit("defaultMaxReadLines", d.MaxReadLines)
	case defaultsFieldMemoryMaxLines:
		m.startNumberEdit("defaultMemoryMaxLines", d.MemoryMaxLines)
	case defaultsFieldSubagentTimeout:
		m.startNumberEdit("defaultSubagentTimeout", d.SubagentTimeoutMinutes)
	case defaultsFieldSubagentMaxConcurrent:
		m.startNumberEdit("defaultSubagentMaxConcurrent", d.SubagentMaxConcurrent)
	case defaultsFieldSubagentMaxRetries:
		m.startNumberEdit("defaultSubagentMaxRetries", d.SubagentMaxRetries)
	case defaultsFieldSubagentMaxIterations:
		m.startNumberEdit("defaultSubagentMaxIterations", d.SubagentMaxIterations)
	case defaultsFieldSubagentRetention:
		m.startNumberEdit("defaultSubagentRetention", d.SubagentRetentionMinutes)
	case defaultsFieldLLMLoopTimeout:
		m.startNumberEdit("defaultLLMLoopTimeout", d.LLMLoopTimeoutMinutes)
	case defaultsFieldPromptCacheEnabled:
		d.PromptCache.Enabled = !d.PromptCache.Enabled
		m.persistDefaultsEdit()
	case defaultsFieldPromptCacheTTL:
		labels := []string{"5m", "1h"}
		m.startSettingsSelector("defaultPromptCacheTTL", d.PromptCache.NormalizedTTL(), labels, labels)
	}
	return nil
}

func (m *Model) startModelFieldSelector(field, current string) {
	labels, values := m.modelSelectorOptions(current)
	if len(values) == 0 {
		labels = []string{"(default)"}
		values = []string{""}
	}
	labels = append(labels, "(custom...)")
	values = append(values, "__custom__")
	m.startSettingsSelector(field, current, labels, values)
}

func (m *Model) persistDefaultsEdit() {
	m.formError = ""
	m.saveConfigToDisk()
	m.applyAgentReload()
	m.loadAgentDetail("")
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
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
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
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
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
	case "defaultWorkspace":
		d.Workspace = value
	case "defaultModelFallbacks":
		d.ModelFallbacks = parseCSVList(value)
	case "defaultImageModel":
		d.ImageModel = value
	case "defaultImageModelFallbacks":
		d.ImageModelFallbacks = parseCSVList(value)
	case "defaultMaxToolIterations":
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
			return
		}
		d.MaxToolIterations = v
	case "defaultMaxReadLines":
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
			return
		}
		d.MaxReadLines = v
	case "defaultMemoryMaxLines":
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
			return
		}
		d.MemoryMaxLines = v
	case "defaultSubagentTimeout":
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
			return
		}
		d.SubagentTimeoutMinutes = v
	case "defaultSubagentMaxConcurrent":
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
			return
		}
		d.SubagentMaxConcurrent = v
	case "defaultSubagentMaxRetries":
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
			return
		}
		d.SubagentMaxRetries = v
	case "defaultSubagentMaxIterations":
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
			return
		}
		d.SubagentMaxIterations = v
	case "defaultSubagentRetention":
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
			return
		}
		d.SubagentRetentionMinutes = v
	case "defaultLLMLoopTimeout":
		v, ok := m.parseIntField(value, 0, math.MaxInt)
		if !ok {
			return
		}
		d.LLMLoopTimeoutMinutes = v
	case "defaultPromptCacheTTL":
		d.PromptCache.TTL = value
	default:
		return
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
