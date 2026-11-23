package tui

import (
	"strconv"
	"strings"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func boolStatus(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

func parseCSVList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func (m *Model) parseIntField(value string, min, max int) (int, bool) {
	return m.parseIntWithErrorKey(value, min, max, "tui.settings.invalidNumber")
}

func (m *Model) parsePercentField(value string) (int, bool) {
	return m.parseIntWithErrorKey(value, 0, 100, "tui.settings.invalidPercent")
}

func (m *Model) parseIntWithErrorKey(value string, min, max int, errKey string) (int, bool) {
	v, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || v < min || v > max {
		m.formError = i18n.T(errKey)
		return 0, false
	}
	return v, true
}

func (m *Model) startNumberEdit(field string, value int) {
	m.settingsEditField = field
	m.textInput.SetValue(strconv.Itoa(value))
	m.textInput.Focus()
}

func settingsFieldLabel(field string) string {
	switch field {
	case "agentName":
		return "Name"
	case "agentDescription":
		return "Description"
	case "agentWorkspace":
		return "Workspace"
	case "agentModel":
		return i18n.T("tui.model")
	case "agentTemperature":
		return "Temperature"
	case "agentThinkingLevel":
		return "Thinking level"
	case "agentSubagentsMaxConcurrent":
		return "Subagents MaxConcurrent"
	case "newAgentID":
		return "Agent ID"
	case "maxMessages":
		return i18n.T("tui.settings.maxMessages")
	case "streamThrottle":
		return i18n.T("tui.settings.streamThrottle")
	case "ephemeralThreshold":
		return i18n.T("tui.settings.ephemeralThreshold")
	case "compactionPercent":
		return i18n.T("tui.settings.compactionPercent")
	case "compactionModel":
		return i18n.T("tui.settings.compactionModel")
	case "dmScope":
		return i18n.T("tui.settings.dmScope")
	case "execTimeout":
		return i18n.T("tui.settings.execTimeout")
	case "customDenyPatterns":
		return i18n.T("tui.settings.customDenyPatterns")
	case "whitelistCommands":
		return i18n.T("tui.settings.whitelistCommands")
	case "cronExecTimeout":
		return i18n.T("tui.settings.cronExecTimeout")
	case "logsPath":
		return i18n.T("tui.settings.logsPath")
	case "logsMaxDays":
		return i18n.T("tui.settings.logsMaxDays")
	case "logsRotation":
		return i18n.T("tui.settings.logsRotation")
	case "goalJudgeMode":
		return i18n.T("tui.settings.goalJudgeMode")
	case "goalJudgeAgent":
		return i18n.T("tui.settings.goalJudgeAgent")
	case "updatesChannel":
		return i18n.T("tui.settings.updatesChannel")
	case "updatesRepo":
		return i18n.T("tui.settings.updatesRepo")
	case "gatewayHost":
		return i18n.T("tui.settings.gatewayHost")
	case "gatewayPort":
		return i18n.T("tui.settings.gatewayPort")
	case "keyringPath":
		return i18n.T("tui.settings.keyringPath")
	case "keyringBackend":
		return i18n.T("tui.settings.keyringBackend")
	case "keyringAuditLogSize":
		return i18n.T("tui.settings.keyringAuditLogSize")
	case "defaultWorkspace":
		return "Workspace"
	case "defaultModelFallbacks":
		return "Model fallbacks"
	case "defaultImageModel":
		return "Image model"
	case "defaultImageModelFallbacks":
		return "Image model fallbacks"
	case "defaultMaxTokens":
		return "MaxTokens"
	case "defaultTemperature":
		return "Temperature"
	case "defaultMaxToolIterations":
		return "MaxToolIterations"
	case "defaultMaxReadLines":
		return "MaxReadLines"
	case "defaultMemoryMaxLines":
		return "MemoryMaxLines"
	case "defaultSubagentTimeout":
		return "SubagentTimeout"
	case "defaultSubagentMaxConcurrent":
		return "SubagentMaxConcurrent"
	case "defaultSubagentMaxRetries":
		return "SubagentMaxRetries"
	case "defaultSubagentMaxIterations":
		return "SubagentMaxIterations"
	case "defaultSubagentRetention":
		return "SubagentRetention"
	case "defaultLLMLoopTimeout":
		return "LLMLoopTimeout"
	}
	return field
}
