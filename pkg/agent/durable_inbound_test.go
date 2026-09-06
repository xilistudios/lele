// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

// The durable inbound spool (pkg/durable) only buys exactly-once processing if
// the consumer completes a row for every turn it really ran, and leaves the row
// alone when the process was torn down mid-turn. These tests pin that consume
// side of the contract, i.e. what AgentLoop.Run owes the spool:
//
//	ShouldSkip true      -> the turn never runs, turn.end still fires
//	success              -> Finish exactly once, with the consumed identity
//	provider error       -> Finish (the user was told; a replay answers twice)
//	user /cancel         -> Finish (running is still true: the turn is over)
//	shutdown teardown    -> NO Finish (running is false: the row must replay)
//	durability not wired -> behaviour identical to before the feature
//
// Durability is injected through the narrow InboundDurability interface, so
// pkg/agent never imports pkg/durable and these tests need no database.

// ──────────────────────────────────────────────────────────────────────────────
// Test doubles
// ──────────────────────────────────────────────────────────────────────────────

// fakeDurability records every ShouldSkip/Finish call. The methods run on the
// message goroutine while the test goroutine reads the counters, so all state
// is mutex guarded.
//
// When bus is set, Finish also samples how deep the outbound queue is at the
// moment it runs. That sample is what proves the ordering rule: the answer must
// already be published before the spool row is completed.
type fakeDurability struct {
	mu        sync.Mutex
	skip      bool
	skipCalls int
	finished  []bus.InboundMessage

	bus               *bus.MessageBus
	pendingAtFinish   int64
	finishSampleTaken bool
}

func (f *fakeDurability) ShouldSkip(msg bus.InboundMessage) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skipCalls++
	return f.skip
}

func (f *fakeDurability) Finish(msg bus.InboundMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished = append(f.finished, msg)
	if f.bus != nil {
		_, _, _, outboundLen, _, _ := f.bus.Stats()
		f.pendingAtFinish = outboundLen
		f.finishSampleTaken = true
	}
}

func (f *fakeDurability) setSkip(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skip = v
}

// snapshot returns the recorded calls under the lock.
func (f *fakeDurability) snapshot() (skipCalls int, finished []bus.InboundMessage, pendingAtFinish int64, sampled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.skipCalls, append([]bus.InboundMessage(nil), f.finished...), f.pendingAtFinish, f.finishSampleTaken
}

// countingMockProvider answers with a fixed string and counts the calls, so a
// test can prove whether the turn really reached the provider.
type countingMockProvider struct {
	mu       sync.Mutex
	response string
	calls    int
}

func (m *countingMockProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return &providers.LLMResponse{Content: m.response, ToolCalls: []providers.ToolCall{}}, nil
}

func (m *countingMockProvider) GetDefaultModel() string { return "mock-counting-model" }

func (m *countingMockProvider) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// failingMockProvider always fails with a fixed error. The default error text is
// a request-format failure, which ClassifyError marks terminal: the fallback
// chain then gives up immediately instead of spending its whole exponential
// backoff budget on a test that only wants to see the error path.
type failingMockProvider struct {
	err error
}

func (m *failingMockProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return nil, errors.New("invalid request format: mock provider error for testing")
}

func (m *failingMockProvider) GetDefaultModel() string { return "mock-failing-model" }

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// newDurableInboundTestLoop builds an AgentLoop on a real bus the way the other
// loop tests do - same config shape, same provider injection - and isolates the
// SQLite store in a throwaway config dir so no test touches ~/.lele/lele.db.
func newDurableInboundTestLoop(t *testing.T, provider providers.LLMProvider) (*AgentLoop, *bus.MessageBus) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "agent-durable-inbound-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

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

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("registry returned no default agent")
	}
	agent.Provider = provider
	return al, msgBus
}

// spooledMessage is an inbound message as the channel hands it over after
// durable.Inbound.Enqueue: external channel, real session key, and the spool
// identity (SpoolID + DedupeID) the loop must echo back on Finish.
func spooledMessage() bus.InboundMessage {
	return bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user1",
		ChatID:     "123",
		Content:    "Hello",
		SessionKey: "telegram:123",
		Metadata:   map[string]string{"message_id": "55"},
		SpoolID:    77,
		DedupeID:   "dedupe-77",
	}
}

// waitFor polls cond until it holds or the budget is spent.
func waitFor(t *testing.T, within time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// stopRun cancels the root context and joins Run.
func stopRun(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent loop returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent loop did not stop")
	}
}

// drainUntil reads outbound events until turn.end arrives, reporting whether the
// content marker was seen before it. The bus is a single channel shared by every
// subscriber, so a test that has seen the answer can keep reading and still
// observe the deferred turn.end.
func drainUntil(t *testing.T, msgBus *bus.MessageBus, within time.Duration, marker string) (sawMarker bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			t.Fatalf("turn.end never arrived (marker %q seen: %v)", marker, sawMarker)
		}
		got, ok := recvOutbound(t, msgBus, left)
		if !ok {
			t.Fatalf("turn.end never arrived (marker %q seen: %v)", marker, sawMarker)
		}
		if got.Event == "turn.end" {
			return sawMarker
		}
		if marker != "" && strings.Contains(got.Content, marker) {
			sawMarker = true
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Tests
// ──────────────────────────────────────────────────────────────────────────────

// TestRun_DurableInbound_SkippedReplayRunsNoTurn covers the replay-dedupe path:
// the ledger says a previous process already answered, so the turn must not run
// again, while the channel still receives its terminal turn.end. That deferred
// signal intentionally keeps firing - handlers are idempotent, and suppressing
// it would reintroduce the "did a final message escape?" bookkeeping the signal
// exists to avoid.
func TestRun_DurableInbound_SkippedReplayRunsNoTurn(t *testing.T) {
	provider := &countingMockProvider{response: "must never run"}
	al, msgBus := newDurableInboundTestLoop(t, provider)

	dur := &fakeDurability{}
	dur.setSkip(true)
	al.SetInboundDurability(dur)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	done := make(chan error, 1)
	go func() { done <- al.Run(runCtx) }()

	if !msgBus.PublishInbound(spooledMessage()) {
		t.Fatal("inbound publish rejected")
	}

	msg, ok := recvOutbound(t, msgBus, 5*time.Second)
	if !ok {
		t.Fatal("skipped replay produced no outbound event: the channel would wait forever")
	}
	if msg.Event != "turn.end" {
		t.Fatalf("outbound = event %q content %q, want turn.end", msg.Event, msg.Content)
	}
	if msg.Channel != "telegram" || msg.ChatID != "123" {
		t.Fatalf("turn.end routing = %q/%q, want telegram/123", msg.Channel, msg.ChatID)
	}

	// Let a stray turn show itself before asserting the negatives.
	time.Sleep(200 * time.Millisecond)

	if calls := provider.count(); calls != 0 {
		t.Errorf("provider called %d times for a replay the ledger already covered, want 0", calls)
	}
	skipCalls, finished, _, _ := dur.snapshot()
	if skipCalls == 0 {
		t.Error("ShouldSkip was never consulted")
	}
	if len(finished) != 0 {
		t.Errorf("Finish called %d times for a skipped replay, want 0", len(finished))
	}

	stopRun(t, cancelRun, done)
}

// TestRun_DurableInbound_SuccessFinishesOnceWithIdentity pins the happy path:
// exactly one Finish carrying the very SpoolID/DedupeID that were consumed, and
// only after the answer was published. The queue-depth sample taken inside
// Finish proves that ordering: the test deliberately does not consume the bus
// until Finish has run, so a non-empty outbound queue at that instant can only
// be the response already on its way out.
func TestRun_DurableInbound_SuccessFinishesOnceWithIdentity(t *testing.T) {
	provider := &countingMockProvider{response: "final answer"}
	al, msgBus := newDurableInboundTestLoop(t, provider)

	dur := &fakeDurability{bus: msgBus}
	al.SetInboundDurability(dur)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	done := make(chan error, 1)
	go func() { done <- al.Run(runCtx) }()

	msg := spooledMessage()
	if !msgBus.PublishInbound(msg) {
		t.Fatal("inbound publish rejected")
	}

	// Nothing is consumed from the bus yet: Finish must land while the answer is
	// still sitting in the outbound queue.
	finishedEarly := waitFor(t, 10*time.Second, func() bool {
		_, finished, _, _ := dur.snapshot()
		return len(finished) > 0
	})
	if !finishedEarly {
		t.Fatal("Finish was never called for a completed turn")
	}

	skipCalls, finished, pending, sampled := dur.snapshot()
	if skipCalls == 0 {
		t.Error("ShouldSkip was never consulted")
	}
	if len(finished) != 1 {
		t.Fatalf("Finish called %d times, want exactly 1", len(finished))
	}
	got := finished[0]
	if got.SpoolID != msg.SpoolID || got.DedupeID != msg.DedupeID {
		t.Fatalf("Finish got spool_id=%d dedupe_id=%q, want %d/%q",
			got.SpoolID, got.DedupeID, msg.SpoolID, msg.DedupeID)
	}
	if got.Channel != msg.Channel || got.SessionKey != msg.SessionKey {
		t.Fatalf("Finish got routing %q/%q, want %q/%q",
			got.Channel, got.SessionKey, msg.Channel, msg.SessionKey)
	}
	if !sampled {
		t.Fatal("Finish did not sample the outbound queue")
	}
	if pending < 1 {
		t.Errorf("outbound queue was %d deep when Finish ran: the row was completed before the answer was published", pending)
	}

	// And the user-visible order is answer first, turn.end last.
	if !drainUntil(t, msgBus, 10*time.Second, "final answer") {
		t.Fatal("response was not published before turn.end")
	}

	stopRun(t, cancelRun, done)
}

// TestRun_DurableInbound_ProviderErrorFinishes covers the failure path: the loop
// answers "Error processing message..." instead, so the user has been told and
// the row must be completed - replaying the message after a restart would only
// confuse them a second time.
//
// The mock fails with a request-format error on purpose: ClassifyError marks it
// terminal, so the fallback chain gives up at once instead of burning its
// exponential backoff budget, which would only make the test slow, not wrong.
func TestRun_DurableInbound_ProviderErrorFinishes(t *testing.T) {
	provider := &failingMockProvider{}
	al, msgBus := newDurableInboundTestLoop(t, provider)

	dur := &fakeDurability{bus: msgBus}
	al.SetInboundDurability(dur)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	done := make(chan error, 1)
	go func() { done <- al.Run(runCtx) }()

	msg := spooledMessage()
	if !msgBus.PublishInbound(msg) {
		t.Fatal("inbound publish rejected")
	}

	if !drainUntil(t, msgBus, 60*time.Second, "Error processing message") {
		t.Fatal("provider failure never reached the user as an error response")
	}

	_, finished, _, _ := dur.snapshot()
	if len(finished) != 1 {
		t.Fatalf("Finish called %d times on the error path, want 1", len(finished))
	}
	if finished[0].SpoolID != msg.SpoolID || finished[0].DedupeID != msg.DedupeID {
		t.Fatalf("Finish got spool_id=%d dedupe_id=%q, want %d/%q",
			finished[0].SpoolID, finished[0].DedupeID, msg.SpoolID, msg.DedupeID)
	}

	stopRun(t, cancelRun, done)
}

// TestRun_DurableInbound_UserCancelFinishes is the /cancel case. StopAgent
// cancels the session context while the loop is still running, so the turn is
// done as far as the user is concerned - they asked for it to stop - and the row
// must be completed even though no final message was published.
func TestRun_DurableInbound_UserCancelFinishes(t *testing.T) {
	provider := &blockingMockProvider{started: make(chan struct{})}
	al, msgBus := newDurableInboundTestLoop(t, provider)

	dur := &fakeDurability{}
	al.SetInboundDurability(dur)

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

	if response := al.providable.StopAgent(msg.SessionKey); response == "" {
		t.Fatal("expected a stop response from StopAgent")
	}
	if !al.running.Load() {
		t.Fatal("StopAgent must leave running set: that flag is what separates a user cancel from shutdown")
	}

	got, ok := recvOutbound(t, msgBus, 5*time.Second)
	if !ok {
		t.Fatal("cancelled turn published no turn.end")
	}
	if got.Event != "turn.end" {
		t.Fatalf("outbound = event %q content %q, want turn.end", got.Event, got.Content)
	}

	_, finished, _, _ := dur.snapshot()
	if len(finished) != 1 {
		t.Fatalf("Finish called %d times after a user cancel, want 1 (running was still true)", len(finished))
	}
	if finished[0].DedupeID != msg.DedupeID || finished[0].SpoolID != msg.SpoolID {
		t.Fatalf("Finish got spool_id=%d dedupe_id=%q, want %d/%q",
			finished[0].SpoolID, finished[0].DedupeID, msg.SpoolID, msg.DedupeID)
	}

	stopRun(t, cancelRun, done)
}

// TestRun_DurableInbound_ShutdownCancelLeavesRowForReplay is the other half of
// the cancellation rule. Shutdown stores running=false BEFORE the coordinator's
// hooks cancel any context, so a turn that dies with the flag false was killed by
// gateway teardown, not by the user: its spool row must survive so the successor
// process replays it.
func TestRun_DurableInbound_ShutdownCancelLeavesRowForReplay(t *testing.T) {
	provider := &blockingMockProvider{started: make(chan struct{})}
	al, msgBus := newDurableInboundTestLoop(t, provider)

	dur := &fakeDurability{}
	al.SetInboundDurability(dur)

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

	// Reproduce the teardown ordering: the flag goes false first (Shutdown),
	// then the root context the turn inherits is cancelled. The drain budget is
	// what lets Shutdown return while the blocking turn is still running - in
	// production that is the shutdown coordinator's per-hook timeout, and it is
	// exactly why a turn can still be in flight when the root is cancelled.
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelDrain()
	if err := al.Shutdown(drainCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() = %v, want context.DeadlineExceeded (the turn is still blocked)", err)
	}
	cancelRun()

	// The turn still winds down and the channel still gets its terminal signal.
	got, ok := recvOutbound(t, msgBus, 10*time.Second)
	if !ok {
		t.Fatal("shutdown-cancelled turn published no turn.end")
	}
	if got.Event != "turn.end" {
		t.Fatalf("outbound = event %q content %q, want turn.end", got.Event, got.Content)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent loop returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent loop did not stop")
	}

	// Give the goroutine's defers a moment, then assert the negative: a Finish
	// here would delete the row and lose the message for good.
	time.Sleep(200 * time.Millisecond)
	if _, finished, _, _ := dur.snapshot(); len(finished) != 0 {
		t.Fatalf("Finish called %d times on shutdown teardown, want 0: the spool row must be replayed", len(finished))
	}
}

// TestRun_DurableInbound_NotWiredKeepsBehaviour proves the off path: with no
// durability wired the loop must still answer and emit turn.end, i.e. the new
// code is inert rather than merely untested.
func TestRun_DurableInbound_NotWiredKeepsBehaviour(t *testing.T) {
	provider := &countingMockProvider{response: "plain answer"}
	al, msgBus := newDurableInboundTestLoop(t, provider)

	if al.inboundDurability != nil {
		t.Fatal("a fresh AgentLoop must start with durability off")
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	done := make(chan error, 1)
	go func() { done <- al.Run(runCtx) }()

	if !msgBus.PublishInbound(spooledMessage()) {
		t.Fatal("inbound publish rejected")
	}

	if !drainUntil(t, msgBus, 10*time.Second, "plain answer") {
		t.Fatal("unwired loop stopped publishing the response")
	}
	if calls := provider.count(); calls != 1 {
		t.Errorf("provider called %d times, want 1", calls)
	}

	stopRun(t, cancelRun, done)
}

// TestRun_DurableInbound_UnspooledMessageNeverTouchesLedger mirrors the TUI and
// synthetic-event paths: durability is on, but the message carries no spool
// identity, so it must be processed normally and Finish must still be reached
// without panicking - pkg/durable's own guards make it a no-op there.
func TestRun_DurableInbound_UnspooledMessageNeverTouchesLedger(t *testing.T) {
	provider := &countingMockProvider{response: "tui answer"}
	al, msgBus := newDurableInboundTestLoop(t, provider)

	dur := &fakeDurability{}
	al.SetInboundDurability(dur)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	done := make(chan error, 1)
	go func() { done <- al.Run(runCtx) }()

	cliMsg := bus.InboundMessage{
		Channel:    "cli",
		ChatID:     "tui",
		SessionKey: "cli:tui",
		Content:    "hello",
	}
	if !msgBus.PublishInbound(cliMsg) {
		t.Fatal("inbound publish rejected")
	}

	if !waitFor(t, 10*time.Second, func() bool {
		_, f, _, _ := dur.snapshot()
		return len(f) > 0
	}) {
		t.Fatal("Finish was never called for an unspooled message")
	}
	if calls := provider.count(); calls != 1 {
		t.Errorf("provider called %d times for an unspooled message, want 1 (ShouldSkip must not block it)", calls)
	}
	_, finished, _, _ := dur.snapshot()
	if len(finished) != 1 || finished[0].SpoolID != 0 || finished[0].DedupeID != "" {
		t.Fatalf("Finish recorded %+v, want the zero-identity message echoed back", finished)
	}

	stopRun(t, cancelRun, done)
}

// TestInboundDurabilityContractIsStable documents the narrow contract the
// gateway wires *durable.Inbound into: the loop stores the implementation
// through the setter and reads it back on every turn. A signature drift breaks
// compilation of this file long before the wiring is exercised at runtime.
func TestInboundDurabilityContractIsStable(t *testing.T) {
	var d InboundDurability = &fakeDurability{}

	al, _ := newDurableInboundTestLoop(t, &mockProvider{})
	if al.inboundDurability != nil {
		t.Fatal("a fresh AgentLoop must start with durability off")
	}
	al.SetInboundDurability(d)
	if al.inboundDurability != d {
		t.Fatal("SetInboundDurability did not store the implementation it was given")
	}
}
