// Archived (display-only) history for the TUI.
//
// After /compact, older messages are evicted from the agent's in-memory
// context but remain in SQLite. The TUI still needs to show them so the
// user can scroll through the full transcript. These helpers load the
// evicted prefix into m.archivedPrefix for rendering purposes only —
// they NEVER re-inject messages into the LLM context.
//
// The loading strategy is "newest pages first": the most recent evicted
// messages are loaded before older ones, so the user sees continuity
// between the archived prefix and the live context immediately after
// a compaction.

package tui

import (
	"fmt"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
)

// maxArchivedPrefixMessages caps the memory of the display-only archived
// prefix in the TUI. It is a var (not const) so tests can lower it.
var maxArchivedPrefixMessages = 2000

// archivedPageSize is the page size for loading archived history.
// Reuses lazyLoadBatchSize (defined in viewport.go = 50).
const archivedPageSize = lazyLoadBatchSize

// archivedWindowLimit mirrors session.maxMessagesWindowLimit (200) — the
// maximum rows a single LoadMessagesWindow call may return. Duplicated here
// to avoid importing an unexported constant from pkg/session.
const archivedWindowLimit = 200

// archivedForCurrentSession returns the display-only archived prefix for the
// current session. The returned slice is a read-only reference; callers MUST
// NOT mutate it. Returns nil when no archived prefix is loaded or when the
// prefix belongs to a different session.
func (m *Model) archivedForCurrentSession() []providers.Message {
	if m.archivedKey == m.currentKey && m.currentKey != "" {
		return m.archivedPrefix
	}
	return nil
}

// archivedHiddenCount returns the number of archived messages that have been
// evicted but are NOT yet loaded into archivedPrefix for display. Returns 0
// when the archived prefix does not belong to the current session.
func (m *Model) archivedHiddenCount() int {
	if m.archivedKey != m.currentKey || m.currentKey == "" {
		return 0
	}
	hidden := m.archivedTotal - len(m.archivedPrefix)
	if hidden < 0 {
		return 0
	}
	return hidden
}

// displayHistoryMessageCount returns the number of user/assistant messages the
// chat viewport renders: archived prefix + resident history. O(1) — the
// archived part is maintained incrementally by prependArchivedPage.
func (m *Model) displayHistoryMessageCount(history []providers.Message) int {
	resident := countHistoryMessages(history)
	if m.archivedKey == m.currentKey && m.currentKey != "" {
		return m.archivedVisibleCount + resident
	}
	return resident
}

// archivedCacheKey fingerprints the display-only archived state that affects
// rendered output: which rows are in the prefix, where its cursor sits, and how
// many older rows the "↑ N earlier messages" banner still has to account for.
//
// Both cache layers must agree on this, so it is computed in exactly one place:
// updateViewport uses it to decide whether the rendered base needs a rebuild,
// and getViewportContentKey folds it into the skip-update fingerprint. Sharing
// it matters — if the two drifted, a frame could be judged "unchanged" by the
// outer guard and never reach the inner rebuild check at all.
func (m *Model) archivedCacheKey() string {
	return fmt.Sprintf("%d|%d|%d|%v", len(m.archivedPrefix), m.archivedOldestSeq,
		m.archivedTotal, m.archivedHasOlder)
}

// resetArchivedHistory clears all archived prefix state. Called on session
// switch, staleness detection, or when no evicted messages exist.
func (m *Model) resetArchivedHistory() {
	m.archivedPrefix = nil
	m.archivedKey = ""
	m.archivedTotal = 0
	m.archivedOldestSeq = 0
	m.archivedHasOlder = false
	m.archivedVisibleCount = 0
}

// refreshArchivedHistory loads or refreshes the display-only archived prefix
// for the current session from SQLite. It is designed to be called from
// Update() routes (NEVER from View()).
//
// Returns true when archivedPrefix changed (so render caches can be
// invalidated in T2).
func (m *Model) refreshArchivedHistory() bool {
	// (a) No session or session manager — nothing to load.
	if m.currentKey == "" || m.sessionMgr == nil {
		m.resetArchivedHistory()
		return false
	}

	// (b) Query eviction stats. Both calls are O(1) for resident sessions.
	evicted := m.sessionMgr.GetEvictedMessageCount(m.currentKey)
	total := m.sessionMgr.GetTotalMessageCount(m.currentKey)
	resident := total - evicted
	if resident < 0 {
		resident = 0
	}

	// (c) Session switch: reset and rebind.
	if m.archivedKey != m.currentKey {
		m.resetArchivedHistory()
		m.archivedKey = m.currentKey
	}

	// (d) Staleness detection: evicted count changed (new compaction,
	// extra eviction, or PruneExcluded). Reset prefix but keep the key.
	if m.archivedTotal != evicted {
		m.resetArchivedHistory()
		m.archivedKey = m.currentKey
		m.archivedTotal = evicted
	}

	// (e) Nothing evicted — no work.
	if evicted == 0 {
		return false
	}

	// (f) Compute how many archived messages we actually need to display.
	needed := evicted
	if m.maxRenderedMessages > 0 {
		wanted := m.maxRenderedMessages - resident
		if wanted < 0 {
			wanted = 0
		}
		if needed > wanted {
			needed = wanted
		}
	}
	if needed <= len(m.archivedPrefix) {
		return false
	}

	// (g) Load pages newest-first until we reach `needed` or hit the cap.
	changed := false
	for len(m.archivedPrefix) < needed {
		limit := needed - len(m.archivedPrefix)
		capRemaining := maxArchivedPrefixMessages - len(m.archivedPrefix)
		if capRemaining <= 0 {
			break
		}
		if limit > capRemaining {
			limit = capRemaining
		}
		if limit > archivedWindowLimit {
			limit = archivedWindowLimit
		}
		if limit <= 0 {
			break
		}

		var page *session.EvictedMessagesPage
		if len(m.archivedPrefix) == 0 {
			// First page: load the newest evicted messages (no cursor).
			page = m.sessionMgr.LoadMessagesWindow(m.currentKey, -1, 0, limit)
		} else {
			// Subsequent pages: older than the current oldest.
			page = m.sessionMgr.LoadMessagesWindow(m.currentKey, m.archivedOldestSeq, 0, limit)
		}
		if page == nil {
			break
		}

		added := m.prependArchivedPage(page)
		if added == 0 {
			break
		}
		changed = true

		if !page.HasOlder {
			break
		}
	}
	return changed
}

// loadOlderArchivedPage loads one more page of older archived messages
// and prepends it to archivedPrefix. Returns the number of messages added
// (0 when there are no more, or the session has no archived messages, or
// the cap has been reached).
//
// Before loading, it ensures the archived prefix belongs to the current
// session by calling refreshArchivedHistory if needed.
func (m *Model) loadOlderArchivedPage() int {
	// Ensure archived state is current.
	if m.archivedKey != m.currentKey || m.archivedTotal == 0 {
		if !m.refreshArchivedHistory() {
			return 0
		}
	}

	if m.currentKey == "" || m.sessionMgr == nil || !m.archivedHasOlder {
		return 0
	}

	capRemaining := maxArchivedPrefixMessages - len(m.archivedPrefix)
	if capRemaining <= 0 {
		return 0
	}

	limit := archivedPageSize
	if limit > capRemaining {
		limit = capRemaining
	}
	if limit > archivedWindowLimit {
		limit = archivedWindowLimit
	}
	if limit <= 0 {
		return 0
	}

	page := m.sessionMgr.LoadMessagesWindow(m.currentKey, m.archivedOldestSeq, 0, limit)
	if page == nil {
		m.archivedHasOlder = false
		return 0
	}

	added := m.prependArchivedPage(page)
	if added > 0 {
		// Refresh the total in case it changed.
		m.archivedTotal = m.sessionMgr.GetEvictedMessageCount(m.currentKey)
	}
	return added
}

// prependArchivedPage prepends a page of evicted messages to the archived
// prefix, skipping any messages whose seq >= archivedOldestSeq (overlap
// guard). Returns the number of messages actually added.
//
// Defensive: if len(page.Seqs) != len(page.Messages), the entire page is
// discarded (the two slices must be aligned for seq tracking to work).
func (m *Model) prependArchivedPage(page *session.EvictedMessagesPage) int {
	if page == nil || len(page.Messages) == 0 {
		return 0
	}
	if len(page.Seqs) != len(page.Messages) {
		// Mismatched seqs — discard the page to avoid corrupt state.
		return 0
	}

	// Filter out overlap. A page is chronological (ascending seq), so any row
	// that duplicates the resident prefix is one of the NEWEST rows of the
	// page, i.e. at its tail: trim from the end until every remaining seq is
	// strictly older than the current oldest prefix message. LoadMessagesWindow
	// uses an exclusive `before` cursor, so this normally trims nothing — it is
	// a guard against a caller retrying with a stale cursor.
	msgs := page.Messages
	seqs := page.Seqs
	if m.archivedOldestSeq > 0 && len(m.archivedPrefix) > 0 {
		end := len(seqs)
		for end > 0 && seqs[end-1] >= m.archivedOldestSeq {
			end--
		}
		msgs = msgs[:end]
		seqs = seqs[:end]
	}

	if len(msgs) == 0 {
		return 0
	}

	// Prepend to the prefix (older messages go first).
	m.archivedPrefix = append(msgs, m.archivedPrefix...)
	m.archivedOldestSeq = seqs[0]
	m.archivedHasOlder = page.HasOlder

	// Count user/assistant messages in the prepended batch for O(1) render.
	for _, msg := range msgs {
		if msg.Role == "user" || msg.Role == "assistant" {
			m.archivedVisibleCount++
		}
	}

	return len(msgs)
}
