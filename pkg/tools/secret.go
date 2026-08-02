package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/keyring"
)

// SecretTool gives agents scoped access to stored secrets by name. Values are
// returned to the LLM in the tool result (so the agent can use them in API
// calls or shell commands), but every access is audit-logged and the result is
// flagged sensitive so UIs can mask it.
type SecretTool struct {
	svc *keyring.Service
}

// NewSecretTool creates a SecretTool backed by the given keyring service.
func NewSecretTool(svc *keyring.Service) *SecretTool {
	return &SecretTool{svc: svc}
}

func (t *SecretTool) Name() string { return "secret" }

func (t *SecretTool) Description() string {
	return "Access stored secrets by name. Use action 'list' to see available secret names and descriptions (values are never shown in the list), or 'get' with a name to retrieve a secret value for use in API calls or commands. Secret access is scoped per agent and audited."
}

func (t *SecretTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"list", "get"},
				"description": "Action to perform: 'list' shows available secrets (no values), 'get' retrieves a secret value by name.",
			},
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Secret name (required for 'get'), e.g. \"openai.api_key\".",
			},
		},
		"required": []string{"action"},
	}
}

func (t *SecretTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.svc == nil {
		return ErrorResult("keyring service is not available")
	}

	action, _ := args["action"].(string)
	agentID, sessionKey := AgentToolContextFromCtx(ctx)
	if agentID == "" {
		agentID = "unknown"
	}

	switch action {
	case "list":
		return t.list(agentID)
	case "get":
		name, _ := args["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return ErrorResult("'name' is required for action 'get'")
		}
		return t.get(name, agentID, sessionKey)
	default:
		return ErrorResult(fmt.Sprintf("unknown action %q (expected 'list' or 'get')", action))
	}
}

func (t *SecretTool) list(agentID string) *ToolResult {
	secrets, err := t.svc.ListForAgent(agentID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to list secrets: %v", err))
	}
	if len(secrets) == 0 {
		return SilentResult("No secrets are available to this agent.")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Available secrets (%d):\n", len(secrets))
	for _, s := range secrets {
		fmt.Fprintf(&b, "- %s", s.Name)
		if s.Description != "" {
			fmt.Fprintf(&b, " — %s", s.Description)
		}
		if len(s.Tags) > 0 {
			fmt.Fprintf(&b, " [tags: %s]", strings.Join(s.Tags, ", "))
		}
		if len(s.Scope) > 0 {
			fmt.Fprintf(&b, " [scope: %s]", strings.Join(s.Scope, ", "))
		}
		b.WriteString("\n")
	}
	b.WriteString("\nUse action 'get' with a name to retrieve a value.")

	res := SilentResult(b.String())
	res.Metadata = map[string]string{"sensitive": "true"}
	return res
}

func (t *SecretTool) get(name, agentID, sessionKey string) *ToolResult {
	value, err := t.svc.GetForAgent(name, agentID, sessionKey)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get secret %q: %v", name, err))
	}

	res := NewToolResult(value)
	res.Silent = true
	res.Metadata = map[string]string{
		"sensitive":   "true",
		"secret_name": name,
	}
	return res
}
