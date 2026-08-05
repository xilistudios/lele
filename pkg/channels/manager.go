// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package channels

import (
	"context"
	"fmt"
	"sync"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/constants"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/store"
)

type Manager struct {
	channels        map[string]Channel
	dispatchQueues  map[string]chan bus.OutboundMessage
	bus             *bus.MessageBus
	config          *config.Config
	agentLoop       AgentProvidable
	approvalManager *ApprovalManager
	dispatchTask    *asyncTask
	runCtx          context.Context
	mu              sync.RWMutex
}

type asyncTask struct {
	cancel context.CancelFunc
}

func NewManager(cfg *config.Config, messageBus *bus.MessageBus, agentLoop AgentProvidable, approvalManager *ApprovalManager) (*Manager, error) {
	m := &Manager{
		channels:        make(map[string]Channel),
		dispatchQueues:  make(map[string]chan bus.OutboundMessage),
		bus:             messageBus,
		config:          cfg,
		agentLoop:       agentLoop,
		approvalManager: approvalManager,
	}

	if err := m.initChannels(); err != nil {
		return nil, err
	}

	return m, nil
}

func (m *Manager) initChannels() error {
	if m.config.Channels.Telegram.Enabled && m.config.Channels.Telegram.Token != "" {
		telegram, err := NewTelegramChannel(m.config, m.bus, m.agentLoop, m.approvalManager)
		if err != nil {
			logger.ErrorCF("channels", "Failed to initialize Telegram channel", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			m.channels["telegram"] = telegram
			m.dispatchQueues["telegram"] = make(chan bus.OutboundMessage, 200)
		}
	}

	if m.config.Channels.WhatsApp.Enabled && m.config.Channels.WhatsApp.BridgeURL != "" {
		whatsapp, err := NewWhatsAppChannel(m.config.Channels.WhatsApp, m.bus)
		if err != nil {
			logger.ErrorCF("channels", "Failed to initialize WhatsApp channel", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			m.channels["whatsapp"] = whatsapp
			m.dispatchQueues["whatsapp"] = make(chan bus.OutboundMessage, 200)
		}
	}

	if m.config.Channels.Feishu.Enabled {
		feishu, err := NewFeishuChannel(m.config.Channels.Feishu, m.bus)
		if err != nil {
			logger.ErrorCF("channels", "Failed to initialize Feishu channel", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			m.channels["feishu"] = feishu
			m.dispatchQueues["feishu"] = make(chan bus.OutboundMessage, 200)
		}
	}

	if m.config.Channels.Discord.Enabled && m.config.Channels.Discord.Token != "" {
		discord, err := NewDiscordChannel(m.config.Channels.Discord, m.bus)
		if err != nil {
			logger.ErrorCF("channels", "Failed to initialize Discord channel", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			m.channels["discord"] = discord
			m.dispatchQueues["discord"] = make(chan bus.OutboundMessage, 200)
		}
	}

	if m.config.Channels.MaixCam.Enabled {
		maixcam, err := NewMaixCamChannel(m.config.Channels.MaixCam, m.bus)
		if err != nil {
			logger.ErrorCF("channels", "Failed to initialize MaixCam channel", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			m.channels["maixcam"] = maixcam
			m.dispatchQueues["maixcam"] = make(chan bus.OutboundMessage, 200)
		}
	}

	if m.config.Channels.QQ.Enabled {
		qq, err := NewQQChannel(m.config.Channels.QQ, m.bus)
		if err != nil {
			logger.ErrorCF("channels", "Failed to initialize QQ channel", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			m.channels["qq"] = qq
			m.dispatchQueues["qq"] = make(chan bus.OutboundMessage, 200)
		}
	}

	if m.config.Channels.DingTalk.Enabled && m.config.Channels.DingTalk.ClientID != "" {
		dingtalk, err := NewDingTalkChannel(m.config.Channels.DingTalk, m.bus)
		if err != nil {
			logger.ErrorCF("channels", "Failed to initialize DingTalk channel", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			m.channels["dingtalk"] = dingtalk
			m.dispatchQueues["dingtalk"] = make(chan bus.OutboundMessage, 200)
		}
	}

	if m.config.Channels.Slack.Enabled && m.config.Channels.Slack.BotToken != "" {
		slackCh, err := NewSlackChannel(m.config.Channels.Slack, m.bus)
		if err != nil {
			logger.ErrorCF("channels", "Failed to initialize Slack channel", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			m.channels["slack"] = slackCh
			m.dispatchQueues["slack"] = make(chan bus.OutboundMessage, 200)
		}
	}

	if m.config.Channels.LINE.Enabled && m.config.Channels.LINE.ChannelAccessToken != "" {
		line, err := NewLINEChannel(m.config.Channels.LINE, m.bus)
		if err != nil {
			logger.ErrorCF("channels", "Failed to initialize LINE channel", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			m.channels["line"] = line
			m.dispatchQueues["line"] = make(chan bus.OutboundMessage, 200)
		}
	}

	if m.config.Channels.OneBot.Enabled && m.config.Channels.OneBot.WSUrl != "" {
		onebot, err := NewOneBotChannel(m.config.Channels.OneBot, m.bus)
		if err != nil {
			logger.ErrorCF("channels", "Failed to initialize OneBot channel", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			m.channels["onebot"] = onebot
			m.dispatchQueues["onebot"] = make(chan bus.OutboundMessage, 200)
		}
	}

	if m.config.Channels.Native.Enabled {
		native, err := NewNativeChannel(m.config, m.bus, m.agentLoop, m.approvalManager)
		if err != nil {
			logger.ErrorCF("channels", "Failed to initialize Native channel", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			m.channels["native"] = native
			m.dispatchQueues["native"] = make(chan bus.OutboundMessage, 200)
		}
	}

	if len(m.channels) > 0 {
		logger.InfoCF("channels", "Channels initialized", map[string]interface{}{
			"count": len(m.channels),
		})
	}

	return nil
}

func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runCtx = ctx

	if len(m.channels) == 0 {
		logger.WarnC("channels", "No channels enabled")
		return nil
	}

	dispatchCtx, cancel := context.WithCancel(ctx)
	m.dispatchTask = &asyncTask{cancel: cancel}

	go m.dispatchOutbound(dispatchCtx)

	for name, channel := range m.channels {
		if err := channel.Start(ctx); err != nil {
			logger.ErrorCF("channels", "Failed to start channel", map[string]interface{}{
				"channel": name,
				"error":   err.Error(),
			})
		}

		queue := m.dispatchQueues[name]
		go m.startChannelDispatcher(dispatchCtx, name, channel, queue)
	}

	return nil
}

func (m *Manager) ReloadConfig(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	m.mu.Lock()
	ctx := m.runCtx
	oldChannels := m.channels
	oldDispatch := m.dispatchTask
	m.config = cfg
	m.channels = make(map[string]Channel)
	m.dispatchTask = nil
	m.mu.Unlock()

	if oldDispatch != nil {
		oldDispatch.cancel()
	}
	for name, channel := range oldChannels {
		if err := channel.Stop(ctx); err != nil {
			logger.ErrorCF("channels", "Error stopping channel during reload", map[string]interface{}{
				"channel": name,
				"error":   err.Error(),
			})
		}
	}

	m.mu.Lock()
	if err := m.initChannels(); err != nil {
		m.mu.Unlock()
		return err
	}
	newChannels := make([]Channel, 0, len(m.channels))
	for _, channel := range m.channels {
		newChannels = append(newChannels, channel)
	}
	if ctx != nil && len(newChannels) > 0 {
		dispatchCtx, cancel := context.WithCancel(ctx)
		m.dispatchTask = &asyncTask{cancel: cancel}
		m.mu.Unlock()
		go m.dispatchOutbound(dispatchCtx)
		for name, channel := range m.channels {
			if err := channel.Start(ctx); err != nil {
				logger.ErrorCF("channels", "Failed to restart channel during reload", map[string]interface{}{
					"error": err.Error(),
				})
			}
			queue := m.dispatchQueues[name]
			go m.startChannelDispatcher(dispatchCtx, name, channel, queue)
		}
		logger.InfoCF("channels", "Channels reloaded", map[string]interface{}{
			"count": len(m.channels),
		})
		return nil
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) StopAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.dispatchTask != nil {
		m.dispatchTask.cancel()
		m.dispatchTask = nil
	}

	for name, channel := range m.channels {
		if err := channel.Stop(ctx); err != nil {
			logger.ErrorCF("channels", "Error stopping channel", map[string]interface{}{
				"channel": name,
				"error":   err.Error(),
			})
		}
	}

	return nil
}

func (m *Manager) dispatchOutbound(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			msg, ok := m.bus.SubscribeOutbound(ctx)
			if !ok {
				continue
			}

			if constants.IsInternalChannel(msg.Channel) {
				continue
			}

			m.mu.RLock()
			queue, exists := m.dispatchQueues[msg.Channel]
			m.mu.RUnlock()

			if !exists {
				logger.WarnCF("channels", "Unknown channel for outbound message", map[string]interface{}{
					"channel": msg.Channel,
				})
				continue
			}

			select {
			case queue <- msg:
			default:
				logger.WarnCF("channels", "Dispatch queue full for channel, dropping message", map[string]interface{}{
					"channel": msg.Channel,
				})
			}
		}
	}
}

func (m *Manager) startChannelDispatcher(ctx context.Context, name string, ch Channel, queue chan bus.OutboundMessage) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-queue:
			if err := sendOutboundMessage(ctx, ch, msg); err != nil {
				logger.ErrorCF("channels", "Error sending message to channel", map[string]interface{}{
					"channel": name,
					"error":   err.Error(),
				})
			}
		}
	}
}

func (m *Manager) GetChannel(name string) (Channel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	channel, ok := m.channels[name]
	return channel, ok
}

// SetNativeClientStore wires the SQLite native client repository into the
// native channel's auth manager. No-op if the native channel is not enabled.
func (m *Manager) SetNativeClientStore(repo *store.NativeClientRepo) {
	if ch, ok := m.GetChannel("native"); ok {
		if nc, ok := ch.(*NativeChannel); ok {
			nc.auth.SetStore(repo)
		}
	}
}

func (m *Manager) GetStatus() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := make(map[string]interface{})
	for name, channel := range m.channels {
		status[name] = map[string]interface{}{
			"enabled": true,
			"running": channel.IsRunning(),
		}
	}
	return status
}

func (m *Manager) GetEnabledChannels() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.channels))
	for name := range m.channels {
		names = append(names, name)
	}
	return names
}

func (m *Manager) RegisterChannel(name string, channel Channel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[name] = channel
}

func (m *Manager) UnregisterChannel(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.channels, name)
}

func (m *Manager) SendToChannel(ctx context.Context, channelName, chatID, content string) error {
	m.mu.RLock()
	channel, exists := m.channels[channelName]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("channel %s not found", channelName)
	}

	msg := bus.OutboundMessage{
		Channel: channelName,
		ChatID:  chatID,
		Content: content,
	}

	return channel.Send(ctx, msg)
}
