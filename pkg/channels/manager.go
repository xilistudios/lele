// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

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

	// outboundSpooler and peerFlusher carry the durable outbound seam into the
	// dispatcher goroutines. Unlike BaseChannel's InboundSpooler - which is set
	// once before a channel starts and read by that channel's own goroutines -
	// these are read from every dispatcher goroutine while SetOutboundSpooler
	// may still be running, so both are guarded by mu.
	outboundSpooler OutboundCompleter
	peerFlusher     PeerFlusher

	mu sync.RWMutex
}

// OutboundCompleter closes the loop on the spool row that backed an outbound
// message. Implemented by *durable.Outbound; pkg/channels deliberately does not
// import pkg/durable - this interface is the seam, exactly as InboundSpooler is
// on the publishing side.
//
// The three methods are the whole vocabulary of "how far did the send get", and
// picking the wrong one is a user-visible bug: Complete deletes a row that every
// chunk of was delivered, Release hands back a row that nothing was sent for so
// the pump can replay it whole, and Forget deletes a row that is now only
// partially on the wire - the one state that must never be replayed, because a
// replay would duplicate the chunks already out.
//
// All three take the message rather than a bare id so the implementation can log
// channel, chat and preview alongside the row, and all three are no-ops on a
// message with SpoolID == 0, which is what makes the flag-off path free.
type OutboundCompleter interface {
	Complete(msg *bus.OutboundMessage) bool
	Forget(msg *bus.OutboundMessage, reason string) bool
	Release(msg *bus.OutboundMessage) bool
}

// PeerFlusher wakes the durable outbound pump so rows for a peer that just
// reconnected go out immediately. Implemented by *durable.Outbound.
//
// It is a signal only: it never claims or delivers anything itself, which is
// what keeps the pump the single owner of row claims. A nil PeerFlusher means
// durability is off and no wake-up is needed.
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
				// Defensive: ShouldSpoolOutbound already excludes internal
				// channels, so this is a no-op in practice - SpoolID is 0.
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
				// Release, NOT Forget: a disabled channel can come back, and the
				// pump's own deliver will then fail with unknown-channel and climb
				// attempts until it dead-letters. Nothing reached the wire here.
				m.releaseOutbound(msg, nil)
				continue
			}

			select {
			case queue <- msg:
			default:
				logger.WarnCF("channels", "Dispatch queue full for channel, dropping message", map[string]interface{}{
					"channel": msg.Channel,
				})
				// The bus accepted this message and the per-channel queue killed
				// it: without the release the durability paid for is lost at the
				// last hop. The pump retries within a tick and very likely delivers.
				m.releaseOutbound(msg, nil)
			}
		}
	}
}

// EventSignalConsumer is implemented by channels that interpret
// bus.OutboundMessage.Event themselves inside Send and therefore must receive
// signals even when those signals carry no content.
//
// The channel dispatcher drops contentless events by default (see
// shouldDropEventSignal) because most channels ignore msg.Event and would
// render a signal as an empty message. A channel that adds handling for a new
// event declares it here, so the guard can never swallow a signal its own
// channel is waiting for — the mistake a channel-name allowlist would invite.
type EventSignalConsumer interface {
	ConsumesEvent(event string) bool
}

// shouldDropEventSignal reports whether an event-only outbound message must be
// kept away from a channel's Send.
//
// A message with Event != "" is a signal, not content. The same FIFO queue
// feeds every channel, and channels that do not switch on msg.Event would
// render an empty-content signal as an empty bubble. Dropping contentless
// signals before Send is the structural guard: a future event can never become
// a blank message on a channel that does not handle it.
//
// Channels that do handle events (native, telegram for turn.end) opt out per
// event by implementing EventSignalConsumer.
//
// Signals that carry content or attachments are always passed through:
// today's behavior for content-carrying events (group.*, verbose tool output)
// is preserved, and no real message can be lost.
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
				// Defensive: spooled rows are never events (ShouldSpoolOutbound
				// excludes them), so this forgets nothing real - it just makes it
				// impossible for a dropped signal to leave a row behind.
				m.forgetOutbound(msg, forgetReasonContentlessSignal)
				continue
			}
			o := m.SendNow(ctx, ch, msg)
			switch {
			case o.Delivered:
				// First action after success: every instruction between the send
				// and the delete widens the at-least-once duplicate window.
				m.completeOutbound(msg)
			case o.SentAnyChunk:
				// Part of the message is on the wire. Releasing would replay it,
				// so the row is dead-lettered instead.
				m.forgetOutbound(msg, o.Err.Error())
			default:
				m.releaseOutbound(msg, o.Err)
			}
		}
	}
}

// SendOutcome reports how far sending one outbound message got. It mirrors
// durable.Delivery field-for-field on purpose: two layer vocabularies, joined
// only by the gateway closure.
type SendOutcome struct {
	// Delivered means every chunk reached the channel.
	Delivered bool
	// SentAnyChunk means at least one chunk went out, so a failure is partial
	// and must never be replayed. It is only ever true together with a non-nil
	// Err - a chunk that failed is the only way to be partway out - which is what
	// lets callers report o.Err.Error() unguarded.
	SentAnyChunk bool
	// Err is the first chunk failure. It is nil only when Delivered. A non-nil
	// Err with SentAnyChunk=false is a transient failure nobody has seen a piece
	// of yet - the row is retryable and the caller Releases it; a non-nil Err
	// with SentAnyChunk=true is a partial send that replay would duplicate -
	// the caller Forgets the row instead. Note the direction: SentAnyChunk
	// implies Err, never the other way round - sendNotStarted returns Err with
	// SentAnyChunk=false - which is what lets callers report o.Err.Error()
	// unguarded on the SentAnyChunk path.
	Err error
}

// SendNow delivers msg on an already-resolved channel: the single send path for
// both the live dispatcher and the durable replay pump. Splitting into chunks,
// the retry policy per chunk and the tri-state result are shared by both, so
// replay can never diverge from live traffic.
//
// It touches no spool row: closing the cycle is the caller's job, because only
// the caller knows whether it is the live dispatcher (which must Complete or
// Forget) or a replay pass (which must not double-count attempts).
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

// SendToPeer is SendNow for callers that only know the channel's name, which is
// what a spool row carries. The gateway's replay deliverer goes through here so
// an unknown channel is reported as an ordinary send failure - transient by
// design: the pump releases the row and retries, and a channel that stays gone
// climbs to the attempt ceiling and dead-letters on its own.
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

// SetOutboundSpooler wires the durable outbound seam into the manager and wakes
// the pump through f when a native peer reconnects. Call after channels are
// constructed and before StartAll; nil,nil turns it off again.
//
// Unlike SetInboundSpooler, which hands the spooler to every channel, this one
// stores the pair on the manager: it is the CONSUMER of durability, so the
// dispatchers need it, not the publishers. The only propagation is the flusher to
// whichever channel can signal a reconnect - today NativeChannel, matched through
// the outboundFlushSetter capability rather than by name so a future channel that
// grows the same need is covered without touching this loop. When no registered
// channel implements the setter the loop is a plain no-op: the pump still retries
// on its own tick, only without the instant wake-up on reconnect.
//
// The two fields are written under m.mu.Lock because dispatcher goroutines read
// them concurrently.
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

// outboundFlushSetter is the seam Manager.SetOutboundSpooler uses to hand the
// pump's wake-up signal to the channel that detects peer reconnects. Satisfied by
// NativeChannel, which owns the WebSocket clients.
type outboundFlushSetter interface {
	SetOutboundFlusher(f PeerFlusher)
}

// completeOutbound deletes the row behind a fully delivered message. It is the
// first thing the dispatcher does after a successful send: the window between the
// last chunk and the delete is the only duplicate window this path has, so nothing
// may be squeezed in before it. No-op without a spooler or a row.
func (m *Manager) completeOutbound(msg bus.OutboundMessage) {
	m.mu.RLock()
	sp := m.outboundSpooler
	m.mu.RUnlock()

	if sp == nil || msg.SpoolID == 0 {
		return
	}
	sp.Complete(&msg)
}

// releaseOutbound hands the row behind a message that never started going out
// back to the pending set, so the pump replays it whole. The error is the reason
// the send failed, or nil when routing dropped the message; when there is one it
// is logged here, which keeps the dispatcher's long-standing "Error sending
// message to channel" line on exactly the paths that used to print it. No-op
// without a spooler or a row.
//
// The level follows whoever owns the retry. When the row survives this call -
// SpoolID set and a spooler wired - Release puts it back in pending and the pump
// will send it again, so the line is a WARN: an ERROR on every retry tick would
// cry wolf at the operator while the queue is healthy and working. Without a
// durable row nobody is going to retry, and the ERROR stands exactly as before,
// which also keeps the flag-off contract byte-identical.
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

// forgetReasonContentlessSignal labels a row dropped because the dispatcher's
// event guard swallowed a contentless signal. It is not a send failure, so
// forgetOutbound must not log it as one - the guard already prints its own debug
// line for that case.
const forgetReasonContentlessSignal = "contentless-signal"

// forgetOutbound dead-letters the row behind a message that is partially on the
// wire. Replay would duplicate the chunks already sent, and a user would rather
// lose the tail with a log than read the beginning twice, so this deletes without
// retrying. reason is the send error, or the label of the routing drop. No-op
// without a spooler or a row.
//
// A real send failure is logged here, whatever the spool wiring says, so the
// dispatcher keeps the "Error sending message to channel" line it has always
// printed: with durability off the row does not exist, but the operator still has
// to see that half a message went out and the rest did not.
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

// SetInboundSpooler wires the durable inbound spooler into every registered
// channel, so all inbound messages are backed by the spool before publish.
// Call after channels are constructed and before StartAll.
//
// Channels are matched through the spoolerSetter interface rather than by
// name, so a channel added later is covered without touching this loop:
// every channel in this package either embeds *BaseChannel - whose setter is
// promoted to the outer type - or forwards to its base explicitly, as
// NativeChannel does because it keeps the base in a named field. Anything that
// implements neither (a custom channel registered by an embedder) is skipped
// silently: its messages simply stay unpersisted, which is exactly the
// behaviour before durability existed.
//
// s may be nil, which turns spooling off again for every channel.
func (m *Manager) SetInboundSpooler(s InboundSpooler) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, channel := range m.channels {
		if setter, ok := channel.(spoolerSetter); ok {
			setter.SetInboundSpooler(s)
		}
	}
}

// spoolerSetter is the seam Manager.SetInboundSpooler uses to reach a channel's
// BaseChannel without enumerating channel types. Satisfied by *BaseChannel and
// therefore by every channel that embeds it.
type spoolerSetter interface {
	SetInboundSpooler(s InboundSpooler)
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
