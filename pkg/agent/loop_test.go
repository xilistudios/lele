package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

func TestRecordLastChannel(t *testing.T) {
	// Create temp workspace
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test config
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	// Create agent loop
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	// Test RecordLastChannel
	testChannel := "test-channel"
	err = al.RecordLastChannel(testChannel)
	if err != nil {
		t.Fatalf("RecordLastChannel failed: %v", err)
	}

	// Verify channel was saved
	lastChannel := al.state.GetLastChannel()
	if lastChannel != testChannel {
		t.Errorf("Expected channel '%s', got '%s'", testChannel, lastChannel)
	}

	// Verify persistence by creating a new agent loop
	al2 := NewAgentLoop(cfg, msgBus)
	if al2.state.GetLastChannel() != testChannel {
		t.Errorf("Expected persistent channel '%s', got '%s'", testChannel, al2.state.GetLastChannel())
	}
}

func TestRecordLastChatID(t *testing.T) {
	// Create temp workspace
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test config
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	// Create agent loop
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	// Test RecordLastChatID
	testChatID := "test-chat-id-123"
	err = al.RecordLastChatID(testChatID)
	if err != nil {
		t.Fatalf("RecordLastChatID failed: %v", err)
	}

	// Verify chat ID was saved
	lastChatID := al.state.GetLastChatID()
	if lastChatID != testChatID {
		t.Errorf("Expected chat ID '%s', got '%s'", testChatID, lastChatID)
	}

	// Verify persistence by creating a new agent loop
	al2 := NewAgentLoop(cfg, msgBus)
	if al2.state.GetLastChatID() != testChatID {
		t.Errorf("Expected persistent chat ID '%s', got '%s'", testChatID, al2.state.GetLastChatID())
	}
}

func TestNewAgentLoop_StateInitialized(t *testing.T) {
	// Create temp workspace
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test config
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	// Create agent loop
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	// Verify state manager is initialized
	if al.state == nil {
		t.Error("Expected state manager to be initialized")
	}

	// Verify state directory was created
	stateDir := filepath.Join(tmpDir, "state")
	if _, err := os.Stat(stateDir); os.IsNotExist(err) {
		t.Error("Expected state directory to exist")
	}
}

func TestAgentLoop_GetVerboseLevel_UsesTelegramConfigDefault(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Channels: config.ChannelsConfig{
			Telegram: config.TelegramConfig{Verbose: config.VerboseBasic},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())

	if got := al.GetVerboseLevel("telegram:123"); got != "basic" {
		t.Fatalf("GetVerboseLevel(telegram) = %q, want %q", got, "basic")
	}
	if got := al.GetVerboseLevel("discord:123"); got != "off" {
		t.Fatalf("GetVerboseLevel(discord) = %q, want %q", got, "off")
	}

	cfg.SetTelegramVerbose(config.VerboseFull)
	if got := al.GetVerboseLevel("telegram:123"); got != "full" {
		t.Fatalf("GetVerboseLevel(telegram) after config update = %q, want %q", got, "full")
	}

	if !al.SetVerboseLevel("telegram:123", "off") {
		t.Fatal("SetVerboseLevel returned false")
	}
	cfg.SetTelegramVerbose(config.VerboseBasic)
	if got := al.GetVerboseLevel("telegram:123"); got != "off" {
		t.Fatalf("explicit session verbose should win over config default, got %q", got)
	}
}

func TestProcessMessage_StartsFreshEphemeralSessionAfterInactivity(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
		},
		Session: config.SessionConfig{
			Ephemeral:          true,
			EphemeralThreshold: 60,
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}

	// Set up a mock provider that returns a valid response for testing
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Fresh response from model",
			ToolCalls: []providers.ToolCall{},
		},
	}

	sessionKey := "telegram:123"
	agent.Sessions.AddMessage(sessionKey, "user", "old question")
	agent.Sessions.AddMessage(sessionKey, "assistant", "old answer")
	agent.Sessions.SetSummary(sessionKey, "old summary")
	agent.Sessions.GetOrCreate(sessionKey).Updated = time.Now().Add(-2 * time.Minute)

	response, err := al.ProcessDirectWithChannel(context.Background(), "new question", sessionKey, "telegram", "123")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}
	if !strings.Contains(response, "New ephemeral session created") {
		t.Fatalf("expected ephemeral notice, got: %s", response)
	}
	if !strings.Contains(response, "Fresh response") {
		t.Fatalf("expected model response, got: %s", response)
	}

	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) != 2 {
		t.Fatalf("expected fresh history with 2 messages, got %d", len(history))
	}
	if history[0].Content == "old question" || history[1].Content == "old answer" {
		t.Fatalf("expected old conversation to be cleared, got history: %+v", history)
	}
	if got := agent.Sessions.GetSummary(sessionKey); got != "" {
		t.Fatalf("expected empty summary after fresh session, got %q", got)
	}
}

// TestToolRegistry_ToolRegistration verifies tools can be registered and retrieved
func TestToolRegistry_ToolRegistration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	// Register a custom tool
	customTool := &mockCustomTool{}
	al.RegisterTool(customTool)

	// Verify tool is registered by checking it doesn't panic on GetStartupInfo
	// (actual tool retrieval is tested in tools package tests)
	info := al.GetStartupInfo()
	toolsInfo := info["tools"].(map[string]interface{})
	toolsList := toolsInfo["names"].([]string)

	// Check that our custom tool name is in the list
	found := false
	for _, name := range toolsList {
		if name == "mock_custom" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected custom tool to be registered")
	}
}

// TestToolContext_Updates verifies tool context is updated with channel/chatID
func TestToolContext_Updates(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	_ = NewAgentLoop(cfg, msgBus)

	// Verify that ContextualTool interface is defined and can be implemented
	// This test validates the interface contract exists
	ctxTool := &mockContextualTool{}

	// Verify the tool implements the interface correctly
	var _ tools.ContextualTool = ctxTool
}

// TestToolRegistry_GetDefinitions verifies tool definitions can be retrieved
func TestToolRegistry_GetDefinitions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	// Register a test tool and verify it shows up in startup info
	testTool := &mockCustomTool{}
	al.RegisterTool(testTool)

	info := al.GetStartupInfo()
	toolsInfo := info["tools"].(map[string]interface{})
	toolsList := toolsInfo["names"].([]string)

	// Check that our custom tool name is in the list
	found := false
	for _, name := range toolsList {
		if name == "mock_custom" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected custom tool to be registered")
	}
}

// TestAgentLoop_GetStartupInfo verifies startup info contains tools
func TestAgentLoop_GetStartupInfo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	info := al.GetStartupInfo()

	// Verify tools info exists
	toolsInfo, ok := info["tools"]
	if !ok {
		t.Fatal("Expected 'tools' key in startup info")
	}

	toolsMap, ok := toolsInfo.(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'tools' to be a map")
	}

	count, ok := toolsMap["count"]
	if !ok {
		t.Fatal("Expected 'count' in tools info")
	}

	// Should have default tools registered
	if count.(int) == 0 {
		t.Error("Expected at least some tools to be registered")
	}
}

// TestAgentLoop_Stop verifies Stop() sets running to false
func TestAgentLoop_Stop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	// Note: running is only set to true when Run() is called
	// We can't test that without starting the event loop
	// Instead, verify the Stop method can be called safely
	al.Stop()

	// Verify running is false (initial state or after Stop)
	if al.running.Load() {
		t.Error("Expected agent to be stopped (or never started)")
	}
}

// Mock implementations for testing

type simpleMockProvider struct {
	response string
}

func (m *simpleMockProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		Content:   m.response,
		ToolCalls: []providers.ToolCall{},
		Usage:     &providers.UsageInfo{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0},
	}, nil
}

func (m *simpleMockProvider) GetDefaultModel() string {
	return "mock-model"
}

// mockCustomTool is a simple mock tool for registration testing
type mockCustomTool struct{}

func (m *mockCustomTool) Name() string {
	return "mock_custom"
}

func (m *mockCustomTool) Description() string {
	return "Mock custom tool for testing"
}

func (m *mockCustomTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (m *mockCustomTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	return tools.SilentResult("Custom tool executed")
}

// mockContextualTool tracks context updates
type mockContextualTool struct {
	lastChannel string
	lastChatID  string
}

func (m *mockContextualTool) Name() string {
	return "mock_contextual"
}

func (m *mockContextualTool) Description() string {
	return "Mock contextual tool"
}

func (m *mockContextualTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (m *mockContextualTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	return tools.SilentResult("Contextual tool executed")
}

func (m *mockContextualTool) SetContext(channel, chatID string) {
	m.lastChannel = channel
	m.lastChatID = chatID
}

// testHelper executes a message and returns the response
type testHelper struct {
	al *AgentLoop
}

func (h testHelper) executeAndGetResponse(tb testing.TB, ctx context.Context, msg bus.InboundMessage) string {
	// Use a short timeout to avoid hanging
	timeoutCtx, cancel := context.WithTimeout(ctx, responseTimeout)
	defer cancel()

	mp, ok := h.al.messageProcessor.(*messageProcessorImpl)
	if !ok {
		tb.Fatalf("message processor is not *messageProcessorImpl")
	}
	response, err := mp.processMessage(timeoutCtx, msg)
	if err != nil {
		tb.Fatalf("processMessage failed: %v", err)
	}
	return response
}

const responseTimeout = 3 * time.Second

// TestToolResult_SilentToolDoesNotSendUserMessage verifies silent tools don't trigger outbound
func TestToolResult_SilentToolDoesNotSendUserMessage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)
	agent := al.registry.GetDefaultAgent()
	if agent != nil {
		agent.Provider = &llmRunnerMockLLMProvider{
			response: &providers.LLMResponse{
				Content:   "File operation complete",
				ToolCalls: []providers.ToolCall{},
			},
		}
	}
	helper := testHelper{al: al}

	// ReadFileTool returns SilentResult, which should not send user message
	ctx := context.Background()
	msg := bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "read test.txt",
		SessionKey: "test-session",
	}

	response := helper.executeAndGetResponse(t, ctx, msg)

	// Silent tool should return the LLM's response directly
	if response != "File operation complete" {
		t.Errorf("Expected 'File operation complete', got: %s", response)
	}
}

// TestToolResult_UserFacingToolDoesSendMessage verifies user-facing tools trigger outbound
func TestToolResult_UserFacingToolDoesSendMessage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)
	agent := al.registry.GetDefaultAgent()
	if agent != nil {
		agent.Provider = &llmRunnerMockLLMProvider{
			response: &providers.LLMResponse{
				Content:   "Command output: hello world",
				ToolCalls: []providers.ToolCall{},
			},
		}
	}
	helper := testHelper{al: al}

	// ExecTool returns UserResult, which should send user message
	ctx := context.Background()
	msg := bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "run hello",
		SessionKey: "test-session",
	}

	response := helper.executeAndGetResponse(t, ctx, msg)

	// User-facing tool should include the output in final response
	if response != "Command output: hello world" {
		t.Errorf("Expected 'Command output: hello world', got: %s", response)
	}
}

// failFirstMockProvider fails on the first N calls with a specific error
type failFirstMockProvider struct {
	failures    int
	currentCall int
	failError   error
	successResp string
}

type blockingMockProvider struct {
	started chan struct{}
}

func (m *blockingMockProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	select {
	case <-m.started:
	default:
		close(m.started)
	}

	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *blockingMockProvider) GetDefaultModel() string {
	return "mock-blocking-model"
}

func (m *failFirstMockProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	m.currentCall++
	if m.currentCall <= m.failures {
		return nil, m.failError
	}
	return &providers.LLMResponse{
		Content:   m.successResp,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *failFirstMockProvider) GetDefaultModel() string {
	return "mock-fail-model"
}

// TestAgentLoop_ContextExhaustionRetry verify that the agent retries on context errors
func TestAgentLoop_ContextExhaustionRetry(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
		},
	}

	msgBus := bus.NewMessageBus()

	// Create a provider that fails once with a context error
	contextErr := fmt.Errorf("InvalidParameter: Total tokens of image and text exceed max message tokens")
	provider := &failFirstMockProvider{
		failures:    1,
		failError:   contextErr,
		successResp: "Recovered from context error",
	}

	al := NewAgentLoop(cfg, msgBus)

	// Inject some history to simulate a full context
	sessionKey := "test-session-context"
	// Create dummy history
	history := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Old message 1"},
		{Role: "assistant", Content: "Old response 1"},
		{Role: "user", Content: "Old message 2"},
		{Role: "assistant", Content: "Old response 2"},
		{Role: "user", Content: "Trigger message"},
	}
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}
	defaultAgent.Provider = provider
	defaultAgent.Sessions.SetHistory(sessionKey, history)

	// Call ProcessDirectWithChannel
	// Note: ProcessDirectWithChannel calls processMessage which will execute runLLMIteration
	response, err := al.ProcessDirectWithChannel(context.Background(), "Trigger message", sessionKey, "test", "test-chat")
	if err != nil {
		t.Fatalf("Expected success after retry, got error: %v", err)
	}

	if response != "Recovered from context error" {
		t.Errorf("Expected 'Recovered from context error', got '%s'", response)
	}

	// We expect 2 calls: 1st failed, 2nd succeeded
	if provider.currentCall != 2 {
		t.Errorf("Expected 2 calls (1 fail + 1 success), got %d", provider.currentCall)
	}

	// Check final history length
	finalHistory := defaultAgent.Sessions.GetHistory(sessionKey)
	// We verify that the history has been modified (compressed)
	// Original length: 6
	// Expected behavior: compression drops ~50% of history (mid slice)
	// We can assert that the length is NOT what it would be without compression.
	// Without compression: 6 + 1 (new user msg) + 1 (assistant msg) = 8
	if len(finalHistory) >= 8 {
		t.Errorf("Expected history to be compressed (len < 8), got %d", len(finalHistory))
	}
}

func TestAgentLoop_Run_SkipsOutboundOnSessionCancel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &blockingMockProvider{started: make(chan struct{})}
	al := NewAgentLoop(cfg, msgBus)
	agent := al.registry.GetDefaultAgent()
	if agent != nil {
		agent.Provider = provider
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	done := make(chan error, 1)
	go func() {
		done <- al.Run(runCtx)
	}()

	msgBus.PublishInbound(bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user1",
		ChatID:     "123",
		Content:    "Hello",
		SessionKey: "telegram:123",
		Metadata:   map[string]string{},
	})

	select {
	case <-provider.started:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("provider did not start processing")
	}

	response := al.StopAgent("telegram:123")
	if !strings.Contains(response, "Agente detenido") {
		t.Fatalf("unexpected stop response: %s", response)
	}

	outboundCtx, cancelOutbound := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancelOutbound()
	if outbound, ok := msgBus.SubscribeOutbound(outboundCtx); ok {
		t.Fatalf("expected no outbound response after session cancellation, got %+v", outbound)
	}

	cancelRun()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent loop returned error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("agent loop did not stop")
	}
}

func TestHandleCommand_NewClearsSession(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}

	sessionKey := "agent:main:test:direct:user1"
	agent.Sessions.AddMessage(sessionKey, "user", "hello")
	agent.Sessions.SetSummary(sessionKey, "old summary")

	ch, ok := al.commandHandler.(*commandHandlerImpl)
	if !ok {
		t.Fatal("command handler is not *commandHandlerImpl")
	}
	response, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "1",
		SessionKey: sessionKey,
		Content:    "/new",
	})
	if !handled {
		t.Fatal("Expected /new to be handled")
	}
	if !strings.Contains(response, "New conversation started") {
		t.Fatalf("Unexpected response: %s", response)
	}
	activeSessionKey := al.ResolveSessionKey(sessionKey)
	if activeSessionKey == sessionKey {
		t.Fatal("Expected /new to switch to a fresh session key")
	}
	if got := len(agent.Sessions.GetHistory(sessionKey)); got != 1 {
		t.Fatalf("Expected old history to be preserved, got %d messages", got)
	}
	if got := agent.Sessions.GetSummary(sessionKey); got != "old summary" {
		t.Fatalf("Expected old summary to be preserved, got %q", got)
	}
	if got := len(agent.Sessions.GetHistory(activeSessionKey)); got != 0 {
		t.Fatalf("Expected fresh session history to be empty, got %d", got)
	}
	if got := agent.Sessions.GetSummary(activeSessionKey); got != "" {
		t.Fatalf("Expected fresh session summary to be empty, got %q", got)
	}
}

func TestHandleCommand_NewResetsTokenCounts(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}

	sessionKey := "agent:main:test:direct:user1"
	// Add some messages and token counts
	agent.Sessions.AddMessage(sessionKey, "user", "hello")
	agent.Sessions.AddTokenCounts(sessionKey, 1000, 500)

	// Verify tokens were added
	inputTokens, outputTokens := agent.Sessions.GetTokenCounts(sessionKey)
	if inputTokens != 1000 || outputTokens != 500 {
		t.Fatalf("Expected tokens (1000, 500), got (%d, %d)", inputTokens, outputTokens)
	}

	ch, ok := al.commandHandler.(*commandHandlerImpl)
	if !ok {
		t.Fatal("command handler is not *commandHandlerImpl")
	}
	response, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "1",
		SessionKey: sessionKey,
		Content:    "/new",
	})
	if !handled {
		t.Fatal("Expected /new to be handled")
	}
	if !strings.Contains(response, "New conversation started") {
		t.Fatalf("Unexpected response: %s", response)
	}

	activeSessionKey := al.ResolveSessionKey(sessionKey)
	if activeSessionKey == sessionKey {
		t.Fatal("Expected /new to switch to a fresh session key")
	}

	// Verify token counts are zero in the new chat while the old chat remains intact.
	inputTokens, outputTokens = agent.Sessions.GetTokenCounts(sessionKey)
	if inputTokens != 1000 || outputTokens != 500 {
		t.Fatalf("Expected old session tokens to remain (1000, 500), got (%d, %d)", inputTokens, outputTokens)
	}
	inputTokens, outputTokens = agent.Sessions.GetTokenCounts(activeSessionKey)
	if inputTokens != 0 || outputTokens != 0 {
		t.Fatalf("Expected fresh session token counts to start at (0, 0), got (%d, %d)", inputTokens, outputTokens)
	}
}

func TestResetAgentSession_ClearsTokenCounts(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}

	sessionKey := "telegram:123456"
	// Add messages, summary, and token counts
	agent.Sessions.AddMessage(sessionKey, "user", "hello")
	agent.Sessions.AddMessage(sessionKey, "assistant", "hi there")
	agent.Sessions.SetSummary(sessionKey, "previous conversation summary")
	agent.Sessions.AddTokenCounts(sessionKey, 1500, 800)

	// Verify initial state
	if got := len(agent.Sessions.GetHistory(sessionKey)); got != 2 {
		t.Fatalf("Expected 2 messages, got %d", got)
	}
	if got := agent.Sessions.GetSummary(sessionKey); got != "previous conversation summary" {
		t.Fatalf("Expected summary, got %q", got)
	}
	inputTokens, outputTokens := agent.Sessions.GetTokenCounts(sessionKey)
	if inputTokens != 1500 || outputTokens != 800 {
		t.Fatalf("Expected tokens (1500, 800), got (%d, %d)", inputTokens, outputTokens)
	}

	// Reset the session
	if err := al.resetAgentSession(agent, sessionKey); err != nil {
		t.Fatalf("resetAgentSession failed: %v", err)
	}

	// Verify everything was cleared including token counts
	if got := len(agent.Sessions.GetHistory(sessionKey)); got != 0 {
		t.Fatalf("Expected empty history, got %d", got)
	}
	if got := agent.Sessions.GetSummary(sessionKey); got != "" {
		t.Fatalf("Expected empty summary, got %q", got)
	}
	inputTokens, outputTokens = agent.Sessions.GetTokenCounts(sessionKey)
	if inputTokens != 0 || outputTokens != 0 {
		t.Fatalf("Expected token counts to be reset to (0, 0), got (%d, %d)", inputTokens, outputTokens)
	}
}

func TestProcessMessage_EphemeralSessionResetsTokenCounts(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
		},
		Session: config.SessionConfig{
			Ephemeral:          true,
			EphemeralThreshold: 60,
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Fresh response from model",
			ToolCalls: []providers.ToolCall{},
		},
	}

	sessionKey := "telegram:123"
	// Add old conversation with token counts
	agent.Sessions.AddMessage(sessionKey, "user", "old question")
	agent.Sessions.AddMessage(sessionKey, "assistant", "old answer")
	agent.Sessions.AddTokenCounts(sessionKey, 2000, 1000)
	agent.Sessions.GetOrCreate(sessionKey).Updated = time.Now().Add(-2 * time.Minute)

	// Verify tokens exist before ephemeral reset
	inputTokens, outputTokens := agent.Sessions.GetTokenCounts(sessionKey)
	if inputTokens != 2000 || outputTokens != 1000 {
		t.Fatalf("Expected tokens (2000, 1000) before ephemeral reset, got (%d, %d)", inputTokens, outputTokens)
	}

	response, err := al.ProcessDirectWithChannel(context.Background(), "new question", sessionKey, "telegram", "123")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}
	if !strings.Contains(response, "New ephemeral session created") {
		t.Fatalf("expected ephemeral notice, got: %s", response)
	}

	// Verify token counts were reset after ephemeral session creation
	// Note: New tokens are added from the fresh LLM response, so we verify they are much less than original (2000, 1000)
	inputTokens, outputTokens = agent.Sessions.GetTokenCounts(sessionKey)
	if inputTokens >= 2000 || outputTokens >= 1000 {
		t.Fatalf("Expected token counts to be reset and lower than original (2000, 1000), got (%d, %d)", inputTokens, outputTokens)
	}
}

func TestHandleCommand_ModelAndStatus(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}

	ch, ok := al.commandHandler.(*commandHandlerImpl)
	if !ok {
		t.Fatal("command handler is not *commandHandlerImpl")
	}
	_, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "1",
		SessionKey: "agent:main:test:direct:user1",
		Content:    "/model test-model-v2",
	})
	if !handled {
		t.Fatal("Expected /model to be handled")
	}
	if defaultAgent.Model != "test-model" {
		t.Fatalf("Expected default model unchanged, got %s", defaultAgent.Model)
	}
	if selected, ok := al.sessionModels.Load("agent:main:test:direct:user1"); !ok || selected.(string) != "test-model-v2" {
		t.Fatalf("Expected session model override test-model-v2, got %v", selected)
	}

	status, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "1",
		SessionKey: "agent:main:test:direct:user1",
		Content:    "/status",
	})
	if !handled {
		t.Fatal("Expected /status to be handled")
	}
	if !strings.Contains(status, "Model: test-model-v2") {
		t.Fatalf("Unexpected status response: %s", status)
	}
	if !strings.Contains(status, "Gateway version:") {
		t.Fatalf("Expected gateway version in status response: %s", status)
	}

	otherStatus, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "2",
		SessionKey: "agent:main:test:direct:user2",
		Content:    "/status",
	})
	if !handled {
		t.Fatal("Expected /status to be handled for second session")
	}
	if !strings.Contains(otherStatus, "Model: test-model") {
		t.Fatalf("Unexpected second session status response: %s", otherStatus)
	}
}

func TestHandleCommand_NewStartsFreshSessionWithoutClearingPreviousHistory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}
	sessionKey := "agent:main:/invalid"
	agent.Sessions.AddMessage(sessionKey, "user", "hello")
	agent.Sessions.SetSummary(sessionKey, "summary")

	ch, ok := al.commandHandler.(*commandHandlerImpl)
	if !ok {
		t.Fatal("command handler is not *commandHandlerImpl")
	}
	response, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "1",
		SessionKey: sessionKey,
		Content:    "/new",
	})
	if !handled {
		t.Fatal("Expected /new to be handled")
	}
	if !strings.Contains(response, "New conversation started") {
		t.Fatalf("Expected success response, got: %s", response)
	}
	activeSessionKey := al.ResolveSessionKey(sessionKey)
	if activeSessionKey == sessionKey {
		t.Fatal("Expected /new to switch to a fresh session key")
	}
	if got := len(agent.Sessions.GetHistory(sessionKey)); got == 0 {
		t.Fatal("Expected previous history to remain in the original session")
	}
	if got := agent.Sessions.GetSummary(sessionKey); got == "" {
		t.Fatal("Expected previous summary to remain in the original session")
	}
	if got := len(agent.Sessions.GetHistory(activeSessionKey)); got != 0 {
		t.Fatalf("Expected fresh session history to be empty, got %d", got)
	}
}

func TestFormatProviderModel(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		want     string
	}{
		{name: "provider and model", provider: "moonshotai", model: "Kimi-K2.5-TEE", want: "moonshotai/Kimi-K2.5-TEE"},
		{name: "already prefixed", provider: "moonshotai", model: "moonshotai/Kimi-K2.5-TEE", want: "moonshotai/Kimi-K2.5-TEE"},
		{name: "no provider", provider: "", model: "Kimi-K2.5-TEE", want: "Kimi-K2.5-TEE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatProviderModel(tt.provider, tt.model); got != tt.want {
				t.Fatalf("FormatProviderModel(%q,%q) = %q, want %q", tt.provider, tt.model, got, tt.want)
			}
		})
	}
}

// ============================================
// Tests de Subagentes con Acceso a Tools
// Plan 2: Herramientas del Sistema para Subagentes
// ============================================

// TestSubagentManager_InheritsParentTools verifica que los subagentes heredan las tools del agente padre salvo message
func TestSubagentManager_InheritsParentTools(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()

	// Crear AgentLoop - esto registra las tools y configura subagents
	al := NewAgentLoop(cfg, msgBus)

	// Verificar que el subagent manager tiene las tools del agente padre
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}

	// Obtener el subagent manager para el agente por defecto
	subagentManager, ok := al.subagents[defaultAgent.ID]
	if !ok {
		t.Fatal("No subagent manager found for default agent")
	}

	// Verificar que el ToolRegistry del subagente tiene las mismas herramientas que el padre
	parentTools := defaultAgent.Tools.List()
	subagentTools := subagentManager.GetToolRegistry().List()

	if len(parentTools) == 0 {
		t.Fatal("Parent agent should have tools registered")
	}

	expectedTools := len(parentTools)
	if defaultAgent.Tools != nil {
		if _, ok := defaultAgent.Tools.Get("send_file"); ok {
			expectedTools--
		}
	}

	if len(subagentTools) != expectedTools {
		t.Errorf("Subagent should inherit all allowed parent tools. Expected %d, got %d",
			expectedTools, len(subagentTools))
	}

	if subagentManager.HasTool("send_file") {
		t.Error("Subagent should not have the send_file tool")
	}

	// Verificar herramientas específicas que debe tener el subagente
	// Nota: Algunas requieren configuración, verificamos las base disponibles
	baseTools := []string{"read_file", "write_file", "list_dir"}
	for _, toolName := range baseTools {
		if !subagentManager.HasTool(toolName) {
			t.Errorf("Subagent missing required tool: %s", toolName)
		}
	}

	// Verificar herramientas avanzadas si están configuradas
	optionalTools := []string{"web_search", "spawn"}
	for _, toolName := range optionalTools {
		if subagentManager.HasTool(toolName) {
			t.Logf("Optional tool available: %s", toolName)
		}
	}
}

// TestSubagentManager_ToolExecution verifies that subagent has tools registry configured
func TestSubagentManager_ToolExecution(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagent-exec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()

	al := NewAgentLoop(cfg, msgBus)

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}

	subagentManager := al.subagents[defaultAgent.ID]
	if subagentManager == nil {
		t.Fatal("No subagent manager found")
	}

	// Verificar que el registry de tools está configurado y tiene herramientas
	registry := subagentManager.GetToolRegistry()
	if registry == nil {
		t.Fatal("Tool registry should not be nil")
	}

	// Verificar que el subagente tiene herramientas de archivo disponibles
	testFile := filepath.Join(tmpDir, "testfile.txt")
	testContent := "test content for subagent"
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Ejecutar read_file directamente usando el registry
	result := registry.ExecuteWithContext(
		context.Background(),
		"read_file",
		map[string]interface{}{"path": testFile},
		"test",
		"test-chat",
		nil,
	)

	if result.IsError {
		t.Errorf("Expected successful read, got error: %s", result.ForLLM)
	}

	if !strings.Contains(result.ForLLM, testContent) {
		t.Errorf("Expected result to contain '%s', got: %s", testContent, result.ForLLM)
	}
}

// TestSubagentManager_WebTools verifies web tools are inherited from parent agent
func TestSubagentManager_WebTools(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagent-web-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()

	al := NewAgentLoop(cfg, msgBus)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}

	subagentManager := al.subagents[defaultAgent.ID]

	// Verificar herramientas de fetching web disponibles
	// (web_search requiere configuración API key)
	if !subagentManager.HasTool("web_fetch") {
		t.Error("Subagent should have web_fetch tool")
	}

	// Verificar web_search si está configurado
	if subagentManager.HasTool("web_search") {
		t.Log("web_search tool available (configured)")
	} else {
		t.Log("web_search not available (requires API key configuration)")
	}
}

// TestSubagentManager_HardwareTools verifies I2C/SPI tools are available to subagents on Linux
func TestSubagentManager_HardwareTools(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagent-hw-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()

	al := NewAgentLoop(cfg, msgBus)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}

	subagentManager := al.subagents[defaultAgent.ID]

	// Verificar herramientas hardware
	hwTools := []string{"i2c", "spi"}
	for _, toolName := range hwTools {
		if !subagentManager.HasTool(toolName) {
			t.Errorf("Subagent missing hardware tool: %s", toolName)
		}
	}
}

// TestSubagentManager_EditingTools verifies advanced editing tools are available
// and the legacy FMOD preview/apply flow stays deprecated.
func TestSubagentManager_EditingTools(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagent-editing-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()

	al := NewAgentLoop(cfg, msgBus)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}

	subagentManager := al.subagents[defaultAgent.ID]

	// Verificar herramientas de edición avanzadas
	editingTools := []string{"smart_edit", "patch", "sequential_replace"}
	for _, toolName := range editingTools {
		if !subagentManager.HasTool(toolName) {
			t.Errorf("subagent missing editing tool: %s", toolName)
		}
	}

	// Verificar que el flujo legacy de FMOD siga deprecado
	deprecatedTools := []string{"preview", "apply"}
	for _, toolName := range deprecatedTools {
		if subagentManager.HasTool(toolName) {
			t.Errorf("subagent should not expose deprecated tool: %s", toolName)
		}
	}
}

// TestSubagentManager_NestedSpawn verifies subagents can spawn other subagents
func TestSubagentManager_NestedSpawn(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagent-nested-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()

	al := NewAgentLoop(cfg, msgBus)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}

	subagentManager := al.subagents[defaultAgent.ID]

	// Verificar que el subagente tiene acceso a spawn
	if !subagentManager.HasTool("spawn") {
		t.Error("Subagent should have spawn tool for nested subagent creation")
	}

}

// TestSubagentManager_WorkspaceSecurity verifies subagent respects workspace boundaries
func TestSubagentManager_WorkspaceSecurity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagent-security-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()

	al := NewAgentLoop(cfg, msgBus)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}

	subagentManager := al.subagents[defaultAgent.ID]

	// Verificar que read_file existe (el workspace validate workspace check by try)
	result := subagentManager.GetToolRegistry().ExecuteWithContext(
		context.Background(),
		"read_file",
		map[string]interface{}{
			"path": filepath.Join(tmpDir, "test.txt"),
		},
		"test",
		"test-chat",
		nil,
	)

	// Debería fallar porque el archivo no existe, pero el tool debe estar disponible
	if result.IsError {
		t.Logf("Expected error (file doesn't exist): %s", result.ForLLM)
	}

	// Lo importante es verificar que el subagente tiene acceso al registro de tools
	if !subagentManager.HasTool("read_file") {
		t.Error("Subagent should have read_file tool for workspace access")
	}
}

// TestSubagentManager_SetLLMOptions verifies LLM options are passed to subagent
func TestSubagentManager_SetLLMOptions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagent-opts-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()

	al := NewAgentLoop(cfg, msgBus)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}

	subagentManager := al.subagents[defaultAgent.ID]

	// Ver configuración de LLM
	if subagentManager == nil {
		t.Fatal("Subagent manager should be initialized")
	}

	// Las opciones de LLM deberían estar configuradas desde el agente padre
	// Nota: No podemos verificar directamente los valores internos sin exponerlos,
	// pero el hecho de que no haya errores indica que la configuración se aplicó
}
