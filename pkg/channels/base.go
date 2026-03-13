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

	c.bus.PublishInbound(msg)
}

func (c *BaseChannel) setRunning(running bool) {
	c.running = running
}
