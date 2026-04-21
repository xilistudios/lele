package bus

import (
	"context"
	"sync"
	"sync/atomic"
)

type MessageBus struct {
	inbound         chan InboundMessage
	outbound        chan OutboundMessage
	handlers        map[string]MessageHandler
	closed          bool
	mu              sync.RWMutex
	droppedInbound  atomic.Int64
	droppedOutbound atomic.Int64
}

func NewMessageBus() *MessageBus {
	return &MessageBus{
		inbound:  make(chan InboundMessage, 500),
		outbound: make(chan OutboundMessage, 500),
		handlers: make(map[string]MessageHandler),
	}
}

func (mb *MessageBus) PublishInbound(msg InboundMessage) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	if mb.closed {
		return
	}
	select {
	case mb.inbound <- msg:
	default:
		mb.droppedInbound.Add(1)
	}
}

func (mb *MessageBus) ConsumeInbound(ctx context.Context) (InboundMessage, bool) {
	select {
	case msg := <-mb.inbound:
		return msg, true
	case <-ctx.Done():
		return InboundMessage{}, false
	}
}

func (mb *MessageBus) PublishOutbound(msg OutboundMessage) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	if mb.closed {
		return
	}
	select {
	case mb.outbound <- msg:
	default:
		mb.droppedOutbound.Add(1)
	}
}

func (mb *MessageBus) SubscribeOutbound(ctx context.Context) (OutboundMessage, bool) {
	select {
	case msg := <-mb.outbound:
		return msg, true
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
