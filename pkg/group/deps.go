package group

import (
	"context"

	"github.com/xilistudios/lele/pkg/bus"
)

type AgentContext struct {
	AgentID       string
	Name          string
	Workspace     string
	SystemPrompt  string
	ContextWindow int
	MaxTokens     int
}

type ResolveAgentFunc func(agentID string) (AgentContext, bool)

type TurnRequest struct {
	GroupID        string
	Speaker        string
	SystemPrompt   string
	Transcript     string
	Instruction    string
	MaxTokens      int
	EnableTools    bool
	OriginChannel  string
	OriginChatID   string
	OriginSenderID string

	OnToolCall func(toolCallID, toolName, args, status, result string)
}

type TurnExecutor func(ctx context.Context, req TurnRequest) (content string, tokens int, err error)

type Publisher func(bus.OutboundMessage)
