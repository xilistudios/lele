package tools

import (
	"github.com/xilistudios/lele/pkg/providers"
)

const (
	SubagentStatusRunning      = "running"
	SubagentStatusCompleted    = "completed"
	SubagentStatusNotDone      = "not_done"
	SubagentStatusNeedsContext = "needs_context"
	SubagentStatusFailed       = "failed"
	SubagentStatusCancelled    = "cancelled"
)

type SubagentTask struct {
	ID               string
	Task             string
	Label            string
	AgentID          string
	OriginChannel    string
	OriginChatID     string
	OriginSessionKey string
	Status           string
	Summary          string
	Result           string
	ContextRequest   string
	Guidance         []string
	Created          int64
	Updated          int64
	Iterations       int
	delivered        bool // tracks whether result was already consumed via wait_for_subagent
}

// AgentContextInfo holds the context and workspace info for a subagent
type AgentContextInfo struct {
	Context       string                // Full context (AGENT.md, SOUL.md, etc.)
	Workspace     string                // Agent's workspace path
	Name          string                // Agent display name
	Model         string                // Agent's model (e.g., "alibaba/kimi-k2.5")
	Provider      providers.LLMProvider // Agent's LLM provider (critical for correct API routing)
	MaxIterations int                   // Agent's max tool iterations (0 means use SubagentManager default)
	MaxTokens     int                   // Agent's max tokens (0 means use SubagentManager default)
	Temperature   float64               // Agent's temperature (0 means use SubagentManager default)
	ContextWindow int                   // Agent's context window for compaction (0 = no compaction)
}

type subagentOutcome struct {
	Status         string
	Summary        string
	Details        string
	ContextRequest string
}
