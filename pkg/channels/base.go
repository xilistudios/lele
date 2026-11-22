package channels

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
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
	config             interface{}
	bus                *bus.MessageBus
	running            bool
	name               string
	allowList          []string
	InboundDroppedHook func(msg bus.InboundMessage)
	InboundSpooler     InboundSpooler
}

type InboundSpooler interface {
	Enqueue(msg *bus.InboundMessage) bool
	Release(msg *bus.InboundMessage) bool
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

func (c *BaseChannel) SetInboundSpooler(s InboundSpooler) {
	c.InboundSpooler = s
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

	idPart := senderID
	userPart := ""
	if idx := strings.Index(senderID, "|"); idx > 0 {
		idPart = senderID[:idx]
		userPart = senderID[idx+1:]
	}

	for _, allowed := range c.allowList {
		trimmed := strings.TrimPrefix(allowed, "@")
		allowedID := trimmed
		allowedUser := ""
		if idx := strings.Index(trimmed, "|"); idx > 0 {
			allowedID = trimmed[:idx]
			allowedUser = trimmed[idx+1:]
		}

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

	c.publishInbound(&msg)
}

func (c *BaseChannel) publishInbound(msg *bus.InboundMessage) {
	if c.InboundSpooler != nil {
		c.InboundSpooler.Enqueue(msg)
	}
	if !c.bus.PublishInbound(*msg) {
		if c.InboundSpooler != nil {
			if !c.InboundSpooler.Release(msg) && msg.SpoolID != 0 {
				logger.WarnCF("channels", "Durable inbound rollback failed; row stays claimed until shutdown",
					map[string]interface{}{
						"channel":  c.name,
						"spool_id": msg.SpoolID,
					})
			}
		}
		c.onInboundDropped(*msg)
	}
}

func (c *BaseChannel) onInboundDropped(msg bus.InboundMessage) {
	if c.InboundDroppedHook != nil {
		c.InboundDroppedHook(msg)
	}
}

func (c *BaseChannel) setRunning(running bool) {
	c.running = running
}
