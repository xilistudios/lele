package session

import (
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

func isToolResultMessage(msg providers.Message) bool {
	return (msg.Role == "tool" || msg.Role == "user") && msg.ToolCallID != ""
}

const summaryMessageMarker = "## Summary of Previous Conversation\n\n"

const preservedUserMessages = 2

var syntheticUserMessageMarkers = []string{
	summaryMessageMarker,
	"[Context compacted",
	"[The context was compacted",
	"[System:",
	"⚠",
	"Your previous response was empty",
	"Your previous tool call was malformed",
}

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
	for l, r := 0, len(idx)-1; l < r; l, r = l+1, r-1 {
		idx[l], idx[r] = idx[r], idx[l]
	}
	return idx
}

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

	if excludeUpTo < len(session.Messages) && isToolResultMessage(session.Messages[excludeUpTo]) {
		msgs := session.Messages

		start := excludeUpTo
		for start > 0 && isToolResultMessage(msgs[start-1]) {
			start--
		}

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

		if owner >= 0 {
			excludeUpTo = owner
		} else {
			excludeUpTo = start
		}
	}

	if excludeUpTo > 0 {
		lastExcluded := session.Messages[excludeUpTo-1]
		if lastExcluded.Role == "assistant" && len(lastExcluded.ToolCalls) > 0 {
			if excludeUpTo >= len(session.Messages) || !isToolResultMessage(session.Messages[excludeUpTo]) {
				excludeUpTo--
			}
		}
	}

	preserved := make(map[int]struct{})
	if len(session.Messages) > 0 {
		preserved[0] = struct{}{}
	}
	for _, idx := range lastHumanUserMessageIndices(session.Messages, preservedUserMessages) {
		preserved[idx] = struct{}{}
	}

	rangeStart := 1
	if len(session.Messages) > 0 && session.Messages[0].ExcludeFromContext {
		session.Messages[0].ExcludeFromContext = false
		rangeStart = 0
	}

	hi := excludeUpTo

	if excludeUpTo <= 1 {
		changed := rangeStart == 0
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
			return
		}
		if hi <= rangeStart {
			hi = rangeStart + 1
		}
		session.Updated = time.Now()
		session.excludedRange = [2]int{rangeStart, hi}
		session.excludeBoundary = hi
		session.bumpEpoch()
		session.publishViewLocked()
		return
	}

	for i := 1; i < excludeUpTo; i++ {
		if _, pin := preserved[i]; !pin {
			session.Messages[i].ExcludeFromContext = true
		}
	}

	for idx := range preserved {
		if idx == 0 {
			continue
		}
		if idx < len(session.Messages) && session.Messages[idx].ExcludeFromContext {
			session.Messages[idx].ExcludeFromContext = false
			if idx+1 > hi {
				hi = idx + 1
			}
		}
	}

	if hi <= rangeStart {
		hi = rangeStart + 1
	}
	session.Updated = time.Now()
	session.excludedRange = [2]int{rangeStart, hi}
	session.excludeBoundary = hi
	session.bumpEpoch()
	session.publishViewLocked()
}

func (sm *SessionManager) EvictImageContent(key string, keepLast int) (int, error) {
	evicted := sm.reduceImageContentUnlocked(key, keepLast)
	if evicted == 0 {
		return 0, nil
	}
	return evicted, sm.Save(key)
}

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
	sm.mu.Lock()
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
