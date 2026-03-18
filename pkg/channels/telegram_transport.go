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
	fn context.CancelFunc
}

func (c *thinkingCancel) Cancel() {
	if c != nil && c.fn != nil {
		c.fn()
	}
}

func (c *TelegramChannel) startTypingIndicator(chatID int64) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	ticker := time.NewTicker(4 * time.Second)

	if err := c.bot.SendChatAction(ctx, tu.ChatAction(tu.ID(chatID), telego.ChatActionTyping)); err != nil {
		logger.ErrorCF("telegram", "Failed to send initial chat action", map[string]interface{}{
			"error": err.Error(),
		})
	}

	go func() {
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

	return cancel
}

func (c *TelegramChannel) stopActiveThinking(chatKey string) {
	if stop, ok := c.stopThinking.Load(chatKey); ok {
		if cf, ok := stop.(*thinkingCancel); ok && cf != nil {
			cf.Cancel()
		}
		c.stopThinking.Delete(chatKey)
	}
}

func (c *TelegramChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("telegram bot not running")
	}

	chatID, err := parseChatID(msg.ChatID)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	if !msg.IsIntermediate {
		c.stopActiveThinking(msg.ChatID)
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
	htmlContent := markdownToTelegramHTML(msg.Content)

	if pID, ok := c.placeholders.Load(msg.ChatID); ok {
		c.placeholders.Delete(msg.ChatID)
		editMsg := tu.EditMessageText(tu.ID(chatID), pID.(int), htmlContent)
		editMsg.ParseMode = telego.ModeHTML

		if _, err := c.bot.EditMessageText(ctx, editMsg); err == nil {
			return nil
		}
	}

	tgMsg := tu.Message(tu.ID(chatID), htmlContent)
	tgMsg.ParseMode = telego.ModeHTML

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

	if _, err := c.bot.SendMessage(ctx, tgMsg); err != nil {
		logger.ErrorCF("telegram", "HTML parse failed, falling back to plain text", map[string]interface{}{
			"error": err.Error(),
		})
		tgMsg.ParseMode = ""
		_, err = c.bot.SendMessage(ctx, tgMsg)
		return err
	}

	return nil
}

func (c *TelegramChannel) resolvePlaceholderWithText(ctx context.Context, chatID int64, chatKey, content string) bool {
	if pID, ok := c.placeholders.Load(chatKey); ok {
		c.placeholders.Delete(chatKey)
		editMsg := tu.EditMessageText(tu.ID(chatID), pID.(int), markdownToTelegramHTML(content))
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
