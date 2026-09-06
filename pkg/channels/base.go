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
	// InboundSpooler backs every inbound message with a durable spool row
	// before it reaches the bus, so a message this channel accepted survives a
	// crash or a self-restart. See publishInbound.
	//
	// Like InboundDroppedHook it must be assigned before the channel starts
	// processing updates (typically through Manager.SetInboundSpooler, which
	// runs after construction and before StartAll) and must not be mutated
	// afterwards: update-handling goroutines read it without additional
	// synchronization. Nil-safe: nil means "durability off" and messages are
	// published exactly as before this field existed.
	InboundSpooler InboundSpooler
}

// InboundSpooler persists an inbound message to the durable spool before it is
// published. Implemented by *durable.Inbound, which pkg/channels deliberately
// does not import: the gateway owns the storage wiring and hands the
// implementation in, so the channel layer depends only on these two methods and
// stays free of any database knowledge.
//
// Enqueue is expected to be non-blocking apart from its own write, to mutate
// msg in place (tagging SpoolID and DedupeID on success) and to return false
// rather than fail loudly when persistence is unavailable or disabled. A false
// answer never stops the message from being published. It also CLAIMS the row it
// writes for its own instance, so the replay pump cannot pick up a message that
// is being answered right now.
//
// Release is the rollback for that claim: it hands a row Enqueue accepted back
// to the pending set when the bus refused the publish, so the pump retries it
// instead of the row sitting claimed for a turn that never runs. Like Enqueue it
// is best-effort and returns false when there is nothing to undo.
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

// SetInboundSpooler wires the durable inbound spooler into the channel. Call
// it after construction and before the channel starts handling updates; like
// the field itself it is not safe to call concurrently with traffic.
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

	c.publishInbound(&msg)
}

// publishInbound backs msg with the durable spool (if a spooler is wired),
// then publishes it. Enqueue claims the row it writes for the spooler's own
// instance, so the replay pump leaves it alone while this message is in flight.
// On a rejected publish the rollback hook fires (the user may already see a
// typing indicator) AND the single row is handed back to pending with Release,
// which is what lets the pump retry it on the next tick instead of waiting out
// the stale-claim timeout.
//
// This is the single chokepoint every channel inbound goes through; native
// publishers reach it via BaseChannel.publishInbound too.
func (c *BaseChannel) publishInbound(msg *bus.InboundMessage) {
	if c.InboundSpooler != nil {
		c.InboundSpooler.Enqueue(msg) // best-effort; false means unpersisted (and so unclaimed)
	}
	if !c.bus.PublishInbound(*msg) {
		// The bus refused a message that may already be spooled. Hand that one
		// row back to pending so the pump retries it, instead of leaving it
		// claimed-and-in-flight for a turn that will never run. Then let the
		// channel undo any side effect (a typing indicator, etc.).
		if c.InboundSpooler != nil {
			if !c.InboundSpooler.Release(msg) && msg.SpoolID != 0 {
				// The row exists (Enqueue wrote it) but could not be handed
				// back to pending. It stays claimed by this instance and the
				// turn that would have finished it never runs, so the message
				// is only recovered when shutdown's blanket release (or a
				// stale reclaim after a crash) frees it for a successor. Log so
				// this gap is observable rather than silent. A false return
				// with SpoolID 0 simply means nothing was spooled: not a fault.
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
