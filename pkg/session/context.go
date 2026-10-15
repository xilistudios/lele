package session

// Context-window management: compaction and message exclusion.
//
// These operations shrink what is sent to the model without deleting history:
// ExcludeOldMessagesFromContext flags messages as excluded, CompactSession
// replaces old turns with a summary, and ShouldStartFreshSession decides when
// a session is too stale to reuse.

import (
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// isToolResultMessage returns true if the message is a tool result
// (role "tool" with a non-empty ToolCallID, or role "user" with ToolCallID).
func isToolResultMessage(msg providers.Message) bool {
	return (msg.Role == "tool" || msg.Role == "user") && msg.ToolCallID != ""
}

// summaryMessageMarker is the header the agent layer prepends when it injects
// the compaction summary as a "user" message (pkg/agent/context.go
// summaryMessageHeader). pkg/session must not import pkg/agent (import cycle),
// so the literal is duplicated here — keep both in sync.
const summaryMessageMarker = "## Summary of Previous Conversation\n\n"

// preservedUserMessages is how many of the most recent human user turns are
// never excluded from the context by a compaction. The summary is lossy, so the
// user's most recent requests are kept verbatim.
const preservedUserMessages = 2

// syntheticUserMessageMarkers are the prefixes of messages that are persisted
// with Role "user" but were not written by the human: runtime guidance,
// injected context, notifications and compaction markers. They must never
// consume one of the preserved recent-user-message slots.
// Providers.Message carries no provenance field, so this list is the only way
// to tell an injected message from a real human turn. Keep it in sync with the
// writers: pkg/agent/llm_runner.go (empty-response and malformed-tool-call
// guidance), pkg/agent/loop_detector.go (loop guidance),
// pkg/agent/message_processor.go ("[System: <sender>]" notifications and the
// subagent-interruption warning), pkg/tools/toolloop.go (compaction summary and
// continue prompt), pkg/agent/context.go (summary header).
var syntheticUserMessageMarkers = []string{
	summaryMessageMarker,
	"[Context compacted",
	"[The context was compacted",
	"[System:",
	"⚠",                                // guidance/injections: "⚠️ GUIDANCE: ...", "⚠ El gateway se reinició..."
	"Your previous response was empty", // empty-response retry guidance
	"Your previous tool call was malformed",
}

// isHumanUserMessage reports whether msg is a turn written by the human user:
// role "user", not a tool result (some providers serialize tool results with
// role "user") and not a runtime-injected synthetic message.
func isHumanUserMessage(msg providers.Message) bool {
	if msg.Role != "user" || msg.ToolCallID != "" {
		return false
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return false
	}
	for _, marker := range syntheticUserMessageMarkers {
		if strings.HasPrefix(content, marker) {
			return false
		}
	}
	return true
}

// lastHumanUserMessageIndices returns the indices of the last n human user
// messages in messages, oldest first. Fewer than n indices are returned when
// the conversation has fewer human turns.
func lastHumanUserMessageIndices(messages []providers.Message, n int) []int {
	if n <= 0 {
		return nil
	}
	idx := make([]int, 0, n)
	for i := len(messages) - 1; i >= 0 && len(idx) < n; i-- {
		if isHumanUserMessage(messages[i]) {
			idx = append(idx, i)
		}
	}
	// reverse in place → oldest first
	for l, r := 0, len(idx)-1; l < r; l, r = l+1, r-1 {
		idx[l], idx[r] = idx[r], idx[l]
	}
	return idx
}

// ExcludeOldMessagesFromContext marks the first len(messages)-keepCount messages
// as excluded from the LLM context, preserving them in storage for the web UI.
// If keepCount <= 0, all messages outside the preserved set are excluded.
//
// The first message (index 0) plus the last preservedUserMessages human user
// turns are never excluded — the summary is lossy so the user's most recent
// requests must survive compaction verbatim. If any of these were previously
// excluded (by an older compaction or legacy version), they are un-excluded.
//
// The persisted excludedRange always spans from the first changed index to one
// past the last changed index. Non-excluded preserved messages inside the span
// are persisted as holes (saveExcludedRangeUnlocked writes each row's actual
// ExcludeFromContext value).
func (sm *SessionManager) ExcludeOldMessagesFromContext(key string, keepCount int) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return
		}
	}

	if len(session.Messages) <= keepCount {
		return
	}

	excludeUpTo := len(session.Messages) - keepCount

	// Adjust boundary to avoid splitting tool_use/tool_result groups.
	//
	// #343: when the first kept message (at excludeUpTo) is a tool result,
	// move the boundary BACK to the assistant that owns the tool group
	// (located by ToolCallID) so the whole group stays in the kept tail. The
	// previous forward-push walked the boundary to the end of the history on
	// the degenerate shape (assistant with N tool_calls + N results, small
	// keepCount), leaving the requested keepCount tail empty — only index 0
	// and the pinned human turns still survived (#340): the no-emptying
	// invariant then reduced eviction to a no-op that kept excluded rows
	// resident in RAM, and the cold-load fallback could leave an orphaned
	// tool_result at resident[0].
	if excludeUpTo < len(session.Messages) && isToolResultMessage(session.Messages[excludeUpTo]) {
		msgs := session.Messages

		// 1. Walk back over the contiguous run of tool results to its start.
		start := excludeUpTo
		for start > 0 && isToolResultMessage(msgs[start-1]) {
			start--
		}

		// 2. Locate the owner by ToolCallID in the bounded window
		// [max(0, start-ownerScanWindow), start). In realistic layouts the
		// owner is the message immediately before the run, so this is O(1);
		// the bound avoids an O(len) scan when the owner is absent (perf
		// hygiene). Matching by ToolCallID — not adjacency — is what keeps
		// interleaved layouts free of orphans.
		const ownerScanWindow = 8
		tid := msgs[excludeUpTo].ToolCallID
		owner := -1
		lo := start - ownerScanWindow
		if lo < 0 {
			lo = 0
		}
		for j := start - 1; j >= lo; j-- {
			if msgs[j].Role != "assistant" {
				continue
			}
			for _, tc := range msgs[j].ToolCalls {
				if tc.ID == tid {
					owner = j
					break
				}
			}
			if owner >= 0 {
				break
			}
		}

		// 3. Owner found → keep the whole group. Otherwise (no owner anywhere
		// before the run, or an owner further back than ownerScanWindow) keep
		// the run and let providers.HealToolCallPairs drop any orphan at
		// request time: the owner can still be excluded while its result stays
		// in context, so the fallback branch deliberately leaves an orphan for
		// the runtime to heal rather than splitting the group.
		if owner >= 0 {
			excludeUpTo = owner
		} else {
			excludeUpTo = start
		}
	}

	// Also check: if the last excluded message is an assistant with tool_use
	// but the first kept message is NOT a tool_result, the tool_use blocks
	// are orphaned (no results). Move the boundary back to also exclude
	// this assistant message.
	if excludeUpTo > 0 {
		lastExcluded := session.Messages[excludeUpTo-1]
		if lastExcluded.Role == "assistant" && len(lastExcluded.ToolCalls) > 0 {
			// The assistant has tool_use blocks. Check if the next message
			// (first kept) is a tool_result for those.
			if excludeUpTo >= len(session.Messages) || !isToolResultMessage(session.Messages[excludeUpTo]) {
				// No tool_results follow — the tool_use is orphaned.
				// Move boundary back to also exclude this assistant message.
				excludeUpTo--
			}
		}
	}

	// Build the preserved set: index 0 ∪ last preservedUserMessages human user turns.
	// Index 0 is always pinned (original user request); the recent human turns
	// are pinned because the summary is lossy and the user's latest requests
	// must survive verbatim.
	preserved := make(map[int]struct{})
	if len(session.Messages) > 0 {
		preserved[0] = struct{}{}
	}
	for _, idx := range lastHumanUserMessageIndices(session.Messages, preservedUserMessages) {
		preserved[idx] = struct{}{}
	}

	// Legacy migration: un-exclude index 0 if it was previously excluded.
	rangeStart := 1
	if len(session.Messages) > 0 && session.Messages[0].ExcludeFromContext {
		session.Messages[0].ExcludeFromContext = false
		rangeStart = 0
	}

	// Track the hi-water mark of the persisted span (one past the last
	// changed index). Start at excludeUpTo; raise it for any un-excluded
	// preserved message that sits at or after excludeUpTo.
	hi := excludeUpTo

	if excludeUpTo <= 1 {
		// The normal exclusion range is empty or trivial. Still check for
		// pending un-exclusions of preserved messages ≥ 1 (a pin that was
		// excluded by an earlier compaction and now needs to be brought back).
		changed := rangeStart == 0 // index 0 was un-excluded above
		for idx := range preserved {
			if idx == 0 {
				continue
			}
			if idx < len(session.Messages) && session.Messages[idx].ExcludeFromContext {
				session.Messages[idx].ExcludeFromContext = false
				changed = true
				if idx+1 > hi {
					hi = idx + 1
				}
			}
		}
		if !changed {
			// Nothing changed at all — preserve today's return behaviour.
			return
		}
		// The range is semi-open [rangeStart, hi). If rangeStart changed
		// (index 0 was un-excluded), the range must include it — an empty
		// range makes saveUnlocked skip the targeted UPDATE, leaving the
		// un-excluded index 0 persisted as excluded=true in SQLite.
		if hi <= rangeStart {
			hi = rangeStart + 1
		}
		session.Updated = time.Now()
		session.excludedRange = [2]int{rangeStart, hi}
		session.excludeBoundary = hi
		session.bumpEpoch()
		session.publishViewLocked() // ExcludeFromContext flags changed in place
		return
	}

	// Exclude indices in [1, excludeUpTo) that are NOT in the preserved set.
	for i := 1; i < excludeUpTo; i++ {
		if _, pin := preserved[i]; !pin {
			session.Messages[i].ExcludeFromContext = true
		}
	}

	// Un-exclude every preserved message that is currently excluded. A pin
	// moves forward as the user keeps writing: an index that was excluded by
	// an earlier compaction can become one of the last preservedUserMessages
	// human turns and must be brought back.
	for idx := range preserved {
		if idx == 0 {
			continue // already handled by the legacy block above
		}
		if idx < len(session.Messages) && session.Messages[idx].ExcludeFromContext {
			session.Messages[idx].ExcludeFromContext = false
			if idx+1 > hi {
				hi = idx + 1
			}
		}
	}

	// The range is semi-open [rangeStart, hi). If rangeStart changed
	// (index 0 was un-excluded), the range must include it — an empty
	// range makes saveUnlocked skip the targeted UPDATE, leaving the
	// un-excluded index 0 persisted as excluded=true in SQLite.
	// NOTE: In this normal-path branch (excludeUpTo >= 2), the condition
	// hi <= rangeStart is unreachable: hi starts at excludeUpTo (>=2),
	// rangeStart is 0 or 1, and hi only grows. Kept as a defensive mirror
	// of the identical clamp in the early-exit path above, where the
	// condition IS reachable (hi may stay at excludeUpTo=0 while
	// rangeStart=1 if index 0 was un-excluded).
	if hi <= rangeStart {
		hi = rangeStart + 1
	}
	session.Updated = time.Now()
	session.excludedRange = [2]int{rangeStart, hi}
	session.excludeBoundary = hi
	session.bumpEpoch()
	session.publishViewLocked() // ExcludeFromContext flags changed in place
}

// EvictImageContent replaces the image_url/video_url content parts of every
// image-bearing message older than the keepLast most recent ones with a text
// placeholder, persists the rewrite and returns how many parts were evicted.
//
// It is the durable counterpart of providers.ReduceImageParts and exists so a
// session that was rejected for its request size stays clean: the next turn
// rebuilds its messages from storage, so an in-memory-only reduction would
// resend the same payload forever.
//
// A call that would evict nothing is a true no-op: no message is marked
// modified, no epoch is bumped and nothing is saved (file dirtiness drives
// incremental saves and view rebuilds, so a cleanup that changed nothing must
// not dirty the session). session.Updated, metaDirty and excludedRange are
// never touched either: this is maintenance, not user activity, and bumping
// Updated would move the session out of the idle-eviction window
// (ShouldStartFreshSession reads it).
//
// When sm.sessionRepo is nil (store-less manager, as in many tests) the
// in-memory rewrite still happens and Save is a no-op — that is the correct
// behaviour.
func (sm *SessionManager) EvictImageContent(key string, keepLast int) (int, error) {
	evicted := sm.reduceImageContentUnlocked(key, keepLast)
	if evicted == 0 {
		// True no-op: nothing was evicted, so nothing was marked modified and
		// there is nothing to persist.
		return 0, nil
	}
	// The lock was released by the helper above; Save takes sm.mu itself. A
	// persistence error must still report the parts evicted in memory.
	return evicted, sm.Save(key)
}

// reduceImageContentUnlocked applies providers.ReduceImageParts to the stored
// session under the write lock and returns how many parts were evicted
// (0 = nothing to evict, session untouched). Callers persist separately, after
// the lock has been released.
func (sm *SessionManager) reduceImageContentUnlocked(key string, keepLast int) int {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return 0
		}
	}

	reduced, evicted := providers.ReduceImageParts(session.Messages, keepLast)
	if evicted == 0 {
		return 0
	}

	// Mark exactly the indices whose message changed so the incremental save
	// rewrites only stale rows (markModified keeps the lowest index and drives
	// saveIncrementalUnlocked).
	for i := range session.Messages {
		before, _ := providers.LiveMultimodalParts(session.Messages[i : i+1])
		after, _ := providers.LiveMultimodalParts(reduced[i : i+1])
		if before != after {
			session.markModified(i)
		}
	}
	session.Messages = reduced
	session.publishViewLocked()
	return evicted
}

// CompactSession applies a loop-compaction result to the persisted session:
// it stores the summary produced by the tool-loop compactor, marks all but
// the last keepCount messages as excluded from context, persists the change,
// and optionally evicts excluded messages from memory when evict is true.
// It returns an error only if persistence fails; state mutations are applied
// before the save attempt so callers can decide whether to continue.
func (sm *SessionManager) CompactSession(key string, summary string, keepCount int, evict bool) error {
	sm.SetSummary(key, summary)
	sm.ExcludeOldMessagesFromContext(key, keepCount)
	err := sm.Save(key)
	if err != nil {
		return err
	}
	if evict {
		sm.EvictExcludedMessages(key)
	}
	sm.IncrementCompactionCount(key)
	return nil
}

func (sm *SessionManager) ShouldStartFreshSession(key string, threshold time.Duration) (bool, time.Duration) {
	if threshold <= 0 {
		return false, 0
	}

	sm.ensureLoaded()
	sm.mu.Lock() // write lock for loadSessionFromDisk
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
	}
	if !ok || session == nil {
		return false, 0
	}

	if len(session.Messages) == 0 && strings.TrimSpace(session.Summary) == "" {
		return false, 0
	}

	lastActivity := session.Updated
	if lastActivity.IsZero() {
		lastActivity = session.Created
	}
	if lastActivity.IsZero() {
		return false, 0
	}

	idle := time.Since(lastActivity)
	if idle <= threshold {
		return false, idle
	}

	return true, idle
}
