package channels

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/store"
	"github.com/xilistudios/lele/pkg/voice"
)

const telegramOffsetKey = "telegram:offset"

const (
	telegramProcessedMax  = 1000
	telegramProcessedKeep = 500
)

type TelegramChannel struct {
	*BaseChannel
	bot             *telego.Bot
	commands        TelegramCommander
	config          *config.Config
	chatIDs         map[string]int64
	transcriber     *voice.GroqTranscriber
	placeholders    sync.Map
	stopThinking    sync.Map
	approvalManager *ApprovalManager
	agentLoop       AgentProvidable
	sendMu          sync.Mutex
	lastSend        time.Time
	cancel          context.CancelFunc
	botHandler      *th.BotHandler
	reconnectMu     sync.Mutex
	stopped         bool
	lastUpdateID    int
	updateIDMu      sync.Mutex
	offsetFilePath  string
	kvRepo          *store.KVRepo
	processedIDs    map[string]int64
	processedMu     sync.Mutex
	deleteHTTP      *http.Client
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
	{name: "models", description: "Select provider:model from UI"},
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

	offsetFilePath := filepath.Join(config.GetLeleDir(), "telegram_offset.txt")

	lastUpdateID := loadLastUpdateIDFromFile(offsetFilePath)

	ch := &TelegramChannel{
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
		processedIDs:    make(map[string]int64),
	}

	ch.InboundDroppedHook = ch.handleInboundDropped

	return ch, nil
}

func (c *TelegramChannel) SetTranscriber(transcriber *voice.GroqTranscriber) {
	c.transcriber = transcriber
}

func (c *TelegramChannel) SetKVRepo(repo *store.KVRepo) {
	c.kvRepo = repo
	if repo == nil {
		return
	}
	if id := c.loadOffsetFromKV(); id > 0 {
		c.updateIDMu.Lock()
		if id > c.lastUpdateID {
			c.lastUpdateID = id
		}
		c.updateIDMu.Unlock()
	}
}

func (c *TelegramChannel) loadOffsetFromKV() int {
	if c.kvRepo == nil {
		return 0
	}
	value, ok, err := c.kvRepo.Get(telegramOffsetKey)
	if err != nil || !ok {
		return 0
	}
	id, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return id
}

func loadLastUpdateIDFromFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	id, err := strconv.Atoi(string(bytes.TrimSpace(data)))
	if err != nil {
		return 0
	}
	return id
}

func (c *TelegramChannel) saveLastUpdateID() {
	c.updateIDMu.Lock()
	id := c.lastUpdateID
	c.updateIDMu.Unlock()

	if id <= 0 {
		return
	}

	if c.kvRepo != nil {
		if err := c.kvRepo.Set(telegramOffsetKey, fmt.Sprintf("%d", id)); err != nil {
			logger.WarnCF("telegram", "Failed to persist UpdateID offset to KV store", map[string]interface{}{
				"error": err.Error(),
				"key":   telegramOffsetKey,
			})
		}
		return
	}

	if c.offsetFilePath == "" {
		return
	}
	data := fmt.Sprintf("%d", id)
	err := os.WriteFile(c.offsetFilePath, []byte(data), 0600)
	if err != nil {
		logger.WarnCF("telegram", "Failed to persist UpdateID offset", map[string]interface{}{
			"error": err.Error(),
			"path":  c.offsetFilePath,
		})
	}
}

func (c *TelegramChannel) updateOffset(update telego.Update) bool {
	c.updateIDMu.Lock()
	defer c.updateIDMu.Unlock()

	if update.UpdateID <= c.lastUpdateID {
		logger.DebugCF("telegram", "Skipping already processed update", map[string]interface{}{
			"update_id":      update.UpdateID,
			"last_update_id": c.lastUpdateID,
		})
		return false
	}

	c.lastUpdateID = update.UpdateID
	return true
}

func (c *TelegramChannel) setupBotHandler(ctx context.Context) (*th.BotHandler, context.CancelFunc, error) {
	cancelCtx, cancel := context.WithCancel(ctx)

	c.updateIDMu.Lock()
	offset := c.lastUpdateID + 1
	c.updateIDMu.Unlock()

	updates, err := c.bot.UpdatesViaLongPolling(cancelCtx, &telego.GetUpdatesParams{
		Timeout: 30,
		Offset:  offset,
	})
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("failed to start long polling: %w", err)
	}

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

func (c *TelegramChannel) wrapUpdatesWithOffsetTracking(ctx context.Context, updates <-chan telego.Update) <-chan telego.Update {
	wrapped := make(chan telego.Update, 100)

	go func() {
		defer close(wrapped)
		saveTicker := time.NewTicker(10 * time.Second)
		defer saveTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				c.saveLastUpdateID()
				return
			case update, ok := <-updates:
				if !ok {
					c.saveLastUpdateID()
					return
				}
				if c.updateOffset(update) {
					wrapped <- update
				}
			case <-saveTicker.C:
				c.saveLastUpdateID()
			}
		}
	}()

	return wrapped
}

func (c *TelegramChannel) Start(ctx context.Context) error {

	c.stopped = false
	c.setRunning(true)

	go c.pollingLoop(ctx)

	return nil
}

func (c *TelegramChannel) pollingLoop(parentCtx context.Context) {
	reconnectDelay := 5 * time.Second
	maxReconnectDelay := 60 * time.Second
	currentDelay := reconnectDelay

	for {
		c.reconnectMu.Lock()
		if c.stopped {
			c.reconnectMu.Unlock()
			return
		}
		c.reconnectMu.Unlock()

		bh, cancel, err := c.setupBotHandler(parentCtx)
		if err != nil {
			logger.ErrorCF("telegram", "Failed to setup bot handler", map[string]interface{}{
				"error": err.Error(),
			})

			time.Sleep(currentDelay)
			currentDelay = min(currentDelay*2, maxReconnectDelay)
			continue
		}

		c.botHandler = bh
		c.cancel = cancel

		logger.InfoCF("telegram", "Telegram bot connected", map[string]interface{}{
			"username": c.bot.Username(),
		})

		currentDelay = reconnectDelay

		bh.Start()

		c.reconnectMu.Lock()
		if c.stopped {
			c.reconnectMu.Unlock()
			return
		}
		c.reconnectMu.Unlock()

		logger.WarnCF("telegram", "BotHandler stopped unexpectedly - reconnecting in %s", map[string]interface{}{
			"delay": currentDelay.String(),
		})

		c.sweepOrphanedTurnState(context.Background(), "bot handler stopped unexpectedly")

		if cancel != nil {
			cancel()
		}

		time.Sleep(currentDelay)
		currentDelay = min(currentDelay*2, maxReconnectDelay)
	}
}

func (c *TelegramChannel) Stop(ctx context.Context) error {

	c.reconnectMu.Lock()
	c.stopped = true
	c.reconnectMu.Unlock()

	c.setRunning(false)

	c.sweepOrphanedTurnState(ctx, "channel stopping")

	if c.cancel != nil {
		c.cancel()
	}
	if c.botHandler != nil {
		c.botHandler.Stop()
	}

	return nil
}

func (c *TelegramChannel) sweepOrphanedTurnState(ctx context.Context, reason string) {
	stopped := countMapEntries(&c.stopThinking)
	placeholders := countMapEntries(&c.placeholders)
	if stopped == 0 && placeholders == 0 {
		return
	}

	c.stopAllThinking()
	c.clearAllPlaceholders(ctx)

	logger.WarnCF("telegram", "Swept orphaned turn state", map[string]interface{}{
		"reason":          reason,
		"typing_loops":    stopped,
		"thinking_bubble": placeholders,
	})
}

func countMapEntries(m *sync.Map) int {
	n := 0
	m.Range(func(_, _ interface{}) bool { n++; return true })
	return n
}

func (c *TelegramChannel) stopAllThinking() {
	c.stopThinking.Range(func(key, value interface{}) bool {
		if cf, ok := value.(*thinkingCancel); ok && cf != nil {
			cf.Cancel()
		}
		c.stopThinking.Delete(key)
		return true
	})
}

const placeholderSweepDeadline = 5 * time.Second

func (c *TelegramChannel) clearAllPlaceholders(ctx context.Context) {
	if c.config == nil {
		return
	}
	deadline, cancel := context.WithTimeout(ctx, placeholderSweepDeadline)
	defer cancel()

	c.placeholders.Range(func(key, value interface{}) bool {
		chatKey, isStr := key.(string)
		id, isInt := value.(int)
		if isStr && isInt {
			chatID, err := parseChatID(chatKey)
			if err == nil {
				c.deleteMessage(deadline, chatID, id)
			}
		}
		c.placeholders.Delete(key)
		return true
	})
}

func (c *TelegramChannel) isDuplicate(messageID string) bool {
	c.processedMu.Lock()
	defer c.processedMu.Unlock()

	if _, exists := c.processedIDs[messageID]; exists {
		logger.InfoCF("telegram", "Duplicate message detected and ignored", map[string]interface{}{
			"message_key":     messageID,
			"processed_count": len(c.processedIDs),
		})
		return true
	}

	c.processedIDs[messageID] = time.Now().UnixNano()

	if len(c.processedIDs) > telegramProcessedMax {
		type entry struct {
			key string
			ts  int64
		}
		entries := make([]entry, 0, len(c.processedIDs))
		for k, ts := range c.processedIDs {
			entries = append(entries, entry{k, ts})
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].ts != entries[j].ts {
				return entries[i].ts > entries[j].ts
			}
			return entries[i].key < entries[j].key
		})
		keep := make(map[string]int64, telegramProcessedKeep)
		for _, e := range entries[:telegramProcessedKeep] {
			keep[e.key] = e.ts
		}
		c.processedIDs = keep
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
