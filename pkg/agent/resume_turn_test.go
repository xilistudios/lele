// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

// Resume-branch tests (R-2.x): processMessage must route a durable replay
// whose DedupeID matches the checkpointed turn into ContinueTurn instead of a
// fresh turn, must keep the user message single, must honor the marker's
// model/agent, must publish the notice exactly once, and must clear the
// checkpoint when the resumed turn finally ends.

// resumeMockProvider captures every provider request (messages + model) so a
// test can assert exactly what the resumed turn sent upstream.
type resumeMockProvider struct {
	mu       sync.Mutex
	requests [][]providers.Message
	models   []string
	response *providers.LLMResponse
}

func (p *resumeMockProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := append([]providers.Message(nil), messages...)
	p.requests = append(p.requests, snapshot)
	p.models = append(p.models, model)
	if p.response != nil {
		resp := *p.response
		return &resp, nil
	}
	return &providers.LLMResponse{Content: "resumed answer", ToolCalls: []providers.ToolCall{}}, nil
}

func (p *resumeMockProvider) GetDefaultModel() string { return "mock-resume-model" }

func (p *resumeMockProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func (p *resumeMockProvider) last() ([]providers.Message, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return nil, ""
	}
	return p.requests[len(p.requests)-1], p.models[len(p.models)-1]
}

// resumeTestLoop builds a loop on an isolated config dir (so the SQLite KV
// and the shared session manager are real) with resume + durable inbound on
// and a fake durability wired, exactly the state the gateway produces when
// both flags are true.
func resumeTestLoop(t *testing.T) (*AgentLoop, *bus.MessageBus) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "agent-resume-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	yes := true
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
			Anthropic: config.ProviderConfig{APIKey: "test-key"},
		},
	}
	cfg.Session.Resume = &yes
	cfg.Session.DurableInbound = &yes

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)
	al.SetInboundDurability(&fakeDurability{})
	return al, msgBus
}

// resumeMessage is a replayed spool row: same identity as the original turn.
func resumeMessage(dedupe, content string) bus.InboundMessage {
	return bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user1",
		ChatID:     "123",
		Content:    content,
		SessionKey: "telegram:123",
		Metadata:   map[string]string{"message_id": "55"},
		SpoolID:    77,
		DedupeID:   dedupe,
	}
}

// seedMarker writes a checkpoint directly to the KV, standing in for the
// process that died mid-turn.
func seedMarker(t *testing.T, al *AgentLoop, m TurnMarker) {
	t.Helper()
	if m.SessionKey == "" {
		t.Fatal("seedMarker: empty SessionKey")
	}
	if m.StartedAt.IsZero() {
		m.StartedAt = time.Now()
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal marker: %v", err)
	}
	if err := al.sessionStateKV().Set(turnKeyPrefix+m.SessionKey, string(data)); err != nil {
		t.Fatalf("seed marker: %v", err)
	}
}

// providerForAgent swaps the default agent's provider for the test's.
func providerForAgent(al *AgentLoop, p providers.LLMProvider) *AgentInstance {
	agent := al.registry.GetDefaultAgent()
	agent.Provider = p
	return agent
}

// countRole counts messages with the given role whose content contains substr.
func countRole(messages []providers.Message, role, substr string) int {
	n := 0
	for _, m := range messages {
		if m.Role == role && strings.Contains(m.Content, substr) {
			n++
		}
	}
	return n
}

// ──────────────────────────────────────────────────────────────────────────────

// R-2.1: a replayed turn whose dedupe matches the marker resumes WITHOUT
// re-appending the user message: the provider sees the original user message
// exactly once.
func TestResume_MatchingDedupeResumesWithoutDuplicateUser(t *testing.T) {
	provider := &resumeMockProvider{}
	al, _ := resumeTestLoop(t)
	agent := providerForAgent(al, provider)

	agent.Sessions.AddMessage("telegram:123", "user", "hazme algo")
	seedMarker(t, al, TurnMarker{
		SessionKey: "telegram:123", DedupeID: "X", Phase: turnPhaseLLMWait, Iter: 1,
		Channel: "telegram", ChatID: "123", MsgID: "55", AgentID: "main",
	})

	if _, err := al.messageProcessor.processMessage(context.Background(), resumeMessage("X", "hazme algo")); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if provider.count() == 0 {
		t.Fatal("provider never called")
	}
	messages, _ := provider.last()
	if users := countRole(messages, "user", "hazme algo"); users != 1 {
		t.Fatalf("resumed request carries %d copies of the user message, want 1", users)
	}
}

// R-2.2: the model the interrupted turn resolved to is honored on resume.
func TestResume_MarkerModelUsed(t *testing.T) {
	provider := &resumeMockProvider{}
	al, _ := resumeTestLoop(t)
	agent := providerForAgent(al, provider)
	agent.Sessions.AddMessage("telegram:123", "user", "hola")
	seedMarker(t, al, TurnMarker{
		SessionKey: "telegram:123", DedupeID: "X", Phase: turnPhaseLLMWait,
		Channel: "telegram", ChatID: "123", AgentID: "main", Model: "test-model-x",
	})

	if _, err := al.messageProcessor.processMessage(context.Background(), resumeMessage("X", "hola")); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	_, model := provider.last()
	if !strings.Contains(model, "test-model-x") {
		t.Fatalf("resumed request model = %q, want test-model-x", model)
	}
}

// R-2.3: an agent that no longer exists must not break the resume; the
// default agent takes over.
func TestResume_UnknownAgentFallsBack(t *testing.T) {
	provider := &resumeMockProvider{}
	al, _ := resumeTestLoop(t)
	agent := providerForAgent(al, provider)
	agent.Sessions.AddMessage("telegram:123", "user", "hola")
	seedMarker(t, al, TurnMarker{
		SessionKey: "telegram:123", DedupeID: "X", Phase: turnPhaseLLMWait,
		Channel: "telegram", ChatID: "123", AgentID: "agente-que-ya-no-esta",
	})

	if _, err := al.messageProcessor.processMessage(context.Background(), resumeMessage("X", "hola")); err != nil {
		t.Fatalf("resume with dead agent must not error, got: %v", err)
	}
	if provider.count() == 0 {
		t.Fatal("fallback agent never reached the provider")
	}
}

// R-2.4: a turn interrupted while waiting on the provider resumes from the
// persisted history (the user message) and produces its final answer once,
// through the real Run pipeline.
func TestResume_MidLLMCompletesTurn(t *testing.T) {
	provider := &resumeMockProvider{}
	al, msgBus := resumeTestLoop(t)
	agent := providerForAgent(al, provider)
	agent.Sessions.AddMessage("telegram:123", "user", "pregunta original")
	seedMarker(t, al, TurnMarker{
		SessionKey: "telegram:123", DedupeID: "X", Phase: turnPhaseLLMWait, Iter: 2,
		Channel: "telegram", ChatID: "123", AgentID: "main",
	})

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	done := make(chan error, 1)
	go func() { done <- al.Run(runCtx) }()

	if !msgBus.PublishInbound(resumeMessage("X", "pregunta original")) {
		t.Fatal("inbound publish rejected")
	}
	if !drainUntil(t, msgBus, 30*time.Second, "resumed answer") {
		t.Fatal("resumed turn never published its answer")
	}
	stopRun(t, cancelRun, done)

	// The resumed request must carry the original user message exactly once
	// (the replay did not re-append it).
	messages, _ := provider.last()
	if original := countRole(messages, "user", "pregunta original"); original != 1 {
		t.Fatalf("original user message appears %d times in the resumed request, want 1", original)
	}
}

// R-2.5: a turn interrupted mid-tools resumes with the orphaned tool call
// healed: the provider request carries the real result for the finished call
// and the synthetic missing-result text for the interrupted one.
func TestResume_MidToolsHealsPendingCalls(t *testing.T) {
	provider := &resumeMockProvider{}
	al, _ := resumeTestLoop(t)
	agent := providerForAgent(al, provider)

	assistantWithCalls := providers.Message{
		Role:    "assistant",
		Content: "voy a ejecutar",
		ToolCalls: []providers.ToolCall{
			{ID: "call_A", Name: "exec", Arguments: map[string]interface{}{"command": "echo a"}},
			{ID: "call_B", Name: "exec", Arguments: map[string]interface{}{"command": "echo b"}},
		},
	}
	agent.Sessions.AddMessage("telegram:123", "user", "corre dos tools")
	agent.Sessions.AddFullMessage("telegram:123", assistantWithCalls)
	agent.Sessions.AddFullMessage("telegram:123", providers.Message{
		Role: "tool", ToolCallID: "call_A", Content: "resultado-de-A",
	})
	seedMarker(t, al, TurnMarker{
		SessionKey: "telegram:123", DedupeID: "X", Phase: turnPhaseToolsRun, Iter: 1,
		Channel: "telegram", ChatID: "123", AgentID: "main",
	})

	if _, err := al.messageProcessor.processMessage(context.Background(), resumeMessage("X", "corre dos tools")); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	messages, _ := provider.last()
	var sawA, sawBSynthetic bool
	for _, m := range messages {
		if m.Role == "tool" && m.ToolCallID == "call_A" && m.Content == "resultado-de-A" {
			sawA = true
		}
		if m.Role == "tool" && m.ToolCallID == "call_B" && strings.Contains(m.Content, "No recorded result for this tool call") {
			sawBSynthetic = true
		}
	}
	if !sawA {
		t.Error("resumed request lost the persisted result of call_A")
	}
	if !sawBSynthetic {
		t.Error("resumed request lacks the synthetic missing-result for interrupted call_B")
	}
	// The user message must not have been appended a second time.
	if users := countRole(messages, "user", "corre dos tools"); users != 1 {
		t.Fatalf("user message appears %d times, want 1", users)
	}
}

// R-2.6: a marker from an abandoned (different) turn must NOT trigger a
// resume; the new turn runs fresh and overwrites the checkpoint.
func TestResume_DifferentDedupeRunsFresh(t *testing.T) {
	provider := &resumeMockProvider{}
	al, _ := resumeTestLoop(t)
	agent := providerForAgent(al, provider)
	agent.Sessions.AddMessage("telegram:123", "user", "turno viejo")
	seedMarker(t, al, TurnMarker{
		SessionKey: "telegram:123", DedupeID: "X", Phase: turnPhaseLLMWait,
		Channel: "telegram", ChatID: "123", AgentID: "main",
	})

	if _, err := al.messageProcessor.processMessage(context.Background(), resumeMessage("Y", "mensaje nuevo")); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	// Fresh turn: the NEW user message reaches the provider...
	messages, _ := provider.last()
	if countRole(messages, "user", "mensaje nuevo") == 0 {
		t.Fatal("fresh turn did not carry its own user message")
	}
	// ...and the marker was overwritten by the fresh seed then cleared on
	// success, so the old dedupe is gone from the KV either way.
	if m, ok := al.getTurnMarker("telegram:123"); ok && m.DedupeID == "X" {
		t.Fatalf("stale marker survived the fresh turn: %+v", m)
	}
}

// R-2.7: a marker whose ResumeNoticeSent flag is set (the flag was persisted
// before a crash happened after/before the publish) must resume WITHOUT
// emitting the notice again - "lost notice beats duplicated notice" is the
// accepted trade, so the gate is the flag, never the publish result.
func TestResume_NoticeSentFlagSuppressesSecondNotice(t *testing.T) {
	provider := &resumeMockProvider{}
	al, msgBus := resumeTestLoop(t)
	agent := providerForAgent(al, provider)
	agent.Sessions.AddMessage("telegram:123", "user", "hola")
	seedMarker(t, al, TurnMarker{
		SessionKey: "telegram:123", DedupeID: "X", Phase: turnPhaseLLMWait,
		Channel: "telegram", ChatID: "123", AgentID: "main",
		ResumeNoticeSent: true,
	})

	if _, err := al.messageProcessor.processMessage(context.Background(), resumeMessage("X", "hola")); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	// The resume itself must have run (provider reached, answer produced)...
	if provider.count() == 0 {
		t.Fatal("resume with notice already sent never reached the provider")
	}
	// ...but no notice may appear in any published outbound message.
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		msg, ok := recvOutbound(t, msgBus, time.Until(deadline))
		if !ok {
			break
		}
		if strings.Contains(strings.ToLower(msg.Content), "reanudando") {
			t.Fatalf("notice re-published despite ResumeNoticeSent: %q", msg.Content)
		}
	}
}

// R-2.8: a successfully resumed turn clears its checkpoint, so a third
// replay of the same message cannot resume a finished conversation.
func TestResume_SuccessClearsMarker(t *testing.T) {
	provider := &resumeMockProvider{}
	al, _ := resumeTestLoop(t)
	agent := providerForAgent(al, provider)
	agent.Sessions.AddMessage("telegram:123", "user", "hola")
	seedMarker(t, al, TurnMarker{
		SessionKey: "telegram:123", DedupeID: "X", Phase: turnPhaseLLMWait,
		Channel: "telegram", ChatID: "123", AgentID: "main",
	})

	if _, err := al.messageProcessor.processMessage(context.Background(), resumeMessage("X", "hola")); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	if m, ok := al.getTurnMarker("telegram:123"); ok {
		t.Fatalf("marker survived a successful resume: %+v", m)
	}
	if _, ok := turnMarkerKV(t, al, "telegram:123"); ok {
		t.Fatal("marker KV row survived a successful resume")
	}
}

// R-2.7b: the ResumeNoticeSent flag must be durable BEFORE the resumed turn
// can progress (publish-first, then block in the provider proves ordering):
// otherwise a crash right after the notice would replay and duplicate it.
func TestResume_NoticeFlagPersistedBeforePublishReturns(t *testing.T) {
	blocked := &blockingMockProvider{started: make(chan struct{})}
	al, msgBus := resumeTestLoop(t)
	agent := providerForAgent(al, blocked)
	agent.Sessions.AddMessage("telegram:123", "user", "hola")
	agent.Sessions.Save("telegram:123")
	seedMarker(t, al, TurnMarker{
		SessionKey: "telegram:123", DedupeID: "X", Phase: turnPhaseLLMWait,
		Channel: "telegram", ChatID: "123", AgentID: "main",
	})

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	done := make(chan error, 1)
	go func() { done <- al.Run(runCtx) }()

	if !msgBus.PublishInbound(resumeMessage("X", "hola")) {
		t.Fatal("inbound publish rejected")
	}

	got, ok := recvOutbound(t, msgBus, 10*time.Second)
	if !ok || !strings.Contains(got.Content, "reanudando") {
		t.Fatalf("first resume did not publish the notice, got %+v (ok=%v)", got, ok)
	}
	if got.Channel != "telegram" || got.ChatID != "123" {
		t.Fatalf("notice routing = %q/%q, want telegram/123", got.Channel, got.ChatID)
	}

	// The turn is still in flight (provider blocked). If the flag is durable
	// now, it was written before the publish above.
	select {
	case <-blocked.started:
	case <-time.After(5 * time.Second):
		t.Fatal("resume never reached the provider")
	}
	if m, mk := al.getTurnMarker("telegram:123"); !mk || !m.ResumeNoticeSent {
		t.Fatalf("ResumeNoticeSent not persisted while the resumed turn is in flight: %+v ok=%v", m, mk)
	}

	// Teardown exactly like a crash: flag off, drain budget spent, turn dies
	// with the marker intact.
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelDrain()
	_ = al.Shutdown(drainCtx)
	cancelRun()
	<-done
}
