package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/durable"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
	"github.com/xilistudios/lele/pkg/tools"
)

type e2eScriptedProvider struct {
	mu      sync.Mutex
	calls   int
	blocked chan struct{}
}

func (p *e2eScriptedProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	blocked := p.blocked
	p.mu.Unlock()

	if n == 1 {
		return &providers.LLMResponse{
			Content: "primero ejecuto una herramienta",
			ToolCalls: []providers.ToolCall{
				{ID: "call_e2e_A", Name: "e2e_probe", Arguments: map[string]interface{}{"probe": "A"}},
			},
		}, nil
	}
	if blocked != nil {
		select {
		case <-blocked:
		default:
			close(blocked)
		}
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *e2eScriptedProvider) GetDefaultModel() string { return "mock-e2e-model" }

type answerProvider struct {
	mu       sync.Mutex
	requests [][]providers.Message
	final    string
}

func (p *answerProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	return &providers.LLMResponse{Content: p.final, ToolCalls: []providers.ToolCall{}}, nil
}

func (p *answerProvider) GetDefaultModel() string { return "mock-e2e-model" }

func (p *answerProvider) last() []providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return nil
	}
	return p.requests[len(p.requests)-1]
}

type e2eProbeTool struct{}

func (e2eProbeTool) Name() string        { return "e2e_probe" }
func (e2eProbeTool) Description() string { return "e2e probe" }
func (e2eProbeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (e2eProbeTool) Execute(context.Context, map[string]interface{}) *tools.ToolResult {
	return tools.NewToolResult("probe-A done")
}

func e2eLoop(t *testing.T, dir string, provider providers.LLMProvider) (*AgentLoop, *bus.MessageBus, *durable.Inbound) {
	t.Helper()

	yes := true
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         dir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: &config.ProvidersConfig{
			Anthropic: config.ProviderConfig{APIKey: "test-key"},
		},
	}
	cfg.Session.Resume = &yes
	cfg.Session.DurableInbound = &yes

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)
	if al.dbStore == nil {
		t.Fatal("expected SQLite store in the e2e loop")
	}
	di := durable.NewInbound(al.dbStore.Spool(), msgBus.PublishInbound)
	al.SetInboundDurability(di)
	agent := al.registry.GetDefaultAgent()
	agent.Provider = provider
	agent.Tools.Register(e2eProbeTool{})
	return al, msgBus, di
}

func collectOutbound(t *testing.T, msgBus *bus.MessageBus, within time.Duration) []bus.OutboundMessage {
	t.Helper()
	var out []bus.OutboundMessage
	deadline := time.Now().Add(within)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			t.Fatalf("turn.end never arrived after %v", within)
		}
		msg, ok := recvOutbound(t, msgBus, left)
		if !ok {
			t.Fatal("turn.end never arrived (bus closed)")
		}
		out = append(out, msg)
		if msg.Event == "turn.end" {
			return out
		}
	}
}

func countContains(msgs []bus.OutboundMessage, substr string) int {
	n := 0
	for _, m := range msgs {
		if strings.Contains(m.Content, substr) {
			n++
		}
	}
	return n
}

func waitForProviderCall(t *testing.T, started chan struct{}, within time.Duration) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(within):
		t.Fatal("second provider request never started")
	}
}

func crashProcessA(t *testing.T, al *AgentLoop, di *durable.Inbound, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelDrain()
	_ = al.Shutdown(drainCtx)
	cancel()
	<-done
	if _, err := di.ReleaseClaims(); err != nil {
		t.Fatalf("ReleaseClaims: %v", err)
	}
	al.Stop()
}

func TestResumeE2E_ReplayResumesInterruptedTurn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)

	providerA := &e2eScriptedProvider{blocked: make(chan struct{})}
	alA, _, diA := e2eLoop(t, dir, providerA)

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	doneA := make(chan error, 1)
	go func() { doneA <- alA.Run(ctxA) }()

	msg := bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user1",
		ChatID:     "e2e-chat",
		Content:    "haz la_probe y responde",
		SessionKey: "telegram:e2e-chat",
		Metadata:   map[string]string{"message_id": "e2e-msg-1"},
	}
	if !diA.Enqueue(&msg) {
		t.Fatal("Enqueue should spool the message (durability on)")
	}
	if !alA.bus.PublishInbound(msg) {
		t.Fatal("PublishInbound failed")
	}

	waitForProviderCall(t, providerA.blocked, 10*time.Second)

	marker, ok := alA.getTurnMarker("telegram:e2e-chat")
	if !ok {
		t.Fatal("expected turn marker while the turn is in flight")
	}
	if marker.DedupeID != msg.DedupeID {
		t.Fatalf("marker dedupe = %q, want %q", marker.DedupeID, msg.DedupeID)
	}

	crashProcessA(t, alA, diA, cancelA, doneA)

	{
		s, err := openStoreForE2E(t, dir)
		if err != nil {
			t.Fatalf("reopen store: %v", err)
		}
		defer s.Close()
		kv := s.KV()
		raw, found, err := kv.Get(turnKeyPrefix + "telegram:e2e-chat")
		if err != nil || !found {
			t.Fatalf("marker must survive process A: found=%v err=%v", found, err)
		}
		var m TurnMarker
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("marker JSON: %v", err)
		}
		if m.DedupeID != msg.DedupeID || m.Phase == turnPhaseInbound {
			t.Fatalf("marker advanced past inbound: %+v", m)
		}
	}

	providerB := &answerProvider{final: "respuesta final tras el resume"}
	alB, busB, diB := e2eLoop(t, dir, providerB)

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	doneB := make(chan error, 1)
	go func() { doneB <- alB.Run(ctxB) }()
	time.Sleep(50 * time.Millisecond)

	if n, err := diB.Drain(ctxB); err != nil || n != 1 {
		t.Fatalf("Drain: replayed=%d err=%v, want 1", n, err)
	}

	out := collectOutbound(t, busB, 20*time.Second)

	if n := countContains(out, "se reinició"); n != 1 {
		t.Fatalf("resume notice published %d times, want 1 (out=%+v)", n, out)
	}
	if n := countContains(out, "respuesta final tras el resume"); n != 1 {
		t.Fatalf("final answer published %d times, want 1 (out=%+v)", n, out)
	}

	req := providerB.last()
	if req == nil {
		t.Fatal("process B never reached the provider")
	}
	if got := countRole(req, "user", "la_probe"); got != 1 {
		t.Fatalf("resumed request has %d copies of the original user message, want 1 (history was re-appended)", got)
	}
	if got := countRole(req, "tool", ""); got < 1 {
		t.Fatalf("resumed request lost the persisted tool result: %+v", req)
	}

	if _, ok := alB.getTurnMarker("telegram:e2e-chat"); ok {
		t.Fatal("marker must be cleared after a successful resume")
	}
	if !waitFor(t, 5*time.Second, func() bool {
		st, err := alB.dbStore.Spool().Stats()
		return err == nil && st.PendingInbound == 0 && st.ClaimedInbound == 0
	}) {
		t.Fatal("spool row must be completed after a successful resume")
	}

	alB.Stop()
}

func openStoreForE2E(t *testing.T, dir string) (*store.Store, error) {
	t.Helper()
	return store.Open(filepath.Join(dir, "lele.db"))
}
