// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
)

// streamHandler handles streaming of LLM response chunks to clients
type streamHandler struct {
	bus       *bus.MessageBus
	channel   string
	chatID    string
	messageID string
}

// newStreamHandler creates a new stream handler
func newStreamHandler(bus *bus.MessageBus, channel, chatID, messageID string) *streamHandler {
	return &streamHandler{
		bus:       bus,
		channel:   channel,
		chatID:    chatID,
		messageID: messageID,
	}
}

// shouldStream returns true if streaming should be enabled for this request
func (sh *streamHandler) shouldStream(sendResponse bool) bool {
	return sh.channel == channels.ChannelName && sendResponse
}

// onChunk sends a streaming chunk to the client
func (sh *streamHandler) onChunk(chunk string, done bool) {
	messageID := sh.messageID
	if messageID == "" {
		messageID = uuid.New().String()
	}
	sh.bus.PublishOutbound(bus.OutboundMessage{
		Channel:   sh.channel,
		ChatID:    sh.chatID,
		Event:     "message.stream",
		MessageID: messageID,
		Content:   chunk,
		Metadata: map[string]string{
			"done": fmt.Sprintf("%v", done),
		},
	})
}

// onReasoning sends a reasoning/thinking chunk to the client
func (sh *streamHandler) onReasoning(reasoningChunk string) {
	messageID := sh.messageID
	if messageID == "" {
		messageID = uuid.New().String()
	}
	sh.bus.PublishOutbound(bus.OutboundMessage{
		Channel:   sh.channel,
		ChatID:    sh.chatID,
		Event:     "message.thinking",
		MessageID: messageID,
		Content:   reasoningChunk,
	})
}
