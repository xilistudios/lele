package session

// Copy-on-write history views.
//
// GetHistoryView is called several times per TUI frame and its contract has
// always been "read-only view, do not mutate" (pkg/agent/agent_providable.go).
// Until now it honoured that contract by copying under the read lock on EVERY
// call: at 6000 messages that is ~328 µs and 1.16 MB per call, ~1 ms and
// ~3.5 MB per frame (3 calls), the single hottest primitive of the render loop
// (docs/perf/tui-long-chat-baseline.md, §4).
//
// This file implements the copy-on-write replacement: writers publish an
// immutable PRIVATE copy of Session.Messages (messageSnapshot) through an
// atomic pointer, and readers hand that slice out without copying.
//
// Why a private copy and not a header aliasing the live array: messages are
// mutated IN PLACE, not only appended —
//   - msg.Content += chunk            (streaming.go AppendAssistantChunk),
//   - *lastMsg = msg                  (manager.go AddFullMessage),
//   - ExcludeFromContext flags        (context.go ExcludeOldMessagesFromContext),
// so a reader that already dropped the lock would observe torn/rewritten
// content if it aliased the session's array. Only a copy is safe.
//
// Validity is tagged with Session.saveEpoch, which every logical mutation
// already bumps (bumpEpoch / markModified). Publication is an optimization,
// never a correctness requirement:
//   - a writer that forgot to publish, or published an older epoch, leaves the
//     snapshot stale → GetHistoryView detects the mismatch and rebuilds the
//     copy itself (correct, just slower);
//   - the reverse cannot happen: a snapshot whose epoch equals saveEpoch always
//     reflects the current Messages (that is the invariant the epoch check
//     guarantees, see publishViewLocked).
//
// The "tail snapshot" shortcut (publishing only the last N messages) is
// explicitly NOT used: pkg/channels/rest_chat.go paginates the resident window
// from index 0, so the view must remain the complete history.

import (
	"github.com/xilistudios/lele/pkg/providers"
)

// messageSnapshot is an immutable, writer-owned copy of a session's Messages
// slice together with the saveEpoch it was taken at.
type messageSnapshot struct {
	// epoch is the value of Session.saveEpoch when view was copied.
	epoch uint64
	// view is a private copy: no other goroutine writes through it, and the
	// session's own slice is never aliased by it.
	view []providers.Message
}

// publishViewLocked replaces the session's published view with a private copy
// of the current Messages slice, stamped with the current saveEpoch, and
// returns that copy (callers on a write-locked path can hand it out directly).
//
// Caller MUST hold sm.mu (write lock): the copy must be a consistent read of
// Messages, and the epoch it carries must be the epoch of exactly this
// content.
//
// The copy is O(n) (providers.Message is 192 B). Structural writers (append,
// in-place replacement, delete, truncate, exclusion, eviction) call this
// eagerly — they run a few times per second at most. Streaming chunk writers
// deliberately do NOT: at chunk rate the copy would cost more than the reads it
// saves, so they leave the snapshot stale and let the epoch check in
// GetHistoryView rebuild it lazily on the next read (see AppendAssistantChunk).
func (s *Session) publishViewLocked() []providers.Message {
	if s == nil {
		return nil
	}
	view := cloneMessages(s.Messages)
	s.viewSnapshot.Store(&messageSnapshot{epoch: s.saveEpoch, view: view})
	return view
}

// cloneMessages returns a private copy of msgs. The returned slice never
// aliases msgs, so it may be shared with readers without a lock.
func cloneMessages(msgs []providers.Message) []providers.Message {
	out := make([]providers.Message, len(msgs))
	copy(out, msgs)
	return out
}
