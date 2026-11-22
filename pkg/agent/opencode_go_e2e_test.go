package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

type ocgoWireRecorder struct {
	mu      sync.Mutex
	headers []http.Header
	paths   []string
	reply   func(w http.ResponseWriter, r *http.Request)
}

func newOCGOWireRecorder(t *testing.T, reply func(w http.ResponseWriter, r *http.Request)) (*ocgoWireRecorder, string) {
	t.Helper()
	rec := &ocgoWireRecorder{reply: reply}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	return rec, srv.URL
}

func (rec *ocgoWireRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	header := r.Header.Clone()
	path := r.URL.Path
	rec.mu.Lock()
	rec.headers = append(rec.headers, header)
	rec.paths = append(rec.paths, path)
	rec.mu.Unlock()
	rec.reply(w, r)
}

func (rec *ocgoWireRecorder) recorded() (paths, sessionIDs []string) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	paths = append(paths, rec.paths...)
	for _, h := range rec.headers {
		sessionIDs = append(sessionIDs, h.Get("X-Opencode-Session"))
	}
	return paths, sessionIDs
}

func (rec *ocgoWireRecorder) requestCount() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.headers)
}

func (rec *ocgoWireRecorder) lastSessionHeader(t *testing.T) string {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.headers) == 0 {
		t.Fatal("recorder saw no request: the test proves nothing")
	}
	return rec.headers[len(rec.headers)-1].Get("X-Opencode-Session")
}

func assertSessionHeader(t *testing.T, got, sessionKey string) {
	t.Helper()
	want := providers.WireSessionID(sessionKey)
	if got != want {
		t.Fatalf("x-opencode-session = %q, want %q", got, want)
	}
}

func ocgoSSECompletion(content string) func(w http.ResponseWriter, _ *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\n", content)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

func registerNamedProvider(cfg *config.Config, name, typ, apiBase string) {
	if cfg.Providers == nil {
		cfg.Providers = &config.ProvidersConfig{}
	}
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	cfg.Providers.Named[name] = config.NamedProviderConfig{
		Type:           typ,
		ProviderConfig: config.ProviderConfig{APIKey: "k", APIBase: apiBase},
		Models:         map[string]config.ProviderModelConfig{"test-model": {Model: "test-model", ContextWindow: 128000}},
	}
}

func TestOpenCodeGo_MainLoopSendsSessionHeader(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	rec, baseURL := newOCGOWireRecorder(t, ocgoSSECompletion("ok"))
	cfg := al.cfg()
	registerNamedProvider(cfg, "opencode_go", "opencode_go", baseURL+"/v1")

	provider, err := providers.CreateProviderForCandidate(cfg, "opencode_go")
	if err != nil {
		t.Fatalf("CreateProviderForCandidate: %v", err)
	}
	agent.Provider = provider

	lc := newLLMCaller(al)
	base := "native:e2e-main"
	opts := llmCallOptions{
		ctx:        t.Context(),
		agent:      agent,
		messages:   []providers.Message{{Role: "user", Content: "hi"}},
		model:      "test-model",
		sessionKey: base,
	}

	if _, err := lc.call(opts); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	assertSessionHeader(t, rec.lastSessionHeader(t), base)

	if _, err := lc.call(opts); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	assertSessionHeader(t, rec.lastSessionHeader(t), base)

	rotated := al.nextConversationSessionKey(base)
	if rotated == base {
		t.Fatal("test setup: rotation must yield a different session key")
	}
	opts.sessionKey = rotated
	if _, err := lc.call(opts); err != nil {
		t.Fatalf("rotated turn: %v", err)
	}
	rotatedHeader := rec.lastSessionHeader(t)
	assertSessionHeader(t, rotatedHeader, rotated)
	if rotatedHeader == providers.WireSessionID(base) {
		t.Fatalf("rotated conversation reused the old id %q", rotatedHeader)
	}
	if rec.requestCount() != 3 {
		t.Fatalf("recorder saw %d requests, want 3", rec.requestCount())
	}
}

func TestOpenCodeGo_FallbackCandidateKeepsSessionHeader(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	rec, baseURL := newOCGOWireRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/fail") {
			http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
			return
		}
		ocgoSSECompletion("fallback ok")(w, r)
	})
	cfg := al.cfg()
	registerNamedProvider(cfg, "prov-a", "opencode_go", baseURL+"/fail")
	registerNamedProvider(cfg, "prov-b", "opencode_go", baseURL+"/pass")

	al.fallback = providers.NewFallbackChain(providers.NewCooldownTracker()).
		WithRetryConfig(1, time.Millisecond)

	lc := newLLMCaller(al)
	sessionKey := "native:e2e-fallback"
	opts := llmCallOptions{
		ctx:      t.Context(),
		agent:    agent,
		messages: []providers.Message{{Role: "user", Content: "hi"}},
		model:    "prov-a:test-model",
		candidates: []providers.FallbackCandidate{
			{Provider: "prov-a", Model: "test-model"},
			{Provider: "prov-b", Model: "test-model"},
		},
		sessionKey: sessionKey,
	}

	resp, err := lc.call(opts)
	if err != nil {
		t.Fatalf("fallback call: %v", err)
	}
	if resp.Content != "fallback ok" {
		t.Fatalf("Content = %q, want %q (the fallback candidate must have answered)", resp.Content, "fallback ok")
	}

	paths, sessionIDs := rec.recorded()
	if len(sessionIDs) < 2 {
		t.Fatalf("recorder saw %d requests, want the failing candidate and the fallback", len(sessionIDs))
	}
	if !strings.HasSuffix(paths[0], "/fail/chat/completions") {
		t.Fatalf("first request path = %q, want the failing candidate first", paths[0])
	}
	if !strings.HasSuffix(paths[len(paths)-1], "/pass/chat/completions") {
		t.Fatalf("last request path = %q, want the fallback candidate last", paths[len(paths)-1])
	}
	want := providers.WireSessionID(sessionKey)
	for i, h := range sessionIDs {
		if h != want {
			t.Fatalf("request %d (%s): x-opencode-session = %q, want %q", i, paths[i], h, want)
		}
	}
}

func TestOpenCodeGo_GoalJudgeSendsSessionHeader(t *testing.T) {
	rec, baseURL := newOCGOWireRecorder(t, ocgoSSECompletion("CONTINUE"))

	cfg := config.DefaultConfig()
	if cfg.Providers == nil {
		cfg.Providers = &config.ProvidersConfig{}
	}
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	registerNamedProvider(cfg, "opencode_go", "opencode_go", baseURL+"/v1")
	provider, err := providers.CreateProviderForCandidate(cfg, "opencode_go")
	if err != nil {
		t.Fatalf("CreateProviderForCandidate: %v", err)
	}

	judge := NewSummaryGoalJudge(provider, "test-model", nil, nil)
	sessionKey := "native:e2e-goal"
	if _, _, err := judge.JudgeGoal(t.Context(), sessionKey, "finish the task", "the last reply"); err != nil {
		t.Fatalf("JudgeGoal: %v", err)
	}
	assertSessionHeader(t, rec.lastSessionHeader(t), sessionKey)
}

func TestOpenCodeGo_SummarizeSendsSessionHeader(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	rec, baseURL := newOCGOWireRecorder(t, ocgoSSECompletion("Summary: the conversation in one line."))
	cfg := al.cfg()
	registerNamedProvider(cfg, "opencode_go", "opencode_go", baseURL+"/v1")
	provider, err := providers.CreateProviderForCandidate(cfg, "opencode_go")
	if err != nil {
		t.Fatalf("CreateProviderForCandidate: %v", err)
	}
	agent.Provider = provider

	sm := newSessionManager(al)
	sessionKey := "native:e2e-summarize"
	for i := 0; i < 6; i++ {
		agent.Sessions.AddMessage(sessionKey, "user", fmt.Sprintf("Question %d about topic", i))
		agent.Sessions.AddMessage(sessionKey, "assistant", fmt.Sprintf("Answer %d with details", i))
	}

	stats, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		t.Fatalf("summarizeSessionWithError: %v", err)
	}
	if stats == nil {
		t.Fatal("expected stats from a successful summarization")
	}
	assertSessionHeader(t, rec.lastSessionHeader(t), sessionKey)
}

func TestOpenCodeGo_WithoutOpenCodeGoProviderSendsNoSessionHeader(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	rec, baseURL := newOCGOWireRecorder(t, ocgoSSECompletion("ok"))
	cfg := al.cfg()
	registerNamedProvider(cfg, "openai", "openai", baseURL+"/v1")
	provider, err := providers.CreateProviderForCandidate(cfg, "openai")
	if err != nil {
		t.Fatalf("CreateProviderForCandidate: %v", err)
	}
	agent.Provider = provider

	lc := newLLMCaller(al)
	opts := llmCallOptions{
		ctx:        t.Context(),
		agent:      agent,
		messages:   []providers.Message{{Role: "user", Content: "hi"}},
		model:      "test-model",
		sessionKey: "native:e2e-negative",
	}
	if _, err := lc.call(opts); err != nil {
		t.Fatalf("call: %v", err)
	}
	if rec.requestCount() == 0 {
		t.Fatal("recorder saw no request: the control proves nothing")
	}
	if h := rec.lastSessionHeader(t); h != "" {
		t.Fatalf("x-opencode-session = %q, want absent for a non-OpenCode-Go provider", h)
	}
}

func TestOpenCodeGo_IntraLoopCompactionSendsSessionHeader(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)
	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	sessionKey := "native:e2e-intra-compact"
	want := providers.WireSessionID(sessionKey)

	largeContent := strings.Repeat("X", 100000)

	summaryCalls := 0
	var summaryStamp string
	var summaryStampOK bool
	regularStamp := ""
	regularCalls := 0

	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, toolsDefs []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			if len(messages) == 1 && messages[0].Role == "user" &&
				strings.Contains(messages[0].Content, "summarizing the conversation history of an AI agent") {
				summaryCalls++
				summaryStamp, summaryStampOK = opts[providers.OptWireSessionID].(string)
				return &providers.LLMResponse{Content: "Summary of prior tool interactions"}, nil
			}
			regularCalls++
			v, _ := opts[providers.OptWireSessionID].(string)
			regularStamp = v
			if regularCalls <= 5 {
				return &providers.LLMResponse{
					ToolCalls: []providers.ToolCall{{
						ID:        fmt.Sprintf("call_%d", regularCalls),
						Type:      "function",
						Name:      "large_tool",
						Arguments: map[string]interface{}{},
					}},
				}, nil
			}
			return &providers.LLMResponse{Content: "Final response"}, nil
		},
	}

	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "large_tool",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			return tools.SilentResult(largeContent)
		},
	})

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Do work"},
	}
	opts := processOptions{
		SessionKey:   sessionKey,
		Channel:      "native",
		ChatID:       "e2e-compact-chat",
		SendResponse: false,
	}

	if _, _, err := runner.runLLMIteration(context.Background(), agent, messages, opts); err != nil {
		t.Fatalf("runLLMIteration: %v", err)
	}

	if summaryCalls == 0 {
		t.Fatal("intra-loop compaction never fired; the test proves nothing")
	}
	if !summaryStampOK || summaryStamp != want {
		t.Fatalf("compaction summary call stamped %q (present=%v), want %q", summaryStamp, summaryStampOK, want)
	}
	if regularStamp != want {
		t.Fatalf("regular iteration stamped %q, want %q", regularStamp, want)
	}
}
