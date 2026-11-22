package session

import (
	"encoding/json"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

func (sm *SessionManager) ensureLoaded() {
	sm.loadOnce.Do(func() {
		if sm.sessionRepo != nil {
			sm.loadSessionMetadataFromSQLite()
		} else {
			logger.WarnCF("session", "SessionManager has no session repository — sessions will not persist to disk", nil)
		}
	})
}

func (sm *SessionManager) loadSessionFromDisk(key string) (*Session, bool) {
	if s, ok := sm.sessions[key]; ok {
		return s, true
	}

	if _, ok := sm.sessionMeta[key]; !ok {
		if sm.sessionRepo == nil {
			return nil, false
		}
		meta, err := sm.sessionRepo.GetSessionMeta(key)
		if err != nil || meta == nil {
			return nil, false
		}
		sm.sessionMeta[key] = &sessionMetadata{
			Key:            meta.Key,
			Name:           meta.Name,
			Mode:           meta.Mode,
			Folder:         meta.Folder,
			AgentID:        meta.AgentID,
			SubagentStatus: meta.SubagentStatus,
			Created:        meta.CreatedAt,
			Updated:        meta.UpdatedAt,
		}
	}

	if sm.sessionRepo != nil {
		return sm.loadFromSQLite(key)
	}

	return nil, false
}

func (sm *SessionManager) loadFromSQLite(key string) (*Session, bool) {
	repo := sm.sessionRepo

	meta, err := repo.GetSessionMeta(key)
	if err != nil || meta == nil {
		return nil, false
	}

	boundary := meta.FirstInMemorySeq
	if boundary < 0 {
		boundary = 0
	}

	msgJSONs, err := repo.LoadMessagesFromSeq(key, boundary)
	if err != nil {
		return nil, false
	}

	if boundary > 0 {
		if maxSeq, seqErr := repo.MaxSeq(key); seqErr == nil {
			if len(msgJSONs) != maxSeq-boundary+1 {
				logger.WarnCF("session", "Stale eviction boundary detected; recovering from persisted rows", map[string]interface{}{
					"session_key":     key,
					"first_in_memory": boundary,
					"max_seq":         maxSeq,
					"expected_rows":   maxSeq - boundary + 1,
					"loaded_rows":     len(msgJSONs),
				})
				fullRows, fErr := repo.LoadMessagesWithSeq(key)
				if fErr != nil {
					return nil, false
				}
				firstInContextIdx := 0
				for firstInContextIdx < len(fullRows) && fullRows[firstInContextIdx].Excluded {
					firstInContextIdx++
				}
				if firstInContextIdx < len(fullRows) {
					boundary = fullRows[firstInContextIdx].Seq
					msgJSONs = make([]string, 0, len(fullRows)-firstInContextIdx)
					for _, fr := range fullRows[firstInContextIdx:] {
						msgJSONs = append(msgJSONs, fr.JSON)
					}
				} else if len(fullRows) > 0 {
					lastIdx := len(fullRows) - 1
					boundary = fullRows[lastIdx].Seq
					msgJSONs = []string{fullRows[lastIdx].JSON}
				} else {
					boundary = 0
					msgJSONs = nil
				}
				_ = repo.UpdateFirstInMemorySeq(key, boundary)
			}
		}
	}

	messages := make([]providers.Message, 0, len(msgJSONs))
	for _, msgJSON := range msgJSONs {
		var msg providers.Message
		if err := json.Unmarshal([]byte(msgJSON), &msg); err != nil {
			continue
		}
		messages = append(messages, msg)
	}

	if boundary == 0 && len(messages) > 0 {
		hasExcluded := false
		for _, m := range messages {
			if m.ExcludeFromContext {
				hasExcluded = true
				break
			}
		}
		if hasExcluded {
			firstInContext := 0
			for firstInContext < len(messages) && messages[firstInContext].ExcludeFromContext {
				firstInContext++
			}
			if firstInContext > 0 && firstInContext < len(messages) {
				boundary = firstInContext
				messages = messages[firstInContext:]
				_ = repo.UpdateFirstInMemorySeq(key, boundary)
			}
		}
	}

	evictedPersisted := 0
	if total, tErr := repo.MessageCount(key); tErr == nil {
		evictedPersisted = total - len(messages)
		if evictedPersisted < 0 {
			evictedPersisted = 0
		}
	}

	session := &Session{
		Key:              meta.Key,
		Name:             meta.Name,
		Mode:             meta.Mode,
		Summary:          meta.Summary,
		VerboseLevel:     meta.VerboseLevel,
		Model:            meta.Model,
		ThinkingLevel:    meta.ThinkingLevel,
		Folder:           meta.Folder,
		AgentID:          meta.AgentID,
		SubagentStatus:   meta.SubagentStatus,
		InputTokens:      meta.InputTokens,
		OutputTokens:     meta.OutputTokens,
		CompactionCount:  meta.CompactionCount,
		Created:          meta.CreatedAt,
		Updated:          meta.UpdatedAt,
		Messages:         messages,
		firstInMemorySeq: boundary,
		evictedTotal:     evictedPersisted,
		lastPersistedSeq: len(messages) - 1,
	}

	sm.evictIfNeeded()

	sm.sessions[key] = session
	sm.accessTimes[key] = time.Now()
	sm.syncSessionMetaLocked(session)
	return session, true
}

func (sm *SessionManager) loadSessionMetadataFromSQLite() error {
	repo := sm.sessionRepo
	metas, err := repo.ListSessionMeta()
	if err != nil {
		return err
	}

	sm.sessionMeta = make(map[string]*sessionMetadata, len(metas))
	for _, meta := range metas {
		sm.sessionMeta[meta.Key] = &sessionMetadata{
			Key:            meta.Key,
			Name:           meta.Name,
			Mode:           meta.Mode,
			Folder:         meta.Folder,
			AgentID:        meta.AgentID,
			SubagentStatus: meta.SubagentStatus,
			Created:        meta.CreatedAt,
			Updated:        meta.UpdatedAt,
		}
	}
	return nil
}
