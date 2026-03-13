package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
)

// ============================================================================
// Mock Implementations (unique to llm_runner_test.go)
// ============================================================================

// llmRunnerMockToolRegistry is a mock implementation of tools.ToolRegistry for testing
type llmRunnerMockToolRegistry struct {
	tools          map[string]tools.Tool
	executeFunc    func(ctx context.Context, name string, args map[string]interface{}, channel, chatID string, asyncCallback tools.AsyncCallback) *tools.ToolResult
	providerDefs   []providers.ToolDefinition
	contextualTool tools.ContextualTool
}

func newLLMRunnerMockToolRegistry() *llmRunnerMockToolRegistry {
	return &llmRunnerMockToolRegistry{
		tools:        make(map[string]tools.Tool),
		providerDefs: make([]providers.ToolDefinition, 0),
	}
}

func (m *llmRunnerMockToolRegistry) Register(tool tools.Tool) {
	m.tools[tool.Name()] = tool
}

func (m *llmRunnerMockToolRegistry) Get(name string) (tools.Tool, bool) {
	tool, ok := m.tools[name]
	return tool, ok
}

func (m *llmRunnerMockToolRegistry) ExecuteWithContext(ctx context.Context, name string, args map[string]interface{}, channel, chatID string, asyncCallback tools.AsyncCallback) *tools.ToolResult {
	if m.executeFunc != nil {
		return m.executeFunc(ctx, name, args, channel, chatID, asyncCallback)
	}
	return tools.SilentResult("mock result")
}

func (m *llmRunnerMockToolRegistry) ToProviderDefs() []providers.ToolDefinition {
	if m.providerDefs != nil {
		return m.providerDefs
	}
	return []providers.ToolDefinition{}
}

func (m *llmRunnerMockToolRegistry) List() []string {
	names := make([]string, 0, len(m.tools))
	for name := range m.tools {
		names = append(names, name)
	}
	return names
}

// llmRunnerMockSessionStore is a mock implementation of session storage
type llmRunnerMockSessionStore struct {
	history      map[string][]providers.Message
	summary      map[string]string
	verboseLevel map[string]string
	mu           sync.RWMutex
}

func newLLMRunnerMockSessionStore() *llmRunnerMockSessionStore {
	return &llmRunnerMockSessionStore{
		history:      make(map[string][]providers.Message),
		summary:      make(map[string]string),
		verboseLevel: make(map[string]string),
	}
}

func (m *llmRunnerMockSessionStore) GetHistory(key string) []providers.Message {
	m.mu.RLock()
	defer m.mu.RUnlock()
	history, ok := m.history[key]
	if !ok {
		return []providers.Message{}
	}
	result := make([]providers.Message, len(history))
	copy(result, history)
	return result
}

func (m *llmRunnerMockSessionStore) SetHistory(key string, history []providers.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msgs := make([]providers.Message, len(history))
	copy(msgs, history)
	m.history[key] = msgs
}

func (m *llmRunnerMockSessionStore) AddMessage(key, role, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history[key] = append(m.history[key], providers.Message{Role: role, Content: content})
}

func (m *llmRunnerMockSessionStore) AddFullMessage(key string, msg providers.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history[key] = append(m.history[key], msg)
}

func (m *llmRunnerMockSessionStore) GetSummary(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.summary[key]
}

func (m *llmRunnerMockSessionStore) SetSummary(key, summary string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.summary[key] = summary
}

func (m *llmRunnerMockSessionStore) TruncateHistory(key string, keepLast int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	history := m.history[key]
	if keepLast <= 0 {
		m.history[key] = []providers.Message{}
		return
	}
	if len(history) <= keepLast {
		return
	}
	m.history[key] = history[len(history)-keepLast:]
}

func (m *llmRunnerMockSessionStore) Save(key string) error {
	return nil
}

func (m *llmRunnerMockSessionStore) GetVerboseLevel(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	level, ok := m.verboseLevel[key]
	if !ok {
		return "off"
	}
	return level
}

func (m *llmRunnerMockSessionStore) SetVerboseLevel(key, level string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.verboseLevel[key] = level
	return nil
}

// llmRunnerMockContextBuilder is a mock implementation of ContextBuilder
type llmRunnerMockContextBuilder struct {
	messages []providers.Message
}

func (m *llmRunnerMockContextBuilder) BuildMessages(history []providers.Message, summary, userMessage string, attachments []bus.FileAttachment, channel, chatID string) []providers.Message {
	if m.messages != nil {
		return m.messages
	}
	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
	}
	for _, h := range history {
		messages = append(messages, h)
	}
	if userMessage != "" {
		messages = append(messages, providers.Message{Role: "user", Content: userMessage})
	}
	return messages
}

func (m *llmRunnerMockContextBuilder) GetInitialContext() string {
	return "mock context"
}

func (m *llmRunnerMockContextBuilder) SetToolsRegistry(registry *tools.ToolRegistry) {}

func (m *llmRunnerMockContextBuilder) GetSkillsInfo() map[string]interface{} {
	return map[string]interface{}{}
}

func (m *llmRunnerMockContextBuilder) ResetMemoryContext() {}

// llmRunnerMockLLMProvider is a mock implementation of providers.LLMProvider
type llmRunnerMockLLMProvider struct {
	response     *providers.LLMResponse
	err          error
	callCount    int
	callHistory  []providers.Message
	onChatCalled func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error)
}

func (m *llmRunnerMockLLMProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	m.callCount++
	if m.callHistory != nil {
		m.callHistory = append(m.callHistory, messages...)
	}
	if m.onChatCalled != nil {
		return m.onChatCalled(ctx, messages, tools, model, opts)
	}
	if m.err != nil {
		return nil, m.err
	}
	if m.response != nil {
		return m.response, nil
	}
	return &providers.LLMResponse{
		Content:   "Mock response",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *llmRunnerMockLLMProvider) GetDefaultModel() string {
	return "mock-model"
}

// llmRunnerMockFallbackChain is a mock implementation of fallback chain
type llmRunnerMockFallbackChain struct {
	executeFunc func(ctx context.Context, candidates []providers.FallbackCandidate, run func(ctx context.Context, provider, model string) (*providers.LLMResponse, error)) (*providers.FallbackResult, error)
}

func (m *llmRunnerMockFallbackChain) Execute(ctx context.Context, candidates []providers.FallbackCandidate, run func(ctx context.Context, provider, model string) (*providers.LLMResponse, error)) (*providers.FallbackResult, error) {
	if m.executeFunc != nil {
		return m.executeFunc(ctx, candidates, run)
	}
	return &providers.FallbackResult{
		Response: &providers.LLMResponse{Content: "Mock fallback response", ToolCalls: []providers.ToolCall{}},
		Provider: "mock",
		Model:    "mock-model",
	}, nil
}

// llmRunnerMockEventBus is a mock implementation of bus.MessageBus
type llmRunnerMockEventBus struct {
	publishedOutbound []bus.OutboundMessage
	publishedInbound  []bus.InboundMessage
	mu                sync.RWMutex
}

func newLLMRunnerMockEventBus() *llmRunnerMockEventBus {
	return &llmRunnerMockEventBus{
		publishedOutbound: make([]bus.OutboundMessage, 0),
		publishedInbound:  make([]bus.InboundMessage, 0),
	}
}

func (m *llmRunnerMockEventBus) PublishOutbound(msg bus.OutboundMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publishedOutbound = append(m.publishedOutbound, msg)
}

func (m *llmRunnerMockEventBus) PublishInbound(msg bus.InboundMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publishedInbound = append(m.publishedInbound, msg)
}

func (m *llmRunnerMockEventBus) GetPublishedOutbound() []bus.OutboundMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]bus.OutboundMessage, len(m.publishedOutbound))
	copy(result, m.publishedOutbound)
	return result
}

func (m *llmRunnerMockEventBus) ClearPublished() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publishedOutbound = m.publishedOutbound[:0]
	m.publishedInbound = m.publishedInbound[:0]
}

// llmRunnerMockToolCoordinator is a mock implementation of toolCoordinator
type llmRunnerMockToolCoordinator struct {
	updateToolContextsCalled bool
	lastAgent                *AgentInstance
	lastChannel              string
	lastChatID               string
}

func (m *llmRunnerMockToolCoordinator) updateToolContexts(agent *AgentInstance, channel, chatID string) {
	m.updateToolContextsCalled = true
	m.lastAgent = agent
	m.lastChannel = channel
	m.lastChatID = chatID
}

func (m *llmRunnerMockToolCoordinator) stopAllSubagents() int { return 0 }

func (m *llmRunnerMockToolCoordinator) cancelSession(sessionKey string) {}

func (m *llmRunnerMockToolCoordinator) listRunningSubagentTasks() []*tools.SubagentTask { return nil }

func (m *llmRunnerMockToolCoordinator) getSubagentTask(taskID string) (*tools.SubagentTask, bool) {
	return nil, false
}

func (m *llmRunnerMockToolCoordinator) stopSubagentTask(taskID string) bool { return false }

func (m *llmRunnerMockToolCoordinator) continueSubagentTask(ctx context.Context, sessionKey, taskID, guidance string) (string, error) {
	return "", nil
}

func (m *llmRunnerMockToolCoordinator) GetStartupInfo() map[string]interface{} {
	return map[string]interface{}{}
}

// llmRunnerMockSessionManager is a mock implementation of sessionManager
type llmRunnerMockSessionManager struct {
	summarizeCalled bool
	summarizeStats  *SummarizeStats
}

func (m *llmRunnerMockSessionManager) maybeSummarize(agent *AgentInstance, sessionKey, channel, chatID string) *SummarizeStats {
	return nil
}

func (m *llmRunnerMockSessionManager) summarizeSession(agent *AgentInstance, sessionKey string) *SummarizeStats {
	m.summarizeCalled = true
	return m.summarizeStats
}

// llmRunnerMockApprovalManager is a mock implementation of channels.ApprovalManager
type llmRunnerMockApprovalManager struct {
	approvalResult  bool
	approvalError   error
	createdApproval *channels.PendingApproval
}

func (m *llmRunnerMockApprovalManager) CreateApproval(sessionKey, command, reason string, chatID int64) *channels.PendingApproval {
	if m.createdApproval != nil {
		return m.createdApproval
	}
	return &channels.PendingApproval{
		ID:      "test-approval-id",
		Command: command,
		Reason:  reason,
		ChatID:  chatID,
	}
}

func (m *llmRunnerMockApprovalManager) BuildApprovalKeyboard(approvalID string) interface{} {
	return nil
}

func (m *llmRunnerMockApprovalManager) GetTimeout() time.Duration {
	return 5 * time.Minute
}

func (m *llmRunnerMockApprovalManager) HandleApproval(approvalID string, approved bool) (*channels.PendingApproval, error) {
	return nil, nil
}

func (m *llmRunnerMockApprovalManager) GetApproval(approvalID string) *channels.PendingApproval {
	return nil
}

func (m *llmRunnerMockApprovalManager) SetTimeout(timeout time.Duration) {}

// llmRunnerMockExecTool is a mock implementation of the exec tool for testing approval flow
type llmRunnerMockExecTool struct {
	approvalRequired bool
	bypassGuard      bool
}

func (m *llmRunnerMockExecTool) Name() string { return "exec" }

func (m *llmRunnerMockExecTool) Description() string { return "Execute shell commands" }

func (m *llmRunnerMockExecTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{"type": "string"},
		},
	}
}

func (m *llmRunnerMockExecTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	if m.approvalRequired && !m.bypassGuard {
		return &tools.ToolResult{
			ForLLM:  "",
			IsError: false,
			ApprovalRequired: &tools.ApprovalInfo{
				Command: args["command"].(string),
				Reason:  "Potentially dangerous command",
			},
		}
	}
	return tools.SilentResult("Command executed")
}

func (m *llmRunnerMockExecTool) SetBypassGuard(bypass bool) {
	m.bypassGuard = bypass
}

// llmRunnerMockContextualTool is a mock tool that implements ContextualTool
type llmRunnerMockContextualTool struct {
	name             string
	channel          string
	chatID           string
	setContextCalled bool
}

func (m *llmRunnerMockContextualTool) Name() string { return m.name }

func (m *llmRunnerMockContextualTool) Description() string { return "Mock contextual tool" }

func (m *llmRunnerMockContextualTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (m *llmRunnerMockContextualTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	return tools.SilentResult("executed")
}

func (m *llmRunnerMockContextualTool) SetContext(channel, chatID string) {
	m.channel = channel
	m.chatID = chatID
	m.setContextCalled = true
}

// llmRunnerMockCustomTool is a flexible mock tool for testing
type llmRunnerMockCustomTool struct {
	name        string
	executeFunc func(ctx context.Context, args map[string]interface{}) *tools.ToolResult
}

func (m *llmRunnerMockCustomTool) Name() string {
	return m.name
}

func (m *llmRunnerMockCustomTool) Description() string {
	return "Mock tool: " + m.name
}

func (m *llmRunnerMockCustomTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (m *llmRunnerMockCustomTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	if m.executeFunc != nil {
		return m.executeFunc(ctx, args)
	}
	return tools.SilentResult("mock result")
}

// ============================================================================
// Test Setup Helpers
// ============================================================================

func createLLMRunnerTestAgentLoop(t *testing.T) (*AgentLoop, string) {
	tmpDir, err := os.MkdirTemp("", "llm-runner-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				Provider:          "test-provider",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	return al, tmpDir
}

func createLLMRunnerTestAgentInstance(t *testing.T, tmpDir string) *AgentInstance {
	sessionsDir := tmpDir + "/sessions"
	os.MkdirAll(sessionsDir, 0755)

	provider := &llmRunnerMockLLMProvider{}
	toolRegistry := tools.NewToolRegistry()

	// Create a properly initialized ContextBuilder
	contextBuilder := NewContextBuilder(tmpDir)
	contextBuilder.SetToolsRegistry(toolRegistry)

	return &AgentInstance{
		ID:             "test-agent",
		Name:           "Test Agent",
		Model:          "test-model",
		Workspace:      tmpDir,
		MaxIterations:  10,
		MaxTokens:      4096,
		Temperature:    0.7,
		ContextWindow:  128000,
		Provider:       provider,
		Sessions:       session.NewSessionManager(sessionsDir),
		ContextBuilder: contextBuilder,
		Tools:          toolRegistry,
		Candidates:     []providers.FallbackCandidate{},
	}
}

// ============================================================================
// Tests for newLLMRunner
// ============================================================================

func TestNewLLMRunner(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)

	if runner == nil {
		t.Fatal("Expected runner to be non-nil")
	}

	if runner.al != al {
		t.Error("Expected runner.al to be the same AgentLoop")
	}
}

// ============================================================================
// Tests for runAgentLoop
// ============================================================================

func TestRunAgentLoop_BasicExecution(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	// Mock the provider to return a simple response
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Hello, world!",
			ToolCalls: []providers.ToolCall{},
		},
	}

	opts := processOptions{
		SessionKey:      "test-session",
		Channel:         "test-channel",
		ChatID:          "test-chat-id",
		UserMessage:     "Hi there",
		DefaultResponse: "Default response",
		EnableSummary:   false,
		SendResponse:    false,
		NoHistory:       false,
	}

	ctx := context.Background()
	response, err := runner.runAgentLoop(ctx, agent, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if response != "Hello, world!" {
		t.Errorf("Expected 'Hello, world!', got: %s", response)
	}

	// Verify message was saved to session
	history := agent.Sessions.GetHistory(opts.SessionKey)
	if len(history) < 2 {
		t.Errorf("Expected at least 2 messages in history, got: %d", len(history))
	}
}

func TestRunAgentLoop_EmptyResponse(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	// Mock the provider to return an empty response
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "",
			ToolCalls: []providers.ToolCall{},
		},
	}

	opts := processOptions{
		SessionKey:      "test-session",
		Channel:         "test-channel",
		ChatID:          "test-chat-id",
		UserMessage:     "Hi there",
		DefaultResponse: "Default fallback response",
		EnableSummary:   false,
		SendResponse:    false,
		NoHistory:       false,
	}

	ctx := context.Background()
	response, err := runner.runAgentLoop(ctx, agent, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if response != "Default fallback response" {
		t.Errorf("Expected default response, got: %s", response)
	}
}

func TestRunAgentLoop_NoHistory(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	// Add some history first
	agent.Sessions.AddMessage("test-session", "user", "Previous message")
	agent.Sessions.AddMessage("test-session", "assistant", "Previous response")

	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Response without history",
			ToolCalls: []providers.ToolCall{},
		},
	}

	opts := processOptions{
		SessionKey:      "test-session",
		Channel:         "test-channel",
		ChatID:          "test-chat-id",
		UserMessage:     "New message",
		DefaultResponse: "Default",
		EnableSummary:   false,
		SendResponse:    false,
		NoHistory:       true, // Skip history
	}

	ctx := context.Background()
	_, err := runner.runAgentLoop(ctx, agent, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify the new message was still saved
	history := agent.Sessions.GetHistory(opts.SessionKey)
	foundNewMessage := false
	for _, msg := range history {
		if msg.Content == "New message" {
			foundNewMessage = true
			break
		}
	}
	if !foundNewMessage {
		t.Error("Expected new message to be saved to session")
	}
}

func TestRunAgentLoop_InternalChannel(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Internal response",
			ToolCalls: []providers.ToolCall{},
		},
	}

	// Test with internal channel (should not record last channel)
	opts := processOptions{
		SessionKey:      "test-session",
		Channel:         "cli", // Internal channel
		ChatID:          "test-chat-id",
		UserMessage:     "Test message",
		DefaultResponse: "Default",
		EnableSummary:   false,
		SendResponse:    false,
		NoHistory:       false,
	}

	ctx := context.Background()
	_, err := runner.runAgentLoop(ctx, agent, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify last channel was NOT recorded for internal channel
	lastChannel := al.state.GetLastChannel()
	if lastChannel == "cli:test-chat-id" {
		t.Error("Expected internal channel to not be recorded as last channel")
	}
}

func TestRunAgentLoop_ProviderError(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	// Mock the provider to return an error
	agent.Provider = &llmRunnerMockLLMProvider{
		err: errors.New("provider connection failed"),
	}

	opts := processOptions{
		SessionKey:      "test-session",
		Channel:         "test-channel",
		ChatID:          "test-chat-id",
		UserMessage:     "Hi there",
		DefaultResponse: "Default",
		EnableSummary:   false,
		SendResponse:    false,
		NoHistory:       false,
	}

	ctx := context.Background()
	_, err := runner.runAgentLoop(ctx, agent, opts)

	if err == nil {
		t.Fatal("Expected error from provider, got nil")
	}

	if !strings.Contains(err.Error(), "provider connection failed") {
		t.Errorf("Expected error to contain 'provider connection failed', got: %v", err)
	}
}

// ============================================================================
// Tests for runLLMIteration
// ============================================================================

func TestRunLLMIteration_DirectResponse(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Direct answer",
			ToolCalls: []providers.ToolCall{},
		},
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Hello"},
	}

	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	ctx := context.Background()
	content, iterations, err := runner.runLLMIteration(ctx, agent, messages, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if content != "Direct answer" {
		t.Errorf("Expected 'Direct answer', got: %s", content)
	}

	if iterations != 1 {
		t.Errorf("Expected 1 iteration, got: %d", iterations)
	}
}

func TestRunLLMIteration_WithToolCalls(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	callCount := 0
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			if callCount == 1 {
				// First call: return tool call
				return &providers.LLMResponse{
					Content: "",
					ToolCalls: []providers.ToolCall{
						{
							ID:        "call_1",
							Type:      "function",
							Name:      "read_file",
							Arguments: map[string]interface{}{"path": "/test/file.txt"},
						},
					},
				}, nil
			}
			// Second call: return final response
			return &providers.LLMResponse{
				Content:   "File content retrieved",
				ToolCalls: []providers.ToolCall{},
			}, nil
		},
	}

	// Register a mock read_file tool
	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "read_file",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			return tools.SilentResult("File contents: test data")
		},
	})

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Read the file"},
	}

	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	ctx := context.Background()
	content, iterations, err := runner.runLLMIteration(ctx, agent, messages, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if content != "File content retrieved" {
		t.Errorf("Expected 'File content retrieved', got: %s", content)
	}

	if iterations != 2 {
		t.Errorf("Expected 2 iterations (tool call + response), got: %d", iterations)
	}
}

func TestRunLLMIteration_MaxIterations(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	// Set max iterations to a low number
	agent.MaxIterations = 3

	// Always return tool calls to force iteration
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content: "",
			ToolCalls: []providers.ToolCall{
				{
					ID:        "call_1",
					Type:      "function",
					Name:      "test_tool",
					Arguments: map[string]interface{}{},
				},
			},
		},
	}

	// Register a test tool
	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "test_tool",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			return tools.SilentResult("done")
		},
	})

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Test"},
	}

	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	ctx := context.Background()
	content, iterations, err := runner.runLLMIteration(ctx, agent, messages, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should stop at max iterations
	if iterations > agent.MaxIterations {
		t.Errorf("Expected iterations <= %d, got: %d", agent.MaxIterations, iterations)
	}

	// Content should be empty since we never got a final response
	_ = content
}

func TestRunLLMIteration_EmptyResponseRetry(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	callCount := 0
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			if callCount == 1 {
				// First call: return empty response
				return &providers.LLMResponse{
					Content:   "   ", // Whitespace only
					ToolCalls: []providers.ToolCall{},
				}, nil
			}
			// Second call: return actual response
			return &providers.LLMResponse{
				Content:   "Actual response",
				ToolCalls: []providers.ToolCall{},
			}, nil
		},
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Hello"},
	}

	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	ctx := context.Background()
	content, iterations, err := runner.runLLMIteration(ctx, agent, messages, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if content != "Actual response" {
		t.Errorf("Expected 'Actual response', got: %s", content)
	}

	if iterations != 2 {
		t.Errorf("Expected 2 iterations (empty retry), got: %d", iterations)
	}
}

func TestRunLLMIteration_ContextErrorRetry(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	callCount := 0
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			if callCount == 1 {
				// First call: return token/context error
				return nil, errors.New("InvalidParameter: Total tokens exceed max message tokens")
			}
			// Second call: succeed after "summarization"
			return &providers.LLMResponse{
				Content:   "Recovered response",
				ToolCalls: []providers.ToolCall{},
			}, nil
		},
	}

	// Add some history to trigger summarization
	for i := 0; i < 10; i++ {
		agent.Sessions.AddMessage("test-session", "user", fmt.Sprintf("Message %d", i))
		agent.Sessions.AddMessage("test-session", "assistant", fmt.Sprintf("Response %d", i))
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Hello"},
	}

	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	ctx := context.Background()
	content, _, err := runner.runLLMIteration(ctx, agent, messages, opts)

	if err != nil {
		t.Fatalf("Expected no error after retry, got: %v", err)
	}

	if content != "Recovered response" {
		t.Errorf("Expected 'Recovered response', got: %s", content)
	}
}

func TestRunLLMIteration_NetworkTimeoutRetry(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	callCount := 0
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			if callCount == 1 {
				// First call: timeout
				return nil, errors.New("context deadline exceeded")
			}
			// Second call: succeed
			return &providers.LLMResponse{
				Content:   "Success after timeout",
				ToolCalls: []providers.ToolCall{},
			}, nil
		},
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Hello"},
	}

	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	ctx := context.Background()
	content, _, err := runner.runLLMIteration(ctx, agent, messages, opts)

	if err != nil {
		t.Fatalf("Expected no error after retry, got: %v", err)
	}

	if content != "Success after timeout" {
		t.Errorf("Expected 'Success after timeout', got: %s", content)
	}
}

func TestRunLLMIteration_FallbackChain(t *testing.T) {
	// This test verifies that the fallback chain mechanism is properly integrated.
	// When there are multiple candidates AND a fallback chain is configured,
	// the code attempts to use the fallback chain. Since we can't test the full
	// fallback without real API keys, we test that the code correctly falls back
	// to using agent.Provider when there's only one candidate.
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	// Set up fallback chain
	al.fallback = providers.NewFallbackChain(providers.NewCooldownTracker())

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	// Use only ONE candidate - this ensures the code uses agent.Provider directly
	// instead of going through the fallback chain
	agent.Candidates = []providers.FallbackCandidate{
		{Provider: "test-provider", Model: "test-model"},
	}

	// Use a provider that succeeds on first call
	callCount := 0
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			return &providers.LLMResponse{
				Content:   "Success response",
				ToolCalls: []providers.ToolCall{},
			}, nil
		},
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Hello"},
	}

	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	ctx := context.Background()
	content, _, err := runner.runLLMIteration(ctx, agent, messages, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if content != "Success response" {
		t.Errorf("Expected 'Success response', got: %s", content)
	}

	// Verify the provider was called
	if callCount != 1 {
		t.Errorf("Expected provider to be called once, got %d calls", callCount)
	}
}

// ============================================================================
// Tests for updateToolContexts
// ============================================================================

func TestUpdateToolContexts(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	// Create mock contextual tools
	messageTool := &llmRunnerMockContextualTool{name: "message"}
	spawnTool := &llmRunnerMockContextualTool{name: "spawn"}
	subagentTool := &llmRunnerMockContextualTool{name: "subagent"}

	agent.Tools.Register(messageTool)
	agent.Tools.Register(spawnTool)
	agent.Tools.Register(subagentTool)

	// Call updateToolContexts
	runner.updateToolContexts(agent, "test-channel", "test-chat-id")

	// Verify all tools received context
	if !messageTool.setContextCalled {
		t.Error("Expected message tool SetContext to be called")
	}
	if messageTool.channel != "test-channel" {
		t.Errorf("Expected message tool channel to be 'test-channel', got: %s", messageTool.channel)
	}
	if messageTool.chatID != "test-chat-id" {
		t.Errorf("Expected message tool chatID to be 'test-chat-id', got: %s", messageTool.chatID)
	}

	if !spawnTool.setContextCalled {
		t.Error("Expected spawn tool SetContext to be called")
	}
	if !subagentTool.setContextCalled {
		t.Error("Expected subagent tool SetContext to be called")
	}
}

func TestUpdateToolContexts_MissingTools(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	// Don't register any contextual tools
	// Should not panic
	runner.updateToolContexts(agent, "test-channel", "test-chat-id")
}

// ============================================================================
// Tests for modelForSession
// ============================================================================

func TestModelForSession_DefaultModel(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Model = "default-model"

	model := runner.modelForSession(agent, "test-session")

	if model != "default-model" {
		t.Errorf("Expected 'default-model', got: %s", model)
	}
}

func TestModelForSession_SessionOverride(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Model = "default-model"

	// Set session-specific model
	al.sessionModels.Store("test-session", "session-specific-model")

	model := runner.modelForSession(agent, "test-session")

	if model != "session-specific-model" {
		t.Errorf("Expected 'session-specific-model', got: %s", model)
	}
}

func TestModelForSession_EmptySessionKey(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Model = "default-model"

	// Should return default model for empty session key
	model := runner.modelForSession(agent, "")

	if model != "default-model" {
		t.Errorf("Expected 'default-model' for empty session, got: %s", model)
	}
}

func TestModelForSession_InvalidStoredValue(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Model = "default-model"

	// Store non-string value
	al.sessionModels.Store("test-session", 12345)

	model := runner.modelForSession(agent, "test-session")

	// Should fall back to default model
	if model != "default-model" {
		t.Errorf("Expected 'default-model' for invalid stored value, got: %s", model)
	}
}

// ============================================================================
// Tests for formatProviderModel (llmRunnerImpl method)
// ============================================================================

func TestLLMRunnerFormatProviderModel(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		want     string
	}{
		{
			name:     "provider and model",
			provider: "openai",
			model:    "gpt-4",
			want:     "openai/gpt-4",
		},
		{
			name:     "already prefixed",
			provider: "openai",
			model:    "openai/gpt-4",
			want:     "openai/gpt-4",
		},
		{
			name:     "empty provider",
			provider: "",
			model:    "gpt-4",
			want:     "gpt-4",
		},
		{
			name:     "whitespace trimming",
			provider: "  openai  ",
			model:    "  gpt-4  ",
			want:     "openai/gpt-4",
		},
		{
			name:     "different provider",
			provider: "anthropic",
			model:    "claude-3",
			want:     "anthropic/claude-3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			al, tmpDir := createLLMRunnerTestAgentLoop(t)
			defer os.RemoveAll(tmpDir)

			runner := newLLMRunner(al)
			got := runner.formatProviderModel(tt.provider, tt.model)

			if got != tt.want {
				t.Errorf("formatProviderModel(%q, %q) = %q, want %q", tt.provider, tt.model, got, tt.want)
			}
		})
	}
}

// ============================================================================
// Tests for Verbose Mode
// ============================================================================

func TestRunLLMIteration_VerboseModeFull(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	// Set verbose mode to full
	al.verboseManager.SetLevel("test-session", session.VerboseFull)

	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content: "",
			ToolCalls: []providers.ToolCall{
				{
					ID:        "call_1",
					Type:      "function",
					Name:      "read_file",
					Arguments: map[string]interface{}{"path": "/test/file.txt"},
				},
			},
		},
	}

	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "read_file",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			return tools.SilentResult("File contents")
		},
	})

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Read file"},
	}

	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	ctx := context.Background()
	_, _, err := runner.runLLMIteration(ctx, agent, messages, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// In full verbose mode with tool calls, the code should execute without panic
	// The actual bus messages are sent via al.bus which is a real MessageBus in this test
}

func TestRunLLMIteration_VerboseModeBasic(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	// Set verbose mode to basic
	al.verboseManager.SetLevel("test-session", session.VerboseBasic)

	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Response",
			ToolCalls: []providers.ToolCall{},
		},
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Hello"},
	}

	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	ctx := context.Background()
	_, _, err := runner.runLLMIteration(ctx, agent, messages, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// In basic mode without tool calls, no verbose messages should be sent
	// (since there are no tools being executed)
}

func TestRunLLMIteration_VerboseModeOff(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	// Ensure verbose mode is off
	al.verboseManager.SetLevel("test-session", session.VerboseOff)

	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content: "",
			ToolCalls: []providers.ToolCall{
				{
					ID:        "call_1",
					Type:      "function",
					Name:      "read_file",
					Arguments: map[string]interface{}{"path": "/test/file.txt"},
				},
			},
		},
	}

	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "read_file",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			return tools.SilentResult("File contents")
		},
	})

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Read file"},
	}

	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	ctx := context.Background()
	_, _, err := runner.runLLMIteration(ctx, agent, messages, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// In verbose off mode, no verbose notifications should be sent
}

// ============================================================================
// Tests for Tool Error Handling
// ============================================================================

func TestRunLLMIteration_ToolExecutionError(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	callCount := 0
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			if callCount == 1 {
				return &providers.LLMResponse{
					Content: "",
					ToolCalls: []providers.ToolCall{
						{
							ID:        "call_1",
							Type:      "function",
							Name:      "failing_tool",
							Arguments: map[string]interface{}{},
						},
					},
				}, nil
			}
			return &providers.LLMResponse{
				Content:   "Handled error",
				ToolCalls: []providers.ToolCall{},
			}, nil
		},
	}

	// Register a tool that returns an error
	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "failing_tool",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			return tools.ErrorResult("Tool execution failed: something went wrong")
		},
	})

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Use failing tool"},
	}

	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	ctx := context.Background()
	content, _, err := runner.runLLMIteration(ctx, agent, messages, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if content != "Handled error" {
		t.Errorf("Expected 'Handled error', got: %s", content)
	}
}

func TestRunLLMIteration_ToolNotFound(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	callCount := 0
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			if callCount == 1 {
				return &providers.LLMResponse{
					Content: "",
					ToolCalls: []providers.ToolCall{
						{
							ID:        "call_1",
							Type:      "function",
							Name:      "nonexistent_tool",
							Arguments: map[string]interface{}{},
						},
					},
				}, nil
			}
			return &providers.LLMResponse{
				Content:   "Tool not found handled",
				ToolCalls: []providers.ToolCall{},
			}, nil
		},
	}

	// Don't register the tool - it doesn't exist

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Use nonexistent tool"},
	}

	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	ctx := context.Background()
	content, _, err := runner.runLLMIteration(ctx, agent, messages, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if content != "Tool not found handled" {
		t.Errorf("Expected 'Tool not found handled', got: %s", content)
	}
}

// ============================================================================
// Tests for SendResponse Option
// ============================================================================

func TestRunAgentLoop_SendResponseEnabled(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Response to send",
			ToolCalls: []providers.ToolCall{},
		},
	}

	opts := processOptions{
		SessionKey:      "test-session",
		Channel:         "test-channel",
		ChatID:          "test-chat-id",
		UserMessage:     "Hello",
		DefaultResponse: "Default",
		EnableSummary:   false,
		SendResponse:    true, // Enable sending response
		NoHistory:       false,
	}

	ctx := context.Background()
	_, err := runner.runAgentLoop(ctx, agent, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// The response should be sent via bus when SendResponse is enabled
	// Note: We can't easily verify this without a mock bus, but we verify no error occurs
}

func TestRunAgentLoop_SendResponseDisabled(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Response not to send",
			ToolCalls: []providers.ToolCall{},
		},
	}

	opts := processOptions{
		SessionKey:      "test-session",
		Channel:         "test-channel",
		ChatID:          "test-chat-id",
		UserMessage:     "Hello",
		DefaultResponse: "Default",
		EnableSummary:   false,
		SendResponse:    false, // Disable sending response
		NoHistory:       false,
	}

	ctx := context.Background()
	_, err := runner.runAgentLoop(ctx, agent, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// The response should NOT be sent via bus when SendResponse is disabled
}

// ============================================================================
// Tests for EnableSummary Option
// ============================================================================

func TestRunAgentLoop_EnableSummary(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Response",
			ToolCalls: []providers.ToolCall{},
		},
	}

	opts := processOptions{
		SessionKey:      "test-session",
		Channel:         "test-channel",
		ChatID:          "test-chat-id",
		UserMessage:     "Hello",
		DefaultResponse: "Default",
		EnableSummary:   true, // Enable summarization
		SendResponse:    false,
		NoHistory:       false,
	}

	ctx := context.Background()
	_, err := runner.runAgentLoop(ctx, agent, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Note: Actual summarization would require enough history to trigger it
	// This test just verifies the option is passed through without error
}

// ============================================================================
// Integration Tests
// ============================================================================

func TestLLMRunner_FullConversationFlow(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	callCount := 0
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			switch callCount {
			case 1:
				return &providers.LLMResponse{
					Content: "",
					ToolCalls: []providers.ToolCall{
						{
							ID:        "call_1",
							Type:      "function",
							Name:      "read_file",
							Arguments: map[string]interface{}{"path": "/test/file.txt"},
						},
					},
				}, nil
			case 2:
				return &providers.LLMResponse{
					Content:   "Based on the file content, here's my analysis...",
					ToolCalls: []providers.ToolCall{},
				}, nil
			default:
				return &providers.LLMResponse{
					Content:   "Final response",
					ToolCalls: []providers.ToolCall{},
				}, nil
			}
		},
	}

	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "read_file",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			return tools.SilentResult("File contains: test data and more information")
		},
	})

	opts := processOptions{
		SessionKey:      "test-session",
		Channel:         "test-channel",
		ChatID:          "test-chat-id",
		UserMessage:     "Analyze this file",
		DefaultResponse: "Default",
		EnableSummary:   false,
		SendResponse:    false,
		NoHistory:       false,
	}

	ctx := context.Background()
	response, err := runner.runAgentLoop(ctx, agent, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if response != "Based on the file content, here's my analysis..." {
		t.Errorf("Expected analysis response, got: %s", response)
	}

	// Verify conversation history
	history := agent.Sessions.GetHistory(opts.SessionKey)
	if len(history) < 4 { // system + user + assistant with tool calls + tool result + final assistant
		t.Errorf("Expected at least 4 messages in history, got: %d", len(history))
	}
}

func TestLLMRunner_MultipleToolCalls(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	callCount := 0
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			if callCount == 1 {
				return &providers.LLMResponse{
					Content: "",
					ToolCalls: []providers.ToolCall{
						{
							ID:        "call_1",
							Type:      "function",
							Name:      "read_file",
							Arguments: map[string]interface{}{"path": "/test/file1.txt"},
						},
						{
							ID:        "call_2",
							Type:      "function",
							Name:      "read_file",
							Arguments: map[string]interface{}{"path": "/test/file2.txt"},
						},
					},
				}, nil
			}
			return &providers.LLMResponse{
				Content:   "Analyzed both files",
				ToolCalls: []providers.ToolCall{},
			}, nil
		},
	}

	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "read_file",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			path := args["path"].(string)
			return tools.SilentResult(fmt.Sprintf("Contents of %s", path))
		},
	})

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Read both files"},
	}

	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	ctx := context.Background()
	content, iterations, err := runner.runLLMIteration(ctx, agent, messages, opts)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if content != "Analyzed both files" {
		t.Errorf("Expected 'Analyzed both files', got: %s", content)
	}

	if iterations != 2 {
		t.Errorf("Expected 2 iterations, got: %d", iterations)
	}
}

func TestLLMRunner_ContextCancellation(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	// Provider that takes a long time
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return &providers.LLMResponse{
					Content:   "Response",
					ToolCalls: []providers.ToolCall{},
				}, nil
			}
		},
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Hello"},
	}

	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	// Create a context that will be cancelled
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, _, err := runner.runLLMIteration(ctx, agent, messages, opts)

	// Should get a timeout error
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}

func TestLLMRunner_SessionModelPersistence(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Model = "default-model"

	// Set a session-specific model
	al.sessionModels.Store("user-session-1", "custom-model-v1")

	// Verify the session model is used
	model := runner.modelForSession(agent, "user-session-1")
	if model != "custom-model-v1" {
		t.Errorf("Expected 'custom-model-v1', got: %s", model)
	}

	// Verify default model is used for different session
	model = runner.modelForSession(agent, "user-session-2")
	if model != "default-model" {
		t.Errorf("Expected 'default-model' for different session, got: %s", model)
	}
}
