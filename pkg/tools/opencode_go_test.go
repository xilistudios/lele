// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

// T2 of the OpenCode Go support plan: every LLM egress in pkg/tools stamps the
// conversation-scoped wire session id (providers.WireSessionID).
//
// RunToolLoop is stamped from the fallback chain SessionKey → OwnerSessionKey
// (C7/D8): an async subagent sets SessionKey (subagent_runner.go), the
// synchronous in-context subagent sets only OwnerSessionKey (subagent_tool.go),
// and neither key means "no session" — the option is then ABSENT, never "".
// The tests below cover both real chains, the exported CompactLoopMessages
// helper (whose session key is a compile-enforced parameter, C6), the exact
// value, and the non-mutation of the caller-owned LLMOptions map.

package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	leleconfig "github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

// optionsCaptureProvider records the options of every Chat call (a copy: the
// loop-owned map must never be observed mutating under us) and answers with a
// direct completion so RunToolLoop exits after one iteration.
type optionsCaptureProvider struct {
	mu      sync.Mutex
	options []map[string]interface{}
}

func (p *optionsCaptureProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, options map[string]interface{}) (*providers.LLMResponse, error) {
	cp := make(map[string]interface{}, len(options))
	for k, v := range options {
		cp[k] = v
	}
	p.mu.Lock()
	p.options = append(p.options, cp)
	p.mu.Unlock()
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *optionsCaptureProvider) GetDefaultModel() string { return "test-model" }

// lastOptions returns the options of the most recent Chat call.
func (p *optionsCaptureProvider) lastOptions(t *testing.T) map[string]interface{} {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.options) == 0 {
		t.Fatal("provider saw no call: the test proves nothing")
	}
	return p.options[len(p.options)-1]
}

// stampedSessionID extracts the wire_session_id option; ok=false when the key
// is absent (the required shape when there is no session).
func stampedSessionID(opts map[string]interface{}) (string, bool) {
	v, ok := opts[providers.OptWireSessionID]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	return s, true
}

// ocgoToolsRecorder records x-opencode-session headers (copied inside the
// handler — post-handler request reads race) and answers with an SSE
// completion, the transport ChatIdle prefers.
type ocgoToolsRecorder struct {
	mu      sync.Mutex
	headers []http.Header
}

func newOCGOToolsServer(t *testing.T) (*ocgoToolsRecorder, string) {
	t.Helper()
	rec := &ocgoToolsRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Clone()
		rec.mu.Lock()
		rec.headers = append(rec.headers, h)
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return rec, srv.URL
}

// lastSessionHeader returns the session header of the most recent request.
func (rec *ocgoToolsRecorder) lastSessionHeader(t *testing.T) string {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.headers) == 0 {
		t.Fatal("recorder saw no request: the test proves nothing")
	}
	return rec.headers[len(rec.headers)-1].Get("X-Opencode-Session")
}

func (rec *ocgoToolsRecorder) count() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.headers)
}

// registerToolsNamedProvider wires a named provider entry in cfg.
func registerToolsNamedProvider(cfg *leleconfig.Config, name, typ, apiBase string) {
	if cfg.Providers == nil {
		cfg.Providers = &leleconfig.ProvidersConfig{}
	}
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = map[string]leleconfig.NamedProviderConfig{}
	}
	cfg.Providers.Named[name] = leleconfig.NamedProviderConfig{
		Type:           typ,
		ProviderConfig: leleconfig.ProviderConfig{APIKey: "k", APIBase: apiBase},
		Models: map[string]leleconfig.ProviderModelConfig{
			"test-model": {Model: "test-model", ContextWindow: 128000},
		},
	}
}

// TestRunToolLoopSendsSessionHeader runs the loop through the REAL factory
// OpenCode Go transport and applies the anti-hollowness guard: the wire value
// must equal providers.WireSessionID(sessionKey). This is also the async
// subagent shape (subagent_runner.go sets SessionKey). The caller-owned
// LLMOptions map must come back untouched (WithWireSessionID copies).
func TestRunToolLoopSendsSessionHeader(t *testing.T) {
	rec, baseURL := newOCGOToolsServer(t)
	cfg := leleconfig.DefaultConfig()
	registerToolsNamedProvider(cfg, "opencode_go", "opencode_go", baseURL+"/v1")
	provider, err := providers.CreateProviderForCandidate(cfg, "opencode_go")
	if err != nil {
		t.Fatalf("CreateProviderForCandidate: %v", err)
	}

	sessionKey := "native:subagent:chat:9"
	want := providers.WireSessionID(sessionKey)
	llmOptions := map[string]any{"max_tokens": 512}

	res, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      provider,
		Model:         "test-model",
		MaxIterations: 2,
		SessionKey:    sessionKey,
		LLMOptions:    llmOptions,
	}, []providers.Message{{Role: "user", Content: "hi"}}, "cli", "direct")
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if res.Content != "done" {
		t.Fatalf("Content = %q, want %q (the recorded completion)", res.Content, "done")
	}
	if rec.count() == 0 {
		t.Fatal("recorder saw no request: the test proves nothing")
	}
	if h := rec.lastSessionHeader(t); h != want {
		t.Fatalf("x-opencode-session = %q, want %q", h, want)
	}
	// Non-mutation: the map came from the caller and is shared with it.
	if _, ok := llmOptions[providers.OptWireSessionID]; ok {
		t.Fatalf("RunToolLoop mutated the caller's LLMOptions map: %#v", llmOptions)
	}
}

// TestRunToolLoopFallsBackToOwnerSessionKey is the synchronous-subagent shape
// (C7): subagent_tool.go passes ONLY OwnerSessionKey, never SessionKey, so the
// loop must resolve the stamp through the SessionKey → OwnerSessionKey
// fallback — otherwise every sync subagent would trip OpenCode Go's local
// ErrMissingWireSession / upstream 400.
func TestRunToolLoopFallsBackToOwnerSessionKey(t *testing.T) {
	provider := &optionsCaptureProvider{}
	ownerSessionKey := "owner:native:chat:1"

	res, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:        provider,
		Model:           "test-model",
		MaxIterations:   2,
		OwnerSessionKey: ownerSessionKey, // sync subagent: no SessionKey at all
	}, []providers.Message{{Role: "user", Content: "hi"}}, "cli", "direct")
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if res == nil {
		t.Fatal("expected a result")
	}

	stamped, ok := stampedSessionID(provider.lastOptions(t))
	if !ok {
		t.Fatal("wire_session_id absent although OwnerSessionKey is set (sync subagent would fail)")
	}
	if want := providers.WireSessionID(ownerSessionKey); stamped != want {
		t.Fatalf("wire_session_id = %q, want %q (owner fallback)", stamped, want)
	}
}

// TestRunToolLoopNoSessionNoHeader: neither SessionKey nor OwnerSessionKey ⇒
// the option is ABSENT (never ""), and the negative control shows the same
// machinery through a plain openai provider sends no header at all — so the
// positive test cannot pass on "the handler sees everything".
func TestRunToolLoopNoSessionNoHeader(t *testing.T) {
	t.Run("no keys leaves the option unstamped", func(t *testing.T) {
		provider := &optionsCaptureProvider{}
		if _, err := RunToolLoop(context.Background(), ToolLoopConfig{
			Provider:      provider,
			Model:         "test-model",
			MaxIterations: 1,
		}, []providers.Message{{Role: "user", Content: "hi"}}, "cli", "direct"); err != nil {
			t.Fatalf("RunToolLoop: %v", err)
		}
		if stamped, ok := stampedSessionID(provider.lastOptions(t)); ok {
			t.Fatalf("wire_session_id = %q, want the option absent without a session", stamped)
		}
	})

	t.Run("openai provider sends no session header", func(t *testing.T) {
		rec, baseURL := newOCGOToolsServer(t)
		cfg := leleconfig.DefaultConfig()
		registerToolsNamedProvider(cfg, "openai", "openai", baseURL+"/v1")
		provider, err := providers.CreateProviderForCandidate(cfg, "openai")
		if err != nil {
			t.Fatalf("CreateProviderForCandidate: %v", err)
		}
		if _, err := RunToolLoop(context.Background(), ToolLoopConfig{
			Provider:      provider,
			Model:         "test-model",
			MaxIterations: 1,
			SessionKey:    "native:anything", // stamped, but a non-OpenCode transport must not send it
		}, []providers.Message{{Role: "user", Content: "hi"}}, "cli", "direct"); err != nil {
			t.Fatalf("RunToolLoop: %v", err)
		}
		if rec.count() == 0 {
			t.Fatal("recorder saw no request: the control proves nothing")
		}
		if h := rec.lastSessionHeader(t); h != "" {
			t.Fatalf("x-opencode-session = %q, want absent for a non-OpenCode-Go provider", h)
		}
	})
}

// TestCompactLoopMessagesSendsSessionHeader covers the exported compaction
// helper (its own ChatIdle with a private options map, C6). The session key is
// a parameter, so the compiler forces every call site to decide what to pass.
func TestCompactLoopMessagesSendsSessionHeader(t *testing.T) {
	messages := []providers.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "question one"},
		{Role: "assistant", Content: "answer one"},
		{Role: "user", Content: "question two"},
		{Role: "assistant", Content: "answer two"},
		{Role: "user", Content: "question three"},
	}

	t.Run("stamps the session key", func(t *testing.T) {
		provider := &optionsCaptureProvider{}
		sessionKey := "native:compact:chat:4"
		if _, ok := CompactLoopMessages(context.Background(), provider, "test-model", messages, 2, sessionKey); !ok {
			t.Fatal("expected compaction to run")
		}
		stamped, present := stampedSessionID(provider.lastOptions(t))
		if !present {
			t.Fatal("wire_session_id absent from the compaction call")
		}
		if want := providers.WireSessionID(sessionKey); stamped != want {
			t.Fatalf("wire_session_id = %q, want %q", stamped, want)
		}
	})

	t.Run("empty session key leaves the option absent", func(t *testing.T) {
		provider := &optionsCaptureProvider{}
		if _, ok := CompactLoopMessages(context.Background(), provider, "test-model", messages, 2, ""); !ok {
			t.Fatal("expected compaction to run")
		}
		if stamped, present := stampedSessionID(provider.lastOptions(t)); present {
			t.Fatalf("wire_session_id = %q, want the option absent for an unknown session", stamped)
		}
	})
}

// TestSubagentTool_SyncPathStampsOwnerWireSession exercises the REAL
// synchronous subagent chain (subagent_tool.Execute → RunToolLoop): the tool
// reads the owner from the tool context and passes it as OwnerSessionKey only,
// so the stamp must come out as WireSessionID(ownerSessionKey).
func TestSubagentTool_SyncPathStampsOwnerWireSession(t *testing.T) {
	provider := &optionsCaptureProvider{}
	manager := NewSubagentManager(provider, "test-model", t.TempDir(), nil, 20)
	tool := NewSubagentTool(manager)
	tool.SetContext("cli", "direct")

	ownerSessionKey := "native:parent:chat:1"
	ctx := WithAgentToolContext(context.Background(), "agent-1", ownerSessionKey)
	result := tool.Execute(ctx, map[string]interface{}{"task": "do something"})
	if result == nil || result.IsError {
		t.Fatalf("expected successful sync subagent run, got: %+v", result)
	}

	stamped, ok := stampedSessionID(provider.lastOptions(t))
	if !ok {
		t.Fatal("wire_session_id absent on the sync subagent path")
	}
	if want := providers.WireSessionID(ownerSessionKey); stamped != want {
		t.Fatalf("wire_session_id = %q, want %q (owner session)", stamped, want)
	}
}

// TestSubagentRunner_AsyncPathStampsChildWireSession exercises the REAL async
// chain (SubagentManager.runTask → RunToolLoop): the runner derives the child
// session key as "<OriginSessionKey>:<TaskID>" and passes it as SessionKey, so
// the stamp must equal WireSessionID of that derived key.
func TestSubagentRunner_AsyncPathStampsChildWireSession(t *testing.T) {
	provider := &optionsCaptureProvider{}
	manager := NewSubagentManager(provider, "test-model", t.TempDir(), nil, 20)

	task := &SubagentTask{
		ID:               "task-1",
		Task:             "do something",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-1",
		OriginSessionKey: "native:chat-1",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()
	manager.AddTaskForTest(task, func() {})

	manager.runTask(context.Background(), task, nil)

	// The async runner derives sessionKey = OriginSessionKey + ":" + TaskID.
	want := providers.WireSessionID("native:chat-1:task-1")
	stamped, ok := stampedSessionID(provider.lastOptions(t))
	if !ok {
		t.Fatal("wire_session_id absent on the async subagent path")
	}
	if stamped != want {
		t.Fatalf("wire_session_id = %q, want %q (child session)", stamped, want)
	}
}
