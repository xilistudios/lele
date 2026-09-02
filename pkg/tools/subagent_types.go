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
	SubagentStatusPending      = "pending" // task waiting for dependencies to complete
)

type SubagentTask struct {
	ID               string
	Task             string
	Label            string
	AgentID          string
	ModelOverride    string // Optional model to use instead of the agent's default
	OriginChannel    string
	OriginChatID     string
	OriginSessionKey string
	// SpawnerSessionKey is the runtime session key of the agent loop that
	// called the spawn tool, captured from the tool context (empty when the
	// spawn happens outside an agent turn). OriginSessionKey is built from
	// message routing ("<channel>:<chatID>") and can differ in form from the
	// runtime key ("agent:<id>:<channel>:<chatID>", a bare uuid, an alias),
	// so cancellation matching uses this key first and falls back to the
	// origin (see tools.SessionKeysRelated, issue #230).
	SpawnerSessionKey string
	Status            string
	Summary           string
	Result            string
	ContextRequest    string
	Guidance          []string
	Created           int64
	Updated           int64
	Iterations        int
	doneCh            chan struct{} // closed when the task reaches a terminal state (event-driven waiting)
	Progress          string        // latest intermediate progress update from the subagent
	Dependencies      []string      // task IDs that must complete before this task starts
	MaxRetries        int           // maximum number of automatic retry attempts for transient failures
	RetryCount        int           // current number of retry attempts made
	delivered         bool          // tracks whether result was already consumed via wait_for_subagent
	// lastErr is the raw error from the most recent run of this task, kept so
	// the retry decision can ask the provider layer's classifier instead of
	// pattern-matching the rendered Result string. Invariant: it is set ONLY by
	// the generic error branch of runTaskImpl; the timeout and cancellation
	// branches deliberately leave it nil, and nil means NOT transient - a
	// subagent that burned its whole SubagentTimeoutMinutes must not get that
	// budget again. It is cleared whenever a run starts or restarts, and on
	// success. Unexported and never JSON-marshalled; Snapshot's value copy
	// carries it, which is fine for a read-only error reference.
	lastErr error
	mu      *sync.Mutex // protects doneCh and delivered from concurrent access
}

// DoneChannel returns the done channel for this task.
// Returns nil if not initialized.
func (task *SubagentTask) DoneChannel() <-chan struct{} {
	if task.mu != nil {
		task.mu.Lock()
		defer task.mu.Unlock()
	}
	return task.doneCh
}

// Snapshot returns a deep copy of the task safe for reading without holding
// the manager's lock.  Value fields (strings, ints, bools) are copied by
// the struct assignment.  Slice fields (Guidance, Dependencies) get fresh
// backing arrays so the snapshot cannot alias the live task's storage.
// The doneCh channel reference is preserved — the snapshot shares the same
// channel the runner will close, so event-driven waiting keeps working.
func (task *SubagentTask) Snapshot() *SubagentTask {
	cp := *task // shallow copy (values + channel ref)
	if task.Guidance != nil {
		cp.Guidance = make([]string, len(task.Guidance))
		copy(cp.Guidance, task.Guidance)
	}
	if task.Dependencies != nil {
		cp.Dependencies = make([]string, len(task.Dependencies))
		copy(cp.Dependencies, task.Dependencies)
	}
	// doneCh is a channel reference — keep the same underlying channel so
	// that select { case <-doneCh } in waiters still works when the runner
	// closes it.
	return &cp
}

// IsPending returns true if the task has unmet dependencies and hasn't started yet.
func (task *SubagentTask) IsPending() bool {
	return task.Status == SubagentStatusPending
}

// IsTerminal returns true if the task is in a state where it will not transition again.
func (task *SubagentTask) IsTerminal() bool {
	switch task.Status {
	case SubagentStatusCompleted, SubagentStatusFailed, SubagentStatusNotDone, SubagentStatusCancelled:
		return true
	default:
		return false
	}
}

// InitDoneChannel initializes the done channel if not already set.
// This should be called when creating a new task.
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

// SignalDone closes the done channel to notify waiters.
// Safe to call multiple times (uses select pattern to detect already-closed channel).
func (task *SubagentTask) SignalDone() {
	if task.mu != nil {
		task.mu.Lock()
		defer task.mu.Unlock()
	}
	if task.doneCh != nil {
		select {
		case <-task.doneCh:
			// already closed
		default:
			close(task.doneCh)
		}
	}
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
