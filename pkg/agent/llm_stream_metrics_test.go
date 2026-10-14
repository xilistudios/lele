// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/providers"
)

// fakeClock is a hand-driven clock: tests advance it explicitly so the
// throughput math is deterministic and no test ever sleeps.
type fakeClock struct {
	t time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newMetricsStreamer builds a stream handler wired to a fake clock, so the
// handler is deterministic without touching the real time source.
func newMetricsStreamer(t *testing.T, msgBus *bus.MessageBus, clock *fakeClock) *streamHandler {
	t.Helper()
	sh := newStreamHandler(msgBus, channels.ChannelName, "chat-metrics", "msg-metrics")
	sh.now = clock.now
	return sh
}

// recvStream pulls the next outbound message and asserts it is a stream event
// for our handler.
func recvStream(t *testing.T, msgBus *bus.MessageBus, sh *streamHandler) bus.OutboundMessage {
	t.Helper()
	msg, ok := recvOutbound(t, msgBus, 5*time.Second)
	if !ok {
		t.Fatal("no message.stream event published (bus closed or timeout)")
	}
	if msg.Event != "message.stream" {
		t.Fatalf("event = %q, want message.stream", msg.Event)
	}
	if msg.Channel != sh.channel || msg.MessageID != sh.messageID {
		t.Fatalf("routing changed: channel=%q messageID=%q", msg.Channel, msg.MessageID)
	}
	for _, key := range []string{"tokens", "elapsed_ms", "tok_per_sec", "estimated"} {
		if _, ok := msg.Metadata[key]; !ok {
			t.Fatalf("metadata is missing %q key: %v", key, msg.Metadata)
		}
	}
	return msg
}

func metadataInt(t *testing.T, msg bus.OutboundMessage, key string) int {
	t.Helper()
	raw, ok := msg.Metadata[key]
	if !ok {
		t.Fatalf("metadata %q missing from %v", key, msg.Metadata)
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("metadata %q = %q, not an int: %v", key, raw, err)
	}
	return n
}

func metadataFloat(t *testing.T, msg bus.OutboundMessage, key string) float64 {
	t.Helper()
	raw, ok := msg.Metadata[key]
	if !ok {
		t.Fatalf("metadata %q missing from %v", key, msg.Metadata)
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatalf("metadata %q = %q, not a float: %v", key, raw, err)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		t.Fatalf("metadata %q = %v, must never be NaN/Inf", key, f)
	}
	return f
}

// TestStreamHandlerMetricsAccumulateAcrossChunks pins the accumulated token
// count to the shared chars*2/5 heuristic and checks the running elapsed window
// on an intermediate (not final) event.
func TestStreamHandlerMetricsAccumulateAcrossChunks(t *testing.T) {
	msgBus := bus.NewMessageBus()
	clock := newFakeClock()
	sh := newMetricsStreamer(t, msgBus, clock)

	chunks := []string{"Hello world! ", "Esta es una respuesta ", "más larga todavía."}

	// Expected cumulative estimate: the handler sums one chars*2/5 estimate per
	// chunk (integer truncation applied per chunk, not on the joined text).
	wantCumulative := make([]int, len(chunks))
	running := 0
	for i, chunk := range chunks {
		running += providers.EstimateTokensFromText(chunk)
		wantCumulative[i] = running
	}
	wantTokens := running

	for i, chunk := range chunks {
		// Pretend each chunk took 500ms of streaming time; the clock is only
		// moved AFTER the first chunk, so chunk 0 lands on elapsed_ms == 0.
		if i > 0 {
			clock.advance(500 * time.Millisecond)
		}
		sh.onChunk(chunk, false)
	}

	for i := range chunks {
		msg := recvStream(t, msgBus, sh)
		if msg.Content != chunks[i] {
			t.Fatalf("chunk %d content = %q, want %q", i, msg.Content, chunks[i])
		}
		if got := metadataInt(t, msg, "tokens"); got != wantCumulative[i] {
			t.Fatalf("chunk %d tokens = %d, want %d", i, got, wantCumulative[i])
		}
		if got := metadataInt(t, msg, "elapsed_ms"); got != i*500 {
			t.Fatalf("chunk %d elapsed_ms = %d, want %d", i, got, i*500)
		}
		if got := msg.Metadata["done"]; got != "false" {
			t.Fatalf("chunk %d done = %q, want \"false\" (preserved semantics)", i, got)
		}
		if got := msg.Metadata["estimated"]; got != "true" {
			t.Fatalf("chunk %d estimated = %q, want \"true\"", i, got)
		}
	}

	// Final event carries the total token count, total elapsed window and a
	// tok/s derived from both.
	clock.advance(500 * time.Millisecond)
	sh.onChunk("", true)

	doneMsg := recvStream(t, msgBus, sh)
	if doneMsg.Content != "" {
		t.Fatalf("final chunk content = %q, want empty", doneMsg.Content)
	}
	if got := doneMsg.Metadata["done"]; got != "true" {
		t.Fatalf("done = %q, want \"true\"", got)
	}
	if got := metadataInt(t, doneMsg, "tokens"); got != wantTokens {
		t.Fatalf("final tokens = %d, want %d", got, wantTokens)
	}
	if got := metadataInt(t, doneMsg, "elapsed_ms"); got != 1500 {
		t.Fatalf("final elapsed_ms = %d, want 1500", got)
	}
	wantRate := float64(wantTokens) / 1.5
	if got := metadataFloat(t, doneMsg, "tok_per_sec"); math.Abs(got-wantRate) > 1e-9 {
		t.Fatalf("final tok_per_sec = %v, want %v", got, wantRate)
	}
	if got := doneMsg.Metadata["estimated"]; got != "true" {
		t.Fatalf("final estimated = %q, want \"true\"", got)
	}
}

// TestStreamHandlerZeroElapsedIsNotNaN covers the division-by-zero guard: when
// the first chunk and the clock read land in the same millisecond, tok/s must
// be reported as 0.0 rather than +Inf or NaN.
func TestStreamHandlerZeroElapsedIsNotNaN(t *testing.T) {
	msgBus := bus.NewMessageBus()
	clock := newFakeClock()
	sh := newMetricsStreamer(t, msgBus, clock)

	sh.onChunk(strings.Repeat("a", 250), false) // 100 estimated tokens
	first := recvStream(t, msgBus, sh)

	if got := metadataInt(t, first, "elapsed_ms"); got != 0 {
		t.Fatalf("elapsed_ms = %d, want 0 (clock never advanced)", got)
	}
	if raw := first.Metadata["tok_per_sec"]; raw != "0" {
		t.Fatalf("tok_per_sec = %q, want \"0\" with no elapsed window", raw)
	}
	if got := metadataFloat(t, first, "tok_per_sec"); got != 0.0 {
		t.Fatalf("tok_per_sec = %v, want 0.0", got)
	}
	if got := metadataInt(t, first, "tokens"); got != 100 {
		t.Fatalf("tokens = %d, want 100", got)
	}

	// A done event on the same frozen clock must not divide by zero either.
	sh.onChunk("", true)
	doneMsg := recvStream(t, msgBus, sh)
	if got := metadataFloat(t, doneMsg, "tok_per_sec"); got != 0.0 {
		t.Fatalf("done tok_per_sec = %v, want 0.0", got)
	}
}

// TestStreamHandlerEmptySegment checks that a segment with no content at all
// (done without any chunk) still emits well-formed zeroed metrics and does not
// panic.
func TestStreamHandlerEmptySegment(t *testing.T) {
	msgBus := bus.NewMessageBus()
	clock := newFakeClock()
	sh := newMetricsStreamer(t, msgBus, clock)

	sh.onChunk("", true)
	msg := recvStream(t, msgBus, sh)

	if msg.Metadata["done"] != "true" {
		t.Fatalf("done = %q, want \"true\"", msg.Metadata["done"])
	}
	if got := metadataInt(t, msg, "tokens"); got != 0 {
		t.Fatalf("tokens = %d, want 0", got)
	}
	if got := metadataInt(t, msg, "elapsed_ms"); got != 0 {
		t.Fatalf("elapsed_ms = %d, want 0", got)
	}
	if got := metadataFloat(t, msg, "tok_per_sec"); got != 0.0 {
		t.Fatalf("tok_per_sec = %v, want 0.0", got)
	}
	if msg.Metadata["estimated"] != "true" {
		t.Fatalf("estimated = %q, want \"true\"", msg.Metadata["estimated"])
	}
	// The clock must not have been started by an empty chunk.
	if !sh.streamStart.IsZero() {
		t.Fatalf("streamStart = %v, want zero for a content-free segment", sh.streamStart)
	}
}

// TestStreamHandlerSegmentsAreIndependent locks in the per-segment scope: two
// handlers (as created for two LLM iterations in the same turn) never share
// counters or start times.
func TestStreamHandlerSegmentsAreIndependent(t *testing.T) {
	msgBus := bus.NewMessageBus()
	clock := newFakeClock()

	first := newMetricsStreamer(t, msgBus, clock)
	first.messageID = "msg-iter-0"
	second := newMetricsStreamer(t, msgBus, clock)
	second.messageID = "msg-iter-1"

	first.onChunk(strings.Repeat("x", 100), false) // 40 tokens
	clock.advance(2 * time.Second)
	first.onChunk("tail", false)

	second.onChunk(strings.Repeat("y", 50), false) // 20 tokens, its own window

	wantFirst := providers.EstimateTokensFromText(strings.Repeat("x", 100)) + providers.EstimateTokensFromText("tail")
	wantSecond := providers.EstimateTokensFromText(strings.Repeat("y", 50))

	got := map[string][2]int{}
	for i := 0; i < 3; i++ {
		msg, ok := recvOutbound(t, msgBus, 5*time.Second)
		if !ok {
			t.Fatal("expected three stream events")
		}
		got[msg.MessageID] = [2]int{metadataInt(t, msg, "tokens"), metadataInt(t, msg, "elapsed_ms")}
	}

	if got["msg-iter-1"][0] != wantSecond || got["msg-iter-1"][1] != 0 {
		t.Fatalf("second segment = %v, want tens=%d elapsed=0", got["msg-iter-1"], wantSecond)
	}
	if got["msg-iter-0"][0] != wantFirst {
		t.Fatalf("first segment tokens = %d, want %d", got["msg-iter-0"][0], wantFirst)
	}
	if last := got["msg-iter-0"][1]; last != 2000 {
		t.Fatalf("first segment final elapsed_ms = %d, want 2000", last)
	}
}

// TestStreamHandlerReasoningNotCounted documents the decision to keep reasoning
// out of the answer-segment metrics.
func TestStreamHandlerReasoningNotCounted(t *testing.T) {
	msgBus := bus.NewMessageBus()
	clock := newFakeClock()
	sh := newMetricsStreamer(t, msgBus, clock)

	sh.onReasoning(strings.Repeat("think ", 100))
	thinking, ok := recvOutbound(t, msgBus, 5*time.Second)
	if !ok {
		t.Fatal("expected message.thinking event")
	}
	if thinking.Event != "message.thinking" {
		t.Fatalf("event = %q, want message.thinking", thinking.Event)
	}
	if thinking.Metadata != nil {
		t.Fatalf("message.thinking metadata = %v, want nil (unchanged)", thinking.Metadata)
	}
	if !sh.streamStart.IsZero() || sh.streamTokens != 0 {
		t.Fatalf("reasoning leaked into stream metrics: start=%v tokens=%d", sh.streamStart, sh.streamTokens)
	}

	// The answer segment afterwards starts its own window from scratch.
	clock.advance(3 * time.Second)
	sh.onChunk(strings.Repeat("a", 25), false) // 10 tokens
	msg := recvStream(t, msgBus, sh)
	if got := metadataInt(t, msg, "tokens"); got != 10 {
		t.Fatalf("tokens = %d, want 10 (reasoning excluded)", got)
	}
	if got := metadataInt(t, msg, "elapsed_ms"); got != 0 {
		t.Fatalf("elapsed_ms = %d, want 0 (segment starts at first content chunk)", got)
	}
}
