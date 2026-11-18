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
//  3. drainOrDeactivateMidTurnNotices is the atomic "is there anything left?"
//     check at the exit of the loop: it either hands over items and stays
//     active (so the loop keeps running), or deactivates and reports nothing
//     left. Once it returns nil, no notice can enter this queue until the next
//     activation, which is exactly when a new turn starts and would have
//     consumed the inbound fallback anyway.
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
)

// midTurnNotice is one subagent completion waiting to be injected into the
// running turn.
//
// content is the RAW result text — the same string publishSubagentAsyncResult
// publishes as the inbound Content — and NOT the formatted message. Keeping it
// raw is what lets the inbound fallback and the injection path persist the
// identical text: the inbound path wraps it in "[System: subagent] " inside
// processSystemMessage, and the injection path adds the same prefix through
// messageContent when it appends to messages/history. Storing the prefixed
// form here would double the prefix on the fallback.
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
type midTurnNoticeQueue struct {
	mu     sync.Mutex
	active bool
	items  []midTurnNotice
}

// midTurnNoticeQueueFor returns the queue of sessionKey, creating it on first
// use. Every method resolves the session key through ResolveSessionKey so that
// the turn that activates the queue, the subagent callback that enqueues into
// it, and the loop that drains it all agree on the same key even after a /new
// aliasing.
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

// activateMidTurnNotices marks the session's queue as accepting notices. It is
// called by runAgentLoop once the per-session semaphore is held, i.e. exactly
// while a parent turn is running.
func (al *AgentLoop) activateMidTurnNotices(sessionKey string) {
	queue := al.midTurnNoticeQueueFor(sessionKey)
	if queue == nil {
		return
	}
	queue.mu.Lock()
	queue.active = true
	queue.mu.Unlock()
}

// enqueueMidTurnNotice queues one notice for mid-turn injection. It reports
// false when no turn is running for the session (the caller must then use the
// inbound-resume path) or when the session key is unusable.
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

// drainMidTurnNotices takes everything queued and LEAVES the queue active: the
// turn is still running and later completions must still be injectable.
func (al *AgentLoop) drainMidTurnNotices(sessionKey string) []midTurnNotice {
	queue := al.midTurnNoticeQueueFor(sessionKey)
	if queue == nil {
		return nil
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	items := queue.items
	queue.items = nil
	return items
}

// drainOrDeactivateMidTurnNotices is the atomic final-exit check of the loop:
// if anything is queued it drains it and stays active (the caller runs another
// iteration), otherwise it deactivates and returns nil. Because enqueue tests
// the same flag under the same mutex, a nil return guarantees that no notice
// can be appended afterwards until the next activation.
func (al *AgentLoop) drainOrDeactivateMidTurnNotices(sessionKey string) []midTurnNotice {
	queue := al.midTurnNoticeQueueFor(sessionKey)
	if queue == nil {
		return nil
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if len(queue.items) > 0 {
		items := queue.items
		queue.items = nil
		return items
	}
	queue.active = false
	return nil
}

// deactivateMidTurnNotices ends the accepting window and returns whatever was
// still queued, so an abnormal turn exit (LLM error, cancellation, iteration
// or empty-response limit, tool failure) can hand the notices back to the
// inbound bus instead of dropping them.
func (al *AgentLoop) deactivateMidTurnNotices(sessionKey string) []midTurnNotice {
	queue := al.midTurnNoticeQueueFor(sessionKey)
	if queue == nil {
		return nil
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.active = false
	items := queue.items
	queue.items = nil
	return items
}
