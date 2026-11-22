package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/durable"
	"github.com/xilistudios/lele/pkg/store"
)

func spoolStore(t *testing.T) *store.SpoolRepo {
	t.Helper()

	path := filepath.Join(t.TempDir(), "gateway-durable.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open(%q): %v", path, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("store.Close(): %v", err)
		}
	})
	return s.Spool()
}

func durableTestConfig(t *testing.T) *config.Config {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, Model: "test-model"},
		},
		Providers: &config.ProvidersConfig{},
	}
}

type spoolChannel struct {
	*channels.BaseChannel
}

func (s *spoolChannel) Start(context.Context) error { return nil }
func (s *spoolChannel) Stop(context.Context) error  { return nil }

func (s *spoolChannel) Send(context.Context, bus.OutboundMessage) error { return nil }

type durableFixture struct {
	di     *durableInbound
	msgBus *bus.MessageBus
	repo   *store.SpoolRepo
	ch     *spoolChannel
}

func newDurableFixture(t *testing.T, enabled bool) *durableFixture {
	t.Helper()

	cfg := durableTestConfig(t)
	repo := spoolStore(t)
	msgBus := bus.NewMessageBus()
	agentLoop := agent.NewAgentLoop(cfg, msgBus)

	manager, err := channels.NewManager(cfg, msgBus, agentLoop.GetProvidable(), channels.NewApprovalManager())
	if err != nil {
		t.Fatalf("channels.NewManager: %v", err)
	}
	ch := &spoolChannel{BaseChannel: channels.NewBaseChannel("telegram", nil, msgBus, nil)}
	manager.RegisterChannel("telegram", ch)

	return &durableFixture{
		di:     setupDurableInbound(enabled, repo, msgBus, agentLoop, manager),
		msgBus: msgBus,
		repo:   repo,
		ch:     ch,
	}
}

func (f *durableFixture) pending(t *testing.T) int {
	t.Helper()
	return f.stats(t).PendingInbound
}

func (f *durableFixture) claimed(t *testing.T) int {
	t.Helper()
	return f.stats(t).ClaimedInbound
}

func (f *durableFixture) stats(t *testing.T) store.SpoolStats {
	t.Helper()

	stats, err := f.repo.Stats()
	if err != nil {
		t.Fatalf("spool stats: %v", err)
	}
	return stats
}

func recvInbound(t *testing.T, msgBus *bus.MessageBus, timeout time.Duration) (bus.InboundMessage, bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return msgBus.ConsumeInbound(ctx)
}

func TestSetupDurableInboundDisabledTouchesNothing(t *testing.T) {
	f := newDurableFixture(t, false)

	if f.di != nil {
		t.Fatalf("setupDurableInbound(enabled=false) = %p, want nil", f.di)
	}
	if f.ch.InboundSpooler != nil {
		t.Error("channel was given an inbound spooler while the feature is off")
	}
}

func TestSetupDurableInboundNeedsStoreAndBus(t *testing.T) {
	cfg := durableTestConfig(t)
	msgBus := bus.NewMessageBus()
	agentLoop := agent.NewAgentLoop(cfg, msgBus)
	repo := spoolStore(t)

	manager, err := channels.NewManager(cfg, msgBus, agentLoop.GetProvidable(), channels.NewApprovalManager())
	if err != nil {
		t.Fatalf("channels.NewManager: %v", err)
	}
	ch := &spoolChannel{BaseChannel: channels.NewBaseChannel("telegram", nil, msgBus, nil)}
	manager.RegisterChannel("telegram", ch)

	if di := setupDurableInbound(true, nil, msgBus, agentLoop, manager); di != nil {
		t.Error("setupDurableInbound with a nil spool repo returned a handle, want nil")
	}
	if di := setupDurableInbound(true, repo, nil, agentLoop, manager); di != nil {
		t.Error("setupDurableInbound with a nil bus returned a handle, want nil")
	}
	if ch.InboundSpooler != nil {
		t.Error("a rejected setup still reached the channel")
	}
}

func TestNilDurableInboundIsInert(t *testing.T) {
	var di *durableInbound

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	di.Drain(ctx)
	di.StartPump(ctx)

	if err := di.Shutdown(); err != nil {
		t.Errorf("nil Shutdown() = %v, want nil", err)
	}
}

func TestSetupDurableInboundEnabledWiresBothSides(t *testing.T) {
	f := newDurableFixture(t, true)

	if f.di == nil {
		t.Fatal("setupDurableInbound(enabled=true) = nil, want a handle")
	}
	if f.di.inbound == nil {
		t.Fatal("handle carries no durable.Inbound")
	}
	if f.ch.InboundSpooler == nil {
		t.Error("channel got no inbound spooler although the feature is on")
	}
	if id := f.di.inbound.InstanceID(); id == "" {
		t.Error("durable inbound has no instance id; its claims would be untraceable")
	}
}

func TestSetupDurableInboundAcceptsNilConsumers(t *testing.T) {
	repo := spoolStore(t)
	msgBus := bus.NewMessageBus()

	di := setupDurableInbound(true, repo, msgBus, nil, nil)
	if di == nil {
		t.Fatal("setupDurableInbound returned nil with a real store and bus")
	}
	if err := di.Shutdown(); err != nil {
		t.Errorf("Shutdown on a handle whose pump never ran = %v, want nil", err)
	}
}

func TestDurableInboundDrainReplaysPersistedRow(t *testing.T) {
	f := newDurableFixture(t, true)

	msg := bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "42",
		ChatID:   "42",
		Content:  "left over from the previous process",
		Metadata: map[string]string{"message_id": "m-1"},
	}
	if !f.di.inbound.Enqueue(&msg) {
		t.Fatal("Enqueue reported the row was not persisted")
	}
	if msg.SpoolID == 0 || msg.DedupeID == "" {
		t.Fatalf("Enqueue tagged spool id %d / dedupe id %q, want both set", msg.SpoolID, msg.DedupeID)
	}
	if !f.di.inbound.Release(&msg) {
		t.Fatal("Release reported the row was not handed back")
	}
	if got := f.pending(t); got != 1 {
		t.Fatalf("pending rows after release = %d, want 1", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.di.Drain(ctx)

	if got := f.pending(t); got != 0 {
		t.Errorf("pending rows after drain = %d, want 0", got)
	}
	got, ok := recvInbound(t, f.msgBus, 2*time.Second)
	if !ok {
		t.Fatal("drain did not republish the persisted message")
	}
	if got.Content != msg.Content || got.Channel != "telegram" {
		t.Errorf("replayed message = %q on %q, want %q on telegram", got.Content, got.Channel, msg.Content)
	}
	if got.SpoolID == 0 {
		t.Error("replayed message lost its spool id; the consumer cannot finish the row")
	}
	if got.DedupeID != msg.DedupeID {
		t.Errorf("replayed dedupe id = %q, want %q", got.DedupeID, msg.DedupeID)
	}
}

func TestDurableInboundDrainSkipsAlreadyProcessedRow(t *testing.T) {
	f := newDurableFixture(t, true)

	msg := bus.InboundMessage{
		Channel: "telegram", ChatID: "5", Content: "answered before the crash",
		Metadata: map[string]string{"message_id": "m-done"},
	}
	if !f.di.inbound.Enqueue(&msg) {
		t.Fatal("Enqueue reported the row was not persisted")
	}
	f.di.inbound.Finish(msg)

	if got := f.pending(t); got != 0 {
		t.Fatalf("pending rows after Finish = %d, want 0", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.di.Drain(ctx)

	if _, ok := recvInbound(t, f.msgBus, 300*time.Millisecond); ok {
		t.Error("drain replayed a message the ledger says was already processed")
	}
}

func TestDurableInboundPumpRecoversPendingRow(t *testing.T) {
	f := newDurableFixture(t, true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f.di.StartPump(ctx)

	msg := bus.InboundMessage{Channel: "telegram", ChatID: "7", Content: "refused by a full bus"}
	if !f.di.inbound.Enqueue(&msg) {
		t.Fatal("Enqueue reported the row was not persisted")
	}
	if !f.di.inbound.Release(&msg) {
		t.Fatal("Release reported the row was not handed back")
	}

	got, ok := recvInbound(t, f.msgBus, 5*time.Second)
	if !ok {
		t.Fatal("pump never republished the pending row")
	}
	if got.Content != msg.Content {
		t.Errorf("pumped message = %q, want %q", got.Content, msg.Content)
	}
	if got.SpoolID != msg.SpoolID {
		t.Errorf("pumped spool id = %d, want %d", got.SpoolID, msg.SpoolID)
	}

	if err := f.di.Shutdown(); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestDurableInboundShutdownReleasesClaims(t *testing.T) {
	f := newDurableFixture(t, true)

	for _, content := range []string{"first", "second"} {
		msg := bus.InboundMessage{Channel: "telegram", ChatID: "9", Content: content}
		if !f.di.inbound.Enqueue(&msg) {
			t.Fatalf("Enqueue(%q) reported the row was not persisted", content)
		}
	}

	if got := f.claimed(t); got != 2 {
		t.Fatalf("claimed rows = %d, want 2 (live rows are born claimed)", got)
	}

	if err := f.di.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := f.claimed(t); got != 0 {
		t.Errorf("claimed rows after shutdown = %d, want 0", got)
	}
	if got := f.pending(t); got != 2 {
		t.Errorf("pending rows after shutdown = %d, want 2", got)
	}

	if err := f.di.Shutdown(); err != nil {
		t.Errorf("second Shutdown = %v, want nil", err)
	}
}

func TestDurableInboundShutdownStopsPump(t *testing.T) {
	f := newDurableFixture(t, true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f.di.StartPump(ctx)
	if err := f.di.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	msg := bus.InboundMessage{Channel: "telegram", ChatID: "3", Content: "arrived after shutdown"}
	if !f.di.inbound.Enqueue(&msg) {
		t.Fatal("Enqueue reported the row was not persisted")
	}
	if !f.di.inbound.Release(&msg) {
		t.Fatal("Release reported the row was not handed back")
	}

	time.Sleep(4 * durable.PumpInterval)
	if got, ok := recvInbound(t, f.msgBus, 200*time.Millisecond); ok {
		t.Errorf("pump published %q after shutdown; its goroutine is still alive", got.Content)
	}
	if got := f.pending(t); got != 1 {
		t.Errorf("pending rows after shutdown = %d, want 1", got)
	}
}

func TestDurableInboundStartPumpIsSingleton(t *testing.T) {
	f := newDurableFixture(t, true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f.di.StartPump(ctx)
	f.di.StartPump(ctx)

	msg := bus.InboundMessage{Channel: "telegram", ChatID: "11", Content: "pumped once"}
	if !f.di.inbound.Enqueue(&msg) {
		t.Fatal("Enqueue reported the row was not persisted")
	}
	if !f.di.inbound.Release(&msg) {
		t.Fatal("Release reported the row was not handed back")
	}
	if _, ok := recvInbound(t, f.msgBus, 5*time.Second); !ok {
		t.Fatal("the pump started by the first call is not working")
	}

	if err := f.di.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	again := bus.InboundMessage{Channel: "telegram", ChatID: "11", Content: "after shutdown"}
	if !f.di.inbound.Enqueue(&again) {
		t.Fatal("Enqueue reported the row was not persisted")
	}
	if !f.di.inbound.Release(&again) {
		t.Fatal("Release reported the row was not handed back")
	}
	time.Sleep(4 * durable.PumpInterval)
	if got, ok := recvInbound(t, f.msgBus, 200*time.Millisecond); ok {
		t.Errorf("a second pump published %q after shutdown", got.Content)
	}
}

func TestDurableInboundDrainReclaimsStaleClaims(t *testing.T) {
	f := newDurableFixture(t, true)

	msg := bus.InboundMessage{Channel: "telegram", ChatID: "13", Content: "orphaned by a crash"}
	if !f.di.inbound.Enqueue(&msg) {
		t.Fatal("Enqueue reported the row was not persisted")
	}

	if !f.di.inbound.Release(&msg) {
		t.Fatal("Release reported the row was not handed back")
	}
	old := time.Now().Add(-2 * durable.StaleClaimTimeout)
	items, err := f.repo.ClaimBatch(store.SpoolInbound, 10, "lele-dead-predecessor", old)
	if err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("claimed %d rows, want 1", len(items))
	}
	if got := f.pending(t); got != 0 {
		t.Fatalf("pending rows while claimed elsewhere = %d, want 0", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.di.Drain(ctx)

	got, ok := recvInbound(t, f.msgBus, 2*time.Second)
	if !ok {
		t.Fatal("drain never reclaimed and replayed the stale claim")
	}
	if got.Content != msg.Content {
		t.Errorf("replayed message = %q, want %q", got.Content, msg.Content)
	}
	if got := f.pending(t) + f.claimed(t); got != 0 {
		t.Errorf("rows left in the spool after the replay = %d, want 0", got)
	}
}

func TestDurableInboundPumpDoesNotReplayInFlightLiveMessage(t *testing.T) {
	f := newDurableFixture(t, true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.di.StartPump(ctx)
	defer func() {
		if err := f.di.Shutdown(); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	}()

	msg := bus.InboundMessage{
		Channel: "telegram", ChatID: "17", Content: "delivered live",
		Metadata: map[string]string{"message_id": "m-live"},
	}
	if !f.di.inbound.Enqueue(&msg) {
		t.Fatal("Enqueue reported the row was not persisted")
	}
	if !f.msgBus.PublishInbound(msg) {
		t.Fatal("live publish rejected")
	}

	first, ok := recvInbound(t, f.msgBus, 2*time.Second)
	if !ok {
		t.Fatal("the live message never reached the bus")
	}
	if first.SpoolID != msg.SpoolID {
		t.Fatalf("live delivery spool id = %d, want %d", first.SpoolID, msg.SpoolID)
	}

	time.Sleep(4 * durable.PumpInterval)
	second, dup := recvInbound(t, f.msgBus, time.Second)

	if dup {
		t.Fatalf("the pump re-published in-flight spool row %d (message %q): a live row must "+
			"stay invisible to ClaimBatch until Finish deletes it or Release hands it back",
			second.SpoolID, second.Content)
	}
	if got := f.claimed(t); got != 1 {
		t.Errorf("claimed rows while the turn is in flight = %d, want 1 (the live row)", got)
	}
	if got := f.pending(t); got != 0 {
		t.Errorf("pending rows while the turn is in flight = %d, want 0", got)
	}
}
