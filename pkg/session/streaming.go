package session

import (
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

const streamFlushInterval = 200 * time.Millisecond

func (sm *SessionManager) AppendAssistantChunk(key, chunk string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)
	if session == nil {
		return
	}

	msg := sm.getOrCreateStreamingMsg(session)
	msg.Content += chunk
	session.Updated = time.Now()
	session.hadStreamedContent = true
	session.markModified(len(session.Messages) - 1)

	sm.maybeFlushStream(key)
}

func (sm *SessionManager) AppendReasoningChunk(key, chunk string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)
	if session == nil {
		return
	}

	msg := sm.getOrCreateStreamingMsg(session)
	msg.ReasoningContent += chunk
	session.Updated = time.Now()
	session.hadStreamedContent = true
	session.markModified(len(session.Messages) - 1)

	sm.maybeFlushStream(key)
}

func (sm *SessionManager) DiscardStreamingDraft(key string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)
	if session == nil || len(session.Messages) == 0 {
		return
	}

	tail := &session.Messages[len(session.Messages)-1]
	if tail.Role != "assistant" || !tail.Streaming {
		return
	}
	if tail.Content == "" && tail.ReasoningContent == "" {
		return
	}

	tail.Content = ""
	tail.ReasoningContent = ""
	session.Updated = time.Now()
	session.markModified(len(session.Messages) - 1)

	sm.maybeFlushStream(key)
}

func (sm *SessionManager) FinalizeAssistantMessage(key string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok || len(session.Messages) == 0 {
			return
		}
	}
	if len(session.Messages) == 0 {
		return
	}

	lastMsg := &session.Messages[len(session.Messages)-1]
	if lastMsg.Role == "assistant" && lastMsg.Streaming {
		session.Updated = time.Now()
		sm.touchSession(key)
		sm.flushStreamNow(key)
	}
}

func (sm *SessionManager) SealStreamingTail(key string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok || len(session.Messages) == 0 {
			return
		}
	}
	if len(session.Messages) == 0 {
		return
	}

	tail := &session.Messages[len(session.Messages)-1]
	if tail.Role != "assistant" || !tail.Streaming {
		return
	}
	if tail.Content == "" && tail.ReasoningContent == "" {
		return
	}

	tail.Streaming = false
	session.Updated = time.Now()
	session.markModified(len(session.Messages) - 1)
	session.publishViewLocked()
	sm.syncSessionMetaLocked(session)
	sm.flushStreamNow(key)
}

func (sm *SessionManager) AttachFilesToLastAssistant(key string, attachments []providers.MessageAttachment) {
	if len(attachments) == 0 {
		return
	}

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

	idx := -1
	for i := len(session.Messages) - 1; i >= 0; i-- {
		if session.Messages[i].Role == "assistant" {
			idx = i
			break
		}
	}
	if idx < 0 {
		logger.DebugCF("session", "AttachFilesToLastAssistant: no assistant message in session",
			map[string]interface{}{"session_key": key})
		return
	}

	msg := &session.Messages[idx]
	existing := make(map[string]struct{}, len(msg.Attachments))
	for _, a := range msg.Attachments {
		existing[a.Path] = struct{}{}
	}
	added := 0
	for _, a := range attachments {
		if _, dup := existing[a.Path]; dup {
			continue
		}
		existing[a.Path] = struct{}{}
		msg.Attachments = append(msg.Attachments, a)
		added++
	}
	if added == 0 {
		return
	}

	session.Updated = time.Now()
	session.markModified(idx)
	session.publishViewLocked()
	sm.touchSession(key)
	if err := sm.saveUnlocked(key); err != nil {
		logger.WarnCF("session", "Failed to persist message attachments",
			map[string]interface{}{"session_key": key, "error": err.Error()})
	}
}

func (sm *SessionManager) HasStreamedContent(key string) bool {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return false
	}

	if session.hadStreamedContent {
		return true
	}

	if len(session.Messages) > 0 {
		lastMsg := session.Messages[len(session.Messages)-1]
		if lastMsg.Role == "assistant" && lastMsg.Streaming &&
			(lastMsg.Content != "" || lastMsg.ReasoningContent != "") {
			return true
		}
	}

	return false
}

func (sm *SessionManager) GetInProgressAssistant(key string) *providers.Message {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok || len(session.Messages) == 0 {
		return nil
	}

	lastMsg := session.Messages[len(session.Messages)-1]
	if lastMsg.Role == "assistant" && lastMsg.Streaming {
		msg := lastMsg
		return &msg
	}
	return nil
}

func (sm *SessionManager) getOrCreateStreamingMsg(session *Session) *providers.Message {
	if len(session.Messages) > 0 {
		lastMsg := &session.Messages[len(session.Messages)-1]
		if lastMsg.Role == "assistant" && lastMsg.Streaming {
			return lastMsg
		}
	}

	session.Messages = append(session.Messages, providers.Message{
		Role:      "assistant",
		Streaming: true,
	})
	session.msgsAppended++
	return &session.Messages[len(session.Messages)-1]
}

func (sm *SessionManager) resolveStreamingTailLocked(session *Session, msg providers.Message) (slotReused bool) {
	if session == nil || len(session.Messages) == 0 {
		return false
	}

	tail := &session.Messages[len(session.Messages)-1]
	if tail.Role != "assistant" || !tail.Streaming {
		return false
	}

	if tail.Content != "" || tail.ReasoningContent != "" {
		tail.Streaming = false
	} else {
		*tail = msg
		slotReused = true
	}

	session.Updated = time.Now()
	session.markModified(len(session.Messages) - 1)
	session.publishViewLocked()
	sm.syncSessionMetaLocked(session)
	return slotReused
}

func (sm *SessionManager) maybeFlushStream(key string) {
	if sm.sessionRepo == nil {
		return
	}

	session, ok := sm.sessions[key]
	if !ok {
		return
	}

	now := time.Now()
	if now.Sub(session.lastStreamFlush) >= streamFlushInterval {
		session.lastStreamFlush = now
		sm.saveIncrementalUnlocked(key)
	}
}

func (sm *SessionManager) flushStreamNow(key string) {
	if sm.sessionRepo == nil {
		return
	}
	session, ok := sm.sessions[key]
	if ok {
		session.lastStreamFlush = time.Now()
	}
	sm.saveIncrementalUnlocked(key)
}
