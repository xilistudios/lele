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

// ExcludeOldMessagesFromContext marks the first len(messages)-keepCount messages
// as excluded from the LLM context, preserving them in storage for the web UI.
// If keepCount <= 0, all messages are excluded.
//
// The first message (index 0) is always preserved — it usually contains the
// original user request/goal and must survive compaction. If it was previously
// excluded (by an older version), it is un-excluded.
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
	// If the first kept message (at excludeUpTo) is a tool result whose
	// corresponding assistant tool_use is in the excluded range, move the
	// boundary forward to exclude the tool_result too. Repeat for any
	// consecutive tool results in the same group.
	for excludeUpTo < len(session.Messages) && isToolResultMessage(session.Messages[excludeUpTo]) {
		excludeUpTo++
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

	// Never exclude the first message (index 0) — it usually contains the
	// original user request/goal and must survive compaction.
	// If it was previously excluded (e.g., by an older version), un-exclude it.
	rangeStart := 1
	if len(session.Messages) > 0 && session.Messages[0].ExcludeFromContext {
		session.Messages[0].ExcludeFromContext = false
		rangeStart = 0
	}

	if excludeUpTo <= 1 {
		if rangeStart == 0 {
			// Only change is un-excluding msg 0 — persist that.
			session.Updated = time.Now()
			session.excludedRange = [2]int{0, 1}
			session.bumpEpoch()
		}
		return
	}

	for i := 1; i < excludeUpTo; i++ {
		session.Messages[i].ExcludeFromContext = true
	}
	session.Updated = time.Now()
	session.excludedRange = [2]int{rangeStart, excludeUpTo}
	session.bumpEpoch()
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
