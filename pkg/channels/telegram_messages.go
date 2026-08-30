package channels

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/utils"
)

func (c *TelegramChannel) handleMessage(ctx context.Context, message *telego.Message) error {
	if message == nil {
		return fmt.Errorf("message is nil")
	}

	// Deduplication: check if we've already processed this message
	messageKey := fmt.Sprintf("%d:%d", message.Chat.ID, message.MessageID)
	if c.isDuplicate(messageKey) {
		logger.DebugCF("telegram", "Duplicate message ignored", map[string]interface{}{
			"message_id": message.MessageID,
			"chat_id":    message.Chat.ID,
		})
		return nil
	}

	if message.Text != "" && strings.HasPrefix(message.Text, "/") {
		text := strings.TrimPrefix(message.Text, "/")
		parts := strings.Fields(text)
		if len(parts) > 0 {
			cmd := parts[0]
			switch cmd {
			case "help", "start", "show", "list", "models", "new", "clear", "stop", "model", "status", "compact", "subagents", "toggle", "verbose", "think", "agent":
				return c.handleCommandWithSession(ctx, message, cmd)
			}
		}
	}

	user := message.From
	if user == nil {
		return fmt.Errorf("message sender (user) is nil")
	}

	senderID := telegramSenderID(user.ID, user.Username)
	if !c.IsAllowed(senderID) {
		logger.DebugCF("telegram", "Message rejected by allowlist", map[string]interface{}{
			"user_id": senderID,
		})
		return nil
	}

	chatID := message.Chat.ID
	c.chatIDs[senderID] = chatID

	content := ""
	attachments := []bus.FileAttachment{}

	if message.Text != "" {
		content += message.Text
	}

	if message.Caption != "" {
		if content != "" {
			content += "\n"
		}
		content += message.Caption
	}

	if len(message.Photo) > 0 {
		photo := message.Photo[len(message.Photo)-1]
		photoPath := c.downloadPhoto(ctx, photo.FileID)
		if photoPath != "" {
			attachments = append(attachments, bus.FileAttachment{
				Name:      filepath.Base(photoPath),
				Path:      photoPath,
				MIMEType:  "image/jpeg",
				Kind:      "image",
				Temporary: true,
			})
			if content != "" {
				content += "\n"
			}
			content += "[image: photo]"
		}
	}

	if message.Voice != nil {
		voicePath := c.downloadFile(ctx, message.Voice.FileID, ".ogg")
		if voicePath != "" {
			attachments = append(attachments, bus.FileAttachment{
				Name:      filepath.Base(voicePath),
				Path:      voicePath,
				MIMEType:  message.Voice.MimeType,
				Kind:      "audio",
				Temporary: true,
			})

			transcribedText := ""
			if c.transcriber != nil && c.transcriber.IsAvailable() {
				ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()

				result, err := c.transcriber.Transcribe(ctx, voicePath)
				if err != nil {
					logger.ErrorCF("telegram", "Voice transcription failed", map[string]interface{}{
						"error": err.Error(),
						"path":  voicePath,
					})
					transcribedText = "[voice (transcription failed)]"
				} else {
					transcribedText = fmt.Sprintf("[voice transcription: %s]", result.Text)
					logger.InfoCF("telegram", "Voice transcribed successfully", map[string]interface{}{
						"text": result.Text,
					})
				}
			} else {
				transcribedText = "[voice]"
			}

			if content != "" {
				content += "\n"
			}
			content += transcribedText
		}
	}

	if message.Audio != nil {
		audioPath := c.downloadFile(ctx, message.Audio.FileID, ".mp3")
		if audioPath != "" {
			attachments = append(attachments, bus.FileAttachment{
				Name:      message.Audio.FileName,
				Path:      audioPath,
				MIMEType:  message.Audio.MimeType,
				Kind:      "audio",
				Temporary: true,
			})
			if content != "" {
				content += "\n"
			}
			content += "[audio]"
		}
	}

	if message.Document != nil {
		docPath := c.downloadFile(ctx, message.Document.FileID, "")
		if docPath != "" {
			attachments = append(attachments, bus.FileAttachment{
				Name:      message.Document.FileName,
				Path:      docPath,
				MIMEType:  message.Document.MimeType,
				Kind:      "file",
				Temporary: true,
			})
			if content != "" {
				content += "\n"
			}
			content += fmt.Sprintf("[file: %s]", telegramAttachmentName(attachments[len(attachments)-1]))
		}
	}

	if content == "" {
		content = "[empty message]"
	}

	logger.DebugCF("telegram", "Received message", map[string]interface{}{
		"sender_id": senderID,
		"chat_id":   fmt.Sprintf("%d", chatID),
		"preview":   utils.Truncate(content, 50),
	})

	chatIDStr := fmt.Sprintf("%d", chatID)
	thinkingKey := fmt.Sprintf("%d:%d", chatID, message.MessageID)

	c.stopAllThinkingForChat(chatIDStr)

	tc := c.startTypingIndicator(chatID)
	c.stopThinking.Store(thinkingKey, tc)

	pMsg, err := c.bot.SendMessage(ctx, tu.Message(tu.ID(chatID), "Thinking... 💭"))
	if err == nil {
		c.placeholders.Store(chatIDStr, pMsg.MessageID)
	}

	metadata := buildTelegramMetadata(message.MessageID, user, message.Chat)

	// Resolve session key through the routing system (unified cross-channel).
	peerKind := metadata["peer_kind"]
	peerID := metadata["peer_id"]
	sessionKey := c.resolveSessionKey(peerKind, peerID)

	c.HandleMessageWithAttachments(fmt.Sprintf("%d", user.ID), fmt.Sprintf("%d", chatID), content, attachments, metadata, sessionKey)
	return nil
}

// handleInboundDropped undoes the user-visible side effects that handleMessage
// starts before publishing to the bus (typing indicator + "Thinking... 💭"
// placeholder). When the bus rejects a message there will never be an outbound
// reply to clean them up, so the user would see a stuck indicator/placeholder.
// Wired as BaseChannel.InboundDroppedHook in NewTelegramChannel.
func (c *TelegramChannel) handleInboundDropped(msg bus.InboundMessage) {
	chatIDStr := msg.ChatID
	if chatIDStr == "" {
		return
	}

	// Exact key used by handleMessage: fmt.Sprintf("%d:%d", chatID, messageID),
	// i.e. "<chatID>:<user message id>". metadata["message_id"] carries the
	// user message id as a string.
	if messageID := msg.Metadata["message_id"]; messageID != "" {
		c.stopActiveThinking(chatIDStr + ":" + messageID)
	}
	// Safety net: cancel any thinking entries left for this chat (e.g. when
	// the metadata key is missing or the indicator was stored under a
	// different message id).
	c.stopAllThinkingForChat(chatIDStr)

	// Delete the "Thinking... 💭" placeholder if it is still pending for
	// this chat. Placeholders are stored keyed by the chatID string.
	// Deletion needs the bot token from config; when it is unavailable the
	// entry is left in place so the regular send path can still resolve it.
	if c.config == nil {
		return
	}
	pID, ok := c.placeholders.Load(chatIDStr)
	if !ok {
		return
	}
	chatID, err := parseChatID(chatIDStr)
	if err != nil {
		return
	}
	id, isInt := pID.(int)
	if !isInt {
		return
	}
	c.placeholders.Delete(chatIDStr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.deleteMessage(ctx, chatID, id)
}

func (c *TelegramChannel) handleCommandWithSession(ctx context.Context, message *telego.Message, cmd string) error {
	if message == nil {
		return fmt.Errorf("message is nil")
	}

	// Deduplication: check if we've already processed this message
	messageKey := fmt.Sprintf("%d:%d", message.Chat.ID, message.MessageID)
	if c.isDuplicate(messageKey) {
		logger.DebugCF("telegram", "Duplicate command ignored", map[string]interface{}{
			"message_id": message.MessageID,
			"chat_id":    message.Chat.ID,
			"command":    cmd,
		})
		return nil
	}

	user := message.From
	if user == nil {
		return fmt.Errorf("message sender (user) is nil")
	}

	senderID := fmt.Sprintf("%d", user.ID)
	chatID := message.Chat.ID

	// Resolve session key through the routing system (unified cross-channel).
	peerKind, peerID := telegramPeerInfo(message)
	sessionKey := c.resolveSessionKey(peerKind, peerID)

	switch cmd {
	case "new":
		response := "🔄 Nueva conversación iniciada. Historial limpiado."
		if c.agentLoop != nil {
			response = c.agentLoop.ClearSession(sessionKey)
		}
		return c.sendReplyText(ctx, message, response)

	case "clear":
		response := "✅ Historial de conversación limpiado."
		if c.agentLoop != nil {
			response = c.agentLoop.ClearSession(sessionKey)
		}
		return c.sendReplyText(ctx, message, response)

	case "stop":
		response := "⏹️ Agente detenido."
		if c.agentLoop != nil {
			response = c.agentLoop.StopAgent(sessionKey)
		}
		chatKey := fmt.Sprintf("%d", chatID)
		c.stopAllThinkingForChat(chatKey)
		if c.resolvePlaceholderWithText(ctx, chatID, chatKey, response) {
			return nil
		}
		return c.sendReplyText(ctx, message, response)

	case "status":
		response := "⚠️ Agent loop not available."
		if c.agentLoop != nil {
			response = c.agentLoop.GetStatus(sessionKey)
		}
		return c.sendReplyText(ctx, message, response)

	case "compact":
		response := "⚠️ Agent loop not available."
		if c.agentLoop != nil {
			response = c.agentLoop.CompactSession(sessionKey)
		}
		return c.sendReplyText(ctx, message, response)

	case "subagents":
		args := strings.TrimSpace(strings.TrimPrefix(message.Text, "/subagents"))
		c.publishSystemCommand(senderID, chatID, message.MessageID, telegramCommandText("subagents", args), map[string]string{
			"message_id": fmt.Sprintf("%d", message.MessageID),
		}, sessionKey)
		return nil

	case "toggle":
		args := strings.TrimSpace(strings.TrimPrefix(message.Text, "/toggle"))
		c.publishSystemCommand(senderID, chatID, message.MessageID, telegramCommandText("toggle", args), map[string]string{
			"message_id": fmt.Sprintf("%d", message.MessageID),
		}, sessionKey)
		return nil

	case "verbose":
		currentLevel := "off"
		if c.agentLoop != nil {
			currentLevel = c.agentLoop.GetVerboseLevel(sessionKey)
		}
		return c.commands.Verbose(ctx, *message, currentLevel)

	case "think":
		currentLevel := "default"
		if c.agentLoop != nil {
			currentLevel = c.agentLoop.GetThinkLevel(sessionKey)
		}
		return c.commands.Think(ctx, *message, currentLevel)

	case "model":
		args := strings.TrimSpace(strings.TrimPrefix(message.Text, "/model"))
		c.publishSystemCommand(senderID, chatID, message.MessageID, telegramCommandText("model", args), map[string]string{
			"message_id": fmt.Sprintf("%d", message.MessageID),
		}, sessionKey)
		return nil
	}

	switch cmd {
	case "help":
		return c.commands.Help(ctx, *message)
	case "start":
		return c.commands.Start(ctx, *message)
	case "show":
		return c.commands.Show(ctx, *message)
	case "list":
		return c.commands.List(ctx, *message)
	case "models":
		return c.commands.Models(ctx, *message)
	case "agent":
		args := strings.TrimSpace(strings.TrimPrefix(message.Text, "/agent"))
		if args != "" {
			c.publishSystemCommand(senderID, chatID, message.MessageID, telegramCommandText("agent", args), map[string]string{
				"message_id": fmt.Sprintf("%d", message.MessageID),
			}, sessionKey)
			return nil
		}
		return c.commands.Agent(ctx, *message)
	}

	return nil
}

func (c *TelegramChannel) sendReplyText(ctx context.Context, message *telego.Message, text string) error {
	// Convert Markdown to HTML for proper formatting
	htmlText := markdownToTelegramHTML(text)
	_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID:    telego.ChatID{ID: message.Chat.ID},
		Text:      htmlText,
		ParseMode: telego.ModeHTML,
		ReplyParameters: &telego.ReplyParameters{
			MessageID: message.MessageID,
		},
	})
	return err
}

func (c *TelegramChannel) publishSystemCommand(senderID string, chatID int64, messageID int, content string, metadata map[string]string, sessionKey string) {
	if c.bus == nil || strings.TrimSpace(content) == "" {
		return
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	if _, ok := metadata["message_id"]; !ok {
		metadata["message_id"] = fmt.Sprintf("%d", messageID)
	}

	c.bus.PublishInbound(bus.InboundMessage{
		Channel:    "system",
		SenderID:   senderID,
		ChatID:     sessionKey,
		Content:    content,
		SessionKey: sessionKey,
		Metadata:   metadata,
	})
}

func telegramCommandText(command, args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return "/" + command
	}
	return "/" + command + " " + args
}

func telegramSenderID(userID int64, username string) string {
	senderID := fmt.Sprintf("%d", userID)
	if username != "" {
		senderID = fmt.Sprintf("%d|%s", userID, username)
	}
	return senderID
}

// resolveSessionKey resolves the session key through the routing system.
// Falls back to legacy telegram:<chatID> format if agentLoop is nil.
func (c *TelegramChannel) resolveSessionKey(peerKind, peerID string) string {
	if c.agentLoop != nil {
		return c.agentLoop.ResolveRoute("telegram", peerKind, peerID)
	}
	return telegramSessionKeyLegacy(peerKind, peerID)
}

// telegramSessionKeyLegacy generates the legacy session key when the routing
// system is not available (e.g. in tests without agentLoop).
func telegramSessionKeyLegacy(peerKind, peerID string) string {
	if peerKind == "group" {
		return fmt.Sprintf("telegram:%s", peerID)
	}
	return fmt.Sprintf("telegram:%s", peerID)
}

// telegramPeerInfo extracts peer kind and peer ID from a Telegram message.
func telegramPeerInfo(message *telego.Message) (peerKind string, peerID string) {
	if message == nil {
		return "direct", ""
	}
	if message.Chat.Type != "private" {
		return "group", fmt.Sprintf("%d", message.Chat.ID)
	}
	if message.From != nil {
		return "direct", fmt.Sprintf("%d", message.From.ID)
	}
	return "direct", ""
}

func buildTelegramMetadata(messageID int, user *telego.User, chat telego.Chat) map[string]string {
	metadata := map[string]string{
		"message_id": fmt.Sprintf("%d", messageID),
	}
	if user == nil {
		return metadata
	}

	peerKind := "direct"
	peerID := fmt.Sprintf("%d", user.ID)
	if chat.Type != "private" {
		peerKind = "group"
		peerID = fmt.Sprintf("%d", chat.ID)
	}

	metadata["user_id"] = fmt.Sprintf("%d", user.ID)
	metadata["username"] = user.Username
	metadata["first_name"] = user.FirstName
	metadata["is_group"] = fmt.Sprintf("%t", chat.Type != "private")
	metadata["peer_kind"] = peerKind
	metadata["peer_id"] = peerID
	return metadata
}
