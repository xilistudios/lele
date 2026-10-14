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
type streamHandler struct {
	bus       *bus.MessageBus
	channel   string
	chatID    string
	messageID string

	// now is the clock used for the throughput accounting. It is a struct field
	// (rather than a direct time.Now() call) so tests can drive elapsed time
	// deterministically without sleeping.
	now func() time.Time

	// streamStart is the instant the first non-empty chunk of this segment
	// arrived. It is intentionally NOT set by the constructor: the constructor
	// runs before the provider request, so anchoring there would bill the
	// model's time-to-first-token as if it were streaming throughput. Measured
	// from the first content chunk, tok/s describes how fast the visible answer
	// was produced. The zero value means "no content seen yet".
	streamStart time.Time

	// streamTokens accumulates the estimated tokens of this segment's text
	// chunks. Reasoning/thinking chunks are deliberately excluded — see
	// onReasoning.
	streamTokens int
}

// newStreamHandler creates a new stream handler.
// If messageID is empty, a stable UUID is generated once and reused for all chunks.
func newStreamHandler(bus *bus.MessageBus, channel, chatID, messageID string) *streamHandler {
	if messageID == "" {
		messageID = uuid.New().String()
	}
	return &streamHandler{
		bus:       bus,
		channel:   channel,
		chatID:    chatID,
		messageID: messageID,
		now:       time.Now,
	}
}

// shouldStream returns true if streaming should be enabled for this request
func (sh *streamHandler) shouldStream(sendResponse bool) bool {
	return sh.channel == channels.ChannelName && sendResponse
}

// elapsedMS reports how many milliseconds have passed since the first content
// chunk of this segment, or 0 while the segment has produced no content yet.
func (sh *streamHandler) elapsedMS() int64 {
	if sh.streamStart.IsZero() {
		return 0
	}
	return sh.now().Sub(sh.streamStart).Milliseconds()
}

// onChunk sends a streaming chunk to the client.
//
// Every publication carries the running throughput metrics for the segment, so
// a frontend can render a live tok/s readout and does not have to wait for the
// final chunk. OutboundMessage.Metadata is a map[string]string on the wire, so
// the numeric metrics are encoded as strings; the parsing side owns formatting.
func (sh *streamHandler) onChunk(chunk string, done bool) {
	if chunk != "" {
		if sh.streamStart.IsZero() {
			sh.streamStart = sh.now()
		}
		// The counter is a running sum of PER-CHUNK estimates: each chunk is
		// truncated to an integer before being added, so this total can differ
		// by ±1 token per chunk from providers.ResponseTokenCounts, which
		// truncates once over the concatenated text. The drift is deliberate -
		// summing incrementally is what lets every intermediate event publish a
		// live tok/s without re-tokenizing the whole answer.
		//
		// On a retry of the LLM call over the same handler, these counters are
		// reused: the segment mixes the tokens and elapsed time of a failed
		// attempt with those of the successful one. Accepted, since resetting
		// would require a hook on every retry path.
		sh.streamTokens += providers.EstimateTokensFromText(chunk)
	}

	elapsedMS := sh.elapsedMS()
	// Guard against division by zero: when the first chunk and the read of the
	// clock happen within the same millisecond there is no elapsed window yet,
	// so report 0.0 instead of +Inf/NaN.
	tokPerSec := 0.0
	if elapsedMS > 0 {
		tokPerSec = float64(sh.streamTokens) / (float64(elapsedMS) / 1000)
	}

	sh.bus.PublishOutbound(bus.OutboundMessage{
		Channel:   sh.channel,
		ChatID:    sh.chatID,
		Event:     "message.stream",
		MessageID: sh.messageID,
		Content:   chunk,
		Metadata: map[string]string{
			// "done" keeps its historical shape exactly ("true"/"false" via %v)
			// because channels/native.go matches on Metadata["done"] == "true".
			"done": fmt.Sprintf("%v", done),
			// Throughput metrics of this segment. "estimated" marks the token
			// count as heuristic-derived: it is true for every event today and
			// would flip to false once real provider usage is plumbed through.
			"tokens":      strconv.Itoa(sh.streamTokens),
			"elapsed_ms":  strconv.FormatInt(elapsedMS, 10),
			"tok_per_sec": strconv.FormatFloat(tokPerSec, 'f', -1, 64),
			"estimated":   "true",
		},
	})
}

// onReasoning sends a reasoning/thinking chunk to the client.
//
// Reasoning tokens are intentionally NOT added to the segment counters: this
// task measures the throughput of the answer surface (message.stream), whose
// chunks are what the chat renders. Reasoning arrives on a separate event
// (message.thinking) with its own payload, and mixing the two would distort
// both the token count and the elapsed window.
func (sh *streamHandler) onReasoning(reasoningChunk string) {
	sh.bus.PublishOutbound(bus.OutboundMessage{
		Channel:   sh.channel,
		ChatID:    sh.chatID,
		Event:     "message.thinking",
		MessageID: sh.messageID,
		Content:   reasoningChunk,
	})
}
