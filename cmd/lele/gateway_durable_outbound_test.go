// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/durable"
	"github.com/xilistudios/lele/pkg/store"
	"github.com/xilistudios/lele/pkg/update"
)

// The gateway cannot be started in a unit test (config, network, providers), so
// this file covers the outbound wiring step on its own, exactly as
// gateway_durable_test.go does for inbound: the real helper the gateway calls,
// against a real SQLite store, a real bus and a real channel manager. What it
// pins down is what only the gateway can get wrong - that a store-only gateway
// still has a spool for replies, which seams are wired, and in which order the
// two durable hooks hand their claims back.
//
// Shared with gateway_durable_test.go (same package): spoolStore,
// durableTestConfig.

// The contract setupDurableOutbound relies on, checked at compile time. If a
// seam ever stopped being satisfied by *durable.Outbound, the wiring in
// gateway.go could not even build; these lines say why.
var (
	_ bus.OutboundSpooler        = (*durable.Outbound)(nil)
	_ channels.OutboundCompleter = (*durable.Outbound)(nil)
	_ channels.PeerFlusher       = (*durable.Outbound)(nil)
)

// ──────────────────────────────────────────────────────────────────────────────
// Fixtures
// ──────────────────────────────────────────────────────────────────────────────

// countingDeliverer stands in for the gateway's deliver closure: it records what
// the replay layer asked to send and reports the outcome the test wants. Guarded
// by a mutex because the pump calls it from its own goroutine.
type countingDeliverer struct {
	mu    sync.Mutex
	calls []bus.OutboundMessage
	out   durable.Delivery
}

func newCountingDeliverer() *countingDeliverer {
	return &countingDeliverer{out: durable.Delivery{Delivered: true}}
}

func (c *countingDeliverer) deliver(_ context.Context, msg bus.OutboundMessage) durable.Delivery {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, msg)
	return c.out
}

func (c *countingDeliverer) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *countingDeliverer) delivered() []bus.OutboundMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]bus.OutboundMessage(nil), c.calls...)
}

// outboundFixture is the whole wiring surface a test needs: real store, real bus,
// real channel manager, and the handle the gateway's own setup helper returned.
type outboundFixture struct {
	do   *durableOutbound
	repo *store.SpoolRepo
	sent *countingDeliverer
	msg  *bus.MessageBus
}

// newOutboundFixture builds the gateway's outbound wiring. It takes no enabled
// parameter because every fixture here is the flag-on case; the off cases are
// exercised directly against setupDurableOutbound.
func newOutboundFixture(t *testing.T) *outboundFixture {
	t.Helper()

	cfg := durableTestConfig(t)
	repo := spoolStore(t)
	msgBus := bus.NewMessageBus()
	agentLoop := agent.NewAgentLoop(cfg, msgBus)

	manager, err := channels.NewManager(cfg, msgBus, agentLoop.GetProvidable(), channels.NewApprovalManager())
	if err != nil {
		t.Fatalf("channels.NewManager: %v", err)
	}

	sent := newCountingDeliverer()
	do := setupDurableOutbound(true, repo, msgBus, manager, sent.deliver)
	if do == nil {
		t.Fatal("setupDurableOutbound(enabled=true, real store/bus/manager) = nil")
	}

	return &outboundFixture{do: do, repo: repo, sent: sent, msg: msgBus}
}

func (f *outboundFixture) stats(t *testing.T) store.SpoolStats {
	t.Helper()

	stats, err := f.repo.Stats()
	if err != nil {
		t.Fatalf("spool stats: %v", err)
	}
	return stats
}

// pendingOut counts the reply rows still waiting to be delivered.
func (f *outboundFixture) pendingOut(t *testing.T) int {
	t.Helper()
	return f.stats(t).PendingOutbound
}

// claimedOut counts the reply rows handed to an instance and still in flight.
func (f *outboundFixture) claimedOut(t *testing.T) int {
	t.Helper()
	return f.stats(t).ClaimedOutbound
}

// enqueueLeftover writes a pending, UNCLAIMED outbound row straight through the
// repo, as a previous process would have left it. The live path
// (Outbound.Enqueue) always claims its own row, so anything staged this way is
// visible to every replay pass.
func enqueueLeftover(t *testing.T, repo *store.SpoolRepo, msg bus.OutboundMessage) {
	t.Helper()

	// SpoolID is json:"-", so marshalling here cannot bake a stale row id in.
	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal outbound payload: %v", err)
	}
	if _, err := repo.Enqueue(store.SpoolOutbound, msg.Channel, msg.ChatID, msg.ChatID, "", string(payload)); err != nil {
		t.Fatalf("spool Enqueue: %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// G1: the spool is available to outbound even with inbound durability off
// ──────────────────────────────────────────────────────────────────────────────

// TestSpoolCreatedForOutboundOnly is the regression guard for the premise this
// feature rests on. gateway.go builds spoolRepo out of the STORE alone
// (agentLoop.Store() != nil, then s.Spool()) and never gates it on
// session.durable_inbound, so a gateway that keeps inbound non-durable still has
// somewhere to write reply rows. A full gateway cannot be mounted in a unit test,
// so the test reproduces exactly those lines and asserts the spool is there and
// usable for outbound with no durable.Inbound anywhere in sight. If someone ever
// moves s.Spool() behind the inbound flag, this is where "durable outbound
// requested, silently nothing to write to" would begin.
func TestSpoolCreatedForOutboundOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbound-only.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open(%q): %v", path, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("store.Close(): %v", err)
		}
	})

	repo := s.Spool()
	if repo == nil {
		t.Fatal("store.Spool() = nil although inbound durability was never enabled; outbound would have nowhere to write")
	}

	enqueueLeftover(t, repo, bus.OutboundMessage{Channel: "telegram", ChatID: "1", Content: "inbound off, outbound on"})

	stats, err := repo.Stats()
	if err != nil {
		t.Fatalf("spool stats: %v", err)
	}
	if stats.PendingOutbound != 1 {
		t.Errorf("pending outbound rows = %d, want 1 (the spool serves outbound on its own)", stats.PendingOutbound)
	}
	if stats.PendingInbound != 0 {
		t.Errorf("pending inbound rows = %d, want 0", stats.PendingInbound)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// G2: the off-switches
// ──────────────────────────────────────────────────────────────────────────────

// TestSetupDurableOutboundNilGuards pins the conditions under which the helper
// must return nil: flag off, no store, no bus, no channel manager - plus the
// fifth case a half-wired gateway would otherwise panic on, a nil deliverer. A
// gateway in any of those states keeps its pre-durability behaviour byte for
// byte, and the nil handle it gets must still take every lifecycle call.
func TestSetupDurableOutboundNilGuards(t *testing.T) {
	cfg := durableTestConfig(t)
	repo := spoolStore(t)
	msgBus := bus.NewMessageBus()
	agentLoop := agent.NewAgentLoop(cfg, msgBus)

	manager, err := channels.NewManager(cfg, msgBus, agentLoop.GetProvidable(), channels.NewApprovalManager())
	if err != nil {
		t.Fatalf("channels.NewManager: %v", err)
	}
	deliver := newCountingDeliverer().deliver

	cases := []struct {
		name    string
		enabled bool
		spool   *store.SpoolRepo
		msgBus  *bus.MessageBus
		manager *channels.Manager
		deliver durable.DeliverFunc
	}{
		{"flag off", false, repo, msgBus, manager, deliver},
		{"no store", true, nil, msgBus, manager, deliver},
		{"no bus", true, repo, nil, manager, deliver},
		{"no channel manager", true, repo, msgBus, nil, deliver},
		{"no deliverer", true, repo, msgBus, manager, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			do := setupDurableOutbound(tc.enabled, tc.spool, tc.msgBus, tc.manager, tc.deliver)
			if do != nil {
				t.Errorf("setupDurableOutbound(%s) = %p, want nil", tc.name, do)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			do.Drain(ctx)
			do.StartPump(ctx)
			if err := do.Shutdown(); err != nil {
				t.Errorf("handle from %s: Shutdown() = %v, want nil", tc.name, err)
			}
		})
	}
}

// TestNilDurableOutboundIsInert is the same guard written out once for the type
// itself, so a regression in nil-receiver safety names the type and not a table
// row.
func TestNilDurableOutboundIsInert(t *testing.T) {
	var do *durableOutbound

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	do.Drain(ctx)
	do.StartPump(ctx)
	if err := do.Shutdown(); err != nil {
		t.Errorf("nil Shutdown() = %v, want nil", err)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Flag on: the seams are actually wired
// ──────────────────────────────────────────────────────────────────────────────

// TestSetupDurableOutboundWiresBusSeam proves the producer side behaviourally:
// after one call to setupDurableOutbound, a real PublishOutbound writes the reply
// row before handing the message to the outbound queue. Without
// msgBus.SetOutboundSpooler no row would exist and the feature would be a no-op
// that still logged "enabled".
func TestSetupDurableOutboundWiresBusSeam(t *testing.T) {
	f := newOutboundFixture(t)
	t.Cleanup(func() { f.msg.Close() })

	f.msg.PublishOutbound(bus.OutboundMessage{Channel: "telegram", ChatID: "42", Content: "a reply to persist"})

	if got := f.claimedOut(t); got != 1 {
		t.Fatalf("claimed outbound rows after one publish = %d, want 1 (bus spooler not wired)", got)
	}
	if got := f.pendingOut(t); got != 0 {
		t.Errorf("pending outbound rows = %d, want 0 (a live row is born claimed)", got)
	}
}

// TestSetupDurableOutboundWiresManagerSeam proves the consumer side, which the
// bus test above cannot see: the manager was handed the completer, so a
// dispatcher that cannot route a message hands the row back instead of leaving
// it claimed forever.
//
// The route chosen is a registered channel with no dispatch queue - what
// dispatchOutbound does there is release the row, and that call only exists if
// Manager.SetOutboundSpooler ran. With the seam missing, the row would still be
// claimed when this test finishes. The pump is deliberately never started, so
// nothing else can move the row.
func TestSetupDurableOutboundWiresManagerSeam(t *testing.T) {
	cfg := durableTestConfig(t)
	repo := spoolStore(t)
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	agentLoop := agent.NewAgentLoop(cfg, msgBus)

	manager, err := channels.NewManager(cfg, msgBus, agentLoop.GetProvidable(), channels.NewApprovalManager())
	if err != nil {
		t.Fatalf("channels.NewManager: %v", err)
	}
	// Registered by hand, so it has no dispatch queue but IS a known channel.
	ch := &spoolChannel{BaseChannel: channels.NewBaseChannel("telegram", nil, msgBus, nil)}
	manager.RegisterChannel("telegram", ch)

	sent := newCountingDeliverer()
	do := setupDurableOutbound(true, repo, msgBus, manager, sent.deliver)
	if do == nil {
		t.Fatal("setupDurableOutbound returned nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.StopAll(context.Background()); err != nil {
			t.Errorf("StopAll: %v", err)
		}
	})

	msg := bus.OutboundMessage{Channel: "telegram", ChatID: "5", Content: "reply the dispatcher cannot route"}
	if !do.outbound.Enqueue(&msg) {
		t.Fatal("Enqueue reported the row was not persisted")
	}
	if msg.SpoolID == 0 {
		t.Fatal("Enqueue did not tag the spool id")
	}
	msgBus.PublishOutbound(msg)

	waitForOutbound(t, 5*time.Second, func() bool {
		stats, err := repo.Stats()
		if err != nil {
			t.Errorf("spool stats: %v", err)
			return false
		}
		return stats.PendingOutbound == 1 && stats.ClaimedOutbound == 0
	}, "the dispatcher never handed the unroutable row back; Manager.SetOutboundSpooler did not run")

	// The replay deliverer must stay out of this: the pump was never started.
	if got := sent.callCount(); got != 0 {
		t.Errorf("replay deliverer called %d times, want 0", got)
	}
}

// waitForOutbound polls cond until it holds or the timeout expires. Same shape as
// pkg/durable's own helper, which a gateway test cannot reach (different
// package).
func waitForOutbound(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out after %s: %s", timeout, msg)
}

// ──────────────────────────────────────────────────────────────────────────────
// G3: drain, pump, and the shutdown release
// ──────────────────────────────────────────────────────────────────────────────

// TestOutboundDrainDeliversLeftoverRow covers the crash scenario through the
// gateway's own handle and closure: a reply a previous process spooled but never
// got out is replayed through the deliver function and the row is deleted.
func TestOutboundDrainDeliversLeftoverRow(t *testing.T) {
	f := newOutboundFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	enqueueLeftover(t, f.repo, bus.OutboundMessage{Channel: "telegram", ChatID: "7", Content: "undelivered before the crash"})
	if got := f.pendingOut(t); got != 1 {
		t.Fatalf("pending rows before drain = %d, want 1", got)
	}

	f.do.Drain(ctx)

	got := f.sent.delivered()
	if len(got) != 1 {
		t.Fatalf("deliverer called %d times, want 1", len(got))
	}
	if got[0].Content != "undelivered before the crash" {
		t.Errorf("replayed message = %q, want the leftover", got[0].Content)
	}
	// The row owns the message's identity: without the id the consumer
	// downstream cannot complete anything.
	if got[0].SpoolID == 0 {
		t.Error("replayed message lost its spool id; the consumer cannot finish the row")
	}
	if left := f.pendingOut(t) + f.claimedOut(t); left != 0 {
		t.Errorf("rows left in the spool after the replay = %d, want 0", left)
	}
}

// TestOutboundShutdownHookReleasesClaims pins what a self-restart depends on:
// rows this instance claimed and had not finished go back to the pending set, so
// the successor re-drains them at once instead of waiting out
// durable.StaleClaimTimeout. Live replies are exactly that set, because
// Outbound.Enqueue writes them born claimed.
//
// The pump is started on purpose: it must not touch those rows (ClaimBatch only
// selects unclaimed ones), and Shutdown must have stopped it before releasing,
// since a tick still in flight could re-claim what the release hands back.
func TestOutboundShutdownHookReleasesClaims(t *testing.T) {
	f := newOutboundFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, content := range []string{"first reply", "second reply"} {
		msg := bus.OutboundMessage{Channel: "telegram", ChatID: "42", Content: content}
		if !f.do.outbound.Enqueue(&msg) {
			t.Fatalf("Enqueue(%q) reported the row was not persisted", content)
		}
	}
	if got := f.claimedOut(t); got != 2 {
		t.Fatalf("claimed outbound rows = %d, want 2 (live rows are born claimed)", got)
	}

	f.do.StartPump(ctx)
	time.Sleep(4 * durable.OutboundPumpInterval)
	if got := f.sent.callCount(); got != 0 {
		t.Errorf("pump delivered %d messages, want 0 (a live row must stay invisible to ClaimBatch)", got)
	}

	if err := f.do.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := f.claimedOut(t); got != 0 {
		t.Errorf("claimed outbound rows after shutdown = %d, want 0", got)
	}
	if got := f.pendingOut(t); got != 2 {
		t.Errorf("pending outbound rows after shutdown = %d, want 2 (handed to the successor)", got)
	}

	// Idempotent: the restart path may drive the teardown twice.
	if err := f.do.Shutdown(); err != nil {
		t.Errorf("second Shutdown = %v, want nil", err)
	}
}

// TestOutboundShutdownStopsPump makes sure Shutdown really waits for the
// goroutine: once it returns, a new pending row must stay in the spool because
// nothing is left to pump it.
func TestOutboundShutdownStopsPump(t *testing.T) {
	f := newOutboundFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f.do.StartPump(ctx)
	if err := f.do.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	enqueueLeftover(t, f.repo, bus.OutboundMessage{Channel: "telegram", ChatID: "3", Content: "arrived after shutdown"})
	time.Sleep(4 * durable.OutboundPumpInterval)

	if got := f.sent.callCount(); got != 0 {
		t.Errorf("pump delivered %d messages after shutdown, want 0 (its goroutine is still alive)", got)
	}
	if got := f.pendingOut(t); got != 1 {
		t.Errorf("pending rows after shutdown = %d, want 1", got)
	}
}

// TestOutboundStartPumpIsSingleton documents the guard against two pumps racing
// over the same claims: a duplicate start is ignored, not fatal, and the one pump
// that is left running is the one Shutdown stops.
func TestOutboundStartPumpIsSingleton(t *testing.T) {
	f := newOutboundFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f.do.StartPump(ctx)
	f.do.StartPump(ctx)

	// The first start's pump must be the working one.
	enqueueLeftover(t, f.repo, bus.OutboundMessage{Channel: "telegram", ChatID: "11", Content: "pumped once"})
	waitForOutbound(t, 5*time.Second, func() bool { return f.sent.callCount() >= 1 },
		"the pump started by the first call is not working")

	if err := f.do.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// A duplicate start that leaked a second goroutine would still be pumping
	// now, so a fresh row must survive untouched.
	enqueueLeftover(t, f.repo, bus.OutboundMessage{Channel: "telegram", ChatID: "11", Content: "after shutdown"})
	time.Sleep(4 * durable.OutboundPumpInterval)
	if got := f.sent.callCount(); got != 1 {
		t.Errorf("deliverer called %d times, want 1 (a second pump is running after shutdown)", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// G4: the shutdown hooks run outbound before inbound
// ──────────────────────────────────────────────────────────────────────────────

// TestHookOrderOutboundBeforeInbound pins the LIFO result of the registrations in
// runGateway. The coordinator is public API and the gateway's own hook list is
// not extractable into a unit test, so the test replays the exact registration
// sequence of gateway.go against a recording hook and asserts the effective
// order. If a future edit reorders the two durable hooks, the double-claim window
// that ordering exists to bound silently widens to StaleClaimTimeout - which is
// why this is a test and not only a comment.
//
// The two hooks are not stubbed here: they call the real Shutdown of real
// handles, so the test also proves the release order is observable in the spool,
// not just in the log.
func TestHookOrderOutboundBeforeInbound(t *testing.T) {
	cfg := durableTestConfig(t)
	repo := spoolStore(t)
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	agentLoop := agent.NewAgentLoop(cfg, msgBus)

	manager, err := channels.NewManager(cfg, msgBus, agentLoop.GetProvidable(), channels.NewApprovalManager())
	if err != nil {
		t.Fatalf("channels.NewManager: %v", err)
	}

	do := setupDurableOutbound(true, repo, msgBus, manager, newCountingDeliverer().deliver)
	if do == nil {
		t.Fatal("setupDurableOutbound returned nil")
	}
	di := setupDurableInbound(true, repo, msgBus, agentLoop, manager)
	if di == nil {
		t.Fatal("setupDurableInbound returned nil")
	}

	// One claimed row per direction, both owned by their own instance: what a
	// graceful shutdown has to hand back.
	outRow := bus.OutboundMessage{Channel: "telegram", ChatID: "1", Content: "reply in flight"}
	if !do.outbound.Enqueue(&outRow) {
		t.Fatal("outbound Enqueue reported the row was not persisted")
	}
	inRow := bus.InboundMessage{Channel: "telegram", ChatID: "1", Content: "turn in flight"}
	if !di.inbound.Enqueue(&inRow) {
		t.Fatal("inbound Enqueue reported the row was not persisted")
	}

	// Registration order copied verbatim from runGateway: the coordinator runs
	// these back to front.
	var mu sync.Mutex
	var order []string
	record := func(name string, fn func() error) func(context.Context) error {
		return func(context.Context) error {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			if fn != nil {
				return fn()
			}
			return nil
		}
	}

	// The durable pair must keep their relative order here too: if someone
	// swaps the two registrations below but leaves the expectation stale, this
	// catches it before the assertions do.
	if idx := indexOfRegistrationOrder(); idx != nil {
		defer func() {
			o, i := idx["durable-outbound-shutdown"], idx["durable-inbound-shutdown"]
			switch {
			case o == 0 || i == 0:
				t.Errorf("gateway.go registration map incomplete: %v", idx)
			case o < i:
				// LIFO: the LAST registration runs FIRST, so outbound must be
				// registered after inbound to release before it.
				t.Errorf("gateway.go registers outbound (%d) BEFORE inbound (%d), "+
					"so LIFO would run inbound first - the opposite of what this "+
					"feature guarantees", o, i)
			}
		}()
	}

	coord := update.NewShutdownCoordinator(update.DefaultShutdownBudget)
	coord.Register("services-stop", time.Second, record("services-stop", nil))
	coord.Register("http-stop", time.Second, record("http-stop", nil))
	coord.Register("channels-stop", time.Second, record("channels-stop", nil))
	coord.Register("sessions-save", time.Second, record("sessions-save", nil))
	coord.Register("durable-inbound-shutdown", 3*time.Second, record("durable-inbound-shutdown", func() error {
		return di.Shutdown()
	}))
	coord.Register("durable-outbound-shutdown", 3*time.Second, record("durable-outbound-shutdown", func() error {
		return do.Shutdown()
	}))
	coord.Register("agent-drain", time.Second, record("agent-drain", nil))

	results := coord.RunAll(context.Background())
	for name, err := range results {
		if err != nil {
			t.Errorf("hook %q: %v", name, err)
		}
	}

	want := []string{
		"agent-drain",
		"durable-outbound-shutdown",
		"durable-inbound-shutdown",
		"sessions-save",
		"channels-stop",
		"http-stop",
		"services-stop",
	}
	if len(order) != len(want) {
		t.Fatalf("ran %d hooks (%v), want %d", len(order), order, len(want))
	}
	for i, name := range want {
		if order[i] != name {
			t.Fatalf("effective shutdown order = %v, want %v", order, want)
		}
	}

	// And the order did its job: the reply rows were already pending by the time
	// the inbound hook ran, which is what bounds the window in which a successor
	// could reclaim them behind our back.
	stats, err := repo.Stats()
	if err != nil {
		t.Fatalf("spool stats: %v", err)
	}
	if stats.ClaimedOutbound != 0 || stats.ClaimedInbound != 0 {
		t.Errorf("claims left after teardown = outbound %d / inbound %d, want 0 / 0",
			stats.ClaimedOutbound, stats.ClaimedInbound)
	}
	if stats.PendingOutbound != 1 {
		t.Errorf("pending outbound rows = %d, want 1", stats.PendingOutbound)
	}
}

// indexOfRegistrationOrder reads gateway.go and returns, for each shutdown hook
// name, its 1-based position in the source order of the coord.Register calls
// inside runGateway. It returns nil if the file cannot be read or a name is
// missing, which the caller treats as "guard not applicable".
//
// Why a test parses its own source file: the effective shutdown order is decided
// by WHERE a hook is registered, and that lives in a function no unit test can
// call. Asserting the relative order of the two durable registrations here means
// a future reorder cannot silently reverse the release order while the rest of
// the suite keeps passing.
func indexOfRegistrationOrder() map[string]int {
	data, err := os.ReadFile("gateway.go")
	if err != nil {
		return nil
	}
	want := map[string]int{
		"services-stop": 0, "http-stop": 0, "channels-stop": 0, "sessions-save": 0,
		"durable-inbound-shutdown": 0, "durable-outbound-shutdown": 0, "agent-drain": 0,
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "coord.Register(") {
			continue
		}
		n++
		for name := range want {
			if strings.Contains(line, "\""+name+"\",") {
				want[name] = n
			}
		}
	}
	for _, v := range want {
		if v == 0 {
			return nil
		}
	}
	return want
}
