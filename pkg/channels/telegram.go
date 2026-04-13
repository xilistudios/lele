package channels

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

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
	sendMu          sync.Mutex
	lastSend        time.Time
	cancel          context.CancelFunc
	botHandler      *th.BotHandler
	reconnectMu     sync.Mutex
	stopped         bool // Flag to prevent reconnect after explicit Stop()
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
	{name: "clear", description: "Clear conversation history"},
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

func NewTelegramChannel(cfg *config.Config, bus *bus.MessageBus, agentLoop AgentProvidable, approvalManager *ApprovalManager) (*TelegramChannel, error) {
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
		BaseChannel:     base,
		commands:        NewTelegramCommands(bot, cfg, agentLoop),
		bot:             bot,
		config:          cfg,
		chatIDs:         make(map[string]int64),
		transcriber:     nil,
		placeholders:    sync.Map{},
		stopThinking:    sync.Map{},
		agentLoop:       agentLoop,
		approvalManager: approvalManager,
	}, nil
}

func (c *TelegramChannel) SetTranscriber(transcriber *voice.GroqTranscriber) {
	c.transcriber = transcriber
}

// setupBotHandler initializes the bot handler with all command and callback handlers
func (c *TelegramChannel) setupBotHandler(ctx context.Context) (*th.BotHandler, context.CancelFunc, error) {
	cancelCtx, cancel := context.WithCancel(ctx)

	updates, err := c.bot.UpdatesViaLongPolling(cancelCtx, &telego.GetUpdatesParams{
		Timeout: 30,
	})
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("failed to start long polling: %w", err)
	}

	bh, err := th.NewBotHandler(c.bot, updates)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("failed to create bot handler: %w", err)
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

	err = c.bot.SetMyCommands(cancelCtx, &telego.SetMyCommandsParams{
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

	return bh, cancel, nil
}

func (c *TelegramChannel) Start(ctx context.Context) error {
	logger.InfoC("telegram", "Starting Telegram bot (polling mode with auto-reconnect)...")

	c.stopped = false
	c.setRunning(true)

	// Start polling loop with auto-reconnect
	go c.pollingLoop(ctx)

	return nil
}

// pollingLoop manages the bot handler lifecycle with automatic reconnect
func (c *TelegramChannel) pollingLoop(parentCtx context.Context) {
	reconnectDelay := 5 * time.Second
	maxReconnectDelay := 60 * time.Second
	currentDelay := reconnectDelay

	for {
		c.reconnectMu.Lock()
		if c.stopped {
			c.reconnectMu.Unlock()
			logger.InfoC("telegram", "Polling loop stopped (explicit Stop() called)")
			return
		}
		c.reconnectMu.Unlock()

		bh, cancel, err := c.setupBotHandler(parentCtx)
		if err != nil {
			logger.ErrorCF("telegram", "Failed to setup bot handler", map[string]interface{}{
				"error": err.Error(),
			})

			// Wait before retry with exponential backoff
			time.Sleep(currentDelay)
			currentDelay = min(currentDelay*2, maxReconnectDelay)
			continue
		}

		c.botHandler = bh
		c.cancel = cancel

		logger.InfoCF("telegram", "Telegram bot connected", map[string]interface{}{
			"username": c.bot.Username(),
		})

		// Reset reconnect delay on successful connection
		currentDelay = reconnectDelay

		// Run the handler - this blocks until it stops
		bh.Start()

		// Check if we should stop reconnecting
		c.reconnectMu.Lock()
		if c.stopped {
			c.reconnectMu.Unlock()
			logger.InfoC("telegram", "Bot handler stopped (explicit Stop() called)")
			return
		}
		c.reconnectMu.Unlock()

		// Handler stopped unexpectedly - log and reconnect
		logger.WarnCF("telegram", "BotHandler stopped unexpectedly - reconnecting in %s", map[string]interface{}{
			"delay": currentDelay.String(),
		})

		// Clean up before reconnect
		if cancel != nil {
			cancel()
		}

		time.Sleep(currentDelay)
		currentDelay = min(currentDelay*2, maxReconnectDelay)
	}
}

func (c *TelegramChannel) Stop(ctx context.Context) error {
	logger.InfoC("telegram", "Stopping Telegram bot...")

	// Mark as stopped to prevent reconnect
	c.reconnectMu.Lock()
	c.stopped = true
	c.reconnectMu.Unlock()

	c.setRunning(false)

	if c.cancel != nil {
		c.cancel()
	}
	if c.botHandler != nil {
		c.botHandler.Stop()
	}

	return nil
}

func (c *TelegramChannel) waitRateLimit(ctx context.Context) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	elapsed := time.Since(c.lastSend)
	if elapsed < 50*time.Millisecond {
		wait := 50*time.Millisecond - elapsed
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	c.lastSend = time.Now()
	return nil
}
