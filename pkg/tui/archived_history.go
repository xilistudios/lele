package tui

import (
	"fmt"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
)

var maxArchivedPrefixMessages = 2000

const archivedPageSize = lazyLoadBatchSize

const archivedWindowLimit = 200

func (m *Model) archivedForCurrentSession() []providers.Message {
	if m.archivedKey == m.currentKey && m.currentKey != "" {
		return m.archivedPrefix
	}
	return nil
}

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

func (m *Model) displayHistoryMessageCount(history []providers.Message) int {
	return m.displayMessageCountFrom(countHistoryMessages(history))
}

func (m *Model) displayMessageCountFrom(resident int) int {
	if m.archivedKey == m.currentKey && m.currentKey != "" {
		return m.archivedVisibleCount + resident
	}
	return resident
}

func (m *Model) archivedCacheKey() string {
	return fmt.Sprintf("%d|%d|%d|%v", len(m.archivedPrefix), m.archivedOldestSeq,
		m.archivedTotal, m.archivedHasOlder)
}

func (m *Model) resetArchivedHistory() {
	m.archivedPrefix = nil
	m.archivedKey = ""
	m.archivedTotal = 0
	m.archivedOldestSeq = 0
	m.archivedHasOlder = false
	m.archivedVisibleCount = 0
}

func (m *Model) refreshArchivedHistory() bool {
	if m.currentKey == "" || m.sessionMgr == nil {
		m.resetArchivedHistory()
		return false
	}

	evicted := m.sessionMgr.GetEvictedMessageCount(m.currentKey)
	total := m.sessionMgr.GetTotalMessageCount(m.currentKey)
	resident := total - evicted
	if resident < 0 {
		resident = 0
	}

	if m.archivedKey != m.currentKey {
		m.resetArchivedHistory()
		m.archivedKey = m.currentKey
	}

	if m.archivedTotal != evicted {
		m.resetArchivedHistory()
		m.archivedKey = m.currentKey
		m.archivedTotal = evicted
	}

	if evicted == 0 {
		return false
	}

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
			page = m.sessionMgr.LoadMessagesWindow(m.currentKey, -1, 0, limit)
		} else {
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

func (m *Model) loadOlderArchivedPage() int {
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
		m.archivedTotal = m.sessionMgr.GetEvictedMessageCount(m.currentKey)
	}
	return added
}

func (m *Model) prependArchivedPage(page *session.EvictedMessagesPage) int {
	if page == nil || len(page.Messages) == 0 {
		return 0
	}
	if len(page.Seqs) != len(page.Messages) {
		return 0
	}

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

	m.archivedPrefix = append(msgs, m.archivedPrefix...)
	m.archivedOldestSeq = seqs[0]
	m.archivedHasOlder = page.HasOlder

	for _, msg := range msgs {
		if msg.Role == "user" || msg.Role == "assistant" {
			m.archivedVisibleCount++
		}
	}

	return len(msgs)
}
