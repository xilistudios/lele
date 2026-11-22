package session

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

type sessionRepo interface {
	UpsertSession(meta store.SessionMeta) error
	ReplaceMessages(sessionKey string, messages []store.MessageRow) error
	InsertMessages(sessionKey string, messages []store.MessageRow) error
	UpdateMessages(sessionKey string, messages []store.MessageRow) error
	DeleteMessagesFrom(sessionKey string, fromSeq int) error
	UpdateMessagesExcludedWithJSON(sessionKey string, messages []store.MessageRow) error
	PruneExcluded(sessionKey string, keepCount int) (int, error)
	LoadMessagesFullBeforeSeq(sessionKey string, beforeSeq int) ([]store.MessageRowFull, error)
	UpdateFirstInMemorySeq(sessionKey string, seq int) error
	SessionListingStats(keys []string) (map[string]store.SessionListingStats, error)
}

var repoFor = func(sm *SessionManager) sessionRepo {
	if sm.sessionRepo == nil {
		return nil
	}
	return sm.sessionRepo
}

func (sm *SessionManager) unlocked(fn func()) {
	sm.mu.Unlock()
	defer sm.mu.Lock()
	fn()
}

var _ sessionRepo = (*store.SessionRepo)(nil)

func (sm *SessionManager) Save(key string) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.saveUnlocked(key)
}

func (sm *SessionManager) SaveAll() (saved int, failed int) {
	sm.ensureLoaded()

	if sm.sessionRepo == nil {
		logger.DebugCF("session", "SaveAll skipped: SessionManager has no session repository", nil)
		return 0, 0
	}

	keys := sm.residentKeys()
	sort.Strings(keys)

	for _, key := range keys {
		persisted, err := sm.saveIfResident(key)
		if err != nil {
			failed++
			logger.WarnCF("session", "SaveAll failed to persist session", map[string]interface{}{
				"session_key": key,
				"error":       err.Error(),
			})
			continue
		}
		if persisted {
			saved++
		}
	}

	logger.InfoCF("session", "SaveAll flushed resident sessions", map[string]interface{}{
		"resident": len(keys),
		"saved":    saved,
		"failed":   failed,
	})
	return saved, failed
}

func (sm *SessionManager) saveIfResident(key string) (persisted bool, err error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, ok := sm.sessions[key]; !ok {
		return false, nil
	}
	if err := sm.saveUnlocked(key); err != nil {
		return false, err
	}
	return true, nil
}

func sessionMetaFromSession(s *Session) store.SessionMeta {
	return store.SessionMeta{
		Key:              s.Key,
		Name:             s.Name,
		Mode:             s.Mode,
		Summary:          s.Summary,
		VerboseLevel:     s.VerboseLevel,
		Model:            s.Model,
		ThinkingLevel:    s.ThinkingLevel,
		Folder:           s.Folder,
		AgentID:          s.AgentID,
		SubagentStatus:   s.SubagentStatus,
		InputTokens:      s.InputTokens,
		OutputTokens:     s.OutputTokens,
		CompactionCount:  s.CompactionCount,
		FirstInMemorySeq: s.firstInMemorySeq,
		CreatedAt:        s.Created,
		UpdatedAt:        s.Updated,
	}
}

func (sm *SessionManager) saveMetaOnlyUnlocked(key string) error {
	repo := repoFor(sm)
	if repo == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	meta := sessionMetaFromSession(session)
	epoch := session.saveEpoch
	var err error
	sm.unlocked(func() { err = repo.UpsertSession(meta) })

	if err != nil {
		return fmt.Errorf("save session meta %q: %w", key, err)
	}
	if session.saveEpoch != epoch {
		return nil
	}
	session.metaDirty = false
	return nil
}

func (sm *SessionManager) saveFullUnlocked(key string) error {
	repo := repoFor(sm)
	if repo == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	meta := sessionMetaFromSession(session)
	epoch := session.saveEpoch
	firstInMemorySeq := session.firstInMemorySeq
	messages := sm.viewLocked(session)

	var (
		evicted int
		pruned  int
		ioErr   error
	)
	sm.unlocked(func() {
		evicted, pruned, ioErr = writeFullUnlocked(repo, key, meta, firstInMemorySeq, messages)
	})

	if ioErr != nil {
		return fmt.Errorf("save session %q to sqlite: %w", key, ioErr)
	}

	if session.saveEpoch != epoch {
		session.lastPersistedSeq = -1
		return nil
	}

	if cur, ok := sm.sessions[key]; !ok || cur != session {
		return nil
	}

	session.lastPersistedSeq = len(session.Messages) - 1
	session.firstInMemorySeq = evicted
	session.evictedTotal = evicted - pruned
	if session.evictedTotal < 0 {
		session.evictedTotal = 0
	}
	session.clearDirtyFlags()
	return nil
}

func writeFullUnlocked(repo sessionRepo, key string, meta store.SessionMeta, firstInMemorySeq int, messages []providers.Message) (evicted int, pruned int, err error) {
	var evictedRows []store.MessageRowFull
	if firstInMemorySeq > 0 {
		evictedRows, err = repo.LoadMessagesFullBeforeSeq(key, firstInMemorySeq)
		if err != nil {
			return 0, 0, fmt.Errorf("re-materialize evicted messages %q: %w", key, err)
		}
	}
	evicted = len(evictedRows)

	rows := make([]store.MessageRow, 0, evicted+len(messages))
	for i, evictedRow := range evictedRows {
		var evt providers.Message
		if uerr := json.Unmarshal([]byte(evictedRow.JSON), &evt); uerr != nil {
			continue
		}
		rows = append(rows, store.MessageRow{
			Seq:      i,
			Role:     evt.Role,
			JSON:     evictedRow.JSON,
			Excluded: evictedRow.Excluded,
		})
	}
	for i, msg := range messages {
		msgJSON, mErr := json.Marshal(msg)
		if mErr != nil {
			return 0, 0, fmt.Errorf("marshal message %d: %w", i, mErr)
		}
		rows = append(rows, store.MessageRow{
			Seq:      evicted + i,
			Role:     msg.Role,
			JSON:     string(msgJSON),
			Excluded: msg.ExcludeFromContext,
		})
	}

	if err = repo.UpsertSession(meta); err != nil {
		return 0, 0, err
	}
	if err = repo.ReplaceMessages(key, rows); err != nil {
		return 0, 0, err
	}
	pruned, _ = repo.PruneExcluded(key, maxStoredMessages)
	return evicted, pruned, nil
}

func (sm *SessionManager) saveIncrementalUnlocked(key string) error {
	repo := repoFor(sm)
	if repo == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	meta := sessionMetaFromSession(session)
	startSeq := session.lastPersistedSeq + 1

	var newRows []store.MessageRow
	for i := startSeq; i < len(session.Messages); i++ {
		msg := session.Messages[i]
		msgJSON, mErr := json.Marshal(msg)
		if mErr != nil {
			return fmt.Errorf("marshal message %d: %w", i, mErr)
		}
		newRows = append(newRows, store.MessageRow{
			Seq:      session.seqForIndex(i),
			Role:     msg.Role,
			JSON:     string(msgJSON),
			Excluded: msg.ExcludeFromContext,
		})
	}

	var updateRows []store.MessageRow
	if session.modifiedFrom > 0 {
		fromIdx := session.modifiedFrom - 1
		if fromIdx > len(session.Messages)-1 {
			fromIdx = len(session.Messages)
		}
		if fromIdx < startSeq {
			endIdx := startSeq - 1
			if endIdx > len(session.Messages)-1 {
				endIdx = len(session.Messages) - 1
			}
			for i := fromIdx; i <= endIdx; i++ {
				msg := session.Messages[i]
				msgJSON, mErr := json.Marshal(msg)
				if mErr != nil {
					return fmt.Errorf("marshal message %d: %w", i, mErr)
				}
				updateRows = append(updateRows, store.MessageRow{
					Seq:      session.seqForIndex(i),
					Role:     msg.Role,
					JSON:     string(msgJSON),
					Excluded: msg.ExcludeFromContext,
				})
			}
		}
	}

	epoch := session.saveEpoch
	var err error
	sm.unlocked(func() {
		err = repo.UpsertSession(meta)
		if err == nil && len(newRows) > 0 {
			err = repo.InsertMessages(key, newRows)
		}
		if err == nil && len(updateRows) > 0 {
			err = repo.UpdateMessages(key, updateRows)
		}
	})

	if err != nil {
		return fmt.Errorf("incremental save %q: %w", key, err)
	}

	if session.saveEpoch != epoch {
		return nil
	}

	session.lastPersistedSeq = len(session.Messages) - 1
	session.msgsAppended = 0
	session.modifiedFrom = 0
	session.metaDirty = false
	return nil
}

func (sm *SessionManager) saveDeleteLastUnlocked(key string) error {
	repo := repoFor(sm)
	if repo == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	meta := sessionMetaFromSession(session)
	fromSeq := session.deleteFromSeq
	epoch := session.saveEpoch

	var err error
	sm.unlocked(func() {
		err = repo.UpsertSession(meta)
		if err == nil {
			err = repo.DeleteMessagesFrom(key, fromSeq)
		}
	})

	if err != nil {
		return fmt.Errorf("delete-last save %q: %w", key, err)
	}

	if session.saveEpoch != epoch {
		session.lastPersistedSeq = -1
		return nil
	}

	session.lastPersistedSeq = len(session.Messages) - 1
	session.lastMsgDeleted = false
	session.deleteFromSeq = 0
	session.metaDirty = false
	return nil
}

func (sm *SessionManager) saveExcludedRangeUnlocked(key string) error {
	repo := repoFor(sm)
	if repo == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	meta := sessionMetaFromSession(session)
	epoch := session.saveEpoch
	from, to := session.excludedRange[0], session.excludedRange[1]
	if from < 0 {
		from = 0
	}
	if to > len(session.Messages) {
		to = len(session.Messages)
	}
	if from > to {
		from = to
	}
	firstSeq := session.firstInMemorySeq + from
	window := cloneMessages(session.Messages[from:to])

	var writeErr error
	sm.unlocked(func() {
		writeErr = writeExcludedRangeUnlocked(repo, key, meta, firstSeq, window)
	})

	if writeErr != nil {
		return fmt.Errorf("excluded-range save %q: %w", key, writeErr)
	}

	if session.saveEpoch != epoch {
		session.lastPersistedSeq = -1
		return nil
	}

	if cur, ok := sm.sessions[key]; !ok || cur != session {
		return nil
	}

	session.excludedRange = [2]int{}
	session.metaDirty = false
	return nil
}

func writeExcludedRangeUnlocked(repo sessionRepo, key string, meta store.SessionMeta, firstSeq int, window []providers.Message) error {
	rows := make([]store.MessageRow, 0, len(window))
	for i, msg := range window {
		msgJSON, mErr := json.Marshal(msg)
		if mErr != nil {
			return fmt.Errorf("marshal excluded message seq %d: %w", firstSeq+i, mErr)
		}
		rows = append(rows, store.MessageRow{
			Seq:      firstSeq + i,
			Role:     msg.Role,
			JSON:     string(msgJSON),
			Excluded: msg.ExcludeFromContext,
		})
	}

	if err := repo.UpsertSession(meta); err != nil {
		return err
	}
	return repo.UpdateMessagesExcludedWithJSON(key, rows)
}

func (sm *SessionManager) saveUnlocked(key string) error {
	if sm.sessionRepo == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}
	sm.syncSessionMetaLocked(session)

	if session.lastPersistedSeq == -1 {
		return sm.saveFullUnlocked(key)
	}

	if session.lastMsgDeleted {
		if err := sm.saveDeleteLastUnlocked(key); err != nil {
			return err
		}
		if session.lastPersistedSeq == -1 {
			return sm.saveFullUnlocked(key)
		}
	}

	if session.excludedRange[1] > session.excludedRange[0] {
		if err := sm.saveExcludedRangeUnlocked(key); err != nil {
			return err
		}
		if session.lastPersistedSeq == -1 {
			return sm.saveFullUnlocked(key)
		}
	}

	if session.msgsAppended > 0 || session.modifiedFrom > 0 {
		return sm.saveIncrementalUnlocked(key)
	}

	if session.metaDirty {
		return sm.saveMetaOnlyUnlocked(key)
	}

	return nil
}
