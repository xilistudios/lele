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

func (p *optionsCaptureProvider) lastOptions(t *testing.T) map[string]interface{} {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.options) == 0 {
		t.Fatal("provider saw no call: the test proves nothing")
	}
	return p.options[len(p.options)-1]
}

func stampedSessionID(opts map[string]interface{}) (string, bool) {
	v, ok := opts[providers.OptWireSessionID]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	return s, true
}

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
	if _, ok := llmOptions[providers.OptWireSessionID]; ok {
		t.Fatalf("RunToolLoop mutated the caller's LLMOptions map: %#v", llmOptions)
	}
}

func TestRunToolLoopFallsBackToOwnerSessionKey(t *testing.T) {
	provider := &optionsCaptureProvider{}
	ownerSessionKey := "owner:native:chat:1"

	res, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:        provider,
		Model:           "test-model",
		MaxIterations:   2,
		OwnerSessionKey: ownerSessionKey,
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

func TestRunToolLoopNoSessionStampsUnkeyedBucket(t *testing.T) {
	t.Run("no keys stamps the shared unkeyed bucket", func(t *testing.T) {
		provider := &optionsCaptureProvider{}
		if _, err := RunToolLoop(context.Background(), ToolLoopConfig{
			Provider:      provider,
			Model:         "test-model",
			MaxIterations: 1,
		}, []providers.Message{{Role: "user", Content: "hi"}}, "cli", "direct"); err != nil {
			t.Fatalf("RunToolLoop: %v", err)
		}
		stamped, ok := stampedSessionID(provider.lastOptions(t))
		if !ok {
			t.Fatal("wire_session_id absent without a session: WithWireSessionID must always stamp")
		}
		if want := providers.UnkeyedSessionBucket; stamped != want {
			t.Fatalf("wire_session_id = %q, want the unkeyed bucket %q", stamped, want)
		}
	})

	t.Run("real opencode go transport accepts the unkeyed call", func(t *testing.T) {
		rec, baseURL := newOCGOToolsServer(t)
		cfg := leleconfig.DefaultConfig()
		registerToolsNamedProvider(cfg, "opencode_go", "opencode_go", baseURL+"/v1")
		provider, err := providers.CreateProviderForCandidate(cfg, "opencode_go")
		if err != nil {
			t.Fatalf("CreateProviderForCandidate: %v", err)
		}
		if _, err := RunToolLoop(context.Background(), ToolLoopConfig{
			Provider:      provider,
			Model:         "test-model",
			MaxIterations: 2,
		}, []providers.Message{{Role: "user", Content: "hi"}}, "cli", "direct"); err != nil {
			t.Fatalf("RunToolLoop without session keys failed: %v (unkeyed egress must work against OpenCode Go)", err)
		}
		if rec.count() == 0 {
			t.Fatal("recorder saw no request: the test proves nothing")
		}
		if h := rec.lastSessionHeader(t); h != providers.UnkeyedSessionBucket {
			t.Fatalf("x-opencode-session = %q, want the unkeyed bucket %q", h, providers.UnkeyedSessionBucket)
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
			SessionKey:    "native:anything",
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

	t.Run("empty session key stamps the unkeyed bucket", func(t *testing.T) {
		provider := &optionsCaptureProvider{}
		if _, ok := CompactLoopMessages(context.Background(), provider, "test-model", messages, 2, ""); !ok {
			t.Fatal("expected compaction to run")
		}
		stamped, present := stampedSessionID(provider.lastOptions(t))
		if !present {
			t.Fatal("wire_session_id absent from the compaction call: the stamp is unconditional (FIX-C)")
		}
		if want := providers.UnkeyedSessionBucket; stamped != want {
			t.Fatalf("wire_session_id = %q, want the unkeyed bucket %q", stamped, want)
		}
	})
}

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

	want := providers.WireSessionID("native:chat-1:task-1")
	stamped, ok := stampedSessionID(provider.lastOptions(t))
	if !ok {
		t.Fatal("wire_session_id absent on the async subagent path")
	}
	if stamped != want {
		t.Fatalf("wire_session_id = %q, want %q (child session)", stamped, want)
	}
}
