package session

// Session core types and per-session bookkeeping helpers.
//
// Owns the Session struct (the in-memory unit of persistence), the lazy
// sessionMetadata record, dirty-flag bookkeeping used by the save paths, and
// session-name generation.

import (
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// maxStoredMessages is the maximum number of messages kept in a session file.
// When exceeded, the oldest excluded messages are pruned on save.
const maxStoredMessages = 10000

type Session struct {
	Key           string              `json:"key"`
	Name          string              `json:"name,omitempty"`
	Mode          string              `json:"mode,omitempty"` // "chat", "agent", "group" (default empty = "agent")
	Messages      []providers.Message `json:"messages"`
	Summary       string              `json:"summary,omitempty"`
	VerboseMode   bool                `json:"verbose_mode,omitempty"`   // Deprecated: use VerboseLevel
	VerboseLevel  string              `json:"verbose_level,omitempty"`  // "off", "basic", or "full"
	Model         string              `json:"model,omitempty"`          // Session-specific model override
	ThinkingLevel string              `json:"thinking_level,omitempty"` // "off", "low", "medium", "high"
	// Folder is the directory selected by the user for this session (WebUI
	// folder picker). Its absolute path plus a first-level listing are injected
	// into the session's system prompt by ContextBuilder's folder resolver.
	// Empty means "no folder selected".
	Folder             string    `json:"folder,omitempty"`
	Created            time.Time `json:"created"`
	Updated            time.Time `json:"updated"`
	lastStreamFlush    time.Time // throttle for stream persistence (not persisted)
	hadStreamedContent bool      // tracks if content was delivered via streaming this turn (not persisted)
	lastPersistedSeq   int       // last message seq persisted to SQLite (-1 = none)
	metaDirty          bool      // metadata changed since last save (needs UpsertSession)
	msgsAppended       int       // messages appended since lastPersistedSeq (needs InsertMessage)
	modifiedFrom       int       // 1 + lowest in-memory index modified in-place since last save (0 = none; needs UpdateMessage)
	excludedRange      [2]int    // [start, end) range of messages whose excluded flag changed (needs UpdateMessagesExcluded)
	lastMsgDeleted     bool      // last message was removed (needs DeleteLastMessage)
	// deleteFromSeq is the absolute SQLite seq of the message removed by
	// RemoveLastMessage, captured at deletion time. saveDeleteLastUnlocked
	// uses it as a watermark (DELETE WHERE seq >= deleteFromSeq) instead of a
	// position-based "delete max seq", which would race with concurrent
	// appends that reuse the same seq slot. 0 = no pending delete.
	deleteFromSeq int
	// firstInMemorySeq is the SQLite seq of in-memory slice element 0.
	// 0 = no eviction gap (slice index == seq, legacy behavior).
	// > 0 = messages with seq < firstInMemorySeq were evicted from memory
	//       (they remain in SQLite with excluded = 1).
	firstInMemorySeq int
	// evictedTotal is the number of messages currently persisted in SQLite but
	// not present in the in-memory slice (evicted after compaction).
	evictedTotal int
	// saveEpoch is bumped on every logical mutation (content or metadata
	// change). Save paths capture it before releasing the lock for disk I/O
	// and compare after re-acquiring it; a mismatch means the session was
	// mutated while the I/O was in flight, so the save is stale and its
	// post-I/O bookkeeping must be discarded (dirty flags left set) to avoid
	// losing the concurrent mutation. Bookkeeping-only changes (clearing dirty
	// flags, advancing lastPersistedSeq) do NOT bump it.
	saveEpoch uint64
	// Token tracking
	InputTokens     int `json:"input_tokens,omitempty"`
	OutputTokens    int `json:"output_tokens,omitempty"`
	CompactionCount int `json:"compaction_count,omitempty"`
}

// sessionMetadata holds lightweight session info for sessions not yet
// fully loaded into memory. This allows listing sessions without
// deserializing their entire message history.
type sessionMetadata struct {
	Key     string    `json:"key"`
	Name    string    `json:"name"`
	Mode    string    `json:"mode,omitempty"`
	Folder  string    `json:"folder,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

func generateSessionName(content string) string {
	maxLen := 50
	content = strings.TrimSpace(content)
	content = strings.ReplaceAll(content, "\n", " ")
	content = strings.ReplaceAll(content, "\r", " ")
	content = strings.ReplaceAll(content, "\t", " ")

	for _, r := range []string{".", ",", "!", "?", ";", ":", "'", "\"", "`"} {
		content = strings.ReplaceAll(content, r, "")
	}

	words := strings.Fields(content)
	if len(words) == 0 {
		return "New Chat"
	}

	result := strings.Join(words, " ")
	if len(result) <= maxLen {
		return result
	}

	result = result[:maxLen]
	lastSpace := strings.LastIndex(result, " ")
	if lastSpace > 0 && lastSpace > maxLen-20 {
		result = result[:lastSpace]
	}

	return strings.TrimSpace(result)
}

// clearDirtyFlags resets all dirty tracking flags on a session.
func (s *Session) clearDirtyFlags() {
	s.metaDirty = false
	s.msgsAppended = 0
	s.modifiedFrom = 0
	s.excludedRange = [2]int{}
	s.lastMsgDeleted = false
	s.deleteFromSeq = 0
}

// markModified records that the in-memory message at idx was changed in-place
// (e.g. streaming chunks or the final replacement carrying tool_calls). It
// keeps the LOWEST modified index (stored 1-based so 0 means "none") so an
// incremental save rewrites every stale row — not just the last message,
// which may already have been superseded by appended tool results.
func (s *Session) markModified(idx int) {
	if idx < 0 {
		return
	}
	if s.modifiedFrom == 0 || idx+1 < s.modifiedFrom {
		s.modifiedFrom = idx + 1
	}
	s.bumpEpoch()
}

// bumpEpoch advances the save epoch, invalidating any in-flight save whose
// snapshot predates this mutation. Every logical mutation (message append,
// in-place edit, deletion, metadata change, eviction-boundary change) must
// call this — directly or via markModified — while holding sm.mu.
func (s *Session) bumpEpoch() {
	s.saveEpoch++
}

// seqForIndex returns the absolute SQLite seq for an in-memory slice index.
// It enforces the core invariant `seq = firstInMemorySeq + sliceIndex` in one
// place so all save paths agree on absolute seqs even after eviction created a
// gap at the front of the in-memory slice.
func (s *Session) seqForIndex(i int) int {
	return s.firstInMemorySeq + i
}
