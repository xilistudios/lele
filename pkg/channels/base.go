package channels

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/xilistudios/lele/pkg/bus"
)

type Channel interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Send(ctx context.Context, msg bus.OutboundMessage) error
	IsRunning() bool
	IsAllowed(senderID string) bool
}

type BaseChannel struct {
	config    interface{}
	bus       *bus.MessageBus
	running   bool
	name      string
	allowList []string
	// InboundDroppedHook is invoked when PublishInbound rejects a message
	// (bus closed or inbound queue full). Channels that start user-visible
	// side effects before publishing (typing indicators, "Thinking..."
	// placeholders) use it to roll those effects back, since no outbound
	// reply will ever arrive for the dropped message.
	//
	// It must be assigned before the channel starts processing updates
	// (typically at the end of the channel constructor) and must not be
	// mutated afterwards: it is read from update-handling goroutines
	// without additional synchronization. Nil-safe: the hook is only called
	// when set.
	InboundDroppedHook func(msg bus.InboundMessage)
}

func NewBaseChannel(name string, config interface{}, bus *bus.MessageBus, allowList []string) *BaseChannel {
	return &BaseChannel{
		config:    config,
		bus:       bus,
		name:      name,
		allowList: allowList,
		running:   false,
	}
}

func (c *BaseChannel) Name() string {
	return c.name
}

func (c *BaseChannel) IsRunning() bool {
	return c.running
}

func (c *BaseChannel) IsAllowed(senderID string) bool {
	if len(c.allowList) == 0 {
		return true
	}

	// Extract parts from compound senderID like "123456|username"
	idPart := senderID
	userPart := ""
	if idx := strings.Index(senderID, "|"); idx > 0 {
		idPart = senderID[:idx]
		userPart = senderID[idx+1:]
	}

	for _, allowed := range c.allowList {
		// Strip leading "@" from allowed value for username matching
		trimmed := strings.TrimPrefix(allowed, "@")
		allowedID := trimmed
		allowedUser := ""
		if idx := strings.Index(trimmed, "|"); idx > 0 {
			allowedID = trimmed[:idx]
			allowedUser = trimmed[idx+1:]
		}

		// Support either side using "id|username" compound form.
		// This keeps backward compatibility with legacy Telegram allowlist entries.
		if senderID == allowed ||
			idPart == allowed ||
			senderID == trimmed ||
			idPart == trimmed ||
			idPart == allowedID ||
			(allowedUser != "" && senderID == allowedUser) ||
			(userPart != "" && (userPart == allowed || userPart == trimmed || userPart == allowedUser)) {
			return true
		}
	}

	return false
}

func (c *BaseChannel) HandleMessage(senderID, chatID, content string, media []string, metadata map[string]string) {
	c.HandleMessageWithSession(senderID, chatID, content, media, metadata, "")
}

func (c *BaseChannel) HandleMessageWithSession(senderID, chatID, content string, media []string, metadata map[string]string, sessionKey string) {
	attachments := make([]bus.FileAttachment, 0, len(media))
	for _, path := range media {
		attachments = append(attachments, bus.FileAttachment{
			Name: filepath.Base(path),
			Path: path,
			Kind: "file",
		})
	}
	c.HandleMessageWithAttachments(senderID, chatID, content, attachments, metadata, sessionKey)
}

func (c *BaseChannel) HandleMessageWithAttachments(senderID, chatID, content string, attachments []bus.FileAttachment, metadata map[string]string, sessionKey string) {
	if !c.IsAllowed(senderID) {
		return
	}

	media := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment.Path != "" {
			media = append(media, attachment.Path)
		}
	}

	msg := bus.InboundMessage{
		Channel:     c.name,
		SenderID:    senderID,
		ChatID:      chatID,
		Content:     content,
		Media:       media,
		Attachments: attachments,
		SessionKey:  sessionKey,
		Metadata:    metadata,
	}

	if !c.bus.PublishInbound(msg) {
		c.onInboundDropped(msg)
	}
}

// onInboundDropped notifies the channel that a message was rejected by the
// bus so it can undo side effects already shown to the user. Safe to call
// when no hook is configured.
func (c *BaseChannel) onInboundDropped(msg bus.InboundMessage) {
	if c.InboundDroppedHook != nil {
		c.InboundDroppedHook(msg)
	}
}

func (c *BaseChannel) setRunning(running bool) {
	c.running = running
}
