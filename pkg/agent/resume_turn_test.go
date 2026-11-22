package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

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

func providerForAgent(al *AgentLoop, p providers.LLMProvider) *AgentInstance {
	agent := al.registry.GetDefaultAgent()
	agent.Provider = p
	return agent
}

func countRole(messages []providers.Message, role, substr string) int {
	n := 0
	for _, m := range messages {
		if m.Role == role && strings.Contains(m.Content, substr) {
			n++
		}
	}
	return n
}

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

	messages, _ := provider.last()
	if original := countRole(messages, "user", "pregunta original"); original != 1 {
		t.Fatalf("original user message appears %d times in the resumed request, want 1", original)
	}
}

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
	if users := countRole(messages, "user", "corre dos tools"); users != 1 {
		t.Fatalf("user message appears %d times, want 1", users)
	}
}

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

	messages, _ := provider.last()
	if countRole(messages, "user", "mensaje nuevo") == 0 {
		t.Fatal("fresh turn did not carry its own user message")
	}
	if m, ok := al.getTurnMarker("telegram:123"); ok && m.DedupeID == "X" {
		t.Fatalf("stale marker survived the fresh turn: %+v", m)
	}
}

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

	if provider.count() == 0 {
		t.Fatal("resume with notice already sent never reached the provider")
	}
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

	select {
	case <-blocked.started:
	case <-time.After(5 * time.Second):
		t.Fatal("resume never reached the provider")
	}
	if m, mk := al.getTurnMarker("telegram:123"); !mk || !m.ResumeNoticeSent {
		t.Fatalf("ResumeNoticeSent not persisted while the resumed turn is in flight: %+v ok=%v", m, mk)
	}

	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelDrain()
	_ = al.Shutdown(drainCtx)
	cancelRun()
	<-done
}

func addLiveSubagent(t *testing.T, al *AgentLoop, id, label, task, spawnerKey string) {
	t.Helper()
	sm, ok := al.GetSubagents()["main"]
	if !ok {
		t.Fatal("no subagent manager for agent main")
	}
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sm.AddTaskForTest(&tools.SubagentTask{
		ID:                id,
		Label:             label,
		Task:              task,
		Status:            tools.SubagentStatusRunning,
		SpawnerSessionKey: spawnerKey,
		Created:           time.Now().UnixMilli(),
		Updated:           time.Now().UnixMilli(),
	}, func() { cancel() })
}

func turnSubsKV(t *testing.T, al *AgentLoop, sessionKey string) (string, bool) {
	t.Helper()
	raw, ok, err := al.sessionStateKV().Get(turnSubsKeyPrefix + sessionKey)
	if err != nil {
		t.Fatalf("kv get: %v", err)
	}
	return raw, ok
}

func TestResumeSubagents_SnapshotFiltersAndTruncates(t *testing.T) {
	al, _ := resumeTestLoop(t)
	resumeOn(t, al)

	long := strings.Repeat("ñ", 250)
	addLiveSubagent(t, al, "task-mine-1", "alfa", long, "telegram:123")
	addLiveSubagent(t, al, "task-mine-2", "", "tarea corta", "telegram:123")
	addLiveSubagent(t, al, "task-other", "ajeno", "de otra sesión", "discord:999")

	al.writeTurnSubagentSnapshot("telegram:123")

	subs := al.readTurnSubagentSnapshot("telegram:123")
	if len(subs) != 2 {
		t.Fatalf("snapshot has %d entries, want 2 (foreign session must be excluded): %+v", len(subs), subs)
	}
	for _, s := range subs {
		if s.ID == "task-other" {
			t.Fatal("foreign-session task leaked into the snapshot")
		}
	}
	for _, s := range subs {
		if s.ID == "task-mine-1" {
			r := []rune(s.Task)
			if len(r) != maxSnapshotTaskRunes+1 || r[maxSnapshotTaskRunes] != '…' {
				t.Fatalf("task not rune-truncated to %d+ellipsis: %q", maxSnapshotTaskRunes, s.Task)
			}
		}
	}

	for i := 0; i < 12; i++ {
		addLiveSubagent(t, al, fmt.Sprintf("task-more-%02d", i), "", "x", "telegram:123")
	}
	al.writeTurnSubagentSnapshot("telegram:123")
	if subs := al.readTurnSubagentSnapshot("telegram:123"); len(subs) != maxSnapshottedSubagents {
		t.Fatalf("snapshot len = %d, want cap %d", len(subs), maxSnapshottedSubagents)
	}
}

func TestResumeSubagents_EmptySnapshotDeletesAndGate(t *testing.T) {
	al, _ := resumeTestLoop(t)
	resumeOn(t, al)
	addLiveSubagent(t, al, "task-a", "A", "tarea", "telegram:123")
	al.writeTurnSubagentSnapshot("telegram:123")
	if _, ok := turnSubsKV(t, al, "telegram:123"); !ok {
		t.Fatal("snapshot row missing after writing one task")
	}

	sm := al.GetSubagents()["main"]
	if !sm.StopTask("task-a") {
		t.Fatal("StopTask: task not found")
	}
	al.writeTurnSubagentSnapshot("telegram:123")
	if _, ok := turnSubsKV(t, al, "telegram:123"); ok {
		t.Fatal("snapshot row survived after the last task went terminal")
	}

	no := false
	cfg := al.cfg()
	cfg.Session.Resume = &no
	al.cfgPtr.Store(cfg)
	addLiveSubagent(t, al, "task-b", "B", "tarea", "telegram:123")
	al.writeTurnSubagentSnapshot("telegram:123")
	if _, ok := turnSubsKV(t, al, "telegram:123"); ok {
		t.Fatal("snapshot written with resume disabled")
	}
}

func TestResumeSubagents_WarningInjectedOnce(t *testing.T) {
	provider := &resumeMockProvider{}
	al, _ := resumeTestLoop(t)
	agent := providerForAgent(al, provider)
	agent.Sessions.AddMessage("telegram:123", "user", "orquestra tareas")

	addLiveSubagent(t, al, "task-w1", "vigía", "mirar el faro", "telegram:123")
	addLiveSubagent(t, al, "task-w2", "", "sin etiqueta", "telegram:123")
	al.writeTurnSubagentSnapshot("telegram:123")

	seedMarker(t, al, TurnMarker{
		SessionKey: "telegram:123", DedupeID: "X", Phase: turnPhaseToolsRun, Iter: 1,
		Channel: "telegram", ChatID: "123", AgentID: "main",
	})

	if _, err := al.messageProcessor.processMessage(context.Background(), resumeMessage("X", "orquestra tareas")); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	messages, _ := provider.last()
	var warnings []providers.Message
	for _, m := range messages {
		if m.Role == "user" && strings.Contains(m.Content, "subagente(s) en curso") {
			warnings = append(warnings, m)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("provider saw %d interruption warnings, want exactly 1", len(warnings))
	}
	w := warnings[0].Content
	if !strings.Contains(w, "vigía: mirar el faro") {
		t.Errorf("warning lost the labelled task: %q", w)
	}
	if !strings.Contains(w, "task-w2: sin etiqueta") {
		t.Errorf("warning missing id-fallback for unlabeled task: %q", w)
	}
	idxWarn, idxUser := -1, -1
	for i, m := range messages {
		if m.Role == "user" {
			if strings.Contains(m.Content, "orquestra tareas") {
				idxUser = i
			}
			if strings.Contains(m.Content, "subagente(s) en curso") {
				idxWarn = i
			}
		}
	}
	if idxWarn < idxUser {
		t.Fatalf("warning (idx %d) must follow the original user message (idx %d)", idxWarn, idxUser)
	}

	if _, ok := turnSubsKV(t, al, "telegram:123"); ok {
		t.Fatal("snapshot row survived injection")
	}
}

func TestResumeSubagents_NoSnapshotNoWarning(t *testing.T) {
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
	messages, _ := provider.last()
	for _, m := range messages {
		if strings.Contains(m.Content, "subagente(s) en curso") {
			t.Fatalf("warning injected without a snapshot: %q", m.Content)
		}
	}
}

func TestResumeSubagents_FreshTurnClearsBothRows(t *testing.T) {
	provider := &resumeMockProvider{}
	al, _ := resumeTestLoop(t)
	providerForAgent(al, provider)
	addLiveSubagent(t, al, "task-c1", "vivo", "todavía corre", "telegram:123")

	if _, err := al.messageProcessor.processMessage(context.Background(), resumeMessage("D", "hola")); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	if _, ok := turnMarkerKV(t, al, "telegram:123"); ok {
		t.Fatal("marker survived a completed turn")
	}
	if _, ok := turnSubsKV(t, al, "telegram:123"); ok {
		t.Fatal("snapshot survived a completed turn")
	}
}

func TestResumeSubagents_CorruptSnapshotSurvived(t *testing.T) {
	provider := &resumeMockProvider{}
	al, _ := resumeTestLoop(t)
	agent := providerForAgent(al, provider)
	agent.Sessions.AddMessage("telegram:123", "user", "hola")
	seedMarker(t, al, TurnMarker{
		SessionKey: "telegram:123", DedupeID: "X", Phase: turnPhaseLLMWait,
		Channel: "telegram", ChatID: "123", AgentID: "main",
	})
	if err := al.sessionStateKV().Set(turnSubsKeyPrefix+"telegram:123", "{no es json"); err != nil {
		t.Fatalf("kv set: %v", err)
	}

	if _, err := al.messageProcessor.processMessage(context.Background(), resumeMessage("X", "hola")); err != nil {
		t.Fatalf("resume with corrupt snapshot must not error, got: %v", err)
	}
	if _, ok := turnSubsKV(t, al, "telegram:123"); ok {
		t.Fatal("corrupt snapshot row survived the read")
	}
	messages, _ := provider.last()
	for _, m := range messages {
		if strings.Contains(m.Content, "subagente(s) en curso") {
			t.Fatalf("corrupt snapshot produced a bogus warning: %q", m.Content)
		}
	}
}
