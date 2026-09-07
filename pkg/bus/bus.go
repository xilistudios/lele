package bus

import (
	"context"
	"sync"
	"sync/atomic"
)

// OutboundSpooler is the seam between the bus and durable outbound storage.
// The bus never decides eligibility itself: Enqueue is called for every
// outbound publish and the implementation may decline (return false, leave
// msg.SpoolID at 0) for events, streaming chunks, internal channels, or
// when durability is off. Release hands a born-claimed row back to the
// pending set when the bus could not accept the message. Both calls must
// be fast, non-blocking for the publisher, and nil-safe; a false return
// NEVER prevents the publish path.
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

	// outboundSpooler is the durable-write seam consulted by PublishOutbound.
	// It is guarded by its own mutex, not mu: mu.RLock is held across the
	// publish select, and SQLite writes performed under it would block
	// Close() (which takes mu.Lock) and every RegisterHandler call.
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

// PublishInbound enqueues msg for the agent loop without blocking.
//
// It returns true if the message was accepted into the inbound queue;
// false if the bus is closed or the queue is full. Callers that have
// already started user-visible side effects (typing indicators,
// placeholder messages) MUST roll them back when this returns false.
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
	case msg := <-mb.inbound:
		return msg, true
	case <-ctx.Done():
		return InboundMessage{}, false
	}
}

// SetOutboundSpooler wires the durable outbound seam. It must be called
// before anything publishes; nil means the old path, byte-for-byte: no row
// is written, msg.SpoolID stays 0, and PublishOutbound behaves exactly as
// it did before this seam existed.
func (mb *MessageBus) SetOutboundSpooler(s OutboundSpooler) {
	mb.spoolerMu.Lock()
	mb.outboundSpooler = s
	mb.spoolerMu.Unlock()
}

// PublishOutbound enqueues msg for the channel dispatchers without blocking.
//
// When an outbound spooler is wired it runs FIRST: the row is written and
// claimed before the bus takes any lock, so a message that survives the
// publish is durable from that instant on. msg is a value parameter, so the
// SpoolID the spooler tags is carried by the copy handed to the channel and
// stays visible to the consumer downstream. If the bus then refuses the
// message - closed, or the buffer is full - the row is released back to the
// pending set so the replay pump delivers it instead of the drop swallowing
// it.
func (mb *MessageBus) PublishOutbound(msg OutboundMessage) {
	mb.spoolerMu.RLock()
	sp := mb.outboundSpooler
	mb.spoolerMu.RUnlock()
	if sp != nil {
		// Tags msg.SpoolID when it writes a row; never blocks the publish.
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
