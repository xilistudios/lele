package channels

import (
	"context"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/utils"
)

const (
	discordTextChunkMaxLen  = 2000
	telegramTextChunkMaxLen = 3800
	whatsappTextChunkMaxLen = 4000
	lineTextChunkMaxLen     = 5000
	slackTextChunkMaxLen    = 40000
)

// splitOutboundMessage expands a single outbound message into multiple sends when a
// channel has strict text limits. Text chunks are sent first; for channels with
// outbound attachment support, attachments are sent as a final attachment-only
// message to avoid duplication.
func splitOutboundMessage(msg bus.OutboundMessage) []bus.OutboundMessage {
	maxLen := outboundTextChunkMaxLen(msg.Channel)
	if maxLen <= 0 || msg.Content == "" {
		return []bus.OutboundMessage{msg}
	}

	chunks := utils.SplitMessage(msg.Content, maxLen)
	if len(chunks) <= 1 {
		return []bus.OutboundMessage{msg}
	}

	expanded := make([]bus.OutboundMessage, 0, len(chunks)+1)
	for index, chunk := range chunks {
		chunkMsg := msg
		chunkMsg.Content = chunk
		chunkMsg.Attachments = nil
		if index > 0 {
			chunkMsg.ReplyTo = ""
			chunkMsg.ReplyMarkup = nil
		}
		expanded = append(expanded, chunkMsg)
	}

	if len(msg.Attachments) > 0 && outboundChannelSeparatesAttachments(msg.Channel) {
		attachmentMsg := msg
		attachmentMsg.Content = ""
		attachmentMsg.ReplyMarkup = nil
		expanded = append(expanded, attachmentMsg)
	}

	return expanded
}

func outboundTextChunkMaxLen(channel string) int {
	switch channel {
	case "discord":
		return discordTextChunkMaxLen
	case "telegram":
		return telegramTextChunkMaxLen
	case "whatsapp":
		return whatsappTextChunkMaxLen
	case "line":
		return lineTextChunkMaxLen
	case "slack":
		return slackTextChunkMaxLen
	default:
		return 0
	}
}

func outboundChannelSeparatesAttachments(channel string) bool {
	switch channel {
	case "telegram", "whatsapp":
		return true
	default:
		return false
	}
}

func sendOutboundMessage(ctx context.Context, channel Channel, msg bus.OutboundMessage) error {
	for _, chunk := range splitOutboundMessage(msg) {
		if err := channel.Send(ctx, chunk); err != nil {
			return err
		}
	}

	return nil
}
