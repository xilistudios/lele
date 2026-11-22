package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

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
		"channels.web",
		"gateway",
	}
}

func ValidateEditableDocument(doc *EditableDocument) []ValidationError {
	var errors []ValidationError

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

	if doc.Agents.Defaults.MaxToolIterations < 0 {
		errors = append(errors, ValidationError{
			Path:    "agents.defaults.max_tool_iterations",
			Message: "max_tool_iterations must be 0 (unlimited) or greater",
			Code:    "invalid_range",
		})
	}

	if v := doc.Agents.Defaults.ThinkingLevel; v != nil {
		if _, ok := NormalizeThinkingLevel(*v); !ok {
			errors = append(errors, ValidationError{
				Path:    "agents.defaults.thinking_level",
				Message: fmt.Sprintf("invalid thinking_level %q (valid: off, low, medium, high)", *v),
				Code:    "invalid_enum",
			})
		}
	}

	for i, agent := range doc.Agents.List {
		if v := agent.ThinkingLevel; v != nil {
			if _, ok := NormalizeThinkingLevel(*v); !ok {
				errors = append(errors, ValidationError{
					Path:    fmt.Sprintf("agents.list.%d.thinking_level", i),
					Message: fmt.Sprintf("invalid thinking_level %q (valid: off, low, medium, high)", *v),
					Code:    "invalid_enum",
				})
			}
		}
	}

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

	if doc.Heartbeat.Enabled && doc.Heartbeat.Interval < 5 {
		errors = append(errors, ValidationError{
			Path:    "heartbeat.interval",
			Message: "interval must be at least 5 minutes",
			Code:    "invalid_range",
		})
	}

	if doc.Channels.Native.Enabled {
		if doc.Channels.Native.Port <= 0 || doc.Channels.Native.Port > 65535 {
			errors = append(errors, ValidationError{
				Path:    "channels.native.port",
				Message: "port must be between 1 and 65535",
				Code:    "invalid_range",
			})
		}
	}

	type rateLimitCheck struct {
		path string
		val  int
	}
	for _, rc := range []rateLimitCheck{
		{"channels.native.rate_limit.pin_per_minute", doc.Channels.Native.RateLimit.PinPerMinute},
		{"channels.native.rate_limit.pair_per_minute", doc.Channels.Native.RateLimit.PairPerMinute},
		{"channels.native.rate_limit.refresh_per_minute", doc.Channels.Native.RateLimit.RefreshPerMinute},
		{"channels.native.rate_limit.api_per_minute", doc.Channels.Native.RateLimit.APIPerMinute},
		{"channels.native.rate_limit.ws_messages_per_minute", doc.Channels.Native.RateLimit.WSMessagesPerMinute},
	} {
		if rc.val < 0 {
			errors = append(errors, ValidationError{
				Path:    rc.path,
				Message: "rate limit must be 0 (use the built-in default) or greater",
				Code:    "invalid_range",
			})
		}
	}

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

	sortedProviderNames := make([]string, 0, len(doc.Providers))
	for name := range doc.Providers {
		sortedProviderNames = append(sortedProviderNames, name)
	}
	sort.Strings(sortedProviderNames)
	for _, name := range sortedProviderNames {
		provider := doc.Providers[name]
		if err := protocoltypes.ValidateCustomHeaders(provider.Headers); err != nil {
			errors = append(errors, ValidationError{
				Path:    fmt.Sprintf("providers.%s.headers", name),
				Message: err.Error(),
				Code:    "invalid_header",
			})
		}
	}

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

	if err := doc.Groups.ValidateGroups(); err != nil {
		errors = append(errors, ValidationError{
			Path:    "groups",
			Message: err.Error(),
			Code:    "invalid",
		})
	}

	return errors
}
