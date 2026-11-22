package session

import (
	"encoding/json"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

type EvictedMessagesPage struct {
	Messages     []providers.Message
	FirstSeq     int
	LastSeq      int
	Seqs         []int
	HasOlder     bool
	HasNewer     bool
	EvictedCount int
	TotalCount   int
}

const maxMessagesWindowLimit = 200

func (sm *SessionManager) LoadMessagesWindow(sessionKey string, before, after, limit int) *EvictedMessagesPage {
	if sm.sessionRepo == nil || limit <= 0 {
		return nil
	}
	if limit > maxMessagesWindowLimit {
		limit = maxMessagesWindowLimit
	}

	sm.ensureLoaded()

	var memFloor, residentCount, evicted int
	resident := false
	sm.mu.RLock()
	if session, ok := sm.sessions[sessionKey]; ok {
		resident = true
		memFloor = session.firstInMemorySeq
		residentCount = len(session.Messages)
		evicted = session.evictedTotal
	}
	sm.mu.RUnlock()

	repo := sm.sessionRepo
	if !resident {
		meta, err := repo.GetSessionMeta(sessionKey)
		if err != nil || meta == nil {
			return nil
		}
		memFloor = meta.FirstInMemorySeq
		if memFloor <= 0 {
			return nil
		}
		n, cerr := repo.CountMessagesBefore(sessionKey, memFloor)
		if cerr != nil {
			return nil
		}
		evicted = n
		if evicted == 0 {
			return nil
		}
		total, merr := repo.MessageCount(sessionKey)
		if merr != nil {
			return nil
		}
		residentCount = total - evicted
	} else if evicted == 0 {
		return nil
	}

	total := residentCount + evicted

	bound := memFloor
	if before >= 0 && (bound < 0 || before < bound) {
		bound = before
	}

	var rows []store.MessageRowFull
	var err error
	if after > 0 {
		if memFloor >= 0 && after >= memFloor {
			return nil
		}
		upper := -1
		if memFloor >= 0 {
			upper = memFloor
		}
		rows, err = repo.LoadMessagesBetweenLimited(sessionKey, after, upper, limit)
	} else {
		if bound <= 0 {
			return nil
		}
		rows, err = repo.LoadMessagesBeforeLimited(sessionKey, bound, limit)
	}
	if err != nil {
		logger.WarnCF("session", "LoadMessagesWindow repository read failed", map[string]interface{}{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
		return nil
	}
	if len(rows) == 0 {
		return nil
	}

	if after <= 0 {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}

	msgs := make([]providers.Message, 0, len(rows))
	seqs := make([]int, 0, len(rows))
	for _, row := range rows {
		var msg providers.Message
		if uerr := json.Unmarshal([]byte(row.JSON), &msg); uerr != nil {
			continue
		}
		msgs = append(msgs, msg)
		seqs = append(seqs, row.Seq)
	}
	if len(msgs) == 0 {
		return nil
	}

	firstSeq, lastSeq := seqs[0], seqs[len(seqs)-1]

	hasOlder := false
	olderThan := firstSeq
	if memFloor >= 0 && memFloor < olderThan {
		olderThan = memFloor
	}
	if olderThan > 0 {
		if n, cerr := repo.CountMessagesBefore(sessionKey, olderThan); cerr == nil && n > 0 {
			hasOlder = true
		}
	}

	hasNewer := lastSeq+1 < memFloor

	return &EvictedMessagesPage{
		Messages:     msgs,
		Seqs:         seqs,
		FirstSeq:     firstSeq,
		LastSeq:      lastSeq,
		HasOlder:     hasOlder,
		HasNewer:     hasNewer,
		EvictedCount: evicted,
		TotalCount:   total,
	}
}
