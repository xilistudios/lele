package group

import (
	"context"

	"github.com/xilistudios/lele/pkg/bus"
)

// AgentContext is the resolved configuration of a participant agent, injected
// by the host (pkg/agent) so that pkg/group never imports pkg/agent.
// Mirrors the SubagentManager.getAgentContext pattern in pkg/tools.
type AgentContext struct {
	AgentID       string
	Name          string
	Workspace     string
	SystemPrompt  string // persona / base system prompt of the agent
	ContextWindow int
	MaxTokens     int
}

// ResolveAgentFunc resolves a participant agent ID to its context.
// Returns false if the agent does not exist or is not allowed.
type ResolveAgentFunc func(agentID string) (AgentContext, bool)

// TurnRequest is the input for executing a group turn (one LLM call).
type TurnRequest struct {
	GroupID      string // group id, used as the session semaphore key
	Speaker      string // agent ID of the speaker
	SystemPrompt string // persona + group role annex (built by render.go)
	Transcript   string // rendered shared transcript (built by render.go)
	Instruction  string // strategy instruction for this turn
	MaxTokens    int    // max tokens per turn (0 = agent default)
	EnableTools  bool   // whether the speaker may use tools this turn
}

// TurnExecutor executes a group turn and returns the produced content and
// tokens used. Implemented by pkg/agent and injected into GroupManager.
type TurnExecutor func(ctx context.Context, req TurnRequest) (content string, tokens int, err error)

// Publisher publishes a group event to the message bus. The host (pkg/agent)
// provides the implementation (typically bus.PublishOutbound). Injected so
// that tests can run without a real bus.
type Publisher func(bus.OutboundMessage)
