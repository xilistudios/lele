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
	doneChan chan struct{}
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

func (c *TelegramChannel) startTypingIndicator(chatID int64) *thinkingCancel {
	ctx, cancel := context.WithCancel(context.Background())
	doneChan := make(chan struct{})
	ticker := time.NewTicker(4 * time.Second)

	if err := c.bot.SendChatAction(ctx, tu.ChatAction(tu.ID(chatID), telego.ChatActionTyping)); err != nil {
		logger.ErrorCF("telegram", "Failed to send initial chat action", map[string]interface{}{
			"error": err.Error(),
		})
	}

	go func() {
		defer close(doneChan)
		for {
			select {
			case <-ticker.C:
				if err := c.bot.SendChatAction(ctx, tu.ChatAction(tu.ID(chatID), telego.ChatActionTyping)); err != nil {
					logger.DebugCF("telegram", "Failed to send chat action", map[string]interface{}{
						"error": err.Error(),
					})
				}
			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()

	return &thinkingCancel{fn: cancel, doneChan: doneChan}
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

func (c *TelegramChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
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
		} else {
			c.stopAllThinkingForChat(msg.ChatID)
		}
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

// sendTextMessage sends a text message to Telegram with proper formatting.
// It handles:
// - Markdown to HTML conversion (default)
// - Direct HTML mode
// - Automatic fallback to plain text if HTML parsing fails
// - Link preview control
func (c *TelegramChannel) sendTextMessage(ctx context.Context, chatID int64, msg bus.OutboundMessage) error {
	// Determine text mode (default: markdown)
	textMode := TextMode(msg.TextMode)
	if textMode == "" {
		textMode = TextModeMarkdown
	}

	// Render the text based on mode
	htmlContent := renderTelegramText(msg.Content, textMode)

	// Determine fallback text (use PlainText if provided, otherwise use original content)
	fallbackText := msg.PlainText
	if fallbackText == "" {
		fallbackText = msg.Content
	}

	// Check if we should disable link previews (default: enabled)
	linkPreviewEnabled := true
	if msg.LinkPreview != nil {
		linkPreviewEnabled = *msg.LinkPreview
	}

	// Try to send with HTML formatting
	err := c.sendFormattedText(ctx, chatID, msg, htmlContent, linkPreviewEnabled)
	if err == nil {
		return nil
	}

	// If it's a parse error, try fallback to plain text
	if isTelegramParseError(err) {
		logger.ErrorCF("telegram", "HTML parse failed, falling back to plain text", map[string]interface{}{
			"error": err.Error(),
		})
		return c.sendPlainTextFallback(ctx, chatID, msg, fallbackText, linkPreviewEnabled)
	}

	return err
}

// sendFormattedText sends text with HTML formatting
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
			logger.WarnCF("telegram", "Failed to edit placeholder, will send new message", map[string]interface{}{
				"error":          err.Error(),
				"placeholder_id": pID,
			})
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
	return err
}

// sendPlainTextFallback sends text without any formatting
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
	return err
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
		_, _ = c.bot.EditMessageText(ctx, editMsg)
		return true
	}
	return false
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
