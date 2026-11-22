package channels

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/utils"
)

type thinkingCancel struct {
	fn       context.CancelFunc
	doneChan <-chan struct{}
}

func (c *thinkingCancel) Cancel() {
	if c != nil && c.fn != nil {
		c.fn()
		if c.doneChan != nil {
			select {
			case <-c.doneChan:
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}

const typingIndicatorInterval = 4 * time.Second

const typingIndicatorMaxLifetime = 30 * time.Minute

func runTypingLoop(ctx context.Context, interval, ttl time.Duration, send func(context.Context) error, onExpire func()) (done <-chan struct{}) {
	doneChan := make(chan struct{})

	go func() {
		defer close(doneChan)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		expiry := time.NewTimer(ttl)
		defer expiry.Stop()

		if send != nil {
			_ = send(ctx)
		}

		for {
			select {
			case <-ticker.C:
				if send != nil {
					_ = send(ctx)
				}
			case <-expiry.C:
				if onExpire != nil {
					onExpire()
				}
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	return doneChan
}

func (c *TelegramChannel) startTypingIndicator(chatID int64) *thinkingCancel {
	ctx, cancel := context.WithCancel(context.Background())

	onExpire := func() {
		logger.WarnCF("telegram", "Typing indicator TTL expired", map[string]interface{}{
			"chat_id":  chatID,
			"ttl":      typingIndicatorMaxLifetime.String(),
			"interval": typingIndicatorInterval.String(),
		})
		cancel()
	}

	attempts := 0
	send := func(ctx context.Context) error {
		err := c.bot.SendChatAction(ctx, tu.ChatAction(tu.ID(chatID), telego.ChatActionTyping))
		if err != nil {
			if attempts == 0 {
				logger.ErrorCF("telegram", "Failed to send initial chat action", map[string]interface{}{
					"error": err.Error(),
				})
			} else {
				logger.DebugCF("telegram", "Failed to send chat action", map[string]interface{}{
					"error": err.Error(),
				})
			}
		}
		attempts++
		return err
	}

	done := runTypingLoop(ctx, typingIndicatorInterval, typingIndicatorMaxLifetime, send, onExpire)

	return &thinkingCancel{fn: cancel, doneChan: done}
}

func (c *TelegramChannel) stopActiveThinking(thinkingKey string) {
	if stop, ok := c.stopThinking.Load(thinkingKey); ok {
		if cf, ok := stop.(*thinkingCancel); ok && cf != nil {
			cf.Cancel()
		}
		c.stopThinking.Delete(thinkingKey)
	}
}

func (c *TelegramChannel) stopAllThinkingForChat(chatID string) {
	c.stopThinking.Range(func(key, value interface{}) bool {
		keyStr, ok := key.(string)
		if !ok {
			return true
		}
		if strings.HasPrefix(keyStr, chatID+":") {
			if cf, ok := value.(*thinkingCancel); ok && cf != nil {
				cf.Cancel()
			}
			c.stopThinking.Delete(key)
		}
		return true
	})
}

func (c *TelegramChannel) clearTransientTurnState(chatKey, messageID string) {
	if chatKey == "" {
		return
	}

	if messageID != "" {
		c.stopActiveThinking(chatKey + ":" + messageID)
	}
	c.stopAllThinkingForChat(chatKey)

	c.clearPlaceholderForChat(chatKey)
}

func (c *TelegramChannel) clearPlaceholderForChat(chatKey string) {
	if c.config == nil {
		return
	}
	pID, ok := c.placeholders.Load(chatKey)
	if !ok {
		return
	}
	chatID, err := parseChatID(chatKey)
	if err != nil {
		return
	}
	id, isInt := pID.(int)
	if !isInt {
		return
	}
	c.placeholders.Delete(chatKey)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.deleteMessage(ctx, chatID, id)
}

func (c *TelegramChannel) finishTurn(chatKey, messageID string) {
	c.clearTransientTurnState(chatKey, messageID)
}

func (c *TelegramChannel) ConsumesEvent(event string) bool {
	return event == "turn.end"
}

func (c *TelegramChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if msg.Event == "turn.end" {
		var turnMsgID string
		if msg.Metadata != nil {
			turnMsgID = msg.Metadata["message_id"]
		}
		c.finishTurn(msg.ChatID, turnMsgID)
		return nil
	}

	if !c.IsRunning() {
		return fmt.Errorf("telegram bot not running")
	}

	chatID, err := parseChatID(msg.ChatID)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	if err := c.waitRateLimit(ctx); err != nil {
		return err
	}

	if !msg.IsIntermediate {
		var thinkingKey string
		if msg.ReplyTo != "" {
			thinkingKey = fmt.Sprintf("%s:%s", msg.ChatID, msg.ReplyTo)
		} else if msg.MessageID != "" {
			thinkingKey = fmt.Sprintf("%s:%s", msg.ChatID, msg.MessageID)
		}
		if thinkingKey != "" {
			c.stopActiveThinking(thinkingKey)
		}
		c.stopAllThinkingForChat(msg.ChatID)
	}

	if len(msg.Attachments) > 0 {
		if strings.TrimSpace(msg.Content) != "" {
			if err := c.sendTextMessage(ctx, chatID, msg); err != nil {
				return err
			}
		} else {
			c.resolvePlaceholderWithText(ctx, chatID, msg.ChatID, "Attached file(s).")
		}

		for _, attachment := range msg.Attachments {
			if err := c.sendDocument(ctx, chatID, msg.ReplyTo, attachment); err != nil {
				return err
			}
		}
		return nil
	}

	return c.sendTextMessage(ctx, chatID, msg)
}

func (c *TelegramChannel) sendTextMessage(ctx context.Context, chatID int64, msg bus.OutboundMessage) error {
	textMode := TextMode(msg.TextMode)
	if textMode == "" {
		textMode = TextModeMarkdown
	}

	htmlContent := renderTelegramText(msg.Content, textMode)

	fallbackText := msg.PlainText
	if fallbackText == "" {
		fallbackText = msg.Content
	}

	linkPreviewEnabled := true
	if msg.LinkPreview != nil {
		linkPreviewEnabled = *msg.LinkPreview
	}

	err := c.sendFormattedText(ctx, chatID, msg, htmlContent, linkPreviewEnabled)
	if err == nil {
		return nil
	}

	if isTelegramParseError(err) {
		logger.ErrorCF("telegram", "HTML parse failed, falling back to plain text", map[string]interface{}{
			"error": err.Error(),
		})
		return c.sendPlainTextFallback(ctx, chatID, msg, fallbackText, linkPreviewEnabled)
	}

	return err
}

func (c *TelegramChannel) sendFormattedText(ctx context.Context, chatID int64, msg bus.OutboundMessage, htmlContent string, linkPreview bool) error {
	logger.DebugCF("telegram", "sendFormattedText called", map[string]interface{}{
		"chat_id":         chatID,
		"msg_chat_id":     msg.ChatID,
		"has_placeholder": c.hasPlaceholder(msg.ChatID),
	})

	if pID, ok := c.placeholders.Load(msg.ChatID); ok {
		c.placeholders.Delete(msg.ChatID)
		editMsg := tu.EditMessageText(tu.ID(chatID), pID.(int), htmlContent)
		editMsg.ParseMode = telego.ModeHTML
		editMsg.LinkPreviewOptions = &telego.LinkPreviewOptions{
			IsDisabled: !linkPreview,
		}

		if _, err := c.bot.EditMessageText(ctx, editMsg); err == nil {
			logger.DebugCF("telegram", "Placeholder edited successfully", map[string]interface{}{
				"placeholder_id": pID,
			})
			return nil
		} else {
			logger.WarnCF("telegram", "Failed to edit placeholder, deleting and sending new message", map[string]interface{}{
				"error":          err.Error(),
				"placeholder_id": pID,
			})
			c.deleteMessage(ctx, chatID, pID.(int))
		}
	}

	tgMsg := tu.Message(tu.ID(chatID), htmlContent)
	tgMsg.ParseMode = telego.ModeHTML
	tgMsg.LinkPreviewOptions = &telego.LinkPreviewOptions{
		IsDisabled: !linkPreview,
	}

	if msg.ReplyTo != "" {
		if replyMsgID, parseErr := strconv.Atoi(msg.ReplyTo); parseErr == nil {
			tgMsg.ReplyParameters = &telego.ReplyParameters{
				MessageID: replyMsgID,
			}
		}
	}

	if msg.ReplyMarkup != nil {
		if markup, ok := msg.ReplyMarkup.(*telego.InlineKeyboardMarkup); ok {
			tgMsg.ReplyMarkup = markup
		}
	}

	_, err := c.bot.SendMessage(ctx, tgMsg)
	return TelegramRateLimitError(err)
}

func (c *TelegramChannel) sendPlainTextFallback(ctx context.Context, chatID int64, msg bus.OutboundMessage, plainText string, linkPreview bool) error {
	if pID, ok := c.placeholders.Load(msg.ChatID); ok {
		c.placeholders.Delete(msg.ChatID)
		editMsg := tu.EditMessageText(tu.ID(chatID), pID.(int), plainText)
		editMsg.ParseMode = ""
		editMsg.LinkPreviewOptions = &telego.LinkPreviewOptions{
			IsDisabled: !linkPreview,
		}

		if _, err := c.bot.EditMessageText(ctx, editMsg); err == nil {
			return nil
		}
		c.deleteMessage(ctx, chatID, pID.(int))
	}

	tgMsg := tu.Message(tu.ID(chatID), plainText)
	tgMsg.ParseMode = ""
	tgMsg.LinkPreviewOptions = &telego.LinkPreviewOptions{
		IsDisabled: !linkPreview,
	}

	if msg.ReplyTo != "" {
		if replyMsgID, parseErr := strconv.Atoi(msg.ReplyTo); parseErr == nil {
			tgMsg.ReplyParameters = &telego.ReplyParameters{
				MessageID: replyMsgID,
			}
		}
	}

	if msg.ReplyMarkup != nil {
		if markup, ok := msg.ReplyMarkup.(*telego.InlineKeyboardMarkup); ok {
			tgMsg.ReplyMarkup = markup
		}
	}

	_, err := c.bot.SendMessage(ctx, tgMsg)
	return TelegramRateLimitError(err)
}

func (c *TelegramChannel) hasPlaceholder(chatID string) bool {
	_, ok := c.placeholders.Load(chatID)
	return ok
}

func (c *TelegramChannel) resolvePlaceholderWithText(ctx context.Context, chatID int64, chatKey, content string) bool {
	if pID, ok := c.placeholders.Load(chatKey); ok {
		c.placeholders.Delete(chatKey)
		htmlContent := markdownToTelegramHTML(content)
		editMsg := tu.EditMessageText(tu.ID(chatID), pID.(int), htmlContent)
		editMsg.ParseMode = telego.ModeHTML
		if _, err := c.bot.EditMessageText(ctx, editMsg); err != nil {
			logger.WarnCF("telegram", "Failed to edit placeholder in resolvePlaceholderWithText", map[string]interface{}{
				"error":          err.Error(),
				"placeholder_id": pID,
			})
			c.deleteMessage(ctx, chatID, pID.(int))
		}
		return true
	}
	return false
}

func (c *TelegramChannel) deleteHTTPClient() *http.Client {
	if c.deleteHTTP != nil {
		return c.deleteHTTP
	}
	return http.DefaultClient
}

func (c *TelegramChannel) deleteMessage(ctx context.Context, chatID int64, messageID int) {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/deleteMessage", c.config.Channels.Telegram.Token)
	body := fmt.Sprintf(`{"chat_id":%d,"message_id":%d}`, chatID, messageID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		logger.DebugCF("telegram", "Failed to create deleteMessage request", map[string]interface{}{"error": err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.deleteHTTPClient().Do(req)
	if err != nil {
		logger.DebugCF("telegram", "Failed to delete message", map[string]interface{}{"error": err.Error(), "message_id": messageID})
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		logger.DebugCF("telegram", "deleteMessage returned non-OK status", map[string]interface{}{
			"status":     resp.StatusCode,
			"message_id": messageID,
		})
	}
}

func (c *TelegramChannel) sendDocument(ctx context.Context, chatID int64, replyTo string, attachment bus.FileAttachment) error {
	file, err := os.Open(attachment.Path)
	if err != nil {
		return fmt.Errorf("open attachment %s: %w", attachment.Path, err)
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if replyTo != "" {
		_ = writer.WriteField("reply_to_message_id", replyTo)
	}

	part, err := writer.CreateFormFile("document", telegramAttachmentName(attachment))
	if err != nil {
		return fmt.Errorf("create telegram multipart file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("copy telegram attachment: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close telegram multipart writer: %w", err)
	}

	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", c.config.Channels.Telegram.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return fmt.Errorf("create telegram request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send telegram attachment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("telegram attachment upload failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	return nil
}

func telegramAttachmentName(attachment bus.FileAttachment) string {
	if attachment.Name != "" {
		return attachment.Name
	}
	if attachment.Path != "" {
		return filepath.Base(attachment.Path)
	}
	return "attachment"
}

func (c *TelegramChannel) downloadPhoto(ctx context.Context, fileID string) string {
	file, err := c.bot.GetFile(ctx, &telego.GetFileParams{FileID: fileID})
	if err != nil {
		logger.ErrorCF("telegram", "Failed to get photo file", map[string]interface{}{
			"error": err.Error(),
		})
		return ""
	}

	return c.downloadFileWithInfo(file, ".jpg")
}

func (c *TelegramChannel) downloadFileWithInfo(file *telego.File, ext string) string {
	if file.FilePath == "" {
		return ""
	}

	url := c.bot.FileDownloadURL(file.FilePath)
	logger.DebugCF("telegram", "File URL", map[string]interface{}{"url": url})

	filename := file.FilePath + ext
	return utils.DownloadFile(url, filename, utils.DownloadOptions{
		LoggerPrefix: "telegram",
	})
}

func (c *TelegramChannel) downloadFile(ctx context.Context, fileID, ext string) string {
	file, err := c.bot.GetFile(ctx, &telego.GetFileParams{FileID: fileID})
	if err != nil {
		logger.ErrorCF("telegram", "Failed to get file", map[string]interface{}{
			"error": err.Error(),
		})
		return ""
	}

	return c.downloadFileWithInfo(file, ext)
}

func parseChatID(chatIDStr string) (int64, error) {
	var id int64
	_, err := fmt.Sscanf(chatIDStr, "%d", &id)
	return id, err
}
