package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/utils"
)

type WhatsAppChannel struct {
	*BaseChannel
	conn      *websocket.Conn
	config    config.WhatsAppConfig
	url       string
	mu        sync.Mutex
	connected bool
}

func NewWhatsAppChannel(cfg config.WhatsAppConfig, bus *bus.MessageBus) (*WhatsAppChannel, error) {
	base := NewBaseChannel("whatsapp", cfg, bus, cfg.AllowFrom)

	return &WhatsAppChannel{
		BaseChannel: base,
		config:      cfg,
		url:         cfg.BridgeURL,
		connected:   false,
	}, nil
}

func (c *WhatsAppChannel) Start(ctx context.Context) error {
	log.Printf("Starting WhatsApp channel connecting to %s...", c.url)

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	conn, _, err := dialer.Dial(c.url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to WhatsApp bridge: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()

	c.setRunning(true)
	log.Println("WhatsApp channel connected")

	go c.listen(ctx)

	return nil
}

func (c *WhatsAppChannel) Stop(ctx context.Context) error {
	log.Println("Stopping WhatsApp channel...")

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			log.Printf("Error closing WhatsApp connection: %v", err)
		}
		c.conn = nil
	}

	c.connected = false
	c.setRunning(false)

	return nil
}

func (c *WhatsAppChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return fmt.Errorf("whatsapp connection not established")
	}

	payload := map[string]interface{}{
		"type":    "message",
		"to":      msg.ChatID,
		"content": msg.Content,
	}
	if len(msg.Attachments) > 0 {
		attachments := make([]map[string]interface{}, 0, len(msg.Attachments))
		for _, attachment := range msg.Attachments {
			attachments = append(attachments, map[string]interface{}{
				"name":      whatsappAttachmentName(attachment),
				"path":      attachment.Path,
				"mime_type": attachment.MIMEType,
				"kind":      attachment.Kind,
			})
		}
		payload["attachments"] = attachments
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}

func (c *WhatsAppChannel) listen(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			c.mu.Lock()
			conn := c.conn
			c.mu.Unlock()

			if conn == nil {
				time.Sleep(1 * time.Second)
				continue
			}

			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("WhatsApp read error: %v", err)
				time.Sleep(2 * time.Second)
				continue
			}

			var msg map[string]interface{}
			if err := json.Unmarshal(message, &msg); err != nil {
				log.Printf("Failed to unmarshal WhatsApp message: %v", err)
				continue
			}

			msgType, ok := msg["type"].(string)
			if !ok {
				continue
			}

			if msgType == "message" {
				c.handleIncomingMessage(msg)
			}
		}
	}
}

func (c *WhatsAppChannel) handleIncomingMessage(msg map[string]interface{}) {
	senderID, ok := msg["from"].(string)
	if !ok {
		return
	}

	chatID, ok := msg["chat"].(string)
	if !ok {
		chatID = senderID
	}

	content, ok := msg["content"].(string)
	if !ok {
		content = ""
	}

	attachments := make([]bus.FileAttachment, 0)
	if mediaData, ok := msg["media"].([]interface{}); ok {
		for _, m := range mediaData {
			if path, ok := m.(string); ok {
				attachments = append(attachments, bus.FileAttachment{
					Name: filepath.Base(path),
					Path: path,
					Kind: "file",
				})
			}
		}
	}
	if attachmentData, ok := msg["attachments"].([]interface{}); ok {
		for _, item := range attachmentData {
			attachmentMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			path, _ := attachmentMap["path"].(string)
			if path == "" {
				continue
			}
			name, _ := attachmentMap["name"].(string)
			mimeType, _ := attachmentMap["mime_type"].(string)
			kind, _ := attachmentMap["kind"].(string)
			caption, _ := attachmentMap["caption"].(string)
			attachments = append(attachments, bus.FileAttachment{
				Name:     name,
				Path:     path,
				MIMEType: mimeType,
				Kind:     kind,
				Caption:  caption,
			})
		}
	}

	metadata := make(map[string]string)
	if messageID, ok := msg["id"].(string); ok {
		metadata["message_id"] = messageID
	}
	if userName, ok := msg["from_name"].(string); ok {
		metadata["user_name"] = userName
	}

	log.Printf("WhatsApp message from %s: %s...", senderID, utils.Truncate(content, 50))

	c.HandleMessageWithAttachments(senderID, chatID, content, attachments, metadata, "")
}

func whatsappAttachmentName(attachment bus.FileAttachment) string {
	if attachment.Name != "" {
		return attachment.Name
	}
	if attachment.Path != "" {
		return filepath.Base(attachment.Path)
	}
	return "attachment"
}
