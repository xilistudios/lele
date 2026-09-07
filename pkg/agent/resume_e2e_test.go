// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

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

// End-to-end resume tests: a REAL durable spool + REAL SQLite across two
// sequential AgentLoops sharing one config dir (LELE_CONFIG_DIR). Process A
// dies mid-turn (provider request outstanding, spool row unfinished,
// checkpoint present); process B boots, drains the spool, and the replayed
// message resumes the interrupted turn instead of re-running it.
//
// This is the full loop of the design: Enqueue stamps DedupeID before the
// bus ever sees the message, the crash leaves the row unfinished (the
// shutdown path in loop.go), Drain re-publishes with the identity intact,
// and the dedupe match in processMessage routes into ContinueTurn.

// e2eScriptedProvider answers iteration 1 with a tool call and blocks inside
// iteration 2 until its context dies - the exact moment a crash interrupts a
// live provider request.
type e2eScriptedProvider struct {
	mu      sync.Mutex
	calls   int
	blocked chan struct{} // closed on entry to call >= 2
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

// answerProvider is the second process's provider: it always answers with a
// fixed final text and records every request.
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

// e2eProbeTool is the fake tool the first iteration calls. It returns
// immediately so the turn reaches iteration 2 (the one that "crashes") with
// the assistant message and the tool result already persisted.
type e2eProbeTool struct{}

func (e2eProbeTool) Name() string        { return "e2e_probe" }
func (e2eProbeTool) Description() string { return "e2e probe" }
func (e2eProbeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (e2eProbeTool) Execute(context.Context, map[string]interface{}) *tools.ToolResult {
	return tools.NewToolResult("probe-A done")
}

// e2eLoop builds one "process": an AgentLoop over the shared config dir with
// resume+durable_inbound on, a real spool-backed durability wired, and the
// given provider installed on the default agent.
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

// collectOutbound drains the bus until turn.end, returning every message seen.
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

// waitForProviderCall blocks until the crashed process enters its second
// provider request (the one left outstanding at teardown).
func waitForProviderCall(t *testing.T, started chan struct{}, within time.Duration) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(within):
		t.Fatal("second provider request never started")
	}
}

// crashProcessA runs the teardown exactly like a gateway death mid-turn:
// running=false, drain budget spent, context cancelled, claims released for
// the successor, then a full Stop that joins the killed turn.
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

// TestResumeE2E_ReplayResumesInterruptedTurn walks the full two-process
// lifecycle on a real SQLite store: process A takes an inbound message,
// executes one tool call, and dies while the second provider request is
// outstanding. The spool row stays unfinished and the turn marker keeps the
// checkpoint. Process B boots on the same store, drains the spool, and the
// replayed message resumes the turn from history - the user message is NOT
// re-appended and the final answer is published exactly once.
func TestResumeE2E_ReplayResumesInterruptedTurn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)

	// ── Process A: crash mid-turn (iteration 2 provider request outstanding)
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

	// The turn must have checkpointed past the tool phase before dying.
	marker, ok := alA.getTurnMarker("telegram:e2e-chat")
	if !ok {
		t.Fatal("expected turn marker while the turn is in flight")
	}
	if marker.DedupeID != msg.DedupeID {
		t.Fatalf("marker dedupe = %q, want %q", marker.DedupeID, msg.DedupeID)
	}

	crashProcessA(t, alA, diA, cancelA, doneA)

	// The checkpoint survives the death of the process (KV is on SQLite).
	// Reopen the store to read it the way the successor will.
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

	// ── Process B: boot, drain, resume
	providerB := &answerProvider{final: "respuesta final tras el resume"}
	alB, busB, diB := e2eLoop(t, dir, providerB)

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	doneB := make(chan error, 1)
	go func() { doneB <- alB.Run(ctxB) }()
	// Give Run a moment to be consuming before the drain republishes.
	time.Sleep(50 * time.Millisecond)

	if n, err := diB.Drain(ctxB); err != nil || n != 1 {
		t.Fatalf("Drain: replayed=%d err=%v, want 1", n, err)
	}

	out := collectOutbound(t, busB, 20*time.Second)

	// The user is told once that the turn is being resumed...
	if n := countContains(out, "se reinició"); n != 1 {
		t.Fatalf("resume notice published %d times, want 1 (out=%+v)", n, out)
	}
	// ...and receives exactly one final answer.
	if n := countContains(out, "respuesta final tras el resume"); n != 1 {
		t.Fatalf("final answer published %d times, want 1 (out=%+v)", n, out)
	}

	// The resumed request must carry the ORIGINAL user message exactly once:
	// a fresh re-run (the bug this feature prevents) would append it again.
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

	// The cycle closes: marker gone, spool row completed.
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

// openStoreForE2E reopens the SQLite store of a "dead process" so a test can
// inspect what survived on disk (markers, spool rows, session history).
func openStoreForE2E(t *testing.T, dir string) (*store.Store, error) {
	t.Helper()
	return store.Open(filepath.Join(dir, "lele.db"))
}
