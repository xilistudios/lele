package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
)

func TestSummarizeSessionWithError_InsufficientMessages(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session-manager-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: &config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Summary",
			ToolCalls: []providers.ToolCall{},
		},
	}
	sessionKey := "test:insufficient"

	stats, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err == nil {
		t.Error("Expected error for no messages")
	}
	if stats != nil {
		t.Error("Expected nil stats for no messages")
	}
	if !strings.Contains(err.Error(), "not enough messages") {
		t.Errorf("Expected not enough messages error, got: %v", err)
	}

	agent.Sessions.AddMessage(sessionKey, "user", "Hello")
	stats, err = sm.summarizeSessionWithError(agent, sessionKey)
	if err == nil {
		t.Error("Expected error for 1 message")
	}
	if stats != nil {
		t.Error("Expected nil stats for 1 message")
	}

	agent.Sessions.AddMessage(sessionKey, "assistant", "Hi")
	stats, err = sm.summarizeSessionWithError(agent, sessionKey)
	if err == nil {
		t.Error("Expected error for 2 messages")
	}
	if stats != nil {
		t.Error("Expected nil stats for 2 messages")
	}

	agent.Sessions.AddMessage(sessionKey, "user", "How are you?")
	stats, err = sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		if !strings.Contains(err.Error(), "LLM summarization failed") {
			t.Errorf("Expected LLM error for 3 messages, got: %v", err)
		}
	} else {
		if stats == nil {
			t.Error("Expected stats when no error")
		} else if stats.BeforeMessages < 3 {
			t.Errorf("Expected at least 3 messages before compaction, got: %d", stats.BeforeMessages)
		}
	}
}

func TestSummarizeSessionWithError_EmptyResult(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session-manager-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: &config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "",
			ToolCalls: []providers.ToolCall{},
		},
	}
	sessionKey := "test:empty-result"

	for i := 0; i < 5; i++ {
		agent.Sessions.AddMessage(sessionKey, "user", fmt.Sprintf("Message %d", i))
		agent.Sessions.AddMessage(sessionKey, "assistant", fmt.Sprintf("Response %d", i))
	}

	stats, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err == nil {
		t.Error("Expected error for empty result")
	}
	if stats != nil {
		t.Error("Expected nil stats for empty result")
	}
	if !strings.Contains(err.Error(), "empty result") {
		t.Errorf("Expected empty result error, got: %v", err)
	}
}

func TestSummarizeSessionWithError_Success(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session-manager-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: &config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	testSessionMgr := session.NewSessionManager()
	al.registry.SetSharedSessionManager(testSessionMgr)

	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Summary: The user asked several questions about important topics and received detailed answers.",
			ToolCalls: []providers.ToolCall{},
		},
	}
	sessionKey := "test:success"

	for i := 0; i < 6; i++ {
		agent.Sessions.AddMessage(sessionKey, "user", fmt.Sprintf("Question %d about important topic", i))
		agent.Sessions.AddMessage(sessionKey, "assistant", fmt.Sprintf("Answer %d with detailed information", i))
	}

	beforeCount := len(agent.Sessions.GetHistory(sessionKey))

	stats, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		t.Fatalf("Unexpected error in successful summarization: %v", err)
	}
	if stats == nil {
		t.Fatal("Expected stats for successful summarization")
	}

	if stats.BeforeMessages != beforeCount {
		t.Errorf("Expected BeforeMessages=%d, got %d", beforeCount, stats.BeforeMessages)
	}
	if stats.AfterMessages >= stats.BeforeMessages {
		t.Errorf("Expected fewer messages after: before=%d, after=%d",
			stats.BeforeMessages, stats.AfterMessages)
	}
	if stats.DroppedMessages <= 0 {
		t.Errorf("Expected positive dropped messages, got %d", stats.DroppedMessages)
	}

	summary := agent.Sessions.GetSummary(sessionKey)
	if summary == "" {
		t.Error("Expected summary to be set, got empty string")
	}
	if !strings.Contains(summary, "Summary") {
		t.Errorf("Expected summary to contain 'Summary', got: %s", summary)
	}

	afterHistory := agent.Sessions.GetHistory(sessionKey)
	excludedCount := 0
	includedCount := 0
	for _, m := range afterHistory {
		if m.ExcludeFromContext {
			excludedCount++
		} else {
			includedCount++
		}
	}
	if excludedCount == 0 {
		t.Error("Expected some messages to be excluded from context")
	}
	if includedCount != 4 {
		t.Errorf("Expected 4 messages included in context (first + preserved human + last 2), got %d", includedCount)
	}
}

func TestAddTokenCounts_SessionKeyWithoutAgentPrefix(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session-manager-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

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

	sm := newSessionManager(al)

	sessionKey := "telegram:12345"
	inputTokens := 100
	outputTokens := 50

	sm.AddTokenCounts(sessionKey, inputTokens, outputTokens)

	defaultAgent := al.registry.GetDefaultAgent()
	inputTokensActual, outputTokensActual := defaultAgent.Sessions.GetTokenCounts(sessionKey)
	if inputTokensActual != inputTokens || outputTokensActual != outputTokens {
		t.Errorf("Expected input=%d, output=%d, got input=%d, output=%d",
			inputTokens, outputTokens, inputTokensActual, outputTokensActual)
	}

	sessionKeyWithPrefix := "agent:main:telegram:12345"
	inputTokens2 := 200
	outputTokens2 := 75

	sm.AddTokenCounts(sessionKeyWithPrefix, inputTokens2, outputTokens2)

	parsed := routing.ParseAgentSessionKey(sessionKeyWithPrefix)
	if parsed == nil {
		t.Fatal("Failed to parse session key with prefix")
	}

	agent, ok := al.registry.GetAgent(parsed.AgentID)
	if !ok {
		t.Fatalf("Failed to get agent %s", parsed.AgentID)
	}

	inputTokensActual2, outputTokensActual2 := agent.Sessions.GetTokenCounts(sessionKeyWithPrefix)
	if inputTokensActual2 != inputTokens2 || outputTokensActual2 != outputTokens2 {
		t.Errorf("Expected input=%d, output=%d, got input=%d, output=%d",
			inputTokens2, outputTokens2, inputTokensActual2, outputTokensActual2)
	}
}

func TestMaybeSummarize_TriggersWhenThresholdExceeded(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session-manager-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				Provider:          "anthropic",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: &config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
			Named: map[string]config.NamedProviderConfig{
				"anthropic": {
					Type:           "anthropic",
					ProviderConfig: config.ProviderConfig{APIKey: "test-key"},
					Models: map[string]config.ProviderModelConfig{
						"test-model": {ContextWindow: 1000},
					},
				},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	testSessionMgr := session.NewSessionManager()
	al.registry.SetSharedSessionManager(testSessionMgr)

	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}

	agent.ContextWindow = 1000

	agent.Provider = &mockProvider{
		mockResponse: "Summary of the conversation about testing compaction behavior.",
	}

	sessionKey := "test:maybe-summarize-triggers-threshold-exceeded"

	longContent := strings.Repeat("x", 200)
	for i := 0; i < 10; i++ {
		agent.Sessions.AddMessage(sessionKey, "user", fmt.Sprintf("Question %d: %s", i, longContent))
		agent.Sessions.AddMessage(sessionKey, "assistant", fmt.Sprintf("Answer %d: %s", i, longContent))
	}

	beforeHistory := agent.Sessions.GetHistory(sessionKey)
	beforeCount := len(beforeHistory)
	t.Logf("Before maybeSummarize: %d messages", beforeCount)
	if beforeCount != 20 {
		t.Fatalf("Expected 20 seeded messages, got %d", beforeCount)
	}

	stats := sm.maybeSummarize(agent, sessionKey, "test", "test-chat")
	if stats == nil {
		t.Fatal("Expected non-nil SummarizeStats; compaction should have been triggered")
	}

	if stats.DroppedMessages <= 0 {
		t.Errorf("Expected DroppedMessages > 0, got %d", stats.DroppedMessages)
	}
	t.Logf("Compaction stats: before=%d after=%d dropped=%d (tokens %d→%d, saved %d)",
		stats.BeforeMessages, stats.AfterMessages, stats.DroppedMessages,
		stats.BeforeTokens, stats.AfterTokens, stats.SavedTokens)

	afterHistory := agent.Sessions.GetHistory(sessionKey)
	excludedCount := 0
	includedCount := 0
	for _, m := range afterHistory {
		if m.ExcludeFromContext {
			excludedCount++
		} else {
			includedCount++
		}
	}
	if excludedCount == 0 {
		t.Error("Expected some messages to be excluded from context")
	}
	if includedCount != 4 {
		t.Errorf("Expected 4 messages included in context (first + preserved human + last 2), got %d", includedCount)
	}

	for i, m := range afterHistory {
		if isSummaryMessage(m) && m.ExcludeFromContext {
			t.Errorf("Summary message [%d] should not be excluded from context", i)
		}
	}
}

func TestEstimateTokens_VideoPartCounts(t *testing.T) {
	sm := &sessionManagerImpl{}

	textOnly := []providers.Message{
		{
			Role: "user",
			ContentParts: []providers.ContentPart{
				{Type: "text", Text: "Describe this clip."},
			},
		},
	}
	withVideo := []providers.Message{
		{
			Role: "user",
			ContentParts: []providers.ContentPart{
				{Type: "text", Text: "Describe this clip."},
				{Type: "video_url", VideoURL: &providers.VideoURL{URL: "https://example.com/clip.mp4"}},
			},
		},
	}

	textTokens := sm.EstimateTokens(textOnly)
	videoTokens := sm.EstimateTokens(withVideo)
	minDelta := videoPartEstimateChars * 2 / 5
	if delta := videoTokens - textTokens; delta < minDelta {
		t.Fatalf("video part raised estimate by %d tokens, want >= %d (text=%d, video=%d)",
			delta, minDelta, textTokens, videoTokens)
	}
}

func TestEstimateTokens_CountsImagesAfterRehydration(t *testing.T) {
	sm := &sessionManagerImpl{}
	msgs := []providers.Message{
		{
			Role:    "user",
			Content: "",
			ContentParts: []providers.ContentPart{
				{Type: "image_url", ImageURL: &providers.ImageURL{
					URL: "data:image/png;base64," + strings.Repeat("A", 200_000),
				}},
			},
		},
	}

	before := sm.EstimateTokens(msgs)
	if before < 1000 {
		t.Fatalf("live message: EstimateTokens = %d, want >= 1000", before)
	}

	raw, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var reloaded []providers.Message
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(reloaded) != 1 {
		t.Fatalf("rehydrated %d messages, want 1", len(reloaded))
	}

	after := sm.EstimateTokens(reloaded)
	if after < 1000 {
		t.Fatalf("rehydrated message: EstimateTokens = %d, want >= 1000 (old gate billed ~0)", after)
	}
}

func TestEstimatorsAgree(t *testing.T) {
	sm := &sessionManagerImpl{}

	textMsg := providers.Message{Role: "system", Content: "You are helpful."}
	toolMsg := providers.Message{
		Role:    "assistant",
		Content: "Reading the file.",
		ToolCalls: []providers.ToolCall{{
			ID:       "call_1",
			Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"/tmp/a.txt"}`},
		}},
	}
	liveImg := providers.Message{
		Role:    "user",
		Content: "",
		ContentParts: []providers.ContentPart{
			{Type: "text", Text: "look at this"},
			{Type: "image_url", ImageURL: &providers.ImageURL{
				URL: "data:image/png;base64," + strings.Repeat("A", 100_000),
			}},
		},
	}
	rehydratedSrc := providers.Message{
		Role:    "user",
		Content: "",
		ContentParts: []providers.ContentPart{
			{Type: "image_url", ImageURL: &providers.ImageURL{
				URL: "data:image/png;base64," + strings.Repeat("B", 50_000),
			}},
		},
	}
	raw, err := json.Marshal([]providers.Message{rehydratedSrc})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var rehydrated []providers.Message
	if err := json.Unmarshal(raw, &rehydrated); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mediaMsg := providers.Message{Role: "user", Content: "See attached.", Media: []string{"/a.png", "/b.png"}}

	msgs := append(
		[]providers.Message{textMsg, toolMsg, liveImg},
		append(rehydrated, mediaMsg)...,
	)
	if loopTokens, smTokens := tools.EstimateLoopTokens(msgs), sm.EstimateTokens(msgs); loopTokens != smTokens {
		t.Errorf("estimators disagree on no-exclusion list: EstimateLoopTokens = %d, EstimateTokens = %d", loopTokens, smTokens)
	}

	excluded := providers.Message{
		Role:               "user",
		Content:            "payload removed",
		ExcludeFromContext: true,
	}
	withExcluded := append(append([]providers.Message{}, msgs...), excluded)

	loopTokens := tools.EstimateLoopTokens(withExcluded)
	smTokens := sm.EstimateTokens(withExcluded)
	wantDelta := providers.MessageCharCost(excluded) * 2 / 5
	if delta := loopTokens - smTokens; delta != wantDelta {
		t.Errorf("gap with excluded message = %d tokens, want exactly %d (loop=%d, sm=%d)",
			delta, wantDelta, loopTokens, smTokens)
	}
	if smTokens >= loopTokens {
		t.Errorf("EstimateTokens (%d) must be strictly lower than EstimateLoopTokens (%d) when a message is excluded", smTokens, loopTokens)
	}
}
