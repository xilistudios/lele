// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/providers"
)

// streamHandler handles streaming of LLM response chunks to clients.
//
// One handler is built per LLM iteration (llm_runner.go creates it inside the
// iteration loop next to a per-iteration message ID), so every counter below is
// scoped to a single stream segment. A turn that performs intermediate tool
// calls produces one segment per LLM response, and a new segment never inherits
// the previous one's start time or token count.
//
// A segment has TWO independent surfaces: the reasoning/thinking text published
// on message.thinking and the answer text published on message.stream. Each one
// owns its throughput meter, so a long thinking phase is reported at the speed
// it was actually produced without diluting the answer rate (and vice versa) —
// and, key for the UI, the client gets a live tok/s readout from the very first
// reasoning token instead of only once the answer starts streaming.
type streamHandler struct {
	bus       *bus.MessageBus
	channel   string
	chatID    string
	messageID string

	// now is the clock used for the throughput accounting. It is a struct field
	// (rather than a direct time.Now() call) so tests can drive elapsed time
	// deterministically without sleeping.
	now func() time.Time

	// content meters the answer surface (message.stream).
	content throughputMeter
	// reasoning meters the reasoning surface (message.thinking).
	reasoning throughputMeter
}

// throughputMeter accumulates the estimated tokens of ONE stream surface and
// the wall-clock window over which they were produced.
//
// now is a func value rather than a captured time.Now call so the handler can
// point both meters at its own (swappable) clock: a test that replaces the
// handler's clock drives every surface deterministically.
type throughputMeter struct {
	now func() time.Time

	// start is the instant the first non-empty chunk of the surface arrived. It
	// is intentionally NOT set by the constructor: the constructor runs before
	// the provider request, so anchoring there would bill the model's
	// time-to-first-token as if it were streaming throughput. Measured from the
	// first chunk, tok/s describes how fast the surface was produced. The zero
	// value means "no content seen yet".
	start time.Time

	// tokens accumulates the estimated tokens of the surface's chunks.
	tokens int
}

// record accounts one chunk of the surface: it anchors the measurement window
// on the first non-empty chunk and adds the chunk's estimated token count.
func (tm *throughputMeter) record(chunk string) {
	if chunk == "" {
		return
	}
	if tm.start.IsZero() {
		tm.start = tm.now()
	}
	// The counter is a running sum of PER-CHUNK estimates: each chunk is
	// truncated to an integer before being added, so this total can differ by
	// ±1 token per chunk from providers.ResponseTokenCounts, which truncates
	// once over the concatenated text. The drift is deliberate — summing
	// incrementally is what lets every intermediate event publish a live tok/s
	// without re-tokenizing the whole answer.
	//
	// On a retry of the LLM call over the same handler, these counters are
	// reused: the segment mixes the tokens and elapsed time of a failed attempt
	// with those of the successful one. Accepted, since resetting would require
	// a hook on every retry path.
	tm.tokens += providers.EstimateTokensFromText(chunk)
}

// elapsedMS reports how many milliseconds have passed since the surface's first
// chunk, or 0 while it has produced no content yet.
func (tm *throughputMeter) elapsedMS() int64 {
	if tm.start.IsZero() {
		return 0
	}
	return tm.now().Sub(tm.start).Milliseconds()
}

// metrics renders the surface's running throughput as the string-encoded
// metadata carried by every chunk of that surface.
//
// OutboundMessage.Metadata is a map[string]string on the wire, so the numeric
// metrics are encoded as strings; the parsing side owns formatting.
func (tm *throughputMeter) metrics() map[string]string {
	elapsedMS := tm.elapsedMS()
	// Guard against division by zero: when the first chunk and the read of the
	// clock happen within the same millisecond there is no elapsed window yet,
	// so report 0.0 instead of +Inf/NaN.
	tokPerSec := 0.0
	if elapsedMS > 0 {
		tokPerSec = float64(tm.tokens) / (float64(elapsedMS) / 1000)
	}
	return map[string]string{
		// "estimated" marks the token count as heuristic-derived: it is true for
		// every event today and would flip to false once real provider usage is
		// plumbed through.
		"tokens":      strconv.Itoa(tm.tokens),
		"elapsed_ms":  strconv.FormatInt(elapsedMS, 10),
		"tok_per_sec": strconv.FormatFloat(tokPerSec, 'f', -1, 64),
		"estimated":   "true",
	}
}

// clock returns the handler's current time source. Both meters hold this method
// value rather than a copy of sh.now, so replacing the handler's clock (tests,
// or a future injectable clock) is observed by every surface.
func (sh *streamHandler) clock() time.Time { return sh.now() }

// newStreamHandler creates a new stream handler.
// If messageID is empty, a stable UUID is generated once and reused for all chunks.
func newStreamHandler(bus *bus.MessageBus, channel, chatID, messageID string) *streamHandler {
	if messageID == "" {
		messageID = uuid.New().String()
	}
	sh := &streamHandler{
		bus:       bus,
		channel:   channel,
		chatID:    chatID,
		messageID: messageID,
		now:       time.Now,
	}
	// Both surfaces read the clock through the handler (see clock).
	sh.content.now = sh.clock
	sh.reasoning.now = sh.clock
	return sh
}

// shouldStream returns true if streaming should be enabled for this request
func (sh *streamHandler) shouldStream(sendResponse bool) bool {
	return sh.channel == channels.ChannelName && sendResponse
}

// onChunk sends a streaming chunk to the client.
//
// Every publication carries the running throughput metrics for the answer
// surface, so a frontend can render a live tok/s readout and does not have to
// wait for the final chunk.
func (sh *streamHandler) onChunk(chunk string, done bool) {
	sh.content.record(chunk)

	metadata := sh.content.metrics()
	// "done" keeps its historical shape exactly ("true"/"false" via %v)
	// because channels/native.go matches on Metadata["done"] == "true".
	metadata["done"] = fmt.Sprintf("%v", done)

	sh.bus.PublishOutbound(bus.OutboundMessage{
		Channel:   sh.channel,
		ChatID:    sh.chatID,
		Event:     "message.stream",
		MessageID: sh.messageID,
		Content:   chunk,
		Metadata:  metadata,
	})
}

// onReasoning sends a reasoning/thinking chunk to the client.
//
// The chunk carries the SAME throughput metric keys as message.stream, but
// measured on the reasoning surface alone: reasoning tokens are accounted
// separately from the answer's (see the streamHandler doc). Publishing them
// here is what lets the TUI and the web UI show a live tok/s readout from the
// first reasoning token instead of only once the answer starts.
func (sh *streamHandler) onReasoning(reasoningChunk string) {
	sh.reasoning.record(reasoningChunk)

	sh.bus.PublishOutbound(bus.OutboundMessage{
		Channel:   sh.channel,
		ChatID:    sh.chatID,
		Event:     "message.thinking",
		MessageID: sh.messageID,
		Content:   reasoningChunk,
		Metadata:  sh.reasoning.metrics(),
	})
}
