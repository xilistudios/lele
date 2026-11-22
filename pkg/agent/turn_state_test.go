package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

func resumeOn(t *testing.T, al *AgentLoop) {
	t.Helper()
	yes := true
	cfg := al.cfg()
	cfg.Session.Resume = &yes
	cfg.Session.DurableInbound = &yes
	al.cfgPtr.Store(cfg)
}

func turnMarkerKV(t *testing.T, al *AgentLoop, sessionKey string) (string, bool) {
	t.Helper()
	repo := al.sessionStateKV()
	if repo == nil {
		t.Fatal("expected a SQLite store in the test loop")
	}
	raw, ok, err := repo.Get(turnKeyPrefix + sessionKey)
	if err != nil {
		t.Fatalf("kv get: %v", err)
	}
	return raw, ok
}

func countTurnKeys(t *testing.T, al *AgentLoop) int {
	t.Helper()
	repo := al.sessionStateKV()
	if repo == nil {
		t.Fatal("expected a SQLite store in the test loop")
	}
	keys, err := repo.Keys(turnKeyPrefix)
	if err != nil {
		t.Fatalf("kv keys: %v", err)
	}
	return len(keys)
}

func TestTurnMarker_GateRequiresBothFlags(t *testing.T) {
	provider := &countingMockProvider{response: "x"}
	yes, no := true, false

	cases := []struct {
		name     string
		resume   *bool
		wireDur  bool
		wantOpen bool
	}{
		{"both on", &yes, true, true},
		{"resume off", &no, true, false},
		{"durable off (no durability wired)", &yes, false, false},
		{"both off", &no, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, _ := newDurableInboundTestLoop(t, provider)
			cfg := al.cfg()
			cfg.Session.Resume = tc.resume
			al.cfgPtr.Store(cfg)
			if tc.wireDur {
				al.SetInboundDurability(&fakeDurability{})
			}
			if got := al.turnMarkerEnabled(); got != tc.wantOpen {
				t.Errorf("turnMarkerEnabled() = %v, want %v", got, tc.wantOpen)
			}
		})
	}
}

func TestTurnMarker_WriteReadClearRoundTrip(t *testing.T) {
	provider := &countingMockProvider{response: "x"}
	al, _ := newDurableInboundTestLoop(t, provider)
	al.SetInboundDurability(&fakeDurability{})
	resumeOn(t, al)

	const sk = "telegram:42"
	al.writeTurnMarker(sk, turnPhaseInbound, 0, func(m *TurnMarker) {
		m.DedupeID = "dedupe-1"
		m.SpoolID = 9
		m.Channel = "telegram"
		m.ChatID = "42"
		m.MsgID = "mid-1"
		m.AgentID = "main"
		m.Model = "claude-test"
	})

	m, ok := al.getTurnMarker(sk)
	if !ok {
		t.Fatal("marker missing after write")
	}
	if m.Phase != turnPhaseInbound || m.DedupeID != "dedupe-1" || m.SpoolID != 9 ||
		m.Channel != "telegram" || m.ChatID != "42" || m.MsgID != "mid-1" ||
		m.AgentID != "main" || m.Model != "claude-test" || m.SessionKey != sk {
		t.Fatalf("marker lost fields: %+v", m)
	}
	if m.StartedAt.IsZero() || m.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not set: %+v", m)
	}

	al.clearTurnMarker(sk, "dedupe-1")
	if _, ok := al.getTurnMarker(sk); ok {
		t.Fatal("marker survived clear with matching dedupe")
	}
}

func TestTurnMarker_UpdateOnlyOutsideInbound(t *testing.T) {
	provider := &countingMockProvider{response: "x"}
	al, _ := newDurableInboundTestLoop(t, provider)
	al.SetInboundDurability(&fakeDurability{})
	resumeOn(t, al)

	const sk = "telegram:7"
	al.writeTurnMarker(sk, turnPhaseLLMWait, 3, nil)
	if _, ok := turnMarkerKV(t, al, sk); ok {
		t.Fatal("llm_wait created a marker with no inbound_start")
	}
	al.writeTurnMarker(sk, turnPhaseToolsRun, 3, nil)
	if _, ok := turnMarkerKV(t, al, sk); ok {
		t.Fatal("tools_running created a marker with no inbound_start")
	}

	al.writeTurnMarker(sk, turnPhaseInbound, 0, func(m *TurnMarker) { m.DedupeID = "d-keep" })
	al.writeTurnMarker(sk, turnPhaseLLMWait, 2, nil)

	m, ok := al.getTurnMarker(sk)
	if !ok {
		t.Fatal("marker missing after inbound_start + llm_wait")
	}
	if m.Phase != turnPhaseLLMWait || m.Iter != 2 {
		t.Fatalf("phase/iter = %q/%d, want %q/2", m.Phase, m.Iter, turnPhaseLLMWait)
	}
	if m.DedupeID != "d-keep" {
		t.Fatalf("update lost seeded dedupe: %+v", m)
	}
}

func TestTurnMarker_CorruptPayloadTreatedAsAbsent(t *testing.T) {
	provider := &countingMockProvider{response: "x"}
	al, _ := newDurableInboundTestLoop(t, provider)
	al.SetInboundDurability(&fakeDurability{})
	resumeOn(t, al)

	const sk = "telegram:8"
	repo := al.sessionStateKV()
	if err := repo.Set(turnKeyPrefix+sk, "{not json"); err != nil {
		t.Fatalf("seed corrupt marker: %v", err)
	}
	if _, ok := al.getTurnMarker(sk); ok {
		t.Fatal("corrupt marker reported as present")
	}
	if _, ok := turnMarkerKV(t, al, sk); ok {
		t.Fatal("corrupt marker row was not deleted")
	}
}

func TestClearTurnMarker_DedupeGuard(t *testing.T) {
	provider := &countingMockProvider{response: "x"}
	al, _ := newDurableInboundTestLoop(t, provider)
	al.SetInboundDurability(&fakeDurability{})
	resumeOn(t, al)

	const sk = "telegram:9"
	al.writeTurnMarker(sk, turnPhaseInbound, 0, func(m *TurnMarker) { m.DedupeID = "turn-A" })

	al.clearTurnMarker(sk, "turn-B")
	if _, ok := al.getTurnMarker(sk); !ok {
		t.Fatal("clear with foreign dedupe deleted the marker")
	}
	al.clearTurnMarker(sk, "turn-A")
	if _, ok := al.getTurnMarker(sk); ok {
		t.Fatal("clear with matching dedupe kept the marker")
	}
	al.writeTurnMarker(sk, turnPhaseInbound, 0, func(m *TurnMarker) { m.DedupeID = "turn-C" })
	al.clearTurnMarker(sk, "")
	if _, ok := al.getTurnMarker(sk); ok {
		t.Fatal("unconditional clear kept the marker")
	}
}

func TestSetResumeNoticeSent_PersistsFlag(t *testing.T) {
	provider := &countingMockProvider{response: "x"}
	al, _ := newDurableInboundTestLoop(t, provider)
	al.SetInboundDurability(&fakeDurability{})
	resumeOn(t, al)

	const sk = "telegram:10"
	al.writeTurnMarker(sk, turnPhaseInbound, 0, func(m *TurnMarker) { m.DedupeID = "d-notice" })
	al.setResumeNoticeSent(sk)

	m, ok := al.getTurnMarker(sk)
	if !ok {
		t.Fatal("marker vanished after setResumeNoticeSent")
	}
	if !m.ResumeNoticeSent {
		t.Fatal("ResumeNoticeSent not persisted")
	}
	if m.DedupeID != "d-notice" || m.Phase != turnPhaseInbound {
		t.Fatalf("notice flag write clobbered fields: %+v", m)
	}
}

func runTurnToCompletion(t *testing.T, al *AgentLoop, msgBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	done := make(chan error, 1)
	go func() { done <- al.Run(runCtx) }()

	msg := spooledMessage()
	if !msgBus.PublishInbound(msg) {
		t.Fatal("inbound publish rejected")
	}
	if !drainUntil(t, msgBus, 15*time.Second, "final answer") {
		t.Fatal("turn produced no response")
	}
	stopRun(t, cancelRun, done)
	return msg
}

func TestTurnMarker_FeatureOffWritesNoKeys(t *testing.T) {
	provider := &countingMockProvider{response: "final answer"}
	al, msgBus := newDurableInboundTestLoop(t, provider)
	al.SetInboundDurability(&fakeDurability{})

	runTurnToCompletion(t, al, msgBus)

	if n := countTurnKeys(t, al); n != 0 {
		t.Fatalf("resume off: %d sess:turn: keys written, want 0", n)
	}
}

func TestTurnMarker_CompletedTurnClearsMarker(t *testing.T) {
	provider := &countingMockProvider{response: "final answer"}
	al, msgBus := newDurableInboundTestLoop(t, provider)
	al.SetInboundDurability(&fakeDurability{})
	resumeOn(t, al)

	msg := runTurnToCompletion(t, al, msgBus)

	if _, ok := turnMarkerKV(t, al, msg.SessionKey); ok {
		t.Fatal("marker still present after a completed turn")
	}
}

func TestTurnMarker_CancelledTurnKeepsMarker(t *testing.T) {
	provider := &blockingMockProvider{started: make(chan struct{})}
	al, msgBus := newDurableInboundTestLoop(t, provider)
	al.SetInboundDurability(&fakeDurability{})
	resumeOn(t, al)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	done := make(chan error, 1)
	go func() { done <- al.Run(runCtx) }()

	msg := spooledMessage()
	if !msgBus.PublishInbound(msg) {
		t.Fatal("inbound publish rejected")
	}
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider never started the turn")
	}

	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelDrain()
	_ = al.Shutdown(drainCtx)
	cancelRun()
	<-done

	raw, ok := turnMarkerKV(t, al, msg.SessionKey)
	if !ok {
		t.Fatal("marker missing after a cancelled turn")
	}
	var m TurnMarker
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("marker unreadable: %v", err)
	}
	if m.Phase != turnPhaseLLMWait || m.DedupeID != msg.DedupeID || m.Iter != 1 {
		t.Fatalf("interrupted marker = %+v, want llm_wait/%s/iter1", m, msg.DedupeID)
	}
}

func TestTurnMarker_SurvivesStoreReopen(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)

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

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	al.SetInboundDurability(&fakeDurability{})
	if al.sessionStateKV() == nil {
		t.Fatal("expected SQLite store under the isolated config dir")
	}

	const sk = "telegram:re-open"
	al.writeTurnMarker(sk, turnPhaseInbound, 0, func(m *TurnMarker) { m.DedupeID = "d-reopen" })

	reopened, err := store.Open(dir + "/lele.db")
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	raw, ok, err := reopened.KV().Get(turnKeyPrefix + sk)
	if err != nil || !ok {
		t.Fatalf("marker not durable across reopen: ok=%v err=%v", ok, err)
	}
	var m TurnMarker
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m.DedupeID != "d-reopen" {
		t.Fatalf("marker payload wrong after reopen: %s", raw)
	}
}
