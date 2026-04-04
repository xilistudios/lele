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

const (
	SubagentStatusRunning      = "running"
	SubagentStatusCompleted    = "completed"
	SubagentStatusNotDone      = "not_done"
	SubagentStatusNeedsContext = "needs_context"
	SubagentStatusFailed       = "failed"
	SubagentStatusCancelled    = "cancelled"
)

type SubagentTask struct {
	ID             string
	Task           string
	Label          string
	AgentID        string
	OriginChannel  string
	OriginChatID   string
	Status         string
	Summary        string
	Result         string
	ContextRequest string
	Guidance       []string
	Created        int64
	Updated        int64
	Iterations     int
}

// AgentContextInfo holds the context and workspace info for a subagent
type AgentContextInfo struct {
	Context   string                // Full context (AGENT.md, SOUL.md, etc.)
	Workspace string                // Agent's workspace path
	Name      string                // Agent display name
	Model     string                // Agent's model (e.g., "alibaba/kimi-k2.5")
	Provider  providers.LLMProvider // Agent's LLM provider (critical for correct API routing)
}

type subagentOutcome struct {
	Status         string
	Summary        string
	Details        string
	ContextRequest string
}

func buildSubagentSystemPrompt(baseContext, agentID, agentName, agentWorkspace string) string {
	identity := "You are a focused subagent."
	if agentID != "" {
		identity = "You are a focused " + agentID + " subagent."
	}

	contract := strings.Join([]string{
		"## Subagent Contract",
		"- Work independently on the assigned task using available tools.",
		"- Do not send messages to users, Telegram, or any external chat/channel.",
		"- Report your outcome only in the final response using the required format below.",
		"- If the task is complete, return STATUS: completed.",
		"- If the task cannot be completed with the current tools/constraints, return STATUS: not_done.",
		"- If you need missing information from the parent agent or user, return STATUS: needs_context.",
		"",
		"Use this exact structure:",
		"STATUS: completed | not_done | needs_context",
		"SUMMARY: one-line summary",
		"CONTEXT_NEEDED: what is missing (required only for needs_context)",
		"DETAILS:",
		"full details",
	}, "\n")

	if baseContext == "" {
		baseContext = identity
	} else {
		baseContext = baseContext + "\n\n---\n\n## Subagent Identity\n\n" +
			"**Agent Type:** " + agentName + " (" + agentID + ")\n" +
			"**Workspace:** " + agentWorkspace
	}

	return baseContext + "\n\n---\n\n" + contract
}

func normalizeSubagentStatus(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.NewReplacer("-", "_", " ", "_").Replace(normalized)

	switch normalized {
	case "completed", "complete", "done", "finished", "success", "task_completed", "task_finished":
		return SubagentStatusCompleted
	case "not_done", "notdone", "failed", "failure", "unable", "cannot_complete", "task_not_done", "not_completed":
		return SubagentStatusNotDone
	case "needs_context", "need_context", "context_needed", "needs_more_context", "needs_more_information", "needs_guidance":
		return SubagentStatusNeedsContext
	case "cancelled", "canceled":
		return SubagentStatusCancelled
	default:
		return SubagentStatusCompleted
	}
}

func summarizeSubagentText(text string) string {
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		return trimmed
	}
	return ""
}

func parseSubagentOutcome(raw string) subagentOutcome {
	outcome := subagentOutcome{
		Status:  SubagentStatusCompleted,
		Details: strings.TrimSpace(raw),
	}

	var detailLines []string
	collectDetails := false

	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		switch {
		case strings.HasPrefix(lower, "status:"):
			outcome.Status = normalizeSubagentStatus(strings.TrimSpace(trimmed[len("status:"):]))
		case strings.HasPrefix(lower, "summary:"):
			outcome.Summary = strings.TrimSpace(trimmed[len("summary:"):])
		case strings.HasPrefix(lower, "context_needed:"),
			strings.HasPrefix(lower, "context needed:"),
			strings.HasPrefix(lower, "needs_context:"),
			strings.HasPrefix(lower, "needs context:"),
			strings.HasPrefix(lower, "question:"),
			strings.HasPrefix(lower, "request:"):
			if idx := strings.Index(trimmed, ":"); idx >= 0 {
				outcome.ContextRequest = strings.TrimSpace(trimmed[idx+1:])
			}
		case strings.HasPrefix(lower, "details:"):
			collectDetails = true
			if value := strings.TrimSpace(trimmed[len("details:"):]); value != "" {
				detailLines = append(detailLines, value)
			}
		default:
			if collectDetails {
				detailLines = append(detailLines, line)
			}
		}
	}

	if len(detailLines) > 0 {
		outcome.Details = strings.TrimSpace(strings.Join(detailLines, "\n"))
	}

	if outcome.Summary == "" {
		outcome.Summary = summarizeSubagentText(outcome.Details)
	}

	if outcome.Status == SubagentStatusCompleted {
		lowerRaw := strings.ToLower(raw)
		switch {
		case strings.Contains(lowerRaw, "needs context"), strings.Contains(lowerRaw, "need more context"), strings.Contains(lowerRaw, "need more information"), strings.Contains(lowerRaw, "need additional context"):
			outcome.Status = SubagentStatusNeedsContext
		case strings.Contains(lowerRaw, "cannot complete"), strings.Contains(lowerRaw, "unable to complete"), strings.Contains(lowerRaw, "task not done"), strings.Contains(lowerRaw, "not completed"):
			outcome.Status = SubagentStatusNotDone
		}
	}

	if outcome.Status == SubagentStatusNeedsContext && outcome.ContextRequest == "" {
		outcome.ContextRequest = outcome.Summary
	}

	return outcome
}

func (task *SubagentTask) displayLabel() string {
	if strings.TrimSpace(task.Label) == "" {
		return "(unnamed)"
	}
	return task.Label
}

func (task *SubagentTask) buildMessages(systemPrompt string) []providers.Message {
	messages := []providers.Message{
		{
			Role:    "system",
			Content: systemPrompt,
		},
		{
			Role:    "user",
			Content: task.Task,
		},
	}

	if task.Result != "" || task.ContextRequest != "" || task.Summary != "" {
		previous := []string{
			"Previous progress report:",
			fmt.Sprintf("STATUS: %s", task.Status),
		}
		if task.Summary != "" {
			previous = append(previous, fmt.Sprintf("SUMMARY: %s", task.Summary))
		}
		if task.ContextRequest != "" {
			previous = append(previous, fmt.Sprintf("CONTEXT_NEEDED: %s", task.ContextRequest))
		}
		if task.Result != "" {
			previous = append(previous, "DETAILS:", task.Result)
		}
		messages = append(messages, providers.Message{
			Role:    "assistant",
			Content: strings.Join(previous, "\n"),
		})
	}

	if len(task.Guidance) > 0 {
		messages = append(messages, providers.Message{
			Role: "user",
			Content: "Additional guidance from the parent agent/user:\n" +
				strings.Join(task.Guidance, "\n\n") +
				"\n\nContinue the original task without repeating completed work.",
		})
	}

	return messages
}

func (task *SubagentTask) statusMessage() string {
	lines := []string{
		"Subagent status update.",
		fmt.Sprintf("Task ID: %s", task.ID),
		fmt.Sprintf("Label: %s", task.displayLabel()),
	}
	if task.AgentID != "" {
		lines = append(lines, fmt.Sprintf("Agent: %s", task.AgentID))
	}
	lines = append(lines, fmt.Sprintf("Status: %s", task.Status))
	if task.Summary != "" {
		lines = append(lines, fmt.Sprintf("Summary: %s", task.Summary))
	}
	if task.ContextRequest != "" {
		lines = append(lines, fmt.Sprintf("Context needed: %s", task.ContextRequest))
	}
	if task.Result != "" {
		lines = append(lines, "Details:\n"+task.Result)
	}

	switch task.Status {
	case SubagentStatusNeedsContext:
		lines = append(lines,
			fmt.Sprintf("The subagent is paused waiting for guidance. Continue it with /subagents continue %s <guidance> once the missing context is available.", task.ID),
		)
	case SubagentStatusNotDone:
		lines = append(lines,
			"The subagent could not complete the task with the current constraints. Decide whether to retry, re-scope, or ask the user for a different plan.",
		)
	case SubagentStatusCompleted:
		lines = append(lines,
			"The subagent finished successfully. Decide whether to reply to the user directly or keep processing the result.",
		)
	}

	return strings.Join(lines, "\n")
}

type SubagentManager struct {
	tasks           map[string]*SubagentTask
	cancels         map[string]context.CancelFunc
	mu              sync.RWMutex
	provider        providers.LLMProvider
	defaultModel    string
	bus             *bus.MessageBus
	workspace       string
	tools           *ToolRegistry
	getAgentContext func(agentID string) AgentContextInfo // Callback to get context info for specific agent
	maxIterations   int
	maxTokens       int
	temperature     float64
	hasMaxTokens    bool
	hasTemperature  bool
	nextID          int
}

func NewSubagentManager(provider providers.LLMProvider, defaultModel, workspace string, bus *bus.MessageBus) *SubagentManager {
	return &SubagentManager{
		tasks:         make(map[string]*SubagentTask),
		cancels:       make(map[string]context.CancelFunc),
		provider:      provider,
		defaultModel:  defaultModel,
		bus:           bus,
		workspace:     workspace,
		tools:         NewToolRegistry(),
		maxIterations: 20, // Increased from 10 to allow more complex tasks
		nextID:        1,
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

// RegisterTool registers a tool for subagent execution.
func (sm *SubagentManager) RegisterTool(tool Tool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.tools.Register(tool)
}

func (sm *SubagentManager) Spawn(ctx context.Context, task, label, agentID, originChannel, originChatID string, callback AsyncCallback) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	taskID := fmt.Sprintf("subagent-%d", sm.nextID)
	sm.nextID++

	subagentTask := &SubagentTask{
		ID:            taskID,
		Task:          task,
		Label:         label,
		AgentID:       agentID,
		OriginChannel: originChannel,
		OriginChatID:  originChatID,
		Status:        SubagentStatusRunning,
		Created:       time.Now().UnixMilli(),
		Updated:       time.Now().UnixMilli(),
	}
	sm.tasks[taskID] = subagentTask

	// Use context.Background() to decouple from parent agent's context
	// This allows the subagent to continue running even after the parent agent finishes
	taskCtx, cancel := context.WithCancel(context.Background())
	sm.cancels[taskID] = cancel
	go sm.runTask(taskCtx, subagentTask, callback)

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

func (sm *SubagentManager) runTask(ctx context.Context, task *SubagentTask, callback AsyncCallback) {
	sm.mu.Lock()
	previousTask := *task
	task.Status = SubagentStatusRunning
	task.Updated = time.Now().UnixMilli()
	sm.mu.Unlock()

	// Get the specific agent's context info (AGENT.md, SOUL.md, workspace, name, model, provider from its workspace)
	sm.mu.RLock()
	getContextInfo := sm.getAgentContext
	agentID := task.AgentID
	sm.mu.RUnlock()

	// Build system prompt for subagent using its own context
	var systemPrompt string
	var agentWorkspace string
	var agentName string
	var agentModel string
	var agentProvider providers.LLMProvider

	if getContextInfo != nil {
		ctxInfo := getContextInfo(agentID)
		if ctxInfo.Context != "" {
			agentWorkspace = ctxInfo.Workspace
			agentName = ctxInfo.Name
			agentModel = ctxInfo.Model
			agentProvider = ctxInfo.Provider
			if agentName == "" {
				agentName = agentID
			}
			systemPrompt = buildSubagentSystemPrompt(ctxInfo.Context, agentID, agentName, agentWorkspace)
		}
	}

	if systemPrompt == "" {
		systemPrompt = buildSubagentSystemPrompt("", agentID, agentName, agentWorkspace)
		agentWorkspace = "unknown"
		agentName = agentID
	}

	// Use the agent's model and provider if available, otherwise fall back to manager's defaults
	if agentModel == "" {
		agentModel = sm.defaultModel
	}
	if agentProvider == nil {
		agentProvider = sm.provider
	}

	messages := previousTask.buildMessages(systemPrompt)

	// Check if context is already cancelled before starting
	select {
	case <-ctx.Done():
		sm.mu.Lock()
		task.Status = SubagentStatusCancelled
		task.Summary = "Task cancelled before execution"
		task.Result = "Task cancelled before execution"
		task.Updated = time.Now().UnixMilli()
		sm.mu.Unlock()
		return
	default:
	}

	// Run tool loop with access to tools
	sm.mu.RLock()
	tools := sm.tools
	maxIter := sm.maxIterations
	maxTokens := sm.maxTokens
	temperature := sm.temperature
	hasMaxTokens := sm.hasMaxTokens
	hasTemperature := sm.hasTemperature
	sm.mu.RUnlock()

	var llmOptions map[string]any
	if hasMaxTokens || hasTemperature {
		llmOptions = map[string]any{}
		if hasMaxTokens {
			llmOptions["max_tokens"] = maxTokens
		}
		if hasTemperature {
			llmOptions["temperature"] = temperature
		}
	}

	loopResult, err := RunToolLoop(ctx, ToolLoopConfig{
		Provider:      agentProvider,
		Model:         agentModel,
		Tools:         tools,
		MaxIterations: maxIter,
		LLMOptions:    llmOptions,
	}, messages, task.OriginChannel, task.OriginChatID)

	sm.mu.Lock()
	var result *ToolResult
	defer func() {
		var cancel context.CancelFunc
		if c, ok := sm.cancels[task.ID]; ok {
			cancel = c
			delete(sm.cancels, task.ID)
		}
		sm.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		// Call callback if provided and result is set
		if callback != nil && result != nil {
			callback(ctx, result)
		}
	}()

	if err != nil {
		task.Status = SubagentStatusFailed
		task.Summary = "Subagent execution failed"
		task.Result = fmt.Sprintf("Error: %v", err)
		task.ContextRequest = ""
		task.Updated = time.Now().UnixMilli()
		// Check if it was cancelled
		if ctx.Err() != nil {
			task.Status = SubagentStatusCancelled
			task.Summary = "Task cancelled during execution"
			task.Result = "Task cancelled during execution"
		}
		result = &ToolResult{
			ForLLM:  task.statusMessage(),
			ForUser: "",
			Silent:  true,
			IsError: true,
			Async:   false,
			Err:     err,
		}
	} else {
		outcome := parseSubagentOutcome(loopResult.Content)
		task.Status = outcome.Status
		task.Summary = outcome.Summary
		task.Result = outcome.Details
		task.ContextRequest = outcome.ContextRequest
		task.Iterations = loopResult.Iterations
		task.Updated = time.Now().UnixMilli()
		result = &ToolResult{
			ForLLM:  task.statusMessage(),
			ForUser: "",
			Silent:  true,
			IsError: false,
			Async:   false,
		}
	}

	// NOTE: Subagents do NOT send messages directly to users.
	// The result is returned via callback to the parent agent, which decides
	// what to do with the result (e.g., display it, use it for further processing,
	// or send a message to the user if appropriate).
}

func (sm *SubagentManager) GetTask(taskID string) (*SubagentTask, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	task, ok := sm.tasks[taskID]
	return task, ok
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
	canStop := taskExists && task != nil && (task.Status == SubagentStatusRunning || task.Status == SubagentStatusNeedsContext)
	if ok {
		delete(sm.cancels, taskID)
	}
	if canStop {
		task.Status = SubagentStatusCancelled
		task.Summary = "Task cancelled"
		task.Result = "Task cancelled"
		task.ContextRequest = ""
		task.Updated = time.Now().UnixMilli()
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
		}
		delete(sm.cancels, taskID)
	}

	for taskID, task := range sm.tasks {
		if _, alreadyHandled := handled[taskID]; alreadyHandled {
			continue
		}
		if task.Status != SubagentStatusNeedsContext {
			continue
		}
		task.Status = SubagentStatusCancelled
		task.Summary = "Task cancelled"
		task.Result = "Task cancelled"
		task.ContextRequest = ""
		task.Updated = time.Now().UnixMilli()
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

// SubagentTool executes a subagent task synchronously and returns the result.
// Unlike SpawnTool which runs tasks asynchronously, SubagentTool waits for completion
// and returns the result directly in the ToolResult.
type SubagentTool struct {
	manager       *SubagentManager
	originChannel string
	originChatID  string
}

func NewSubagentTool(manager *SubagentManager) *SubagentTool {
	return &SubagentTool{
		manager:       manager,
		originChannel: "cli",
		originChatID:  "direct",
	}
}

func (t *SubagentTool) Name() string {
	return "subagent"
}

func (t *SubagentTool) Description() string {
	return "Execute a subagent task synchronously and return the result. Use this for delegating specific tasks to an independent agent instance. Returns execution summary to user and full details to LLM."
}

func (t *SubagentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task": map[string]interface{}{
				"type":        "string",
				"description": "The task for subagent to complete",
			},
			"label": map[string]interface{}{
				"type":        "string",
				"description": "Optional short label for the task (for display)",
			},
		},
		"required": []string{"task"},
	}
}

func (t *SubagentTool) SetContext(channel, chatID string) {
	t.originChannel = channel
	t.originChatID = chatID
}

func (t *SubagentTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	task, ok := args["task"].(string)
	if !ok {
		return ErrorResult("task is required").WithError(fmt.Errorf("task parameter is required"))
	}

	label, _ := args["label"].(string)

	if t.manager == nil {
		return ErrorResult("Subagent manager not configured").WithError(fmt.Errorf("manager is nil"))
	}

	// Build messages for subagent
	messages := []providers.Message{
		{
			Role:    "system",
			Content: "You are a subagent. Complete the given task independently and provide a clear, concise result.",
		},
		{
			Role:    "user",
			Content: task,
		},
	}

	// Use RunToolLoop to execute with tools (same as async SpawnTool)
	sm := t.manager
	sm.mu.RLock()
	tools := sm.tools
	maxIter := sm.maxIterations
	maxTokens := sm.maxTokens
	temperature := sm.temperature
	hasMaxTokens := sm.hasMaxTokens
	hasTemperature := sm.hasTemperature
	sm.mu.RUnlock()

	var llmOptions map[string]any
	if hasMaxTokens || hasTemperature {
		llmOptions = map[string]any{}
		if hasMaxTokens {
			llmOptions["max_tokens"] = maxTokens
		}
		if hasTemperature {
			llmOptions["temperature"] = temperature
		}
	}

	loopResult, err := RunToolLoop(ctx, ToolLoopConfig{
		Provider:      sm.provider,
		Model:         sm.defaultModel,
		Tools:         tools,
		MaxIterations: maxIter,
		LLMOptions:    llmOptions,
	}, messages, t.originChannel, t.originChatID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Subagent execution failed: %v", err)).WithError(err)
	}

	// ForUser: Brief summary for user (truncated if too long)
	userContent := loopResult.Content
	maxUserLen := 500
	if len(userContent) > maxUserLen {
		userContent = userContent[:maxUserLen] + "..."
	}

	// ForLLM: Full execution details
	labelStr := label
	if labelStr == "" {
		labelStr = "(unnamed)"
	}
	llmContent := fmt.Sprintf("Subagent task completed:\nLabel: %s\nIterations: %d\nResult: %s",
		labelStr, loopResult.Iterations, loopResult.Content)

	return &ToolResult{
		ForLLM:  llmContent,
		ForUser: userContent,
		Silent:  false,
		IsError: false,
		Async:   false,
	}
}
