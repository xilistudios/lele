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

type streamHandler struct {
	bus       *bus.MessageBus
	channel   string
	chatID    string
	messageID string

	now func() time.Time

	content   throughputMeter
	reasoning throughputMeter
}

type throughputMeter struct {
	now func() time.Time

	start time.Time

	tokens int
}

func (tm *throughputMeter) record(chunk string) {
	if chunk == "" {
		return
	}
	if tm.start.IsZero() {
		tm.start = tm.now()
	}
	tm.tokens += providers.EstimateTokensFromText(chunk)
}

func (tm *throughputMeter) elapsedMS() int64 {
	if tm.start.IsZero() {
		return 0
	}
	return tm.now().Sub(tm.start).Milliseconds()
}

func (tm *throughputMeter) metrics() map[string]string {
	elapsedMS := tm.elapsedMS()
	tokPerSec := 0.0
	if elapsedMS > 0 {
		tokPerSec = float64(tm.tokens) / (float64(elapsedMS) / 1000)
	}
	return map[string]string{
		"tokens":      strconv.Itoa(tm.tokens),
		"elapsed_ms":  strconv.FormatInt(elapsedMS, 10),
		"tok_per_sec": strconv.FormatFloat(tokPerSec, 'f', -1, 64),
		"estimated":   "true",
	}
}

func (sh *streamHandler) clock() time.Time { return sh.now() }

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
	sh.content.now = sh.clock
	sh.reasoning.now = sh.clock
	return sh
}

func (sh *streamHandler) shouldStream(sendResponse bool) bool {
	return sh.channel == channels.ChannelName && sendResponse
}

func (sh *streamHandler) onChunk(chunk string, done bool) {
	sh.content.record(chunk)

	metadata := sh.content.metrics()
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
