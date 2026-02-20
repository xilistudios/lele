package channels

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	th "github.com/mymmrac/telego/telegohandler"

	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/utils"
	"github.com/sipeed/picoclaw/pkg/voice"
)

type TelegramChannel struct {
	*BaseChannel
	bot          *telego.Bot
	commands     TelegramCommander
	config       *config.Config
	chatIDs      map[string]int64
	transcriber  *voice.GroqTranscriber
	placeholders sync.Map // chatID -> messageID
	stopThinking sync.Map // chatID -> thinkingCancel
}

type thinkingCancel struct {
	fn context.CancelFunc
}

func (c *thinkingCancel) Cancel() {
	if c != nil && c.fn != nil {
		c.fn()
	}
}

func NewTelegramChannel(cfg *config.Config, bus *bus.MessageBus) (*TelegramChannel, error) {
	var opts []telego.BotOption
	telegramCfg := cfg.Channels.Telegram

	if telegramCfg.Proxy != "" {
		proxyURL, parseErr := url.Parse(telegramCfg.Proxy)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", telegramCfg.Proxy, parseErr)
		}
		opts = append(opts, telego.WithHTTPClient(&http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			},
		}))
	}

	bot, err := telego.NewBot(telegramCfg.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	base := NewBaseChannel("telegram", telegramCfg, bus, telegramCfg.AllowFrom)

	return &TelegramChannel{
		BaseChannel:  base,
		commands:     NewTelegramCommands(bot, cfg),
		bot:          bot,
		config:       cfg,
		chatIDs:      make(map[string]int64),
		transcriber:  nil,
		placeholders: sync.Map{},
		stopThinking: sync.Map{},
	}, nil
}

func (c *TelegramChannel) SetTranscriber(transcriber *voice.GroqTranscriber) {
	c.transcriber = transcriber
}

func (c *TelegramChannel) Start(ctx context.Context) error {
	logger.InfoC("telegram", "Starting Telegram bot (polling mode)...")

	updates, err := c.bot.UpdatesViaLongPolling(ctx, &telego.GetUpdatesParams{
		Timeout: 30,
	})
	if err != nil {
		return fmt.Errorf("failed to start long polling: %w", err)
	}

	bh, err := telegohandler.NewBotHandler(c.bot, updates)
	if err != nil {
		return fmt.Errorf("failed to create bot handler: %w", err)
	}

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		c.commands.Help(ctx, message)
		return nil
	}, th.CommandEqual("help"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.commands.Start(ctx, message)
	}, th.CommandEqual("start"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.commands.Show(ctx, message)
	}, th.CommandEqual("show"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.commands.List(ctx, message)
	}, th.CommandEqual("list"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.commands.Models(ctx, message)
	}, th.CommandEqual("models"))
	bh.HandleCallbackQuery(func(ctx *th.Context, query telego.CallbackQuery) error {
		return c.handleModelsCallback(ctx, query)
	}, th.AnyCallbackQueryWithMessage(), th.CallbackDataPrefix("models:"))

	err = c.bot.SetMyCommands(ctx, &telego.SetMyCommandsParams{
		Commands: []telego.BotCommand{
			{Command: "new", Description: "Start a new conversation"},
			{Command: "stop", Description: "Stop the agent"},
			{Command: "model", Description: "Show models and change current model"},
			{Command: "models", Description: "Select provider/model from UI"},
			{Command: "status", Description: "Show model, tokens and gateway version"},
			{Command: "subagents", Description: "List and manage running subagents"},
		},
	})
	if err != nil {
		logger.WarnCF("telegram", "Failed to set Telegram command menu", map[string]interface{}{
			"error": err.Error(),
		})
	}

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleMessage(ctx, &message)
	}, th.AnyMessage())

	c.setRunning(true)
	logger.InfoCF("telegram", "Telegram bot connected", map[string]interface{}{
		"username": c.bot.Username(),
	})

	go bh.Start()

	go func() {
		<-ctx.Done()
		bh.Stop()
	}()

	return nil
}
func (c *TelegramChannel) Stop(ctx context.Context) error {
	logger.InfoC("telegram", "Stopping Telegram bot...")
	c.setRunning(false)
	return nil
}

func (c *TelegramChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("telegram bot not running")
	}

	chatID, err := parseChatID(msg.ChatID)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	// Stop thinking animation
	if stop, ok := c.stopThinking.Load(msg.ChatID); ok {
		if cf, ok := stop.(*thinkingCancel); ok && cf != nil {
			cf.Cancel()
		}
		c.stopThinking.Delete(msg.ChatID)
	}

	htmlContent := markdownToTelegramHTML(msg.Content)

	// Try to edit placeholder
	if pID, ok := c.placeholders.Load(msg.ChatID); ok {
		c.placeholders.Delete(msg.ChatID)
		editMsg := tu.EditMessageText(tu.ID(chatID), pID.(int), htmlContent)
		editMsg.ParseMode = telego.ModeHTML

		if _, err = c.bot.EditMessageText(ctx, editMsg); err == nil {
			return nil
		}
		// Fallback to new message if edit fails
	}

	tgMsg := tu.Message(tu.ID(chatID), htmlContent)
	tgMsg.ParseMode = telego.ModeHTML

	if _, err = c.bot.SendMessage(ctx, tgMsg); err != nil {
		logger.ErrorCF("telegram", "HTML parse failed, falling back to plain text", map[string]interface{}{
			"error": err.Error(),
		})
		tgMsg.ParseMode = ""
		_, err = c.bot.SendMessage(ctx, tgMsg)
		return err
	}

	return nil
}

func (c *TelegramChannel) handleMessage(ctx context.Context, message *telego.Message) error {
	if message == nil {
		return fmt.Errorf("message is nil")
	}

	user := message.From
	if user == nil {
		return fmt.Errorf("message sender (user) is nil")
	}

	senderID := fmt.Sprintf("%d", user.ID)
	if user.Username != "" {
		senderID = fmt.Sprintf("%d|%s", user.ID, user.Username)
	}

	// 检查白名单，避免为被拒绝的用户下载附件
	if !c.IsAllowed(senderID) {
		logger.DebugCF("telegram", "Message rejected by allowlist", map[string]interface{}{
			"user_id": senderID,
		})
		return nil
	}

	chatID := message.Chat.ID
	c.chatIDs[senderID] = chatID

	content := ""
	mediaPaths := []string{}
	localFiles := []string{} // 跟踪需要清理的本地文件

	// 确保临时文件在函数返回时被清理
	defer func() {
		for _, file := range localFiles {
			if err := os.Remove(file); err != nil {
				logger.DebugCF("telegram", "Failed to cleanup temp file", map[string]interface{}{
					"file":  file,
					"error": err.Error(),
				})
			}
		}
	}()

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
			localFiles = append(localFiles, photoPath)
			mediaPaths = append(mediaPaths, photoPath)
			if content != "" {
				content += "\n"
			}
			content += "[image: photo]"
		}
	}

	if message.Voice != nil {
		voicePath := c.downloadFile(ctx, message.Voice.FileID, ".ogg")
		if voicePath != "" {
			localFiles = append(localFiles, voicePath)
			mediaPaths = append(mediaPaths, voicePath)

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
			localFiles = append(localFiles, audioPath)
			mediaPaths = append(mediaPaths, audioPath)
			if content != "" {
				content += "\n"
			}
			content += "[audio]"
		}
	}

	if message.Document != nil {
		docPath := c.downloadFile(ctx, message.Document.FileID, "")
		if docPath != "" {
			localFiles = append(localFiles, docPath)
			mediaPaths = append(mediaPaths, docPath)
			if content != "" {
				content += "\n"
			}
			content += "[file]"
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

	// Thinking indicator
	err := c.bot.SendChatAction(ctx, tu.ChatAction(tu.ID(chatID), telego.ChatActionTyping))
	if err != nil {
		logger.ErrorCF("telegram", "Failed to send chat action", map[string]interface{}{
			"error": err.Error(),
		})
	}

	// Stop any previous thinking animation
	chatIDStr := fmt.Sprintf("%d", chatID)
	if prevStop, ok := c.stopThinking.Load(chatIDStr); ok {
		if cf, ok := prevStop.(*thinkingCancel); ok && cf != nil {
			cf.Cancel()
		}
	}

	// Create cancel function for thinking state
	_, thinkCancel := context.WithTimeout(ctx, 5*time.Minute)
	c.stopThinking.Store(chatIDStr, &thinkingCancel{fn: thinkCancel})

	pMsg, err := c.bot.SendMessage(ctx, tu.Message(tu.ID(chatID), "Thinking... 💭"))
	if err == nil {
		pID := pMsg.MessageID
		c.placeholders.Store(chatIDStr, pID)
	}

	peerKind := "direct"
	peerID := fmt.Sprintf("%d", user.ID)
	if message.Chat.Type != "private" {
		peerKind = "group"
		peerID = fmt.Sprintf("%d", chatID)
	}

	metadata := map[string]string{
		"message_id": fmt.Sprintf("%d", message.MessageID),
		"user_id":    fmt.Sprintf("%d", user.ID),
		"username":   user.Username,
		"first_name": user.FirstName,
		"is_group":   fmt.Sprintf("%t", message.Chat.Type != "private"),
		"peer_kind":  peerKind,
		"peer_id":    peerID,
	}

	c.HandleMessage(fmt.Sprintf("%d", user.ID), fmt.Sprintf("%d", chatID), content, mediaPaths, metadata)
	return nil
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

	// Use FilePath as filename for better identification
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

func markdownToTelegramHTML(text string) string {
	if text == "" {
		return ""
	}

	codeBlocks := extractCodeBlocks(text)
	text = codeBlocks.text

	inlineCodes := extractInlineCodes(text)
	text = inlineCodes.text

	text = regexp.MustCompile(`^#{1,6}\s+(.+)$`).ReplaceAllString(text, "$1")

	text = regexp.MustCompile(`^>\s*(.*)$`).ReplaceAllString(text, "$1")

	text = escapeHTML(text)

	text = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`).ReplaceAllString(text, `<a href="$2">$1</a>`)

	text = regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllString(text, "<b>$1</b>")

	text = regexp.MustCompile(`__(.+?)__`).ReplaceAllString(text, "<b>$1</b>")

	reItalic := regexp.MustCompile(`_([^_]+)_`)
	text = reItalic.ReplaceAllStringFunc(text, func(s string) string {
		match := reItalic.FindStringSubmatch(s)
		if len(match) < 2 {
			return s
		}
		return "<i>" + match[1] + "</i>"
	})

	text = regexp.MustCompile(`~~(.+?)~~`).ReplaceAllString(text, "<s>$1</s>")

	text = regexp.MustCompile(`^[-*]\s+`).ReplaceAllString(text, "• ")

	for i, code := range inlineCodes.codes {
		escaped := escapeHTML(code)
		text = strings.ReplaceAll(text, fmt.Sprintf("\x00IC%d\x00", i), fmt.Sprintf("<code>%s</code>", escaped))
	}

	for i, code := range codeBlocks.codes {
		escaped := escapeHTML(code)
		text = strings.ReplaceAll(text, fmt.Sprintf("\x00CB%d\x00", i), fmt.Sprintf("<pre><code>%s</code></pre>", escaped))
	}

	return text
}

type codeBlockMatch struct {
	text  string
	codes []string
}

func extractCodeBlocks(text string) codeBlockMatch {
	re := regexp.MustCompile("```[\\w]*\\n?([\\s\\S]*?)```")
	matches := re.FindAllStringSubmatch(text, -1)

	codes := make([]string, 0, len(matches))
	for _, match := range matches {
		codes = append(codes, match[1])
	}

	i := 0
	text = re.ReplaceAllStringFunc(text, func(m string) string {
		placeholder := fmt.Sprintf("\x00CB%d\x00", i)
		i++
		return placeholder
	})

	return codeBlockMatch{text: text, codes: codes}
}

type inlineCodeMatch struct {
	text  string
	codes []string
}

func extractInlineCodes(text string) inlineCodeMatch {
	re := regexp.MustCompile("`([^`]+)`")
	matches := re.FindAllStringSubmatch(text, -1)

	codes := make([]string, 0, len(matches))
	for _, match := range matches {
		codes = append(codes, match[1])
	}

	i := 0
	text = re.ReplaceAllStringFunc(text, func(m string) string {
		placeholder := fmt.Sprintf("\x00IC%d\x00", i)
		i++
		return placeholder
	})

	return inlineCodeMatch{text: text, codes: codes}
}

func escapeHTML(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}

func (c *TelegramChannel) handleModelsCallback(ctx context.Context, query telego.CallbackQuery) error {
	if query.Message == nil {
		return nil
	}
	parts := strings.SplitN(query.Data, ":", 4)
	if len(parts) < 3 {
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Invalid action"))
		return nil
	}

	switch parts[1] {
	case "provider":
		provider := parts[2]
		page := 0
		if len(parts) >= 4 {
			if p, err := strconv.Atoi(parts[3]); err == nil {
				page = p
			}
		}
		if err := c.sendProviderModelsMenu(ctx, query.Message.GetChat().ID, query.Message.GetMessageID(), provider, page); err != nil {
			return err
		}
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Provider selected"))
	case "page":
		if len(parts) < 4 {
			_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Invalid page action"))
			return nil
		}
		provider := parts[2]
		page, err := strconv.Atoi(parts[3])
		if err != nil {
			page = 0
		}
		if err := c.sendProviderModelsMenu(ctx, query.Message.GetChat().ID, query.Message.GetMessageID(), provider, page); err != nil {
			return err
		}
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Page updated"))
	case "model":
		if len(parts) < 4 {
			_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Invalid model action"))
			return nil
		}
		provider := parts[2]
		model := parts[3]
		if c.applySelectedModel(query, provider, model) {
			if err := c.collapseModelsMenu(ctx, query); err != nil {
				logger.WarnCF("telegram", "Failed to collapse /models keyboard", map[string]interface{}{
					"error": err.Error(),
				})
			}
			_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Model selected"))
		} else {
			_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Model not applied"))
		}
	}

	return nil
}

func (c *TelegramChannel) sendProviderModelsMenu(ctx context.Context, chatID int64, messageID int, provider string, page int) error {
	isEdit := messageID > 0
	named, ok := c.config.Providers.GetNamed(provider)
	if !ok || len(named.Models) == 0 {
		if isEdit {
			_, err := c.bot.EditMessageText(ctx, tu.EditMessageText(tu.ID(chatID), messageID, "No models configured for this provider."))
			return err
		}
		_, err := c.bot.SendMessage(ctx, tu.Message(tu.ID(chatID), "No models configured for this provider."))
		return err
	}
	models := make([]string, 0, len(named.Models))
	for name := range named.Models {
		models = append(models, name)
	}
	sort.Strings(models)
	start, end, currentPage, totalPages := modelPageBounds(len(models), page, 6)
	rows := make([][]telego.InlineKeyboardButton, 0, end-start)
	for _, model := range models[start:end] {
		rows = append(rows, tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(model).WithCallbackData(fmt.Sprintf("models:model:%s:%s", provider, model)),
		))
	}
	if totalPages > 1 {
		nav := make([]telego.InlineKeyboardButton, 0, 2)
		if currentPage > 0 {
			nav = append(nav, tu.InlineKeyboardButton("⬅️ Previous Page").
				WithCallbackData(fmt.Sprintf("models:page:%s:%d", provider, currentPage-1)))
		}
		if currentPage < totalPages-1 {
			nav = append(nav, tu.InlineKeyboardButton("➡️ Next Page").
				WithCallbackData(fmt.Sprintf("models:page:%s:%d", provider, currentPage+1)))
		}
		if len(nav) > 0 {
			rows = append(rows, tu.InlineKeyboardRow(nav...))
		}
	}
	text := fmt.Sprintf("Provider: %s\nSelect a model (page %d/%d):", provider, currentPage+1, totalPages)
	markup := tu.InlineKeyboard(rows...)
	if isEdit {
		edit := tu.EditMessageText(tu.ID(chatID), messageID, text)
		edit.ReplyMarkup = markup
		_, err := c.bot.EditMessageText(ctx, edit)
		return err
	}
	_, err := c.bot.SendMessage(ctx, tu.Message(tu.ID(chatID), text).WithReplyMarkup(markup))
	return err
}

func modelPageBounds(total, page, perPage int) (start, end, currentPage, totalPages int) {
	if perPage <= 0 {
		perPage = 6
	}
	if total <= 0 {
		return 0, 0, 0, 1
	}
	totalPages = (total + perPage - 1) / perPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	start = page * perPage
	end = start + perPage
	if end > total {
		end = total
	}
	return start, end, page, totalPages
}

func (c *TelegramChannel) applySelectedModel(query telego.CallbackQuery, provider, model string) bool {
	if query.Message == nil {
		return false
	}
	senderID := fmt.Sprintf("%d", query.From.ID)
	if query.From.Username != "" {
		senderID = fmt.Sprintf("%d|%s", query.From.ID, query.From.Username)
	}
	if !c.IsAllowed(senderID) {
		return false
	}
	chat := query.Message.GetChat()
	chatID := chat.ID
	peerKind := "direct"
	peerID := fmt.Sprintf("%d", query.From.ID)
	if chat.Type != "private" {
		peerKind = "group"
		peerID = fmt.Sprintf("%d", chatID)
	}
	metadata := map[string]string{
		"user_id":   fmt.Sprintf("%d", query.From.ID),
		"username":  query.From.Username,
		"is_group":  fmt.Sprintf("%t", chat.Type != "private"),
		"peer_kind": peerKind,
		"peer_id":   peerID,
	}
	c.HandleMessage(
		fmt.Sprintf("%d", query.From.ID),
		fmt.Sprintf("%d", chatID),
		selectedModelCommand(provider, model),
		nil,
		metadata,
	)
	return true
}

func selectedModelCommand(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if isModelReference(model) || provider == "" {
		return "/model " + model
	}
	return fmt.Sprintf("/model %s/%s", provider, model)
}

func isModelReference(model string) bool {
	idx := strings.Index(model, "/")
	return idx > 0 && idx < len(model)-1
}

func (c *TelegramChannel) collapseModelsMenu(ctx context.Context, query telego.CallbackQuery) error {
	if query.Message == nil {
		return nil
	}
	_, err := c.bot.EditMessageReplyMarkup(ctx, tu.EditMessageReplyMarkup(
		tu.ID(query.Message.GetChat().ID),
		query.Message.GetMessageID(),
		nil,
	))
	return err
}
