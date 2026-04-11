package config

import (
	"fmt"
	"strings"
)

// detectRestartRequiredSections determines which sections require a restart.
func detectRestartRequiredSections(doc *EditableDocument) []string {
	_ = doc
	return []string{
		"channels.whatsapp",
		"channels.telegram",
		"channels.feishu",
		"channels.discord",
		"channels.maixcam",
		"channels.qq",
		"channels.dingtalk",
		"channels.slack",
		"channels.line",
		"channels.onebot",
		"channels.native",
		"gateway",
	}
}

// ValidateEditableDocument validates the editable document.
func ValidateEditableDocument(doc *EditableDocument) []ValidationError {
	var errors []ValidationError

	// Validate agents.
	if doc.Agents.Defaults.Workspace == "" {
		errors = append(errors, ValidationError{
			Path:    "agents.defaults.workspace",
			Message: "workspace is required",
			Code:    "required",
		})
	}

	if doc.Agents.Defaults.Provider == "" {
		errors = append(errors, ValidationError{
			Path:    "agents.defaults.provider",
			Message: "provider is required",
			Code:    "required",
		})
	}

	if doc.Agents.Defaults.Model == "" {
		errors = append(errors, ValidationError{
			Path:    "agents.defaults.model",
			Message: "model is required",
			Code:    "required",
		})
	}

	if doc.Agents.Defaults.MaxTokens <= 0 {
		errors = append(errors, ValidationError{
			Path:    "agents.defaults.max_tokens",
			Message: "max_tokens must be greater than 0",
			Code:    "invalid_range",
		})
	}

	if doc.Agents.Defaults.MaxToolIterations <= 0 {
		errors = append(errors, ValidationError{
			Path:    "agents.defaults.max_tool_iterations",
			Message: "max_tool_iterations must be greater than 0",
			Code:    "invalid_range",
		})
	}

	// Validate channels.telegram.verbose.
	if doc.Channels.Telegram.Verbose != "" {
		validVerbose := map[string]bool{"off": true, "basic": true, "full": true}
		if !validVerbose[strings.ToLower(string(doc.Channels.Telegram.Verbose))] {
			errors = append(errors, ValidationError{
				Path:    "channels.telegram.verbose",
				Message: "verbose must be one of: off, basic, full",
				Code:    "invalid_enum",
			})
		}
	}

	// Validate logs.rotation.
	if doc.Logs.Rotation != "" {
		validRotations := map[string]bool{"daily": true, "weekly": true}
		if !validRotations[strings.ToLower(doc.Logs.Rotation)] {
			errors = append(errors, ValidationError{
				Path:    "logs.rotation",
				Message: "rotation must be one of: daily, weekly",
				Code:    "invalid_enum",
			})
		}
	}

	// Validate heartbeat.interval.
	if doc.Heartbeat.Enabled && doc.Heartbeat.Interval < 5 {
		errors = append(errors, ValidationError{
			Path:    "heartbeat.interval",
			Message: "interval must be at least 5 minutes",
			Code:    "invalid_range",
		})
	}

	// Validate native channel.
	if doc.Channels.Native.Enabled {
		if doc.Channels.Native.Port <= 0 || doc.Channels.Native.Port > 65535 {
			errors = append(errors, ValidationError{
				Path:    "channels.native.port",
				Message: "port must be between 1 and 65535",
				Code:    "invalid_range",
			})
		}
	}

	// Validate duplicate providers.
	providerNames := make(map[string]bool)
	for name := range doc.Providers {
		lowerName := strings.ToLower(name)
		if providerNames[lowerName] {
			errors = append(errors, ValidationError{
				Path:    fmt.Sprintf("providers.%s", name),
				Message: fmt.Sprintf("duplicate provider name: %s", name),
				Code:    "duplicate",
			})
		}
		providerNames[lowerName] = true
	}

	// Validate duplicate agent IDs.
	agentIDs := make(map[string]bool)
	for _, agent := range doc.Agents.List {
		if agentIDs[agent.ID] {
			errors = append(errors, ValidationError{
				Path:    fmt.Sprintf("agents.list.%s", agent.ID),
				Message: fmt.Sprintf("duplicate agent ID: %s", agent.ID),
				Code:    "duplicate",
			})
		}
		agentIDs[agent.ID] = true
	}

	return errors
}
