package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// Agent edit field indices (for ModalSettingsAgentEdit). For the agent detail
// view the first 8 indices correspond to read-only/editable fields and the
// last two are actions (set as default / delete).
const (
	agentFieldID          = iota // 0: ID (read-only)
	agentFieldName               // 1: Name
	agentFieldDescription        // 2: Description
	agentFieldWorkspace          // 3: Workspace
	agentFieldModel              // 4: Model (provider/alias)
	agentFieldTemperature        // 5: Temperature
	agentFieldSkills             // 6: Skills (read-only)
	agentFieldSubagents          // 7: Subagents (read-only)
)

// settingsAgentAddKey is the synthetic key in settingsAgentKeys that maps to
// the "Add Agent" action row.
const settingsAgentAddKey = "__add__"

// loadAgentsSettings populates the agents list with "Defaults" + each agent
// + the "Add Agent" action. settingsAgentKeys maps each row to an agent ID
// (empty string = defaults view, "__add__" = new agent flow).
func (m *Model) loadAgentsSettings() {
	m.modalItems = nil
	m.settingsAgentKeys = nil // maps items to agent IDs (empty = defaults)

	// Defaults entry
	m.modalItems = append(m.modalItems, fmt.Sprintf("📋 %s", i18n.T("tui.settings.agentDefaults")))
	m.settingsAgentKeys = append(m.settingsAgentKeys, "")

	// Agent entries
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

	// Add "new agent" action
	m.modalItems = append(m.modalItems, fmt.Sprintf("+ %s", i18n.T("tui.settings.addAgent")))
	m.settingsAgentKeys = append(m.settingsAgentKeys, settingsAgentAddKey)
}

// loadAgentDetail populates the detail view for a single agent. When agentID
// is empty it renders the agent-defaults view.
func (m *Model) loadAgentDetail(agentID string) {
	m.settingsAgentID = agentID

	if agentID == "" {
		// Defaults view
		d := m.cfg.Agents.Defaults
		m.modalItems = []string{
			fmt.Sprintf("Provider: %s", valueOr(d.Provider, "default")),
			fmt.Sprintf("Model: %s", valueOr(d.Model, "default")),
			fmt.Sprintf("MaxTokens: %d", d.MaxTokens),
			fmt.Sprintf("Temperature: %s", formatFloatPtr(d.Temperature)),
			fmt.Sprintf("MaxToolIterations: %d", d.MaxToolIterations),
			fmt.Sprintf("MaxReadLines: %d", d.MaxReadLines),
			fmt.Sprintf("SubagentTimeout: %dm", d.SubagentTimeoutMinutes),
			fmt.Sprintf("SubagentMaxConcurrent: %d", d.SubagentMaxConcurrent),
			fmt.Sprintf("LLMLoopTimeout: %dm", d.LLMLoopTimeoutMinutes),
		}
		return
	}

	// Agent view
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

	skillsStr := "none"
	if len(agent.Skills) > 0 {
		skillsStr = strings.Join(agent.Skills, ", ")
	}

	subagentsStr := "default"
	if agent.Subagents != nil {
		parts := []string{}
		if len(agent.Subagents.AllowAgents) > 0 {
			parts = append(parts, "allow: "+strings.Join(agent.Subagents.AllowAgents, ","))
		}
		if agent.Subagents.MaxConcurrent > 0 {
			parts = append(parts, fmt.Sprintf("max:%d", agent.Subagents.MaxConcurrent))
		}
		if len(parts) > 0 {
			subagentsStr = strings.Join(parts, " ")
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
		fmt.Sprintf("Skills: %s", skillsStr),
		fmt.Sprintf("Subagents: %s", subagentsStr),
		fmt.Sprintf("★ %s", i18n.T("tui.settings.setAsDefault")),
		fmt.Sprintf("🗑 %s", i18n.T("tui.settings.deleteAgent")),
	}
}

// handleAgentsEnter handles enter on the agents list.
func (m *Model) handleAgentsEnter() tea.Cmd {
	if m.modalSelectedIdx >= len(m.settingsAgentKeys) {
		return nil
	}
	key := m.settingsAgentKeys[m.modalSelectedIdx]

	if key == settingsAgentAddKey {
		// Start add-agent flow
		m.settingsEditField = "newAgentID"
		m.textInput.SetValue("")
		m.textInput.Focus()
		return nil
	}

	// Navigate to agent detail
	m.resetModal(ModalSettingsAgentEdit)
	m.settingsAgentID = key
	m.loadAgentDetail(key)
	m.modalSelectedIdx = 0
	m.modalScrollOffset = 0
	return nil
}

// handleAgentEditEnter handles enter on agent detail items.
func (m *Model) handleAgentEditEnter() tea.Cmd {
	agentID := m.settingsAgentID

	if agentID == "" {
		// Defaults view — editable fields
		return m.handleDefaultsEditEnter()
	}

	// Agent view
	agent := m.findAgent(agentID)
	if agent == nil {
		return nil
	}

	switch m.modalSelectedIdx {
	case agentFieldID: // 0: ID — not editable
		return nil
	case agentFieldName: // 1: Name
		m.settingsEditField = "agentName"
		m.textInput.SetValue(agent.Name)
		m.textInput.Focus()
	case agentFieldDescription: // 2: Description
		m.settingsEditField = "agentDescription"
		m.textInput.SetValue(agent.Description)
		m.textInput.Focus()
	case agentFieldWorkspace: // 3: Workspace
		m.settingsEditField = "agentWorkspace"
		m.textInput.SetValue(agent.Workspace)
		m.textInput.Focus()
	case agentFieldModel: // 4: Model — selector from default provider's models
		providerName := m.cfg.Agents.Defaults.Provider
		models := m.listProviderModels(providerName)
		modelStr := ""
		if agent.Model != nil {
			modelStr = agent.Model.Primary
		}
		if len(models) == 0 {
			// No models configured — fall back to text input
			m.settingsEditField = "agentModel"
			m.textInput.SetValue(modelStr)
			m.textInput.Focus()
		} else {
			labels := make([]string, 0, len(models)+1)
			values := make([]string, 0, len(models)+1)
			labels = append(labels, "(default)")
			values = append(values, "")
			for _, model := range models {
				labels = append(labels, model)
				values = append(values, model)
			}
			m.startSettingsSelector("agentModel", modelStr, labels, values)
		}
	case agentFieldTemperature: // 5: Temperature
		m.settingsEditField = "agentTemperature"
		tempStr := ""
		if agent.Temperature != nil {
			tempStr = fmt.Sprintf("%.2f", *agent.Temperature)
		}
		m.textInput.SetValue(tempStr)
		m.textInput.Focus()
	case agentFieldSkills: // 6: Skills — not editable (managed by /skills)
		return nil
	case agentFieldSubagents: // 7: Subagents — not editable in this view
		return nil
	case 8: // Set as default
		m.setAgentDefault(agentID)
	case 9: // Delete
		m.settingsEditField = "confirmDelete"
		m.formError = i18n.T("tui.settings.confirmDelete")
	}
	return nil
}

// handleDefaultsEditEnter handles enter on defaults detail items (indices
// correspond to the order in loadAgentDetail's defaults branch).
func (m *Model) handleDefaultsEditEnter() tea.Cmd {
	d := &m.cfg.Agents.Defaults
	switch m.modalSelectedIdx {
	case 0: // Provider — selector from configured providers
		providers := m.listProviders()
		if len(providers) == 0 {
			// No providers configured — fall back to text input
			m.settingsEditField = "defaultProvider"
			m.textInput.SetValue(d.Provider)
			m.textInput.Focus()
		} else {
			labels := make([]string, 0, len(providers)+1)
			values := make([]string, 0, len(providers)+1)
			labels = append(labels, "(default)")
			values = append(values, "")
			for _, p := range providers {
				labels = append(labels, p)
				values = append(values, p)
			}
			m.startSettingsSelector("defaultProvider", d.Provider, labels, values)
		}
	case 1: // Model — selector from default provider's models
		providerName := d.Provider
		models := m.listProviderModels(providerName)
		if len(models) == 0 {
			// No models configured — fall back to text input
			m.settingsEditField = "defaultModel"
			m.textInput.SetValue(d.Model)
			m.textInput.Focus()
		} else {
			labels := make([]string, 0, len(models)+1)
			values := make([]string, 0, len(models)+1)
			labels = append(labels, "(default)")
			values = append(values, "")
			for _, model := range models {
				labels = append(labels, model)
				values = append(values, model)
			}
			m.startSettingsSelector("defaultModel", d.Model, labels, values)
		}
	case 2: // MaxTokens
		m.settingsEditField = "defaultMaxTokens"
		m.textInput.SetValue(strconv.Itoa(d.MaxTokens))
		m.textInput.Focus()
	case 3: // Temperature
		m.settingsEditField = "defaultTemperature"
		m.textInput.SetValue(formatFloatPtr(d.Temperature))
		m.textInput.Focus()
	case 4: // MaxToolIterations
		m.settingsEditField = "defaultMaxToolIterations"
		m.textInput.SetValue(strconv.Itoa(d.MaxToolIterations))
		m.textInput.Focus()
	case 5: // MaxReadLines
		m.settingsEditField = "defaultMaxReadLines"
		m.textInput.SetValue(strconv.Itoa(d.MaxReadLines))
		m.textInput.Focus()
	case 6: // SubagentTimeout
		m.settingsEditField = "defaultSubagentTimeout"
		m.textInput.SetValue(strconv.Itoa(d.SubagentTimeoutMinutes))
		m.textInput.Focus()
	case 7: // SubagentMaxConcurrent
		m.settingsEditField = "defaultSubagentMaxConcurrent"
		m.textInput.SetValue(strconv.Itoa(d.SubagentMaxConcurrent))
		m.textInput.Focus()
	case 8: // LLMLoopTimeout
		m.settingsEditField = "defaultLLMLoopTimeout"
		m.textInput.SetValue(strconv.Itoa(d.LLMLoopTimeoutMinutes))
		m.textInput.Focus()
	}
	return nil
}

// handleAgentSettingsInput saves an edited agent setting. It handles the
// add-agent flow, delete confirmation, agent field edits, and defaults edits.
func (m *Model) handleAgentSettingsInput(value string) {
	value = strings.TrimSpace(value)

	// Handle new agent creation
	if m.settingsEditField == "newAgentID" {
		if value == "" {
			m.formError = i18n.T("tui.settings.agentIDRequired")
			return
		}
		// Check for duplicate
		for _, a := range m.cfg.Agents.List {
			if a.ID == value {
				m.formError = i18n.T("tui.settings.agentIDDuplicate")
				return
			}
		}
		// Create new agent
		newAgent := config.AgentConfig{
			ID:      value,
			Name:    value,
			Default: len(m.cfg.Agents.List) == 0, // first agent is default
		}
		m.cfg.Agents.List = append(m.cfg.Agents.List, newAgent)
		m.saveConfigToDisk()
		m.applyAgentReload()
		m.settingsEditField = ""
		m.formError = ""
		m.loadAgentsSettings()
		return
	}

	// Handle delete confirmation
	if m.settingsEditField == "confirmDelete" {
		if value == "y" || value == "yes" {
			m.deleteAgent(m.settingsAgentID)
		}
		m.settingsEditField = ""
		m.formError = ""
		return
	}

	// Handle agent field edits
	agent := m.findAgent(m.settingsAgentID)
	if agent != nil {
		m.handleAgentFieldEdit(agent, value)
		return
	}

	// Handle defaults field edits
	m.handleDefaultsFieldEdit(value)
}

// applyAgentReload hot-reloads the agent registry when the model instance is
// wired to an agent loop (never in unit tests where it is nil).
func (m *Model) applyAgentReload() {
	if m.agentLoop != nil {
		m.agentLoop.ReloadRegistry(m.cfg)
	}
}

// handleAgentFieldEdit saves an edited field for an agent in-place (findAgent
// returns a pointer into cfg.Agents.List so mutations persist).
func (m *Model) handleAgentFieldEdit(agent *config.AgentConfig, value string) {
	switch m.settingsEditField {
	case "agentName":
		agent.Name = value
	case "agentDescription":
		agent.Description = value
	case "agentWorkspace":
		agent.Workspace = value
	case "agentModel":
		// The AgentModelConfig struct stores Primary/Fallbacks (not the
		// provider/alias split), so the whole value is the primary model.
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
	}

	m.formError = ""
	m.saveConfigToDisk()
	m.applyAgentReload()
	m.settingsEditField = ""
	m.loadAgentDetail(m.settingsAgentID)
}

// handleDefaultsFieldEdit saves an edited field for the agent defaults in-place.
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

// setAgentDefault marks an agent as default and unsets all others.
func (m *Model) setAgentDefault(agentID string) {
	for i := range m.cfg.Agents.List {
		m.cfg.Agents.List[i].Default = (m.cfg.Agents.List[i].ID == agentID)
	}
	m.saveConfigToDisk()
	m.applyAgentReload()
	m.loadAgentDetail(agentID)
}

// deleteAgent removes an agent from the config. If the deleted agent was the
// default, the first remaining agent becomes the default. Afterwards it
// returns to the agents list.
func (m *Model) deleteAgent(agentID string) {
	// Find if it's the default
	isDefault := false
	for _, a := range m.cfg.Agents.List {
		if a.ID == agentID && a.Default {
			isDefault = true
			break
		}
	}

	// Remove from list
	newList := make([]config.AgentConfig, 0, len(m.cfg.Agents.List)-1)
	for _, a := range m.cfg.Agents.List {
		if a.ID != agentID {
			newList = append(newList, a)
		}
	}
	m.cfg.Agents.List = newList

	// If deleted was default, make first agent default
	if isDefault && len(m.cfg.Agents.List) > 0 {
		m.cfg.Agents.List[0].Default = true
	}

	m.saveConfigToDisk()
	m.applyAgentReload()

	// Go back to agents list
	m.settingsAgentID = ""
	m.loadAgentsSettings()
}

// findAgent finds an agent by ID in the config and returns a pointer to the
// element inside the slice (for in-place editing).
func (m *Model) findAgent(agentID string) *config.AgentConfig {
	for i := range m.cfg.Agents.List {
		if m.cfg.Agents.List[i].ID == agentID {
			return &m.cfg.Agents.List[i]
		}
	}
	return nil
}

// formatFloatPtr formats a *float64 for display; nil renders as "default".
func formatFloatPtr(f *float64) string {
	if f == nil {
		return "default"
	}
	return fmt.Sprintf("%.2f", *f)
}
