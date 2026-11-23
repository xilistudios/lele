package tools

import "context"

type contextKey string

const (
	channelCtxKey    contextKey = "tool_channel"
	chatIDCtxKey     contextKey = "tool_chat_id"
	agentIDCtxKey    contextKey = "tool_agent_id"
	sessionKeyCtxKey contextKey = "tool_session_key"

	bypassGuardCtxKey     contextKey = "exec_bypass_guard"
	approvedCommandCtxKey contextKey = "exec_approved_command"

	subagentScopeCtxKey contextKey = "tool_subagent_scope"
)

func WithSubagentScope(ctx context.Context) context.Context {
	return context.WithValue(ctx, subagentScopeCtxKey, true)
}

func SubagentScopeFromCtx(ctx context.Context) bool {
	v, _ := ctx.Value(subagentScopeCtxKey).(bool)
	return v
}

func WithToolContext(ctx context.Context, channel, chatID string) context.Context {
	ctx = context.WithValue(ctx, channelCtxKey, channel)
	ctx = context.WithValue(ctx, chatIDCtxKey, chatID)
	return ctx
}

func WithAgentToolContext(ctx context.Context, agentID, sessionKey string) context.Context {
	ctx = context.WithValue(ctx, agentIDCtxKey, agentID)
	ctx = context.WithValue(ctx, sessionKeyCtxKey, sessionKey)
	return ctx
}

func ToolContextFromCtx(ctx context.Context) (channel, chatID string) {
	ch, _ := ctx.Value(channelCtxKey).(string)
	cid, _ := ctx.Value(chatIDCtxKey).(string)
	return ch, cid
}

func AgentToolContextFromCtx(ctx context.Context) (agentID, sessionKey string) {
	aid, _ := ctx.Value(agentIDCtxKey).(string)
	sk, _ := ctx.Value(sessionKeyCtxKey).(string)
	return aid, sk
}

func WithBypassGuard(ctx context.Context, approvedCmd string) context.Context {
	ctx = context.WithValue(ctx, bypassGuardCtxKey, true)
	ctx = context.WithValue(ctx, approvedCommandCtxKey, NormalizeWhitelistKey(approvedCmd))
	return ctx
}

func BypassGuardFor(ctx context.Context, command string) bool {
	bypass, _ := ctx.Value(bypassGuardCtxKey).(bool)
	if !bypass {
		return false
	}
	approved, _ := ctx.Value(approvedCommandCtxKey).(string)
	return approved != "" && NormalizeWhitelistKey(command) == approved
}

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]interface{}
	Execute(ctx context.Context, args map[string]interface{}) *ToolResult
}

type ContextualTool interface {
	Tool
	SetContext(channel, chatID string)
}

type SessionAwareTool interface {
	Tool
	SetSessionContext(channel, chatID, sessionKey string)
}

type AsyncCallback func(ctx context.Context, result *ToolResult)

type AsyncTool interface {
	Tool
	SetCallback(cb AsyncCallback)
}

func ToolToSchema(tool Tool) map[string]interface{} {
	return map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters":  tool.Parameters(),
		},
	}
}
