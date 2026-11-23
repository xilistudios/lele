package session

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

const (
	streamFlushInterval       = 200 * time.Millisecond
	attachmentRowDedupeWindow = 8
)

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

func (sm *SessionManager) AddAttachmentMessage(key, content string, attachments []providers.MessageAttachment) {
	if len(attachments) == 0 {
		return
	}

	toolCallID := attachmentToolCallID(attachments)

	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			logger.DebugCF("session", "AddAttachmentMessage: session not found",
				map[string]interface{}{"session_key": key})
			return
		}
	}

	if idx := recentAttachmentRowIndex(session.Messages, toolCallID); idx >= 0 {
		sm.mergeAttachmentRowLocked(key, session, idx, content, attachments)
		return
	}

	pos := len(session.Messages)
	if last := len(session.Messages) - 1; last >= 0 && session.Messages[last].Role == "assistant" && session.Messages[last].Streaming {
		pos = last
	}

	session.Messages = slices.Insert(session.Messages, pos, providers.Message{
		Role:               "tool",
		ToolCallID:         toolCallID,
		Content:            content,
		Attachments:        attachments,
		ExcludeFromContext: true,
	})
	session.markModified(pos)
	session.Updated = time.Now()
	session.publishViewLocked()
	sm.syncSessionMetaLocked(session)
	sm.touchSession(key)
	if err := sm.saveUnlocked(key); err != nil {
		logger.WarnCF("session", "Failed to persist attachment message",
			map[string]interface{}{"session_key": key, "error": err.Error()})
	}
}

func (sm *SessionManager) mergeAttachmentRowLocked(key string, session *Session, idx int, content string, attachments []providers.MessageAttachment) {
	msg := &session.Messages[idx]

	changed := false
	if msg.Content == "" && content != "" {
		msg.Content = content
		changed = true
	}

	existing := make(map[string]struct{}, len(msg.Attachments))
	for _, a := range msg.Attachments {
		existing[a.Path] = struct{}{}
	}
	for _, a := range attachments {
		if _, dup := existing[a.Path]; dup {
			continue
		}
		existing[a.Path] = struct{}{}
		msg.Attachments = append(msg.Attachments, a)
		changed = true
	}
	if !changed {
		return
	}

	session.Updated = time.Now()
	session.markModified(idx)
	session.publishViewLocked()
	sm.syncSessionMetaLocked(session)
	sm.touchSession(key)
	if err := sm.saveUnlocked(key); err != nil {
		logger.WarnCF("session", "Failed to persist message attachments",
			map[string]interface{}{"session_key": key, "error": err.Error()})
	}
}

func attachmentToolCallID(attachments []providers.MessageAttachment) string {
	paths := make([]string, 0, len(attachments))
	for _, a := range attachments {
		paths = append(paths, a.Path)
	}
	sort.Strings(paths)
	sum := sha256.Sum256([]byte(strings.Join(paths, "\n")))
	return "file:" + hex.EncodeToString(sum[:])[:16]
}

func recentAttachmentRowIndex(messages []providers.Message, toolCallID string) int {
	start := len(messages) - attachmentRowDedupeWindow
	if start < 0 {
		start = 0
	}
	for i := len(messages) - 1; i >= start; i-- {
		msg := messages[i]
		if msg.Role == "tool" && strings.HasPrefix(msg.ToolCallID, "file:") && msg.ToolCallID == toolCallID {
			return i
		}
	}
	return -1
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
