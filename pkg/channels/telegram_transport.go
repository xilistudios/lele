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

// thinkingCancel is the handle to a running typing-indicator loop.
//
// doneChan is receive-only on purpose: the loop owns the close, every other
// party may only wait on it (Cancel does, with a timeout, so a wedged loop can
// never wedge the sweeper with it).
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

// typingIndicatorInterval is how often the typing chat action is refreshed.
// Telegram renders "typing..." for ~5s after each call, so 4s keeps it alive
// without visible gaps.
const typingIndicatorInterval = 4 * time.Second

// typingIndicatorMaxLifetime is the hard ceiling of a single typing indicator
// loop: a backstop against an indicator that outlives its turn because every
// terminal signal was lost (crash mid-turn, event dropped, channel restarted).
//
// 30 minutes, not 5: long turns are legitimate here — many tool iterations,
// context compactions, and subagents that may themselves run for 30 minutes.
// When the TTL expires the failure is deliberately silent (warning log only,
// no user-facing message): losing "typing..." on a turn that is still running
// is far less harmful than an indicator that never stops.
const typingIndicatorMaxLifetime = 30 * time.Minute

// runTypingLoop keeps a user-visible "working" state alive until the turn ends
// or the TTL expires, whichever comes first.
//
// send is invoked immediately and then once per interval; returning an error
// does not stop the loop (a transient Bot API failure must not kill the
// indicator for the rest of a long turn) — the caller decides how to log it.
//
// The returned done channel is closed when the loop exits, for any reason.
// onExpire runs exactly once and only on the TTL path, so a caller that
// cancels normally never sees it.
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
				// TTL is terminal: report it and leave. The loop owns its own
				// exit, so thinkingCancel.Cancel() stays correct — doneChan is
				// closed here and the (outer) context cancel is a no-op.
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

	// On expiry the loop exits on its own; cancelling here additionally frees
	// the context and releases its resources (and keeps thinkingCancel.Cancel
	// consistent with the normal path).
	onExpire := func() {
		logger.WarnCF("telegram", "Typing indicator TTL expired", map[string]interface{}{
			"chat_id":  chatID,
			"ttl":      typingIndicatorMaxLifetime.String(),
			"interval": typingIndicatorInterval.String(),
		})
		cancel()
	}

	// send runs exclusively on the loop goroutine, so attempts needs no lock.
	// The original implementation logged the first failure at error level and
	// refresh failures at debug; that distinction is preserved.
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

// clearTransientTurnState removes everything the user could still perceive as
// "the bot is still working on my message": the typing indicator loop(s) for
// the chat and the pending "Thinking... 💭" placeholder message.
//
// chatKey is the chat ID as a string (the same representation used as the map
// key when the indicator/placeholder was created); messageID is the originating
// user message ID and may be empty, in which case only the per-chat sweep runs.
//
// It is idempotent and never fails: calling it twice, or after the final
// message already performed the cleanup, is a no-op that sends nothing.
// Deleting the placeholder requires the numeric chat ID for the Bot API call;
// when it cannot be parsed the indicator is still stopped and the placeholder
// entry is left for the normal send path to resolve.
func (c *TelegramChannel) clearTransientTurnState(chatKey, messageID string) {
	if chatKey == "" {
		return
	}

	// Exact key used when the indicator was started:
	// fmt.Sprintf("%d:%d", chatID, messageID), i.e. "<chatID>:<user message id>".
	if messageID != "" {
		c.stopActiveThinking(chatKey + ":" + messageID)
	}
	// Safety net: cancel any indicator left for this chat (concurrent messages,
	// error paths, or an indicator stored under a different message id).
	c.stopAllThinkingForChat(chatKey)

	c.clearPlaceholderForChat(chatKey)
}

// clearPlaceholderForChat deletes the pending "Thinking... 💭" placeholder of a
// chat, if any, and forgets it. Best-effort: when the chat ID is not numeric or
// the stored value is not a message ID, nothing is removed, so the regular send
// path can still resolve the placeholder into the real answer.
func (c *TelegramChannel) clearPlaceholderForChat(chatKey string) {
	// Deletion needs the bot token from config; without it the entry is left
	// in place so the regular send path can still resolve the placeholder.
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

// finishTurn handles the terminal turn.end signal from the agent loop.
// Dedicated to keep Send readable: the signal carries no content, so all it
// does is drop the transient per-turn state of the chat.
func (c *TelegramChannel) finishTurn(chatKey, messageID string) {
	c.clearTransientTurnState(chatKey, messageID)
}

// ConsumesEvent declares the events Telegram interprets inside Send. Only
// turn.end is listed: it carries no content, so without this declaration the
// dispatcher's contentless-signal guard would drop it and the typing indicator
// would never stop — the exact bug #240 is about.
//
// Adding an event to Send() without adding it here makes the signal
// undeliverable, which the companion test detects structurally.
func (c *TelegramChannel) ConsumesEvent(event string) bool {
	return event == "turn.end"
}

func (c *TelegramChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	// turn.end: the agent loop's guaranteed terminal signal for a turn. It is
	// a cleanup signal, not content, so it is handled FIRST — before the
	// running guard, chat-ID parsing and rate limiting: cleanup must not
	// depend on any of them.
	//
	// Processing it even when the channel is already stopped is safe and
	// desirable. Stop() sweeps typing state and placeholders on its way out,
	// so finishTurn normally finds nothing to do and is an idempotent no-op
	// that sends nothing to Telegram; and when the sweep did not catch this
	// turn, this is precisely the last chance to clean it up. Rejecting the
	// signal instead would leave the state behind AND log a misleading
	// "Error sending message to channel" during shutdown. The call itself
	// cannot fail loudly: deleteMessage is best-effort (debug log, no error).
	if msg.Event == "turn.end" {
		// metadata["message_id"] carries the originating user message id, so
		// the exact "<chat>:<msg>" indicator key can be cancelled directly.
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
		// Always sweep all thinking entries for this chat as a safety net.
		// This catches orphaned indicators from concurrent messages, error
		// paths, or any edge case where the specific key was not stored.
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
			logger.WarnCF("telegram", "Failed to edit placeholder, deleting and sending new message", map[string]interface{}{
				"error":          err.Error(),
				"placeholder_id": pID,
			})
			// Delete the stale "Thinking... 💭" placeholder so the user
			// doesn't see it sitting above the real response.
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
		// Edit failed — delete the stale placeholder before sending a new message.
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

// deleteHTTPClient returns the HTTP client used by deleteMessage. It defaults
// to http.DefaultClient; tests may override deleteHTTP to keep the call local.
func (c *TelegramChannel) deleteHTTPClient() *http.Client {
	if c.deleteHTTP != nil {
		return c.deleteHTTP
	}
	return http.DefaultClient
}

// deleteMessage deletes a Telegram message by chat ID and message ID.
// Best-effort: errors are logged but not returned.
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
