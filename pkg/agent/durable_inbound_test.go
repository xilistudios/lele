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

func (f *fakeDurability) snapshot() (skipCalls int, finished []bus.InboundMessage, pendingAtFinish int64, sampled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.skipCalls, append([]bus.InboundMessage(nil), f.finished...), f.pendingAtFinish, f.finishSampleTaken
}

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

	if !drainUntil(t, msgBus, 10*time.Second, "final answer") {
		t.Fatal("response was not published before turn.end")
	}

	stopRun(t, cancelRun, done)
}

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

	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelDrain()
	if err := al.Shutdown(drainCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() = %v, want context.DeadlineExceeded (the turn is still blocked)", err)
	}
	cancelRun()

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

	time.Sleep(200 * time.Millisecond)
	if _, finished, _, _ := dur.snapshot(); len(finished) != 0 {
		t.Fatalf("Finish called %d times on shutdown teardown, want 0: the spool row must be replayed", len(finished))
	}
}

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
