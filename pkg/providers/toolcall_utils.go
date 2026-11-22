package providers

import (
	"encoding/json"
	"fmt"
	"strings"
)

func buildCLIToolsPrompt(tools []ToolDefinition) string {
	var sb strings.Builder

	sb.WriteString("## Available Tools\n\n")
	sb.WriteString("When you need to use a tool, respond with ONLY a JSON object:\n\n")
	sb.WriteString("```json\n")
	sb.WriteString(
		`{"tool_calls":[{"id":"call_xxx","type":"function","function":{"name":"tool_name","arguments":"{...}"}}]}`,
	)
	sb.WriteString("\n```\n\n")
	sb.WriteString("CRITICAL: The 'arguments' field MUST be a JSON-encoded STRING.\n\n")
	sb.WriteString("### Tool Definitions:\n\n")

	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}
		sb.WriteString(fmt.Sprintf("#### %s\n", tool.Function.Name))
		if tool.Function.Description != "" {
			sb.WriteString(fmt.Sprintf("Description: %s\n", tool.Function.Description))
		}
		if len(tool.Function.Parameters) > 0 {
			paramsJSON, _ := json.Marshal(tool.Function.Parameters)
			sb.WriteString(fmt.Sprintf("Parameters:\n```json\n%s\n```\n", string(paramsJSON)))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func NormalizeToolCall(tc ToolCall) ToolCall {
	normalized := tc

	if normalized.Name == "" && normalized.Function != nil {
		normalized.Name = normalized.Function.Name
	}

	if normalized.Arguments == nil {
		normalized.Arguments = map[string]any{}
	}

	if len(normalized.Arguments) == 0 && normalized.Function != nil && normalized.Function.Arguments != "" {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(normalized.Function.Arguments), &parsed); err == nil && parsed != nil {
			normalized.Arguments = parsed
		}
	}

	argsJSON, _ := json.Marshal(normalized.Arguments)
	if normalized.Function == nil {
		normalized.Function = &FunctionCall{
			Name:      normalized.Name,
			Arguments: string(argsJSON),
		}
	} else {
		if normalized.Function.Name == "" {
			normalized.Function.Name = normalized.Name
		}
		if normalized.Name == "" {
			normalized.Name = normalized.Function.Name
		}
		if normalized.Function.Arguments == "" {
			normalized.Function.Arguments = string(argsJSON)
		}
	}

	return normalized
}
