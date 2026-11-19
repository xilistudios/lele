// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import "sync"

// Mid-turn subagent notices
// -------------------------
// A subagent that finishes while its PARENT is mid-turn used to be delivered
// through the inbound bus only: publishSubagentAsyncResult claimed the result
// and published a "system" inbound, whose turn then blocked on the per-session
// semaphore (runAgentLoop acquires it at the top) until the parent's turn was
// over. The parent therefore only learned about the completion AFTER it had
// finished its own work, even though the information had been ready for
// minutes.
//
// This queue closes that gap. While a parent turn runs it is "active":
// enqueueMidTurnNotice accepts notices, and the LLM loop injects them into the
// LIVE message context at iteration boundaries (and once more before it would
// exit with a final answer), so the model reacts to each result inside the
// same turn. When the parent is idle the queue is inactive and enqueue returns
// false — the caller keeps the existing inbound-resume path unchanged.
//
// Producer vs. consumer key resolution (deliberately asymmetric)
// -------------------------------------------------------------
// The CONSUMER side (the running turn) resolves the session key exactly ONCE,
// at activation, and keeps the resulting *midTurnNoticeQueue for the whole
// turn: drain, drainOrDeactivate and deactivate are methods on that captured
// identity and never look the key up again. This is required, not a
// micro-optimisation: ResolveSessionKey reads the sessionAliases map, which a
// /new mid-conversation can REMAP while a turn is still running
// (startFreshConversation -> setSessionAlias). If each step re-resolved the
// key, a turn could activate queue A and later drain/deactivate queue B,
// leaving notices that were accepted into A (and whose producer therefore
// skipped the inbound fallback) stranded forever.
//
// The PRODUCER side (publishSubagentAsyncResult) stays by-key and resolves the
// CURRENT alias at enqueue time. Both halves of a remap deliver:
//   - enqueue lands in the queue the running turn captured (alias still points
//     at it, or it was enqueued through a key that resolves to it): the turn
//     drains it mid-turn.
//   - enqueue lands in the NEW key's queue, which is inactive (this turn
//     activated the old one): enqueue returns false and the producer publishes
//     the ordinary inbound, which resumes the parent after the remap.
//
// Delivery invariant (exactly once, never lost)
// ---------------------------------------------
//  1. The result is CLAIMED first (SubagentManager.ClaimResultForInjection in
//     publishSubagentAsyncResult) and enqueued only if the claim was won, so a
//     result has exactly one owner at any moment.
//  2. enqueue and deactivate are serialized by the queue mutex and both test
//     or write the same `active` flag, so an enqueue either happens strictly
//     before the queue is deactivated (the notice is in `items` and the turn's
//     safety flush returns it) or strictly after (enqueue returns false and
//     the caller publishes the inbound instead). There is no window in which a
//     claimed result is neither queued nor handed back.
//  3. drainOrDeactivate is the atomic "is there anything left?" check at the
//     exit of the loop: it either hands over items and stays active (so the
//     loop keeps running), or deactivates and reports nothing left. Once it
//     returns nil, no notice can enter this queue until the next activation,
//     which is exactly when a new turn starts and would have consumed the
//     inbound fallback anyway. The loop only calls it while another iteration
//     is still available; when the iteration budget is spent the notices stay
//     queued and the flush defer below re-publishes them as inbound.
//
// The net effect: every claimed result is persisted exactly once, either
// mid-turn (injected into messages/history) or through the inbound fallback
// (a normal continuation turn).

const (
	// midTurnNoticeSender is the SenderID the subagent inbound path uses
	// (publishSubagentAsyncResult). It is reused here so both delivery paths
	// persist the byte-identical synthetic message.
	midTurnNoticeSender = "subagent"
	// midTurnNoticePrefix is the canonical synthetic marker for such a message
	// (processSystemMessage formats "[System: <sender>] <content>"). It must
	// stay in sync with pkg/tui/system_notice.go and with
	// pkg/session/context.go:syntheticUserMessageMarkers, which classify a
	// persisted message by this prefix.
	midTurnNoticePrefix = "[System: " + midTurnNoticeSender + "] "
	// midTurnNoticeSeparator joins several notices that were drained together
	// into the single user message they are persisted as.
	midTurnNoticeSeparator = "\n\n"
)

// midTurnNotice is one subagent completion waiting to be injected into the
// running turn.
//
// content is the RAW result text — the same string publishSubagentAsyncResult
// publishes as the inbound Content — and NOT the formatted message. Keeping it
// raw is what lets the inbound fallback and the injection path persist the
// identical text: the inbound path wraps it in "[System: subagent] " inside
// processSystemMessage, and the injection path adds the same prefix when it
// appends to messages/history. Storing the prefixed form here would double the
// prefix on the fallback.
type midTurnNotice struct {
	content string
	taskID  string
}

// messageContent renders the notice as the persisted synthetic message text.
func (n midTurnNotice) messageContent() string {
	return midTurnNoticePrefix + n.content
}

// midTurnNoticeQueue holds the notices of one session. The zero value is a
// usable, inactive queue; mu guards both fields.
//
// The methods below are the CONSUMER side of the queue and are always called
// on the pointer captured by activateMidTurnNotices, so a mid-turn alias remap
// cannot make the turn drain a different queue than the one it activated. They
// are nil-safe: a turn with an unusable session key has no queue at all and
// every operation degrades to a no-op.
type midTurnNoticeQueue struct {
	mu     sync.Mutex
	active bool
	items  []midTurnNotice
}

// midTurnNoticeQueueFor returns the queue of sessionKey, creating it on first
// use. It is the by-key LOOKUP used by the producer (and by activation, which
// then hands the resulting pointer to the turn). Resolving the alias here is
// correct for the producer: it must find whatever queue the current
// conversation key maps to.
func (al *AgentLoop) midTurnNoticeQueueFor(sessionKey string) *midTurnNoticeQueue {
	if al == nil || sessionKey == "" {
		return nil
	}
	key := al.ResolveSessionKey(sessionKey)
	if key == "" {
		return nil
	}
	if q, ok := al.midTurnNotices.Load(key); ok {
		if queue, ok := q.(*midTurnNoticeQueue); ok {
			return queue
		}
	}
	queue := &midTurnNoticeQueue{}
	actual, _ := al.midTurnNotices.LoadOrStore(key, queue)
	if existing, ok := actual.(*midTurnNoticeQueue); ok {
		return existing
	}
	return queue
}

// activateMidTurnNotices marks the session's queue as accepting notices and
// returns it. It is called by runAgentLoop once the per-session semaphore is
// held, i.e. exactly while a parent turn is running, and the returned pointer
// becomes the turn's identity for every later operation (drain, exit check,
// flush): the alias is resolved here and NEVER again, so a /new remap during
// the turn cannot split the producer and the consumer across two queues.
//
// It returns nil for an empty or otherwise unusable session key; all queue
// methods are nil-safe, so the caller threads it through unconditionally.
func (al *AgentLoop) activateMidTurnNotices(sessionKey string) *midTurnNoticeQueue {
	queue := al.midTurnNoticeQueueFor(sessionKey)
	if queue == nil {
		return nil
	}
	queue.mu.Lock()
	queue.active = true
	queue.mu.Unlock()
	return queue
}

// enqueueMidTurnNotice queues one notice for mid-turn injection. It reports
// false when no turn is running for the session (the caller must then use the
// inbound-resume path) or when the session key is unusable.
//
// This is the PRODUCER side and stays by-key on purpose: the completion
// callback has no captured queue, so it resolves the CURRENT alias. Either it
// finds the queue the running turn activated (mid-turn delivery) or it finds
// the post-remap queue, which is inactive, and returns false so the caller
// publishes the inbound instead. See the file header.
func (al *AgentLoop) enqueueMidTurnNotice(sessionKey, content, taskID string) bool {
	queue := al.midTurnNoticeQueueFor(sessionKey)
	if queue == nil || content == "" {
		return false
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if !queue.active {
		return false
	}
	queue.items = append(queue.items, midTurnNotice{content: content, taskID: taskID})
	return true
}

// drain takes everything queued and LEAVES the queue active: the turn is still
// running and later completions must still be injectable.
func (q *midTurnNoticeQueue) drain() []midTurnNotice {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.items
	q.items = nil
	return items
}

// drainOrDeactivate is the atomic final-exit check of the loop: if anything is
// queued it drains it and stays active (the caller runs another iteration),
// otherwise it deactivates and returns nil. Because enqueue tests the same
// flag under the same mutex, a nil return guarantees that no notice can be
// appended afterwards until the next activation.
func (q *midTurnNoticeQueue) drainOrDeactivate() []midTurnNotice {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) > 0 {
		items := q.items
		q.items = nil
		return items
	}
	q.active = false
	return nil
}

// deactivate ends the accepting window and returns whatever was still queued,
// so an abnormal turn exit (LLM error, cancellation, iteration or
// empty-response limit, tool failure) can hand the notices back to the inbound
// bus instead of dropping them.
func (q *midTurnNoticeQueue) deactivate() []midTurnNotice {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.active = false
	items := q.items
	q.items = nil
	return items
}
