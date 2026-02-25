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

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/utils"
	"github.com/sipeed/picoclaw/pkg/voice"
)

type TelegramChannel struct {
	*BaseChannel
	bot             *telego.Bot
	commands        TelegramCommander
	config          *config.Config
	chatIDs         map[string]int64
	transcriber     *voice.GroqTranscriber
	placeholders    sync.Map // chatID -> messageID
	stopThinking    sync.Map // chatID -> thinkingCancel
	approvalManager *ApprovalManager // Gestor de aprobaciones de comandos
	agentLoop       AgentProvidable // Reference to agent loop for session agent management
}

type thinkingCancel struct {
	fn context.CancelFunc
}

func (c *thinkingCancel) Cancel() {
	if c != nil && c.fn != nil {
		c.fn()
	}
}

// startTypingIndicator starts a persistent typing indicator that updates every 4 seconds
func (c *TelegramChannel) startTypingIndicator(chatID int64) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	ticker := time.NewTicker(4 * time.Second)

	// Send initial typing action
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

func NewTelegramChannel(cfg *config.Config, bus *bus.MessageBus, agentLoop AgentProvidable) (*TelegramChannel, error) {
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
		agentLoop:    agentLoop,
	}, nil
}

func (c *TelegramChannel) SetTranscriber(transcriber *voice.GroqTranscriber) {
	c.transcriber = transcriber
}

// SetApprovalManager configures the approval manager for handling command approvals
func (c *TelegramChannel) SetApprovalManager(am *ApprovalManager) {
	c.approvalManager = am
}

// handleApprovalCallback processes callback queries from approval inline keyboards
func (c *TelegramChannel) handleApprovalCallback(ctx context.Context, query telego.CallbackQuery) error {
	logger.DebugCF("telegram", "handleApprovalCallback called", map[string]interface{}{
		"data":       query.Data,
		"approval_manager_nil": c.approvalManager == nil,
	})

	if c.approvalManager == nil {
		logger.ErrorC("telegram", "handleApprovalCallback: approval manager is nil")
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Approval system not available"))
	}

	// Parse callback data: approval:action:approvalID
	parts := strings.SplitN(query.Data, ":", 3)
	if len(parts) != 3 || parts[0] != "approval" {
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Invalid callback data"))
	}

	action := parts[1]
	approvalID := parts[2]

	logger.DebugCF("telegram", "handleApprovalCallback parsed", map[string]interface{}{
		"action":      action,
		"approval_id": approvalID,
	})

	var approved bool
	switch action {
	case "approve":
		approved = true
	case "reject":
		approved = false
	case "view":
		// Handle view action - show full command
		return c.handleApprovalView(ctx, query, approvalID)
	default:
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Unknown action"))
	}

	logger.DebugCF("telegram", "handleApprovalCallback calling HandleApproval", map[string]interface{}{
		"approval_id": approvalID,
		"approved":    approved,
	})

	// Handle the approval
	approval, err := c.approvalManager.HandleApproval(approvalID, approved)
	if err != nil {
		logger.WarnCF("telegram", "Failed to handle approval", map[string]interface{}{
			"error":       err.Error(),
			"approval_id": approvalID,
		})
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Approval request expired or not found"))
	}

	// Update the message to show the decision
	chatID := query.Message.GetChat().ID
	messageID := query.Message.GetMessageID()

	var statusEmoji, statusText string
	if approved {
		statusEmoji = "✅"
		statusText = "APROBADO"
	} else {
		statusEmoji = "❌"
		statusText = "RECHAZADO"
	}

	updatedText := fmt.Sprintf("%s **Comando %s**\n`%s`", statusEmoji, statusText, approval.Command)

	// Edit message to remove keyboard and show decision
	editMsg := tu.EditMessageText(tu.ID(chatID), messageID, markdownToTelegramHTML(updatedText))
	editMsg.ParseMode = telego.ModeHTML

	if _, editErr := c.bot.EditMessageText(ctx, editMsg); editErr != nil {
		logger.DebugCF("telegram", "Failed to edit approval message", map[string]interface{}{
			"error": editErr.Error(),
		})
	}

	// Answer the callback query
	feedback := fmt.Sprintf("Command %s", strings.ToLower(statusText))
	return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText(feedback))
}

// handleApprovalView handles the "view" action to show the full command
func (c *TelegramChannel) handleApprovalView(ctx context.Context, query telego.CallbackQuery, approvalID string) error {
	approval := c.approvalManager.GetApproval(approvalID)
	if approval == nil {
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Approval request not found or expired"))
	}

	// Show the full command in a popup
	text := fmt.Sprintf("Comando:\n`%s`\n\nRazón: %s", approval.Command, approval.Reason)
	return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText(text))
}

func (c *TelegramChannel) Start(ctx context.Context) error {
	logger.InfoC("telegram", "Starting Telegram bot (polling mode)...")

	updates, err := c.bot.UpdatesViaLongPolling(ctx, &telego.GetUpdatesParams{
		Timeout: 30,
	})
	if err != nil {
		return fmt.Errorf("failed to start long polling: %w", err)
	}

	bh, err := th.NewBotHandler(c.bot, updates)
	if err != nil {
		return fmt.Errorf("failed to create bot handler: %w", err)
	}

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleCommandWithSession(ctx, &message, "help")
	}, th.CommandEqual("help"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleCommandWithSession(ctx, &message, "start")
	}, th.CommandEqual("start"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleCommandWithSession(ctx, &message, "show")
	}, th.CommandEqual("show"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleCommandWithSession(ctx, &message, "list")
	}, th.CommandEqual("list"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleCommandWithSession(ctx, &message, "models")
	}, th.CommandEqual("models"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleCommandWithSession(ctx, &message, "new")
	}, th.CommandEqual("new"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleCommandWithSession(ctx, &message, "stop")
	}, th.CommandEqual("stop"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleCommandWithSession(ctx, &message, "model")
	}, th.CommandEqual("model"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleCommandWithSession(ctx, &message, "status")
	}, th.CommandEqual("status"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleCommandWithSession(ctx, &message, "compact")
	}, th.CommandEqual("compact"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleCommandWithSession(ctx, &message, "subagents")
	}, th.CommandEqual("subagents"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleCommandWithSession(ctx, &message, "verbose")
	}, th.CommandEqual("verbose"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleCommandWithSession(ctx, &message, "agent")
	}, th.CommandEqual("agent"))
	bh.HandleCallbackQuery(func(ctx *th.Context, query telego.CallbackQuery) error {
		return c.handleModelsCallback(ctx, query)
	}, th.AnyCallbackQueryWithMessage(), th.CallbackDataPrefix("models:"))
	
	// Register approval callback handler
	bh.HandleCallbackQuery(func(ctx *th.Context, query telego.CallbackQuery) error {
		return c.handleApprovalCallback(ctx, query)
	}, th.AnyCallbackQueryWithMessage(), th.CallbackDataPrefix("approval:"))
	
	// Register agent callback handler
	bh.HandleCallbackQuery(func(ctx *th.Context, query telego.CallbackQuery) error {
		return c.handleAgentCallback(ctx, query)
	}, th.AnyCallbackQueryWithMessage(), th.CallbackDataPrefix("agent:"))

	err = c.bot.SetMyCommands(ctx, &telego.SetMyCommandsParams{
		Commands: []telego.BotCommand{
			{Command: "new", Description: "Start a new conversation"},
			{Command: "stop", Description: "Stop the agent"},
			{Command: "model", Description: "Show models and change current model"},
			{Command: "models", Description: "Select provider/model from UI"},
			{Command: "agent", Description: "Select or change current agent"},
			{Command: "status", Description: "Show model, tokens and gateway version"},
			{Command: "compact", Description: "Compact conversation history and save tokens"},
			{Command: "subagents", Description: "List and manage running subagents"},
			{Command: "verbose", Description: "Toggle verbose mode for tool execution"},
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

	// Stop thinking animation ONLY for non-intermediate messages
	if !msg.IsIntermediate {
		if stop, ok := c.stopThinking.Load(msg.ChatID); ok {
			if cf, ok := stop.(*thinkingCancel); ok && cf != nil {
				cf.Cancel()
			}
			c.stopThinking.Delete(msg.ChatID)
		}
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

	// Set reply parameters if we have a message to reply to
	if msg.ReplyTo != "" {
		if replyMsgID, parseErr := strconv.Atoi(msg.ReplyTo); parseErr == nil {
			tgMsg.ReplyParameters = &telego.ReplyParameters{
				MessageID: replyMsgID,
			}
		}
	}

	// Set inline keyboard markup if provided (for approval buttons, etc.)
	if msg.ReplyMarkup != nil {
		if markup, ok := msg.ReplyMarkup.(*telego.InlineKeyboardMarkup); ok {
			tgMsg.ReplyMarkup = markup
		}
	}

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

	// Check if this is a command message
	if message.Text != "" && strings.HasPrefix(message.Text, "/") {
		// Extract command name (first word, without /)
		text := strings.TrimPrefix(message.Text, "/")
		parts := strings.Fields(text)
		if len(parts) > 0 {
			cmd := parts[0]
			// Handle known commands
			switch cmd {
			case "help", "start", "show", "list", "models", "new", "stop", "model", "status", "compact", "subagents", "verbose", "agent":
				return c.handleCommandWithSession(ctx, message, cmd)
			}
		}
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

	// Stop any previous thinking animation and start new one
	chatIDStr := fmt.Sprintf("%d", chatID)
	if prevStop, ok := c.stopThinking.Load(chatIDStr); ok {
		if cf, ok := prevStop.(*thinkingCancel); ok && cf != nil {
			cf.Cancel()
		}
	}

	// Start persistent typing indicator that updates every 4 seconds
	typingCancel := c.startTypingIndicator(chatID)
	c.stopThinking.Store(chatIDStr, &thinkingCancel{fn: typingCancel})

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

	// Generate session key based on chat (unique per chat)
	sessionKey := fmt.Sprintf("telegram:%d", chatID)

	c.HandleMessageWithSession(fmt.Sprintf("%d", user.ID), fmt.Sprintf("%d", chatID), content, mediaPaths, metadata, sessionKey)
	return nil
}

// handleCommandWithSession handles Telegram commands with session context
func (c *TelegramChannel) handleCommandWithSession(ctx context.Context, message *telego.Message, cmd string) error {
	if message == nil {
		return fmt.Errorf("message is nil")
	}

	user := message.From
	if user == nil {
		return fmt.Errorf("message sender (user) is nil")
	}

	senderID := fmt.Sprintf("%d", user.ID)
	chatID := message.Chat.ID
	sessionKey := fmt.Sprintf("telegram:%d", chatID)
	messageID := fmt.Sprintf("%d", message.MessageID)

	// Execute commands directly via agentLoop (async - doesn't wait for agent processing)
	switch cmd {
	case "new":
		var response string
		if c.agentLoop != nil {
			response = c.agentLoop.ClearSession(sessionKey)
		} else {
			response = "🔄 Nueva conversación iniciada. Historial limpiado."
		}
		_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID: telego.ChatID{ID: message.Chat.ID},
			Text:   response,
			ReplyParameters: &telego.ReplyParameters{
				MessageID: message.MessageID,
			},
		})
		return err

	case "stop":
		var response string
		if c.agentLoop != nil {
			response = c.agentLoop.StopAgent(sessionKey)
		} else {
			response = "⏹️ Agente detenido."
		}
		_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID: telego.ChatID{ID: message.Chat.ID},
			Text:   response,
			ReplyParameters: &telego.ReplyParameters{
				MessageID: message.MessageID,
			},
		})
		return err

	case "status":
		var response string
		if c.agentLoop != nil {
			response = c.agentLoop.GetStatus(sessionKey)
		} else {
			response = "⚠️ Agent loop not available."
		}
		_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID: telego.ChatID{ID: message.Chat.ID},
			Text:   response,
			ReplyParameters: &telego.ReplyParameters{
				MessageID: message.MessageID,
			},
		})
		return err

	case "compact":
		var response string
		if c.agentLoop != nil {
			response = c.agentLoop.CompactSession(sessionKey)
		} else {
			response = "⚠️ Agent loop not available."
		}
		_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID: telego.ChatID{ID: message.Chat.ID},
			Text:   response,
			ReplyParameters: &telego.ReplyParameters{
				MessageID: message.MessageID,
			},
		})
		return err

	case "subagents":
		var response string
		if c.agentLoop != nil {
			response = c.agentLoop.GetSubagents()
		} else {
			response = "🤖 No subagents running."
		}
		_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID: telego.ChatID{ID: message.Chat.ID},
			Text:   response,
			ReplyParameters: &telego.ReplyParameters{
				MessageID: message.MessageID,
			},
		})
		return err

	case "verbose":
		var response string
		if c.agentLoop != nil {
			response = c.agentLoop.ToggleVerbose(sessionKey)
		} else {
			response = "⚠️ Agent loop not available."
		}
		_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID: telego.ChatID{ID: message.Chat.ID},
			Text:   response,
			ReplyParameters: &telego.ReplyParameters{
				MessageID: message.MessageID,
			},
		})
		return err

	case "model":
		// Extract model argument and send to agent loop
		args := strings.TrimSpace(strings.TrimPrefix(message.Text, "/model"))
		systemMsg := bus.InboundMessage{
			Channel:    "system",
			SenderID:   senderID,
			ChatID:     sessionKey,
			Content:    "/model " + args,
			SessionKey: sessionKey,
			Metadata: map[string]string{
				"message_id": messageID,
			},
		}
		c.bus.PublishInbound(systemMsg)
		return nil // Agent loop will respond
	}

	// For other commands (help, start, show, list, models, agent), use direct response
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
			// Direct agent change - send system message
			systemMsg := bus.InboundMessage{
				Channel:    "system",
				SenderID:   senderID,
				ChatID:     sessionKey,
				Content:    "/agent " + args,
				SessionKey: sessionKey,
				Metadata: map[string]string{
					"message_id": messageID,
				},
			}
			c.bus.PublishInbound(systemMsg)
			return nil
		}
		// Show agent selection UI
		return c.commands.Agent(ctx, *message)
	}

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
	
	// Build session key (same as handleMessage)
	sessionKey := fmt.Sprintf("telegram:%d", chatID)
	
	peerKind := "direct"
	peerID := fmt.Sprintf("%d", query.From.ID)
	if chat.Type != "private" {
		peerKind = "group"
		peerID = fmt.Sprintf("%d", chatID)
	}
	
	// Check if user is allowed before sending
	if !c.IsAllowed(senderID) {
		return false
	}
	
	// Send system message directly to agent loop (like handleCommandWithSession does for /model)
	msg := bus.InboundMessage{
		Channel:    "system",
		SenderID:   senderID,
		ChatID:     sessionKey,
		Content:    selectedModelCommand(provider, model),
		SessionKey: sessionKey,
		Metadata: map[string]string{
			"message_id": fmt.Sprintf("%d", query.Message.GetMessageID()),
			"user_id":    fmt.Sprintf("%d", query.From.ID),
			"username":   query.From.Username,
			"is_group":   fmt.Sprintf("%t", chat.Type != "private"),
			"peer_kind":  peerKind,
			"peer_id":    peerID,
		},
	}
	
	if c.BaseChannel.bus != nil {
		c.BaseChannel.bus.PublishInbound(msg)
	}
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


// handleAgentCallback processes callback queries for agent selection
func (c *TelegramChannel) handleAgentCallback(ctx context.Context, query telego.CallbackQuery) error {
	if query.Message == nil {
		return nil
	}

	// Check agentLoop is available
	if c.agentLoop == nil {
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Agent management not available"))
		return nil
	}

	parts := strings.SplitN(query.Data, ":", 3)
	if len(parts) < 3 || parts[0] != "agent" {
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Invalid action"))
		return nil
	}

	action := parts[1]
	agentID := parts[2]

	switch action {
	case "select":
		// Validate agent exists using agentLoop interface
		agentInfo, agentExists := c.agentLoop.GetAgentInfo(agentID)
		if !agentExists {
			_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Agent not found"))
			return nil
		}

		agentName := agentInfo.Name
		if agentName == "" {
			agentName = agentID
		}

		// Send system message to change agent
		senderID := fmt.Sprintf("%d", query.From.ID)
		if query.From.Username != "" {
			senderID = fmt.Sprintf("%d|%s", query.From.ID, query.From.Username)
		}
		chatID := query.Message.GetChat().ID
		sessionKey := fmt.Sprintf("telegram:%d", chatID)

		msg := bus.InboundMessage{
			Channel:    "system",
			SenderID:   senderID,
			ChatID:     sessionKey,
			Content:    "/agent " + agentID,
			SessionKey: sessionKey,
			Metadata: map[string]string{
				"message_id": fmt.Sprintf("%d", query.Message.GetMessageID()),
				"user_id":    fmt.Sprintf("%d", query.From.ID),
				"username":   query.From.Username,
			},
		}

		if c.BaseChannel.bus != nil {
			c.BaseChannel.bus.PublishInbound(msg)
		}

		// Update message with detailed agent info
		chat := query.Message.GetChat()
		messageID := query.Message.GetMessageID()
		text := formatAgentSelectedMessage(agentInfo, agentID)
		editMsg := tu.EditMessageText(tu.ID(chat.ID), messageID, text)
		editMsg.ParseMode = telego.ModeMarkdown
		_, _ = c.bot.EditMessageText(ctx, editMsg)

		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Agent selected: "+agentName))
	default:
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Unknown action"))
	}

	return nil
}

// formatAgentSelectedMessage formats a message when an agent is selected
func formatAgentSelectedMessage(agent AgentBasicInfo, agentID string) string {
	var parts []string
	parts = append(parts, "✅ *Agente seleccionado*")
	parts = append(parts, "")
	parts = append(parts, fmt.Sprintf("*Nombre:* %s", agent.Name))
	parts = append(parts, fmt.Sprintf("*ID:* `%s`", agentID))
	parts = append(parts, fmt.Sprintf("*Modelo:* `%s`", agent.Model))
	if agent.Workspace != "" && agent.Workspace != "workspace" {
		parts = append(parts, fmt.Sprintf("*Workspace:* `%s`", agent.Workspace))
	}
	if len(agent.SkillsFilter) > 0 {
		parts = append(parts, fmt.Sprintf("*Skills:* %s", strings.Join(agent.SkillsFilter, ", ")))
	}
	return strings.Join(parts, "\n")
}
