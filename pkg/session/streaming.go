package session

// Streaming assistant replies.
//
// Accumulates streamed chunks into an in-progress assistant message and
// persists them on a throttle (streamFlushInterval) so a crash mid-turn loses
// at most one interval of output. FinalizeAssistantMessage closes the turn.

import (
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

// streamFlushInterval is the minimum time between session saves during active streaming.
const streamFlushInterval = 200 * time.Millisecond

// AppendAssistantChunk appends a content chunk to the in-progress assistant message.
// If no in-progress message exists, it creates one with Streaming=true.
// The session is saved to disk periodically (throttled) so the partial content
// survives restarts and allows reconnecting clients to recover the stream.
func (sm *SessionManager) AppendAssistantChunk(key, chunk string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)
	if session == nil {
		return
	}

	// Find or create the in-progress assistant message
	msg := sm.getOrCreateStreamingMsg(session)
	msg.Content += chunk
	session.Updated = time.Now()
	session.hadStreamedContent = true
	session.markModified(len(session.Messages) - 1)

	sm.maybeFlushStream(key)
}

// AppendReasoningChunk appends a reasoning/thinking chunk to the in-progress
// assistant message. Creates the streaming message if it doesn't exist yet.
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

// FinalizeAssistantMessage marks the in-progress assistant message as complete
// by persisting the session to disk immediately. The Streaming flag is NOT
// cleared here — it stays until AddFullMessage replaces the streaming message
// with the final version. This allows HasStreamedContent to detect that content
// was already delivered via streaming chunks for deduplication.
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

// AttachFilesToLastAssistant appends file attachments to the last assistant
// message of a session (even if it is still in streaming state) and persists
// the session. Attachments already present with the same path are skipped
// (dedupe by path), so the operation is idempotent across repeated
// message.complete events. When the session has no assistant message the call
// is a silent no-op: attachments delivered out of band (e.g. a send_file whose
// turn produced no assistant bubble yet) must not fail the delivery path.
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
	sm.touchSession(key)
	if err := sm.saveUnlocked(key); err != nil {
		logger.WarnCF("session", "Failed to persist message attachments",
			map[string]interface{}{"session_key": key, "error": err.Error()})
	}
}

// HasStreamedContent returns true if the session already had content delivered
// via streaming chunks this turn. Used to prevent duplicate message.stream
// delivery. It checks the in-memory flag (set by AppendAssistantChunk and
// cleared when a new user message arrives).
// Note: If the session is not in memory (evicted), returns false. The session
// will be loaded on-demand by the streaming methods.
func (sm *SessionManager) HasStreamedContent(key string) bool {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return false
	}

	// Check the in-memory flag first (survives Streaming flag being cleared by AddFullMessage)
	if session.hadStreamedContent {
		return true
	}

	// Fallback: check if the last message is still streaming
	if len(session.Messages) > 0 {
		lastMsg := session.Messages[len(session.Messages)-1]
		if lastMsg.Role == "assistant" && lastMsg.Streaming &&
			(lastMsg.Content != "" || lastMsg.ReasoningContent != "") {
			return true
		}
	}

	return false
}

// GetInProgressAssistant returns the in-progress assistant message, if any.
// Note: If the session is not in memory (evicted), returns nil. The session
// will be loaded on-demand by the streaming methods.
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

// getOrCreateStreamingMsg finds or creates the in-progress assistant message.
// Caller must hold sm.mu.
func (sm *SessionManager) getOrCreateStreamingMsg(session *Session) *providers.Message {
	if len(session.Messages) > 0 {
		lastMsg := &session.Messages[len(session.Messages)-1]
		if lastMsg.Role == "assistant" && lastMsg.Streaming {
			return lastMsg
		}
	}

	// Create a new streaming assistant message
	session.Messages = append(session.Messages, providers.Message{
		Role:      "assistant",
		Streaming: true,
	})
	session.msgsAppended++
	return &session.Messages[len(session.Messages)-1]
}

// maybeFlushStream saves the session to disk if enough time has passed since
// the last stream flush. Uses incremental save to avoid rewriting all messages.
// Caller must hold sm.mu.
func (sm *SessionManager) maybeFlushStream(key string) {
	if sm.store == nil {
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

// flushStreamNow saves the session to disk immediately using incremental save.
// Caller must hold sm.mu.
func (sm *SessionManager) flushStreamNow(key string) {
	if sm.store == nil {
		return
	}
	session, ok := sm.sessions[key]
	if ok {
		session.lastStreamFlush = time.Now()
	}
	sm.saveIncrementalUnlocked(key)
}
