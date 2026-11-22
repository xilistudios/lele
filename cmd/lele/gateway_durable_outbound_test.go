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

var (
	_ bus.OutboundSpooler        = (*durable.Outbound)(nil)
	_ channels.OutboundCompleter = (*durable.Outbound)(nil)
	_ channels.PeerFlusher       = (*durable.Outbound)(nil)
)

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

type outboundFixture struct {
	do   *durableOutbound
	repo *store.SpoolRepo
	sent *countingDeliverer
	msg  *bus.MessageBus
}

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

func (f *outboundFixture) pendingOut(t *testing.T) int {
	t.Helper()
	return f.stats(t).PendingOutbound
}

func (f *outboundFixture) claimedOut(t *testing.T) int {
	t.Helper()
	return f.stats(t).ClaimedOutbound
}

func enqueueLeftover(t *testing.T, repo *store.SpoolRepo, msg bus.OutboundMessage) {
	t.Helper()

	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal outbound payload: %v", err)
	}
	if _, err := repo.Enqueue(store.SpoolOutbound, msg.Channel, msg.ChatID, msg.ChatID, "", string(payload)); err != nil {
		t.Fatalf("spool Enqueue: %v", err)
	}
}

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

	if got := sent.callCount(); got != 0 {
		t.Errorf("replay deliverer called %d times, want 0", got)
	}
}

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
	if got[0].SpoolID == 0 {
		t.Error("replayed message lost its spool id; the consumer cannot finish the row")
	}
	if left := f.pendingOut(t) + f.claimedOut(t); left != 0 {
		t.Errorf("rows left in the spool after the replay = %d, want 0", left)
	}
}

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

	if err := f.do.Shutdown(); err != nil {
		t.Errorf("second Shutdown = %v, want nil", err)
	}
}

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

func TestOutboundStartPumpIsSingleton(t *testing.T) {
	f := newOutboundFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f.do.StartPump(ctx)
	f.do.StartPump(ctx)

	enqueueLeftover(t, f.repo, bus.OutboundMessage{Channel: "telegram", ChatID: "11", Content: "pumped once"})
	waitForOutbound(t, 5*time.Second, func() bool { return f.sent.callCount() >= 1 },
		"the pump started by the first call is not working")

	if err := f.do.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	enqueueLeftover(t, f.repo, bus.OutboundMessage{Channel: "telegram", ChatID: "11", Content: "after shutdown"})
	time.Sleep(4 * durable.OutboundPumpInterval)
	if got := f.sent.callCount(); got != 1 {
		t.Errorf("deliverer called %d times, want 1 (a second pump is running after shutdown)", got)
	}
}

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

	outRow := bus.OutboundMessage{Channel: "telegram", ChatID: "1", Content: "reply in flight"}
	if !do.outbound.Enqueue(&outRow) {
		t.Fatal("outbound Enqueue reported the row was not persisted")
	}
	inRow := bus.InboundMessage{Channel: "telegram", ChatID: "1", Content: "turn in flight"}
	if !di.inbound.Enqueue(&inRow) {
		t.Fatal("inbound Enqueue reported the row was not persisted")
	}

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

	if idx := indexOfRegistrationOrder(); idx != nil {
		defer func() {
			o, i := idx["durable-outbound-shutdown"], idx["durable-inbound-shutdown"]
			switch {
			case o == 0 || i == 0:
				t.Errorf("gateway.go registration map incomplete: %v", idx)
			case o < i:
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
