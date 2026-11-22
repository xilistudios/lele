package channels

import (
	"context"
	"fmt"
	"strings"
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

	outboundSpooler OutboundCompleter
	peerFlusher     PeerFlusher

	nativeClientRepo *store.NativeClientRepo
	inboundSpooler   InboundSpooler

	nativeStoreUnavailable error

	mu sync.RWMutex
}

type OutboundCompleter interface {
	Complete(msg *bus.OutboundMessage) bool
	Forget(msg *bus.OutboundMessage, reason string) bool
	Release(msg *bus.OutboundMessage) bool
}

type PeerFlusher interface {
	FlushPeer(channel, peer string)
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
	if m.nativeClientRepo != nil {
		if ch, ok := m.channels["native"]; ok {
			if nc, ok := ch.(*NativeChannel); ok {
				nc.auth.SetStore(m.nativeClientRepo)
			}
		}
	}
	if m.nativeStoreUnavailable != nil {
		if ch, ok := m.channels["native"]; ok {
			if nc, ok := ch.(*NativeChannel); ok {
				nc.auth.SetStoreUnavailable(m.nativeStoreUnavailable)
			}
		}
	}
	if m.inboundSpooler != nil {
		for _, channel := range m.channels {
			if setter, ok := channel.(spoolerSetter); ok {
				setter.SetInboundSpooler(m.inboundSpooler)
			}
		}
	}
	reloaded := make(map[string]Channel, len(m.channels))
	queues := make(map[string]chan bus.OutboundMessage, len(m.channels))
	for name, channel := range m.channels {
		reloaded[name] = channel
		if q, ok := m.dispatchQueues[name]; ok {
			queues[name] = q
		}
	}
	if ctx != nil && len(reloaded) > 0 {
		dispatchCtx, cancel := context.WithCancel(ctx)
		m.dispatchTask = &asyncTask{cancel: cancel}
		m.mu.Unlock()
		go m.dispatchOutbound(dispatchCtx)
		for name, channel := range reloaded {
			if err := channel.Start(ctx); err != nil {
				logger.ErrorCF("channels", "Failed to restart channel during reload", map[string]interface{}{
					"error": err.Error(),
				})
			}
			go m.startChannelDispatcher(dispatchCtx, name, channel, queues[name])
		}
		logger.InfoCF("channels", "Channels reloaded", map[string]interface{}{
			"count": len(reloaded),
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
				m.releaseOutbound(msg, nil)
				continue
			}

			m.mu.RLock()
			queue, exists := m.dispatchQueues[msg.Channel]
			m.mu.RUnlock()

			if !exists {
				logger.WarnCF("channels", "Unknown channel for outbound message", map[string]interface{}{
					"channel": msg.Channel,
				})
				m.releaseOutbound(msg, nil)
				continue
			}

			select {
			case queue <- msg:
			default:
				logger.WarnCF("channels", "Dispatch queue full for channel, dropping message", map[string]interface{}{
					"channel": msg.Channel,
				})
				m.releaseOutbound(msg, nil)
			}
		}
	}
}

type EventSignalConsumer interface {
	ConsumesEvent(event string) bool
}

func shouldDropEventSignal(ch Channel, msg bus.OutboundMessage) bool {
	if msg.Event == "" {
		return false
	}
	if consumer, ok := ch.(EventSignalConsumer); ok && consumer.ConsumesEvent(msg.Event) {
		return false
	}
	return strings.TrimSpace(msg.Content) == "" && len(msg.Attachments) == 0
}

func (m *Manager) startChannelDispatcher(ctx context.Context, name string, ch Channel, queue chan bus.OutboundMessage) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-queue:
			if shouldDropEventSignal(ch, msg) {
				logger.DebugCF("channels", "Dropped contentless event signal for channel", map[string]interface{}{
					"channel": name,
					"event":   msg.Event,
				})
				m.forgetOutbound(msg, forgetReasonContentlessSignal)
				continue
			}
			o := m.SendNow(ctx, ch, msg)
			switch {
			case o.Delivered:
				m.completeOutbound(msg)
			case o.SentAnyChunk:
				m.forgetOutbound(msg, o.Err.Error())
			default:
				m.releaseOutbound(msg, o.Err)
			}
		}
	}
}

type SendOutcome struct {
	Delivered    bool
	SentAnyChunk bool
	Err          error
}

func (m *Manager) SendNow(ctx context.Context, ch Channel, msg bus.OutboundMessage) SendOutcome {
	res, err := sendOutboundMessage(ctx, ch, msg)
	switch res {
	case sendDelivered:
		return SendOutcome{Delivered: true}
	case sendPartial:
		return SendOutcome{SentAnyChunk: true, Err: err}
	default:
		return SendOutcome{Err: err}
	}
}

func (m *Manager) SendToPeer(ctx context.Context, channelName string, msg bus.OutboundMessage) SendOutcome {
	ch, ok := m.GetChannel(channelName)
	if !ok {
		return SendOutcome{Err: fmt.Errorf("channels: unknown channel %q", channelName)}
	}
	return m.SendNow(ctx, ch, msg)
}

func (m *Manager) GetChannel(name string) (Channel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	channel, ok := m.channels[name]
	return channel, ok
}

func (m *Manager) SetOutboundSpooler(s OutboundCompleter, f PeerFlusher) {
	m.mu.Lock()
	m.outboundSpooler = s
	m.peerFlusher = f
	for _, channel := range m.channels {
		if setter, ok := channel.(outboundFlushSetter); ok {
			setter.SetOutboundFlusher(f)
		}
	}
	m.mu.Unlock()
}

type outboundFlushSetter interface {
	SetOutboundFlusher(f PeerFlusher)
}

func (m *Manager) completeOutbound(msg bus.OutboundMessage) {
	m.mu.RLock()
	sp := m.outboundSpooler
	m.mu.RUnlock()

	if sp == nil || msg.SpoolID == 0 {
		return
	}
	sp.Complete(&msg)
}

func (m *Manager) releaseOutbound(msg bus.OutboundMessage, err error) {
	m.mu.RLock()
	sp := m.outboundSpooler
	m.mu.RUnlock()

	if err != nil {
		if msg.SpoolID != 0 && sp != nil {
			logger.WarnCF("channels", "Error sending message to channel", map[string]interface{}{
				"channel":         msg.Channel,
				"error":           err.Error(),
				"durable_release": true,
			})
		} else {
			logger.ErrorCF("channels", "Error sending message to channel", map[string]interface{}{
				"channel": msg.Channel,
				"error":   err.Error(),
			})
		}
	}
	if sp == nil || msg.SpoolID == 0 {
		return
	}
	sp.Release(&msg)
}

const forgetReasonContentlessSignal = "contentless-signal"

func (m *Manager) forgetOutbound(msg bus.OutboundMessage, reason string) {
	m.mu.RLock()
	sp := m.outboundSpooler
	m.mu.RUnlock()

	if reason != forgetReasonContentlessSignal {
		logger.ErrorCF("channels", "Error sending message to channel", map[string]interface{}{
			"channel": msg.Channel,
			"error":   reason,
		})
	}
	if sp == nil || msg.SpoolID == 0 {
		return
	}
	sp.Forget(&msg, reason)
}

func (m *Manager) SetInboundSpooler(s InboundSpooler) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.inboundSpooler = s

	for _, channel := range m.channels {
		if setter, ok := channel.(spoolerSetter); ok {
			setter.SetInboundSpooler(s)
		}
	}
}

type spoolerSetter interface {
	SetInboundSpooler(s InboundSpooler)
}

func (m *Manager) SetNativeClientStore(repo *store.NativeClientRepo) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nativeClientRepo = repo

	if ch, ok := m.channels["native"]; ok {
		if nc, ok := ch.(*NativeChannel); ok {
			nc.auth.SetStore(repo)
		}
	}
}

func (m *Manager) SetNativeStoreUnavailable(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nativeStoreUnavailable = err

	if ch, ok := m.channels["native"]; ok {
		if nc, ok := ch.(*NativeChannel); ok {
			nc.auth.SetStoreUnavailable(err)
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
