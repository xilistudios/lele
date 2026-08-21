// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"os"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
)

// ---- llm_types.go ----

func TestMessageSignaturesEqual(t *testing.T) {
	base := []toolCallSignature{{name: "exec", arguments: "ls"}}
	cases := []struct {
		name string
		a, b []toolCallSignature
		want bool
	}{
		{"both empty", nil, nil, true},
		{"empty vs non-empty", nil, base, false},
		{"non-empty vs empty", base, nil, false},
		{"equal", base, []toolCallSignature{{name: "exec", arguments: "ls"}}, true},
		{"different name", base, []toolCallSignature{{name: "read", arguments: "ls"}}, false},
		{"different args", base, []toolCallSignature{{name: "exec", arguments: "pwd"}}, false},
		{"different length", base, []toolCallSignature{{name: "exec", arguments: "ls"}, {name: "x", arguments: "y"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := messageSignaturesEqual(c.a, c.b); got != c.want {
				t.Errorf("messageSignaturesEqual = %v, want %v", got, c.want)
			}
		})
	}
}

func TestIsDeepSeekModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"deepseek/deepseek-chat", true},
		{"deepseek-v3", true},
		{"deepseek_reasoner", false},
		{"DeepSeek-Chat", true},
		{"gpt-4o", false},
		{"claude-3", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isDeepSeekModel(c.model); got != c.want {
			t.Errorf("isDeepSeekModel(%q) = %v, want %v", c.model, got, c.want)
		}
	}
}

func TestKnownToolNames(t *testing.T) {
	al := newTestAgentLoop(t)
	agent := al.registry.GetDefaultAgent()
	names := knownToolNames(agent)
	if len(names) == 0 {
		t.Fatal("expected at least one tool")
	}
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Fatalf("tool names not sorted: %v", names)
		}
	}
	if _, ok := agent.Tools.Get(names[0]); !ok {
		t.Errorf("first tool %q not found in registry", names[0])
	}
}

func TestContainsPlainToolCall(t *testing.T) {
	al := newTestAgentLoop(t)
	agent := al.registry.GetDefaultAgent()

	toolName := agent.Tools.List()[0]
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"empty", "", false},
		{"whitespace", "   ", false},
		{"no pattern", "hello world", false},
		{"plain tool call with quotes", toolName + `{"path":"x"}`, true},
		{"tool name no brace", "just a name", false},
		{"tool with no closing brace", toolName + `{"path":"x"`, false},
		{"tool with non-json inner", toolName + `{literal}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := containsPlainToolCall(c.content, agent); got != c.want {
				t.Errorf("containsPlainToolCall = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBuildAssistantMessageWithToolCalls(t *testing.T) {
	resp := &providers.LLMResponse{
		Content:          "thinking...",
		ReasoningContent: "deep",
		ToolCalls: []providers.ToolCall{
			{ID: "call_1", Name: "exec", Arguments: map[string]interface{}{"command": "ls"}},
		},
	}
	msg := buildAssistantMessageWithToolCalls(resp)
	if msg.Role != "assistant" {
		t.Errorf("role = %q", msg.Role)
	}
	if msg.Content != "thinking..." {
		t.Errorf("content = %q", msg.Content)
	}
	if msg.ReasoningContent != "deep" {
		t.Errorf("reasoning = %q", msg.ReasoningContent)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("toolcalls len = %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "exec" || tc.Type != "function" {
		t.Errorf("toolcall mismatch: %+v", tc)
	}
	if tc.Function == nil || tc.Function.Name != "exec" || tc.Function.Arguments == "" {
		t.Errorf("function nil or wrong: %+v", tc.Function)
	}

	emptyMsg := buildAssistantMessageWithToolCalls(&providers.LLMResponse{Content: "c"})
	if len(emptyMsg.ToolCalls) != 0 {
		t.Errorf("expected no tool calls")
	}
}

// ---- llm_stream.go ----

func TestNewStreamHandler_GeneratesMessageID(t *testing.T) {
	mb := bus.NewMessageBus()
	h1 := newStreamHandler(mb, "web", "chat1", "")
	h2 := newStreamHandler(mb, "web", "chat1", "")
	if h1.messageID == "" || h1.messageID == h2.messageID {
		t.Errorf("expected unique generated message IDs, got %q and %q", h1.messageID, h2.messageID)
	}
}

func TestNewStreamHandler_UsesProvidedMessageID(t *testing.T) {
	mb := bus.NewMessageBus()
	h := newStreamHandler(mb, "web", "chat1", "fixed-id")
	if h.messageID != "fixed-id" {
		t.Errorf("messageID = %q, want fixed-id", h.messageID)
	}
}

func TestStreamHandler_ShouldStream(t *testing.T) {
	mb := bus.NewMessageBus()
	h := newStreamHandler(mb, "native", "chat1", "id")
	if !h.shouldStream(true) {
		t.Error("expected shouldStream true")
	}
	if h.shouldStream(false) {
		t.Error("expected shouldStream false when sendResponse false")
	}
	telegram := newStreamHandler(mb, "telegram", "chat1", "id")
	if telegram.shouldStream(true) {
		t.Error("expected shouldStream false for non-web channel")
	}
}

func TestStreamHandler_OnChunk(t *testing.T) {
	mb := bus.NewMessageBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := make(chan bus.OutboundMessage, 10)
	go func() {
		for {
			m, ok := mb.SubscribeOutbound(ctx)
			if !ok {
				return
			}
			seen <- m
		}
	}()
	h := newStreamHandler(mb, "web", "chat1", "mid")
	h.onChunk("hello", false)
	h.onChunk("", true)

	var msgs []bus.OutboundMessage
	for i := 0; i < 2; i++ {
		select {
		case m := <-seen:
			msgs = append(msgs, m)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for outbound message")
		}
	}
	if msgs[0].Event != "message.stream" || msgs[0].Channel != "web" || msgs[0].ChatID != "chat1" || msgs[0].MessageID != "mid" {
		t.Errorf("chunk message mismatch: %+v", msgs[0])
	}
	if msgs[0].Metadata["done"] != "false" || msgs[1].Metadata["done"] != "true" {
		t.Errorf("done metadata wrong: %v %v", msgs[0].Metadata["done"], msgs[1].Metadata["done"])
	}
}

func TestStreamHandler_OnReasoning(t *testing.T) {
	mb := bus.NewMessageBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := make(chan bus.OutboundMessage, 1)
	go func() {
		for {
			m, ok := mb.SubscribeOutbound(ctx)
			if !ok {
				return
			}
			seen <- m
		}
	}()
	h := newStreamHandler(mb, "web", "chat1", "mid")
	h.onReasoning("thinking...")
	select {
	case m := <-seen:
		if m.Event != "message.thinking" || m.Content != "thinking..." || m.MessageID != "mid" {
			t.Errorf("reasoning message mismatch: %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reasoning message")
	}
}

// ---- token_tracker.go ----

func TestTrackTokenUsage_NilResponse(t *testing.T) {
	trackTokenUsage(nil, "", "agent1", nil, nil)
	trackTokenUsage(nil, "abc", "agent1", nil, nil)
}

func TestTrackTokenUsage_WithUsage(t *testing.T) {
	sess := session.NewSessionManager()
	sesKey := "telegram:123"
	resp := &providers.LLMResponse{
		Content: "hi",
		Usage:   &providers.UsageInfo{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
	}
	trackTokenUsage(sess, sesKey, "agent1", nil, resp)
	in, out := sess.GetTokenCounts(sesKey)
	if in != 100 || out != 50 {
		t.Errorf("token counts = %d,%d want 100,50", in, out)
	}
}

func TestTrackTokenUsage_Estimated(t *testing.T) {
	sess := session.NewSessionManager()
	sesKey := "telegram:456"
	msgs := []providers.Message{{Role: "user", Content: "hello world"}}
	resp := &providers.LLMResponse{Content: "how are you"}
	trackTokenUsage(sess, sesKey, "agent1", msgs, resp)
	in, out := sess.GetTokenCounts(sesKey)
	if in != 4 || out != 4 {
		t.Errorf("token counts = %d,%d want 4,4", in, out)
	}
}

// ---- registry.go helpers ----

func TestPtrStringsEqual(t *testing.T) {
	a := "x"
	b := "x"
	c := "y"
	cases := []struct {
		name string
		a, b *string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"a nil", nil, &a, false},
		{"b nil", &a, nil, false},
		{"equal", &a, &b, true},
		{"different", &a, &c, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ptrStringsEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("ptrStringsEqual = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStringSlicesEqual2(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"diff len", []string{"a"}, nil, false},
		{"equal", []string{"a", "b"}, []string{"a", "b"}, true},
		{"diff elem", []string{"a"}, []string{"b"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stringSlicesEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("stringSlicesEqual = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReasoningConfigsEqual2(t *testing.T) {
	low := "low"
	cases := []struct {
		name string
		a, b *config.ReasoningConfig
		want bool
	}{
		{"both nil", nil, nil, true},
		{"a nil", nil, &config.ReasoningConfig{Enable: true}, false},
		{"b nil", &config.ReasoningConfig{Enable: true}, nil, false},
		{"equal", &config.ReasoningConfig{Enable: true, Effort: &low}, &config.ReasoningConfig{Enable: true, Effort: &low}, true},
		{"enable diff", &config.ReasoningConfig{Enable: true}, &config.ReasoningConfig{Enable: false}, false},
		{"effort nil diff", &config.ReasoningConfig{Enable: true, Effort: &low}, &config.ReasoningConfig{Enable: true}, false},
		{"effort val diff", &config.ReasoningConfig{Enable: true, Effort: &low}, &config.ReasoningConfig{Enable: true, Effort: ptrString("high")}, false},
		{"summary diff", &config.ReasoningConfig{Enable: true}, &config.ReasoningConfig{Enable: true, Summary: ptrString("s")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reasoningConfigsEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("reasoningConfigsEqual = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---- goal.go ----

func TestSubagentGoalJudge_WithRetries(t *testing.T) {
	j := &SubagentGoalJudge{}
	j.WithRetries(3)
	if j.retries != 3 {
		t.Errorf("retries = %d, want 3", j.retries)
	}
}

func TestSubagentGoalJudge_JudgeGoal_NoRunner(t *testing.T) {
	j := &SubagentGoalJudge{}
	_, _, err := j.JudgeGoal(context.Background(), "s", "g", "r")
	if err == nil {
		t.Fatal("expected error when runner is nil")
	}
}

// ---- helpers.go ----

func TestGatewayVersion_Matches(t *testing.T) {
	if GatewayVersion() != gatewayVersion() {
		t.Errorf("GatewayVersion()=%q gatewayVersion()=%q", GatewayVersion(), gatewayVersion())
	}
}

// ---- session_manager: truncateUTF8Safe ----

func TestTruncateUTF8Safe(t *testing.T) {
	ascii := "hello world"
	if got := truncateUTF8Safe(ascii, 100); got != ascii {
		t.Errorf("short string truncated: %q", got)
	}
	if got := truncateUTF8Safe(ascii, 5); got != "hello" {
		t.Errorf("ascii truncation = %q, want hello", got)
	}
	uni := "ééééé" // 10 bytes
	got := truncateUTF8Safe(uni, 3)
	if !utf8.ValidString(got) || len(got) == 0 {
		t.Errorf("multi-byte truncation invalid: %q (%d bytes)", got, len(got))
	}
}

func ptrString(s string) *string {
	return &s
}

func helper_test_SetupTmpTodo(t *testing.T) *config.Config {
	tmpDir, err := os.MkdirTemp("", "agent-cov-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	return &config.Config{
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Workspace: tmpDir, Model: "m", MaxTokens: 4096, MaxToolIterations: 10}},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"testp": {Type: "openai", ProviderConfig: config.ProviderConfig{APIKey: "k", APIBase: "https://x"}},
			},
		},
	}
}

func newTestAgentLoop(t *testing.T) *AgentLoop {
	cfg := helper_test_SetupTmpTodo(t)
	return NewAgentLoop(cfg, bus.NewMessageBus())
}