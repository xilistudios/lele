// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

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

// The gateway cannot be started in a unit test (config, network, providers), so
// these tests cover the wiring step on its own: the exact function the gateway
// calls, against a real SQLite store and a real message bus. What they pin down
// is the two-sided contract of the gateway integration: with the flag off
// nothing is touched and every lifecycle call is a no-op, and with the flag on
// the spool is drained before consumption, pumped while live, and released at
// shutdown.

// ──────────────────────────────────────────────────────────────────────────────
// Fixtures
// ──────────────────────────────────────────────────────────────────────────────

// spoolStore opens a throwaway database and returns its spool repo. The handle
// is closed by the cleanup; tests never need it.
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

// durableTestConfig is the minimum config a real AgentLoop and a real channel
// Manager accept without any network access: a temp workspace, no provider
// keys, no channel enabled.
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

// spoolChannel is a Channel that is nothing but a BaseChannel, so a test can
// read the spooler field that Manager.SetInboundSpooler is supposed to fill.
type spoolChannel struct {
	*channels.BaseChannel
}

func (s *spoolChannel) Start(context.Context) error { return nil }
func (s *spoolChannel) Stop(context.Context) error  { return nil }

func (s *spoolChannel) Send(context.Context, bus.OutboundMessage) error { return nil }

// durableFixture is the whole wiring surface a test needs: real store, real bus,
// real agent loop, and a real channel manager holding one registered channel.
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

// pending counts the inbound rows still waiting to be replayed.
func (f *durableFixture) pending(t *testing.T) int {
	t.Helper()
	return f.stats(t).PendingInbound
}

// claimed counts the inbound rows handed to an instance and still in flight.
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

// recvInbound reads one inbound message off the bus, reporting false on
// timeout. It is the only way to observe what a replay actually published.
func recvInbound(t *testing.T, msgBus *bus.MessageBus, timeout time.Duration) (bus.InboundMessage, bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return msgBus.ConsumeInbound(ctx)
}

// ──────────────────────────────────────────────────────────────────────────────
// Flag off: the gateway must behave exactly as before this feature existed
// ──────────────────────────────────────────────────────────────────────────────

// TestSetupDurableInboundDisabledTouchesNothing is the regression guard for the
// default configuration: with the flag off the helper returns nil AND neither
// the agent loop nor the channels are given a spooler, so inbound keeps going
// straight to the bus exactly as it did before durability existed.
func TestSetupDurableInboundDisabledTouchesNothing(t *testing.T) {
	f := newDurableFixture(t, false)

	if f.di != nil {
		t.Fatalf("setupDurableInbound(enabled=false) = %p, want nil", f.di)
	}
	if f.ch.InboundSpooler != nil {
		t.Error("channel was given an inbound spooler while the feature is off")
	}
}

// TestSetupDurableInboundNeedsStoreAndBus pins the other off-switches: no
// persistence (a store-less gateway) or no bus means no durability, whatever
// the flag says.
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

// TestNilDurableInboundIsInert is what lets the gateway call the lifecycle
// methods unconditionally: a nil handle (feature off) must not panic anywhere.
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

// ──────────────────────────────────────────────────────────────────────────────
// Flag on: wiring
// ──────────────────────────────────────────────────────────────────────────────

// TestSetupDurableInboundEnabledWiresBothSides checks that one call reaches the
// producer side: the channel manager got a spooler, and the handle carries a
// live service with an identity of its own for the gateway to drain and pump.
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

// TestSetupDurableInboundAcceptsNilConsumers documents that the helper is safe
// to drive from a partially built gateway: a nil agent loop or manager must not
// panic, since both setters are plain field writes.
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

// ──────────────────────────────────────────────────────────────────────────────
// Flag on: drain, pump, shutdown
// ──────────────────────────────────────────────────────────────────────────────

// TestDurableInboundDrainReplaysPersistedRow is the crash scenario the feature
// exists for: a message was accepted by a channel, written to the spool, and
// handed back (the bus was full, or the process left its claims at shutdown) but
// never consumed. Drain must put it back on the bus and clear the row.
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
	// A live Enqueue claims its row, so the leftover of a previous process has
	// to be handed back before any pass can see it - exactly what Release does
	// when the bus refuses the publish, and what shutdown does for the rest.
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
	// The replay must carry the row identity, or the consumer cannot complete it.
	if got.SpoolID == 0 {
		t.Error("replayed message lost its spool id; the consumer cannot finish the row")
	}
	if got.DedupeID != msg.DedupeID {
		t.Errorf("replayed dedupe id = %q, want %q", got.DedupeID, msg.DedupeID)
	}
}

// TestDurableInboundDrainSkipsAlreadyProcessedRow covers the other half of the
// guarantee: a row whose answer was already produced must not run a second
// time. The ledger is the authority, so the row is dropped, not replayed.
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

// TestDurableInboundPumpRecoversPendingRow covers the steady-state path the
// gateway starts after Run: a row that never reached the bus (a full buffer at
// publish time) is handed back to pending by Release and picked up by the pump
// on its own, with no drain involved.
func TestDurableInboundPumpRecoversPendingRow(t *testing.T) {
	f := newDurableFixture(t, true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f.di.StartPump(ctx)

	msg := bus.InboundMessage{Channel: "telegram", ChatID: "7", Content: "refused by a full bus"}
	if !f.di.inbound.Enqueue(&msg) {
		t.Fatal("Enqueue reported the row was not persisted")
	}
	// What publishInbound does when the bus rejects the message.
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

// TestDurableInboundShutdownReleasesClaims pins what a self-restart depends on:
// rows this instance claimed but had not finished go back to the pending set, so
// the successor replays them at once instead of waiting out the stale-claim
// timeout. Live traffic is already claimed by Enqueue, which is exactly the set
// the blanket release at shutdown has to hand over.
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

	// Idempotent: the restart path may drive the teardown twice.
	if err := f.di.Shutdown(); err != nil {
		t.Errorf("second Shutdown = %v, want nil", err)
	}
}

// TestDurableInboundShutdownStopsPump makes sure Shutdown really waits for the
// goroutine: once it returns, a new pending row must stay in the spool, because
// nothing is left to pump it.
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

	// Several pump intervals' worth of time, and the row must still be there.
	time.Sleep(4 * durable.PumpInterval)
	if got, ok := recvInbound(t, f.msgBus, 200*time.Millisecond); ok {
		t.Errorf("pump published %q after shutdown; its goroutine is still alive", got.Content)
	}
	if got := f.pending(t); got != 1 {
		t.Errorf("pending rows after shutdown = %d, want 1", got)
	}
}

// TestDurableInboundStartPumpIsSingleton documents the guard against two pumps
// racing over the same claims: a second start is ignored, not fatal, and the one
// pump still running is the one Shutdown stops.
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

	// A duplicate start that leaked a second goroutine would still be pumping
	// now, so a fresh row must survive untouched.
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

// TestDurableInboundDrainReclaimsStaleClaims is the restart handover: a row the
// previous process claimed and never released (a crash) is older than the stale
// timeout, so Drain must take it back and replay it.
func TestDurableInboundDrainReclaimsStaleClaims(t *testing.T) {
	f := newDurableFixture(t, true)

	msg := bus.InboundMessage{Channel: "telegram", ChatID: "13", Content: "orphaned by a crash"}
	if !f.di.inbound.Enqueue(&msg) {
		t.Fatal("Enqueue reported the row was not persisted")
	}

	// Hand the row back to pending and claim it as a different instance, in the
	// past, like a dead predecessor: a live Enqueue claims its own row, so the
	// orphan a crash leaves behind has to be staged through the pending set.
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

// TestDurableInboundPumpDoesNotReplayInFlightLiveMessage is the hard
// no-double-delivery guarantee: a message that a channel spooled and published
// normally, and whose turn is still in flight (nothing has been Finished), must
// never be published a second time by the pump.
//
// The mechanism that makes this true is the claim on the live row: Enqueue
// writes it already claimed by this instance, and SpoolRepo.ClaimBatch only
// selects rows whose claimed_by is empty, so the pump cannot see a message the
// agent loop is working on. Before that, a live row and a row left behind by a
// dead process were indistinguishable and any turn slower than one pump tick got
// re-published - the consumer's ledger could not absorb the copy either, because
// ShouldSkip only answers "already finished" and Finish had not run yet.
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

	// The channel path exactly as BaseChannel.publishInbound runs it.
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

	// The loop consumes it and starts a turn that takes longer than one pump
	// tick. Nothing is Finished: the row is still the only copy of the message.
	first, ok := recvInbound(t, f.msgBus, 2*time.Second)
	if !ok {
		t.Fatal("the live message never reached the bus")
	}
	if first.SpoolID != msg.SpoolID {
		t.Fatalf("live delivery spool id = %d, want %d", first.SpoolID, msg.SpoolID)
	}

	// Several pump ticks go by while the turn is still in flight. This sleep is
	// the test: a negative assertion needs the pump to have had every chance to
	// re-claim the row and must not be shortened.
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
