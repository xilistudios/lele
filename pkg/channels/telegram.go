package channels

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/voice"
)

type TelegramChannel struct {
	*BaseChannel
	bot             *telego.Bot
	commands        TelegramCommander
	config          *config.Config
	chatIDs         map[string]int64
	transcriber     *voice.GroqTranscriber
	placeholders    sync.Map         // chatID -> messageID
	stopThinking    sync.Map         // chatID -> thinkingCancel
	approvalManager *ApprovalManager // Gestor de aprobaciones de comandos
	agentLoop       AgentProvidable  // Reference to agent loop for session agent management
}

type telegramCommandSpec struct {
	name        string
	description string
}

type telegramCallbackSpec struct {
	prefix  string
	handler func(context.Context, telego.CallbackQuery) error
}

var telegramCommandRegistry = []telegramCommandSpec{
	{name: "help"},
	{name: "start"},
	{name: "show"},
	{name: "list"},
	{name: "models", description: "Select provider/model from UI"},
	{name: "new", description: "Start a new conversation"},
	{name: "stop", description: "Stop the agent"},
	{name: "model", description: "Show models and change current model"},
	{name: "status", description: "Show model, tokens and gateway version"},
	{name: "compact", description: "Compact conversation history and save tokens"},
	{name: "subagents", description: "List and manage running subagents"},
	{name: "toggle", description: "Toggle runtime features like ephemeral sessions"},
	{name: "verbose", description: "Toggle verbose mode for tool execution"},
	{name: "think", description: "Toggle reasoning effort level (off/low/medium/high)"},
	{name: "agent", description: "Select or change current agent"},
}

func telegramMenuCommands(specs []telegramCommandSpec) []telego.BotCommand {
	commands := make([]telego.BotCommand, 0, len(specs))
	for _, spec := range specs {
		if spec.description == "" {
			continue
		}
		commands = append(commands, telego.BotCommand{
			Command:     spec.name,
			Description: spec.description,
		})
	}
	return commands
}

func (c *TelegramChannel) telegramCallbackRegistry() []telegramCallbackSpec {
	return []telegramCallbackSpec{
		{prefix: "models:", handler: c.handleModelsCallback},
		{prefix: "approval:", handler: c.handleApprovalCallback},
		{prefix: "agent:", handler: c.handleAgentCallback},
		{prefix: "verbose:", handler: c.handleVerboseCallback},
		{prefix: "think:", handler: c.handleThinkCallback},
	}
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
		commands:     NewTelegramCommands(bot, cfg, agentLoop),
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

	for _, command := range telegramCommandRegistry {
		commandName := command.name
		bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
			return c.handleCommandWithSession(ctx, &message, commandName)
		}, th.CommandEqual(commandName))
	}

	for _, callback := range c.telegramCallbackRegistry() {
		callbackPrefix := callback.prefix
		callbackHandler := callback.handler
		bh.HandleCallbackQuery(func(ctx *th.Context, query telego.CallbackQuery) error {
			return callbackHandler(ctx, query)
		}, th.AnyCallbackQueryWithMessage(), th.CallbackDataPrefix(callbackPrefix))
	}

	err = c.bot.SetMyCommands(ctx, &telego.SetMyCommandsParams{
		Commands: telegramMenuCommands(telegramCommandRegistry),
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
