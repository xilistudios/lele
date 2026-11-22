package channels

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mymmrac/telego/telegoapi"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/utils"
)

const (
	discordTextChunkMaxLen  = 2000
	telegramTextChunkMaxLen = 3800
	whatsappTextChunkMaxLen = 4000
	lineTextChunkMaxLen     = 5000
	slackTextChunkMaxLen    = 40000
)

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

type sendResult int

const (
	sendDelivered sendResult = iota
	sendNotStarted
	sendPartial
)

func sendOutboundMessage(ctx context.Context, channel Channel, msg bus.OutboundMessage) (sendResult, error) {
	chunks := splitOutboundMessage(msg)
	for i, chunk := range chunks {
		if err := sendChunkWithRetry(ctx, channel, chunk); err != nil {
			if i == 0 {
				return sendNotStarted, err
			}
			return sendPartial, err
		}
	}
	return sendDelivered, nil
}

type RateLimitError struct {
	Channel    string
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("%s rate limited, retry after %s", e.Channel, e.RetryAfter)
}

func TelegramRateLimitError(err error) error {
	var apiErr *telegoapi.Error
	if !errors.As(err, &apiErr) || apiErr.ErrorCode != 429 {
		return err
	}
	retryAfter := time.Duration(0)
	if apiErr.Parameters != nil && apiErr.Parameters.RetryAfter > 0 {
		retryAfter = time.Duration(apiErr.Parameters.RetryAfter) * time.Second
	}
	return &RateLimitError{Channel: "telegram", RetryAfter: retryAfter}
}

const maxRetryAfter = 60 * time.Second

func retryWaitTime(lastErr error, attempt int) time.Duration {
	waitTime := time.Duration(attempt) * time.Second
	var rle *RateLimitError
	if errors.As(lastErr, &rle) && rle.RetryAfter > 0 {
		waitTime = min(rle.RetryAfter, maxRetryAfter)
	}
	return waitTime
}

func sendChunkWithRetry(ctx context.Context, channel Channel, msg bus.OutboundMessage) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			waitTime := retryWaitTime(lastErr, attempt)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(waitTime):
			}
		}

		err := channel.Send(ctx, msg)
		if err == nil {
			return nil
		}

		lastErr = err
		if !isTransientError(err) {
			return err
		}

		logger.WarnCF("channels", "Retrying outbound message", map[string]interface{}{
			"channel": msg.Channel,
			"attempt": attempt + 1,
			"error":   err.Error(),
		})
	}
	return lastErr
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "temporary") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "429") ||
		strings.Contains(lower, "503") ||
		strings.Contains(lower, "502") ||
		strings.Contains(lower, "network") ||
		strings.Contains(lower, "i/o timeout")
}
