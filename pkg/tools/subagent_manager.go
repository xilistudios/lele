package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
)

type SubagentManager struct {
	tasks                 map[string]*SubagentTask
	cancels               map[string]context.CancelFunc
	mu                    sync.RWMutex
	provider              providers.LLMProvider
	defaultModel          string
	bus                   *bus.MessageBus
	workspace             string
	tools                 *ToolRegistry
	getAgentContext       func(agentID string) AgentContextInfo
	maxIterations         int
	maxTokens             int
	temperature           float64
	hasMaxTokens          bool
	hasTemperature        bool
	timeout               time.Duration // 0 means no timeout
	retentionPeriod       time.Duration // how long to keep terminal tasks before cleanup (0 = no cleanup)
	nextID                int
	sessionRecorder       SessionRecorder
	sessionKeyCallback    func(sessionKey, agentID string)                          // called when subagent session key is created
	registerSessionCancel func(sessionKey string, cancel context.CancelFunc) func() // registers cancel function on session manager
	sessionEvictCallback  func(sessionKey string)                                   // called when a terminal task is cleaned up to evict its session from memory
	maxConcurrent         int                                                       // max concurrent running tasks (0 = unlimited)
	defaultMaxRetries     int                                                       // default max retry attempts for transient failures
}

func NewSubagentManager(provider providers.LLMProvider, defaultModel, workspace string, bus *bus.MessageBus, maxIterations int) *SubagentManager {
	if maxIterations <= 0 {
		maxIterations = 100 // Sensible fallback, but callers should always provide a real value
	}
	return &SubagentManager{
		tasks:           make(map[string]*SubagentTask),
		cancels:         make(map[string]context.CancelFunc),
		provider:        provider,
		defaultModel:    defaultModel,
		bus:             bus,
		workspace:       workspace,
		tools:           NewToolRegistry(),
		maxIterations:   maxIterations,
		nextID:          1,
		retentionPeriod: 5 * time.Minute,
	}
}

// SetLLMOptions sets max tokens and temperature for subagent LLM calls.
func (sm *SubagentManager) SetLLMOptions(maxTokens int, temperature float64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.maxTokens = maxTokens
	sm.hasMaxTokens = true
	sm.temperature = temperature
	sm.hasTemperature = true
}

// SetMaxIterations sets the maximum number of tool iterations for subagent execution.
func (sm *SubagentManager) SetMaxIterations(maxIterations int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.maxIterations = maxIterations
}

// SetTimeout sets the maximum execution time for subagent tasks.
// A value of 0 means no timeout (subagent runs until completion or iteration limit).
func (sm *SubagentManager) SetTimeout(timeout time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.timeout = timeout
}

// SetRetentionPeriod sets how long terminal tasks are kept before cleanup.
// A value of 0 disables automatic cleanup.
func (sm *SubagentManager) SetRetentionPeriod(period time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.retentionPeriod = period
}

// SetMaxConcurrent sets the maximum number of subagent tasks that can run
// concurrently. A value of 0 means unlimited.
func (sm *SubagentManager) SetMaxConcurrent(max int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.maxConcurrent = max
}

// SetDefaultMaxRetries sets the default maximum number of automatic retry
// attempts for transient failures.
func (sm *SubagentManager) SetDefaultMaxRetries(maxRetries int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.defaultMaxRetries = maxRetries
}

// SetTools sets the tool registry for subagent execution.
// If not set, subagent will have access to the provided tools.
func (sm *SubagentManager) SetTools(tools *ToolRegistry) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.tools = tools
}

// SetAgentContextCallback sets a callback function that returns the context info
// for a specific agent ID. Each subagent type gets its own context, workspace, and name.
func (sm *SubagentManager) SetAgentContextCallback(callback func(agentID string) AgentContextInfo) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.getAgentContext = callback
}

func (sm *SubagentManager) SetSessionRecorder(rec SessionRecorder) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessionRecorder = rec
}

// SetSessionKeyCallback sets a callback function that is called whenever a
// subagent session key is created. The callback receives the session key and
// the agent ID of the agent whose session storage holds the history.
// This enables the owner to build a lookup map for O(1) session history retrieval.
func (sm *SubagentManager) SetSessionKeyCallback(callback func(sessionKey, agentID string)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessionKeyCallback = callback
}

// SetRegisterSessionCancelCallback sets a callback function that registers a subagent session cancel function.
func (sm *SubagentManager) SetRegisterSessionCancelCallback(callback func(sessionKey string, cancel context.CancelFunc) func()) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.registerSessionCancel = callback
}

// SetSessionEvictCallback sets a callback that is called when a terminal
// subagent task is cleaned up. The callback receives the subagent's session
// key so the owner can evict the session from the in-memory SessionManager,
// freeing RAM. The session data remains on disk.
func (sm *SubagentManager) SetSessionEvictCallback(callback func(sessionKey string)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessionEvictCallback = callback
}

// RegisterTool registers a tool for subagent execution.
func (sm *SubagentManager) RegisterTool(tool Tool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.tools.Register(tool)
}

// countRunning returns the number of tasks currently in "running" status.
func (sm *SubagentManager) countRunning() int {
	count := 0
	for _, task := range sm.tasks {
		if task.Status == SubagentStatusRunning {
			count++
		}
	}
	return count
}

// checkDependencies returns true if all dependency tasks have reached terminal state.
func (sm *SubagentManager) checkDependencies(task *SubagentTask) bool {
	for _, depID := range task.Dependencies {
		dep, ok := sm.tasks[depID]
		if !ok {
			continue // dependency doesn't exist, treat as satisfied
		}
		if !dep.IsTerminal() {
			return false
		}
	}
	return true
}

func (sm *SubagentManager) Spawn(ctx context.Context, task, label, agentID, originChannel, originChatID string, callback AsyncCallback) (string, error) {
	return sm.SpawnWithDeps(ctx, task, label, agentID, originChannel, originChatID, callback, nil, 0)
}

// SpawnWithDeps is like Spawn but also accepts dependency task IDs and max retries.
// If dependencies are specified and not yet satisfied, the task starts in "pending"
// status and a background goroutine polls until all dependencies are met before
// transitioning to "running".
func (sm *SubagentManager) SpawnWithDeps(ctx context.Context, task, label, agentID, originChannel, originChatID string, callback AsyncCallback, dependencies []string, maxRetries int) (string, error) {
	sm.CleanupTerminalTasks() // Prevent unbounded growth of terminal tasks
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Enforce concurrency limit
	if sm.maxConcurrent > 0 && sm.countRunning() >= sm.maxConcurrent {
		return "", fmt.Errorf("maximum concurrent subagents reached (%d running, limit %d)", sm.countRunning(), sm.maxConcurrent)
	}

	taskID := fmt.Sprintf("subagent-%d", sm.nextID)
	sm.nextID++

	originSessionKey := originChannel + ":" + originChatID
	if strings.HasPrefix(originChatID, originChannel+":") {
		originSessionKey = originChatID
	}

	// Determine initial status based on dependencies
	initialStatus := SubagentStatusRunning
	if len(dependencies) > 0 && !sm.checkDependencies(&SubagentTask{Dependencies: dependencies}) {
		initialStatus = SubagentStatusPending
	}

	// Use default max retries if not explicitly set
	if maxRetries <= 0 {
		maxRetries = sm.defaultMaxRetries
	}

	subagentTask := &SubagentTask{
		ID:               taskID,
		Task:             task,
		Label:            label,
		AgentID:          agentID,
		OriginChannel:    originChannel,
		OriginChatID:     originChatID,
		OriginSessionKey: originSessionKey,
		Status:           initialStatus,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		Dependencies:     dependencies,
		MaxRetries:       maxRetries,
	}
	subagentTask.InitDoneChannel()
	sm.tasks[taskID] = subagentTask

	// Use context.Background() to decouple from parent agent's context
	// This allows the subagent to continue running even after the parent agent finishes
	taskCtx, cancel := context.WithCancel(context.Background())
	sm.cancels[taskID] = cancel

	if initialStatus == SubagentStatusPending {
		// Start a lightweight goroutine that polls until dependencies are met
		go func() {
			defer subagentTask.SignalDone()

			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-taskCtx.Done():
					// Task was cancelled while waiting for dependencies
					sm.mu.Lock()
					if subagentTask.Status == SubagentStatusPending {
						subagentTask.Status = SubagentStatusCancelled
						subagentTask.Summary = "Task cancelled while waiting for dependencies"
						subagentTask.Result = "Task cancelled while waiting for dependencies"
						subagentTask.Updated = time.Now().UnixMilli()
					}
					sm.mu.Unlock()
					return
				case <-ticker.C:
					sm.mu.Lock()
					allMet := sm.checkDependencies(subagentTask)
					if allMet {
						subagentTask.Status = SubagentStatusRunning
						subagentTask.Updated = time.Now().UnixMilli()
						sm.mu.Unlock()
						sm.runTask(taskCtx, subagentTask, callback)
						return
					}
					sm.mu.Unlock()
				}
			}
		}()
	} else {
		go func() {
			sm.runTask(taskCtx, subagentTask, callback)
			subagentTask.SignalDone()
		}()
	}

	if label != "" {
		return fmt.Sprintf("Spawned subagent task %s ('%s') for task: %s", taskID, label, task), nil
	}
	return fmt.Sprintf("Spawned subagent task %s for task: %s", taskID, task), nil
}

func (sm *SubagentManager) ContinueTask(ctx context.Context, taskID, guidance string, callback AsyncCallback) (string, error) {
	guidance = strings.TrimSpace(guidance)
	if guidance == "" {
		return "", fmt.Errorf("guidance is required")
	}

	sm.CleanupTerminalTasks() // Prevent unbounded growth of terminal tasks
	sm.mu.Lock()
	task, ok := sm.tasks[taskID]
	if !ok {
		sm.mu.Unlock()
		return "", fmt.Errorf("subagent task not found: %s", taskID)
	}
	if task.Status != SubagentStatusNeedsContext {
		status := task.Status
		sm.mu.Unlock()
		return "", fmt.Errorf("subagent task %s is not waiting for context (status: %s)", taskID, status)
	}

	task.Guidance = append(task.Guidance, guidance)
	task.Status = SubagentStatusRunning
	task.Updated = time.Now().UnixMilli()
	// Use context.Background() to decouple from parent agent's context
	taskCtx, cancel := context.WithCancel(context.Background())
	sm.cancels[taskID] = cancel
	sm.mu.Unlock()

	go sm.runTask(taskCtx, task, callback)

	return fmt.Sprintf("Continuing subagent task %s with new guidance.", taskID), nil
}

func (sm *SubagentManager) GetTask(taskID string) (*SubagentTask, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	task, ok := sm.tasks[taskID]
	return task, ok
}

// MarkDelivered atomically marks a task's result as delivered.
// Returns true if the task was already delivered (i.e., this call lost the race).
// Returns false if this is the first delivery.
func (sm *SubagentManager) MarkDelivered(taskID string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	task, ok := sm.tasks[taskID]
	if !ok || task == nil {
		return false // Task not found, allow delivery (edge case)
	}
	if task.delivered {
		return true // Already delivered
	}
	task.delivered = true
	return false // First delivery
}

// CleanupTerminalTasks removes tasks that have been in a terminal state
// (completed, failed, cancelled, not_done) for longer than the retention
// period. This prevents the tasks map from growing indefinitely.
// Returns the number of tasks removed.
func (sm *SubagentManager) CleanupTerminalTasks() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.retentionPeriod <= 0 {
		return 0
	}

	now := time.Now().UnixMilli()
	thresholdMs := int64(sm.retentionPeriod / time.Millisecond)
	removed := 0

	evictCallback := sm.sessionEvictCallback
	for taskID, task := range sm.tasks {
		if !isSubagentTerminalStatus(task.Status) {
			continue
		}
		// Use Updated timestamp — it's set when the task reaches terminal state
		if now-task.Updated > thresholdMs {
			// Evict the subagent's session from memory before deleting the task
			if evictCallback != nil {
				sessionKey := task.OriginSessionKey + ":" + taskID
				evictCallback(sessionKey)
			}
			delete(sm.tasks, taskID)
			removed++
		}
	}

	return removed
}

func (sm *SubagentManager) ListTasks() []*SubagentTask {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	tasks := make([]*SubagentTask, 0, len(sm.tasks))
	for _, task := range sm.tasks {
		tasks = append(tasks, task)
	}
	return tasks
}

func (sm *SubagentManager) StopTask(taskID string) bool {
	sm.mu.Lock()
	task, taskExists := sm.tasks[taskID]
	cancel, ok := sm.cancels[taskID]
	canStop := taskExists && task != nil && (task.Status == SubagentStatusRunning || task.Status == SubagentStatusNeedsContext || task.Status == SubagentStatusPending)
	if ok {
		delete(sm.cancels, taskID)
	}
	if canStop {
		task.Status = SubagentStatusCancelled
		task.Summary = "Task cancelled"
		task.Result = "Task cancelled"
		task.ContextRequest = ""
		task.Updated = time.Now().UnixMilli()
		task.SignalDone()
	}
	sm.mu.Unlock()
	if ok && cancel != nil {
		cancel()
	}
	return ok || canStop
}

// StopAll stops all running subagent tasks.
func (sm *SubagentManager) StopAll() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	stoppedCount := 0
	handled := make(map[string]struct{}, len(sm.cancels))
	for taskID, cancel := range sm.cancels {
		if cancel != nil {
			cancel()
			stoppedCount++
		}
		handled[taskID] = struct{}{}
		if task, ok := sm.tasks[taskID]; ok {
			task.Status = SubagentStatusCancelled
			task.Summary = "Task cancelled"
			task.Result = "Task cancelled"
			task.ContextRequest = ""
			task.Updated = time.Now().UnixMilli()
			task.SignalDone()
		}
		delete(sm.cancels, taskID)
	}

	for taskID, task := range sm.tasks {
		if _, alreadyHandled := handled[taskID]; alreadyHandled {
			continue
		}
		if task.Status != SubagentStatusNeedsContext && task.Status != SubagentStatusPending {
			continue
		}
		task.Status = SubagentStatusCancelled
		task.Summary = "Task cancelled"
		task.Result = "Task cancelled"
		task.ContextRequest = ""
		task.Updated = time.Now().UnixMilli()
		task.SignalDone()
		stoppedCount++
	}

	return stoppedCount
}

// GetToolRegistry returns the tool registry available to subagents.
// This allows tests and callers to inspect what tools subagents can use.
func (sm *SubagentManager) GetToolRegistry() *ToolRegistry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.tools
}

// HasTool checks if a tool with the given name is available to subagents.
func (sm *SubagentManager) HasTool(name string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	_, ok := sm.tools.Get(name)
	return ok
}
