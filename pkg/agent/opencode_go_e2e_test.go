// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

// End-to-end proof that every main-loop egress in pkg/agent stamps the
// conversation-scoped wire session id (providers.WireSessionID) and that the
// factory-built OpenCode Go transport turns it into the x-opencode-session
// header on the wire (T2 of the OpenCode Go support plan).
//
// Each positive test runs the REAL transport (CreateProviderForCandidate, not
// NewHTTPProvider — that is what applies the OpenCode Go detection) against an
// httptest server and applies the anti-hollowness guard: the header value must
// equal providers.WireSessionID(sessionKey) exactly, so a test cannot pass on
// "some header arrived". One negative control per file runs the same path
// through a plain openai provider and asserts the header is absent, so the
// positive tests cannot pass on "the handler sees everything".

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

// ocgoWireRecorder records the headers of every request it serves. Headers are
// CLONED inside the handler: reading any part of *http.Request after the
// handler returns races with the server's connection cleanup.
type ocgoWireRecorder struct {
	mu      sync.Mutex
	headers []http.Header
	paths   []string
	reply   func(w http.ResponseWriter, r *http.Request)
}

// newOCGOWireRecorder starts a server that records every request and answers
// with the given reply. Returns the recorder and the server's base URL.
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

// recorded returns copies of the recorded paths and x-opencode-session values.
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

// lastSessionHeader returns the header value of the most recent request.
func (rec *ocgoWireRecorder) lastSessionHeader(t *testing.T) string {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.headers) == 0 {
		t.Fatal("recorder saw no request: the test proves nothing")
	}
	return rec.headers[len(rec.headers)-1].Get("X-Opencode-Session")
}

// assertSessionHeader is the anti-hollowness guard: the wire value must be
// exactly the id derived from the session key.
func assertSessionHeader(t *testing.T, got, sessionKey string) {
	t.Helper()
	want := providers.WireSessionID(sessionKey)
	if got != want {
		t.Fatalf("x-opencode-session = %q, want %q", got, want)
	}
}

// ocgoSSECompletion answers the streaming transport (ChatIdle prefers it) with
// a single completion chunk and [DONE], the shape openai_compat parses.
func ocgoSSECompletion(content string) func(w http.ResponseWriter, _ *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\n", content)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

// registerNamedProvider wires a named provider entry in cfg (creating the
// Providers struct when the test harness left it nil).
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

// TestOpenCodeGo_MainLoopSendsSessionHeader drives the main-loop egress
// (llmCaller.call → buildLLMOptions) through the real OpenCode Go transport
// and proves: two consecutive turns of one conversation send the SAME id, and
// a rotated key (/new via nextConversationSessionKey) sends a DIFFERENT one.
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

	// Turn 1.
	if _, err := lc.call(opts); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	assertSessionHeader(t, rec.lastSessionHeader(t), base)

	// Turn 2, same conversation: identical id.
	if _, err := lc.call(opts); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	assertSessionHeader(t, rec.lastSessionHeader(t), base)

	// Rotation: the next conversation on the same base must change the id.
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

// TestOpenCodeGo_FallbackCandidateKeepsSessionHeader proves the stamp survives
// the fallback chain: every candidate provider is rebuilt from config per
// attempt, so each request (the failing first candidate included) must carry
// the same conversation id.
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

	// Minimal retries so the failing candidate hands over quickly.
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

// TestOpenCodeGo_GoalJudgeSendsSessionHeader covers the goal-judge egress
// (goal.go JudgeGoal).
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

// TestOpenCodeGo_SummarizeSendsSessionHeader covers the conversation
// summarizer egress (session_manager.go summarizeSessionCore).
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
	// summarizeSessionCore needs at least 3 materializable messages.
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

// TestOpenCodeGo_WithoutOpenCodeGoProviderSendsNoSessionHeader is the file's
// negative control: the same direct main-loop path with the same stamped
// options, but through a plain openai provider. The transport without a
// configured session header must send nothing — otherwise the positive tests
// could not distinguish "we stamp it and the transport sends it" from "the
// handler sees every header the client library adds" (D6).
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

// TestOpenCodeGo_IntraLoopCompactionSendsSessionHeader covers the main-loop
// intra-loop compaction egress (llm_runner.go:1138 → tools.CompactLoopMessages,
// the T2.5 call site that must pass the session key as a parameter). The
// compaction summary call is made with a private options map built inside
// CompactLoopMessages, so this test captures the options the call actually
// carries; that they become the x-opencode-session header on the wire is
// proven by the transport-level tests in this file (same option key, same
// factory-built provider).
func TestOpenCodeGo_IntraLoopCompactionSendsSessionHeader(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)
	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	sessionKey := "native:e2e-intra-compact"
	want := providers.WireSessionID(sessionKey)

	// ~100K chars per tool result (≈40K tokens): the intra-loop threshold is
	// 128000*75/100 = 96000 tokens, so a few iterations trigger compaction
	// (same recipe as TestRunLLMIteration_IntraLoopCompaction).
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
	// The regular iterations share buildLLMOptions — same stamp, one source.
	if regularStamp != want {
		t.Fatalf("regular iteration stamped %q, want %q", regularStamp, want)
	}
}
