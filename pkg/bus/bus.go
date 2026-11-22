package bus

import (
	"context"
	"sync"
	"sync/atomic"
)

type OutboundSpooler interface {
	Enqueue(msg *OutboundMessage) bool
	Release(msg *OutboundMessage) bool
}

type MessageBus struct {
	inbound         chan InboundMessage
	outbound        chan OutboundMessage
	handlers        map[string]MessageHandler
	closed          bool
	mu              sync.RWMutex
	droppedInbound  atomic.Int64
	droppedOutbound atomic.Int64

	outboundSpooler OutboundSpooler
	spoolerMu       sync.RWMutex
}

func NewMessageBus() *MessageBus {
	return &MessageBus{
		inbound:  make(chan InboundMessage, 500),
		outbound: make(chan OutboundMessage, 500),
		handlers: make(map[string]MessageHandler),
	}
}

func (mb *MessageBus) PublishInbound(msg InboundMessage) bool {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	if mb.closed {
		return false
	}
	select {
	case mb.inbound <- msg:
		return true
	default:
		mb.droppedInbound.Add(1)
		return false
	}
}

func (mb *MessageBus) ConsumeInbound(ctx context.Context) (InboundMessage, bool) {
	select {
	case msg, ok := <-mb.inbound:
		return msg, ok
	case <-ctx.Done():
		return InboundMessage{}, false
	}
}

func (mb *MessageBus) SetOutboundSpooler(s OutboundSpooler) {
	mb.spoolerMu.Lock()
	mb.outboundSpooler = s
	mb.spoolerMu.Unlock()
}

func (mb *MessageBus) PublishOutbound(msg OutboundMessage) {
	mb.spoolerMu.RLock()
	sp := mb.outboundSpooler
	mb.spoolerMu.RUnlock()
	if sp != nil {
		sp.Enqueue(&msg)
	}

	mb.mu.RLock()
	if mb.closed {
		mb.mu.RUnlock()
		if msg.SpoolID != 0 {
			sp.Release(&msg)
		}
		return
	}
	select {
	case mb.outbound <- msg:
		mb.mu.RUnlock()
	default:
		mb.mu.RUnlock()
		mb.droppedOutbound.Add(1)
		if msg.SpoolID != 0 {
			sp.Release(&msg)
		}
	}
}

func (mb *MessageBus) SubscribeOutbound(ctx context.Context) (OutboundMessage, bool) {
	select {
	case msg, ok := <-mb.outbound:
		return msg, ok
	case <-ctx.Done():
		return OutboundMessage{}, false
	}
}

func (mb *MessageBus) RegisterHandler(channel string, handler MessageHandler) {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.handlers[channel] = handler
}

func (mb *MessageBus) GetHandler(channel string) (MessageHandler, bool) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	handler, ok := mb.handlers[channel]
	return handler, ok
}

func (mb *MessageBus) Close() {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	if mb.closed {
		return
	}
	mb.closed = true
	close(mb.inbound)
	close(mb.outbound)
}

func (mb *MessageBus) Stats() (inboundLen, inboundCap, droppedInbound int64, outboundLen, outboundCap, droppedOutbound int64) {
	return int64(len(mb.inbound)), int64(cap(mb.inbound)), mb.droppedInbound.Load(),
		int64(len(mb.outbound)), int64(cap(mb.outbound)), mb.droppedOutbound.Load()
}
