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
//
// COALESCING (do not "fix" this): the write is in-place (msg.Content += chunk)
// and deliberately does NOT publish the read-only history view. Publishing is
// an O(n) copy of the whole history (192 B/message: ~1.16 MB per copy at 6000
// messages) and this method runs once per streamed token — far more often than
// the view is read — so eager publication here would make streaming slower than
// the copy-per-read it replaces. Instead the epoch bumped by markModified leaves
// the published snapshot visibly stale (snapshot.epoch != saveEpoch) and
// GetHistoryView rebuilds the copy on its next read, which republishes it for
// every following read in the same burst. Worst case is therefore one copy per
// read burst (per frame) instead of one copy per chunk and per read.
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
//
// Coalesced exactly like AppendAssistantChunk: the in-place write leaves the
// published view stale on purpose (see that method for the rationale).
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

// DiscardStreamingDraft clears the content and reasoning accumulated in the
// in-progress (Streaming) tail assistant message, keeping that message as the
// live tail.
//
// WHY THIS EXISTS: chunks accumulate per SESSION while the runner thinks per
// ITERATION. pkg/channels/native.go forwards every streamed reasoning/content
// delta to AppendReasoningChunk/AppendAssistantChunk, and both append to
// whatever the session's tail streaming assistant happens to be — they receive
// no message or iteration id, so nothing in the session layer can tell which
// iteration produced a chunk. Nearly every iteration ends by persisting its
// result through AddFullMessage, whose in-place branch takes the tail over
// (Streaming=false) and thereby hands the next iteration a fresh slot. But
// pkg/agent/llm_runner.go has `continue` paths that DISCARD an iteration
// without persisting anything: the bounded empty-response retry and the
// all-tool-calls-invalid path when the response has no content. Those
// iterations only append the blank assistant turn to the runner's in-memory
// `messages` slice; the session tail is never finalized, so the NEXT
// iteration's chunks are appended ON TOP of the discarded iteration's chunks.
// The tail then carries N concatenated copies of the same reasoning (up to
// maxConsecutiveEmptyResponses+1 when the turn ends through the empty-limit
// early return, which persists nothing and therefore leaves that tail as the
// surviving record of the whole turn) and a reload/TUI shows the same thinking
// paragraph repeated. The runner calls this method at the top of every
// iteration after the first, so each iteration starts from an empty draft.
//
// The guards are what make it safe to call unconditionally:
//
//   - the tail must be an assistant message still flagged Streaming, i.e. a
//     real in-progress draft. A finalized iteration (AddFullMessage cleared
//     Streaming) is a REAL message and must never lose its content, and a
//     user/tool/system tail is not a draft at all.
//   - an already-empty draft is a no-op, so the call does not churn the epoch
//     (and therefore does not invalidate the published history view) on every
//     iteration of a turn that streamed nothing.
//
// Nothing is deleted or reordered: the message keeps its slot and only the
// accumulated text is emptied, so index bookkeeping (markModified) and any
// client-side message ordering stay consistent.
//
// Coalesced exactly like the appends: markModified bumps the epoch, which
// leaves the published view stale on purpose and makes GetHistoryView rebuild
// it on its next read — so publishViewLocked is deliberately NOT called here
// (see the coalescing comment on AppendAssistantChunk).
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

// FinalizeAssistantMessage marks the in-progress assistant message as complete
// by persisting the session to disk immediately. The Streaming flag is NOT
// cleared here — it is cleared later, either by AddFullMessage's in-place branch
// (which replaces the streaming message with the final version) or, when a
// non-assistant message is appended after it, by resolveStreamingTailLocked.
// Keeping the flag set until then is what lets HasStreamedContent detect that
// content was already delivered via streaming chunks for deduplication.
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
	session.publishViewLocked() // attachments land once per turn
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
//
// Appending the new streaming message is an un-published mutation on purpose:
// both callers (AppendAssistantChunk/AppendReasoningChunk) bump the epoch right
// after through markModified, so the published view is stale and the next
// GetHistoryView rebuilds it (streaming coalescing, see AppendAssistantChunk).
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

// resolveStreamingTailLocked restores the invariant "a Streaming assistant is
// only ever the last message" before a non-assistant message is appended.
// Returns true when msg was written into the tail slot (the tail was an empty
// streaming placeholder) and must NOT be appended again.
// Caller must hold sm.mu.
//
// Why this exists: the only production writer that clears Streaming=false was
// AddFullMessage's in-place branch, which requires the incoming message to be
// role "assistant" AND the tail to still be a streaming assistant. Every other
// route that follows a streamed assistant — the loop-detector guidance
// (AddMessage "user", llm_runner.go), the malformed-tool-call guidance, tool
// results, and the early returns that end a turn mid-stream — appends a
// NON-assistant message, so the streaming flag was never cleared. The orphan
// then sat in the middle of the history with Streaming=true: persisted to disk,
// surviving restarts, and replayed to the LLM on every later request.
//
// The resolution is deliberately local (no reordering, no deletion, the live
// in-memory message list in the runner is untouched):
//
//   - a tail that already carries content is finalized IN PLACE — same slice
//     position, same content, only the flag flips. An orphan left by a crash is
//     still visible to the crash-recovery path of pkg/channels/rest_chat.go,
//     which only hides a streaming message while the session is processing.
//   - an empty placeholder (created before the first chunk arrived) has nothing
//     to preserve, so the incoming message takes its slot. That reuses the
//     position instead of RemoveLastMessage, whose deleteFromSeq watermark
//     (persist.go) composes badly with the msgsAppended++ of the very same
//     call. RemoveLastMessage also re-takes sm.mu (the mutex is not
//     reentrant), so it is never called from here.
//
// When the tail is not a streaming assistant this is a strict no-op and the
// caller appends exactly as it did before.
func (sm *SessionManager) resolveStreamingTailLocked(session *Session, msg providers.Message) (slotReused bool) {
	if session == nil || len(session.Messages) == 0 {
		return false
	}

	tail := &session.Messages[len(session.Messages)-1]
	if tail.Role != "assistant" || !tail.Streaming {
		return false
	}

	if tail.Content != "" || tail.ReasoningContent != "" {
		// Content was already delivered: finalize the message in place.
		tail.Streaming = false
	} else {
		// Empty placeholder: reuse the tail slot for the incoming message.
		*tail = msg
		slotReused = true
	}

	session.Updated = time.Now()
	session.markModified(len(session.Messages) - 1)
	session.publishViewLocked()
	sm.syncSessionMetaLocked(session)
	return slotReused
}

// maybeFlushStream saves the session to disk if enough time has passed since
// the last stream flush. Uses incremental save to avoid rewriting all messages.
// Caller must hold sm.mu.
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

// flushStreamNow saves the session to disk immediately using incremental save.
// Caller must hold sm.mu.
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
