package channels

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	// Deduplication using UpdateID (Telegram's unique identifier for each update)
	lastUpdateID   int
	updateIDMu     sync.Mutex
	offsetFilePath string // Path to persist last UpdateID
	// Fallback deduplication using ChatID:MessageID
	processedIDs   map[string]struct{}
	processedMu    sync.Mutex
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

	// Determine offset file path for persisting last UpdateID
	homeDir, _ := os.UserHomeDir()
	offsetFilePath := filepath.Join(homeDir, ".lele", "telegram_offset.txt")

	// Load persisted last UpdateID
	lastUpdateID := loadLastUpdateID(offsetFilePath)

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
		lastUpdateID:    lastUpdateID,
		offsetFilePath:  offsetFilePath,
		processedIDs:    make(map[string]struct{}),
	}, nil
}

func (c *TelegramChannel) SetTranscriber(transcriber *voice.GroqTranscriber) {
	c.transcriber = transcriber
}

// loadLastUpdateID reads the last processed UpdateID from a file
func loadLastUpdateID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0 // No persisted offset, start fresh
	}
	var id int
	fmt.Sscanf(string(data), "%d", &id)
	return id
}

// saveLastUpdateID persists the last processed UpdateID to a file
func (c *TelegramChannel) saveLastUpdateID() {
	c.updateIDMu.Lock()
	id := c.lastUpdateID
	c.updateIDMu.Unlock()

	if id <= 0 || c.offsetFilePath == "" {
		return
	}

	// Write as simple text file
	data := fmt.Sprintf("%d", id)
	err := os.WriteFile(c.offsetFilePath, []byte(data), 0600)
	if err != nil {
		logger.WarnCF("telegram", "Failed to persist UpdateID offset", map[string]interface{}{
			"error": err.Error(),
			"path":  c.offsetFilePath,
		})
	}
}

// updateOffset processes an update and updates the last UpdateID
func (c *TelegramChannel) updateOffset(update telego.Update) bool {
	c.updateIDMu.Lock()
	defer c.updateIDMu.Unlock()

	// Skip if we've already processed this or older updates
	if update.UpdateID <= c.lastUpdateID {
		logger.DebugCF("telegram", "Skipping already processed update", map[string]interface{}{
			"update_id":      update.UpdateID,
			"last_update_id": c.lastUpdateID,
		})
		return false
	}

	// Update the last processed ID
	c.lastUpdateID = update.UpdateID
	return true
}

// setupBotHandler initializes the bot handler with all command and callback handlers
func (c *TelegramChannel) setupBotHandler(ctx context.Context) (*th.BotHandler, context.CancelFunc, error) {
	cancelCtx, cancel := context.WithCancel(ctx)

	// Get the current offset to pass to Telegram API
	c.updateIDMu.Lock()
	offset := c.lastUpdateID + 1 // Telegram expects offset = lastProcessedUpdateID + 1
	c.updateIDMu.Unlock()

	updates, err := c.bot.UpdatesViaLongPolling(cancelCtx, &telego.GetUpdatesParams{
		Timeout: 30,
		Offset:  offset, // Start from last processed update + 1
	})
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("failed to start long polling: %w", err)
	}

	// Create a wrapper channel that tracks UpdateIDs and filters duplicates
	wrappedUpdates := c.wrapUpdatesWithOffsetTracking(cancelCtx, updates)

	bh, err := th.NewBotHandler(c.bot, wrappedUpdates)
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

	// Handler for non-command messages (exclude commands that have specific handlers)
	// This prevents double processing where both CommandEqual and AnyMessage match
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleMessage(ctx, &message)
	}, th.Not(th.Or(
		th.CommandEqual("help"),
		th.CommandEqual("start"),
		th.CommandEqual("show"),
		th.CommandEqual("list"),
		th.CommandEqual("models"),
		th.CommandEqual("new"),
		th.CommandEqual("clear"),
		th.CommandEqual("stop"),
		th.CommandEqual("model"),
		th.CommandEqual("status"),
		th.CommandEqual("compact"),
		th.CommandEqual("subagents"),
		th.CommandEqual("toggle"),
		th.CommandEqual("verbose"),
		th.CommandEqual("think"),
		th.CommandEqual("agent"),
	)))

	return bh, cancel, nil
}

// wrapUpdatesWithOffsetTracking creates a wrapper channel that tracks UpdateIDs
// and periodically persists the last processed offset
func (c *TelegramChannel) wrapUpdatesWithOffsetTracking(ctx context.Context, updates <-chan telego.Update) <-chan telego.Update {
	wrapped := make(chan telego.Update, 100)

	go func() {
		defer close(wrapped)
		saveTicker := time.NewTicker(10 * time.Second) // Persist offset every 10s
		defer saveTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				// Save offset before exiting
				c.saveLastUpdateID()
				return
			case update, ok := <-updates:
				if !ok {
					c.saveLastUpdateID()
					return
				}
				// Track and validate the update
				if c.updateOffset(update) {
					wrapped <- update
				}
			case <-saveTicker.C:
				// Periodically persist the offset
				c.saveLastUpdateID()
			}
		}
	}()

	return wrapped
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

// isDuplicate checks if a message has already been processed
// Returns true if the message is a duplicate
func (c *TelegramChannel) isDuplicate(messageID string) bool {
	c.processedMu.Lock()
	defer c.processedMu.Unlock()

	if _, exists := c.processedIDs[messageID]; exists {
		logger.InfoCF("telegram", "Duplicate message detected and ignored", map[string]interface{}{
			"message_key": messageID,
			"processed_count": len(c.processedIDs),
		})
		return true
	}

	// Add to processed set
	c.processedIDs[messageID] = struct{}{}

	// Cleanup old entries if set grows too large (keep last 1000)
	if len(c.processedIDs) > 1000 {
		// Simple cleanup: create new map with recent entries
		// In a production system, you might want a ring buffer or LRU cache
		newMap := make(map[string]struct{})
		count := 0
		for k := range c.processedIDs {
			if count >= 500 {
				break
			}
			newMap[k] = struct{}{}
			count++
		}
		c.processedIDs = newMap
	}

	return false
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
