package tools

import (
	"sync"

	"github.com/xilistudios/lele/pkg/providers"
)

const (
	SubagentStatusRunning      = "running"
	SubagentStatusCompleted    = "completed"
	SubagentStatusNotDone      = "not_done"
	SubagentStatusNeedsContext = "needs_context"
	SubagentStatusFailed       = "failed"
	SubagentStatusCancelled    = "cancelled"
	SubagentStatusPending      = "pending"
	SubagentStatusUnknown      = "unknown"
)

type SubagentTask struct {
	ID                string
	Task              string
	Label             string
	AgentID           string
	ModelOverride     string
	OriginChannel     string
	OriginChatID      string
	OriginSessionKey  string
	SpawnerSessionKey string
	Status            string
	Summary           string
	Result            string
	ContextRequest    string
	Guidance          []string
	Created           int64
	Updated           int64
	Iterations        int
	doneCh            chan struct{}
	Progress          string
	Dependencies      []string
	MaxRetries        int
	RetryCount        int
	resultClaimed     bool
	resultWaiters     int
	lastErr           error
	mu                *sync.Mutex
}

func (task *SubagentTask) DoneChannel() <-chan struct{} {
	if task.mu != nil {
		task.mu.Lock()
		defer task.mu.Unlock()
	}
	return task.doneCh
}

func (task *SubagentTask) Snapshot() *SubagentTask {
	cp := *task
	if task.Guidance != nil {
		cp.Guidance = make([]string, len(task.Guidance))
		copy(cp.Guidance, task.Guidance)
	}
	if task.Dependencies != nil {
		cp.Dependencies = make([]string, len(task.Dependencies))
		copy(cp.Dependencies, task.Dependencies)
	}
	return &cp
}

func (task *SubagentTask) IsPending() bool {
	return task.Status == SubagentStatusPending
}

func (task *SubagentTask) IsTerminal() bool {
	switch task.Status {
	case SubagentStatusCompleted, SubagentStatusFailed, SubagentStatusNotDone, SubagentStatusCancelled:
		return true
	default:
		return false
	}
}

func (task *SubagentTask) InitDoneChannel() {
	if task.mu == nil {
		task.mu = &sync.Mutex{}
	}
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.doneCh == nil {
		task.doneCh = make(chan struct{})
	}
}

func (task *SubagentTask) SignalDone() {
	if task.mu != nil {
		task.mu.Lock()
		defer task.mu.Unlock()
	}
	if task.doneCh != nil {
		select {
		case <-task.doneCh:
		default:
			close(task.doneCh)
		}
	}
}

type AgentContextInfo struct {
	Context       string
	Workspace     string
	Name          string
	Model         string
	Provider      providers.LLMProvider
	MaxIterations int
	MaxTokens     int
	Temperature   float64
	ContextWindow int
	ThinkingLevel string
}

type subagentOutcome struct {
	Status         string
	Summary        string
	Details        string
	ContextRequest string
}
