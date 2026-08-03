/**
 * Pure transitions over the streaming message list.
 *
 * Every WebSocket handler that mutates `streamingMessages` should go through
 * one of these functions instead of inlining `setStreamingMessages(updater)`
 * logic. Keeping the transitions here means the rules (ID migration, restore
 * hydration, placeholder upsert, failure marking) live in ONE place and can be
 * unit-tested without React or a WebSocket.
 *
 * All functions are pure: (current, ...args) => next.
 */
import { createAssistantMessage } from '../lib/chatMessageBuilder'
import type { ChatMessage } from '../lib/types'
import { computeAssistantInsertIndex } from './messageInsertion'

const RESTORE_PREFIX = 'restore-'

/** True if the message is a reconnect/restore placeholder for a session. */
function isRestorePlaceholder(m: ChatMessage, sessionKey: string): boolean {
  return m.id.startsWith(RESTORE_PREFIX) && m.sessionKey === sessionKey
}

/**
 * Migrate a `restore-<session>` placeholder to a real message id.
 *
 * After a page reload or reconnect, the welcome/subscribe event creates a
 * placeholder holding accumulated content. When the real stream/thinking
 * chunks arrive with the actual message_id, we rename the placeholder so its
 * content is preserved instead of spawning a duplicate. No-op if a message
 * with the real id already exists.
 */
export function migrateRestoreId(
  current: ChatMessage[],
  msgId: string,
  sessionKey: string,
): ChatMessage[] {
  if (current.some((m) => m.id === msgId)) return current
  const restoreIdx = current.findIndex((m) => isRestorePlaceholder(m, sessionKey))
  if (restoreIdx < 0) return current
  return current.map((m, i) => (i === restoreIdx ? { ...m, id: msgId } : m))
}

/** Remove all restore placeholders for a session. */
export function removeRestorePlaceholders(
  current: ChatMessage[],
  sessionKey: string,
): ChatMessage[] {
  return current.filter((m) => !isRestorePlaceholder(m, sessionKey))
}

/**
 * Insert (or update) an assistant placeholder at the correct chronological
 * position. Used by message.ack and by thinking chunks that arrive before any
 * stream chunk.
 *
 * - If a message with `msgId` exists, it is returned unchanged (content is
 *   filled in by the stream queue / thinking handler).
 * - Otherwise a new empty assistant is spliced in at
 *   `computeAssistantInsertIndex`.
 */
export function upsertAssistantPlaceholder(
  current: ChatMessage[],
  msgId: string,
  sessionKey: string,
): ChatMessage[] {
  if (current.some((m) => m.id === msgId)) return current

  const newMsg = createAssistantMessage({
    id: msgId,
    sessionKey,
    content: '',
    streaming: true,
  })
  const arr = [...current]
  arr.splice(computeAssistantInsertIndex(current), 0, newMsg)
  return arr
}

/**
 * Append (or merge into) a restore placeholder from accumulated in-progress
 * content sent by the backend on welcome/reconnect/subscribe.
 *
 * `insertAfterLastUserForSession` controls placement:
 *   - true  (subscribe.ack): insert right after the session's last user
 *     message. Pushing to the end would break position-based matching in
 *     mergeMessages and render the restored assistant ABOVE the user.
 *   - false (welcome): push to the end (no user context yet).
 */
export function restoreInProgressAssistant(
  current: ChatMessage[],
  sessionKey: string,
  content: string,
  reasoning: string,
  insertAfterLastUserForSession: boolean,
): ChatMessage[] {
  const restoreId = `${RESTORE_PREFIX}${sessionKey}`
  const existingIdx = current.findIndex((m) => m.id === restoreId)

  if (existingIdx >= 0) {
    return current.map((m, i) =>
      i === existingIdx
        ? {
            ...m,
            content: content || m.content,
            reasoningContent: reasoning || m.reasoningContent,
            streaming: true,
          }
        : m,
    )
  }

  const newMsg = createAssistantMessage({
    id: restoreId,
    sessionKey,
    content,
    reasoningContent: reasoning || undefined,
    streaming: true,
  })

  const arr = [...current]
  if (insertAfterLastUserForSession) {
    let insertIdx = arr.length
    for (let i = arr.length - 1; i >= 0; i--) {
      if (arr[i].sessionKey === sessionKey && arr[i].role === 'user') {
        insertIdx = i + 1
        break
      }
    }
    arr.splice(insertIdx, 0, newMsg)
  } else {
    arr.push(newMsg)
  }
  return arr
}

/**
 * Apply the final state for a completed message:
 *   - drop restore placeholders for the session,
 *   - mark the completed assistant (overwriting content only if the server
 *     sent a non-empty final version),
 *   - drop empty user messages for the session,
 *   - mark session tools as no longer streaming.
 */
export function applyMessageComplete(
  current: ChatMessage[],
  msgId: string,
  sessionKey: string,
  serverContent: string | undefined,
): ChatMessage[] {
  // If the message never made it into the streaming list (e.g. message.complete
  // arrived before the typewriter queue drained its first tick, or the stream
  // events were coalesced/lost), create it with the final content instead of
  // silently dropping the response.
  if (!current.some((m) => m.role === 'assistant' && m.id === msgId)) {
    const content = serverContent && serverContent.length > 0 ? serverContent : ''
    const newMsg = createAssistantMessage({
      id: msgId,
      sessionKey,
      content,
      streaming: false,
    })
    const arr = [...current]
    arr.splice(computeAssistantInsertIndex(current), 0, newMsg)
    return arr.filter((m) => !isRestorePlaceholder(m, sessionKey))
  }

  return current.flatMap((m) => {
    if (isRestorePlaceholder(m, sessionKey)) return []

    if (m.role === 'assistant' && m.id === msgId) {
      const content = serverContent && serverContent.length > 0 ? serverContent : m.content
      return [{ ...m, content, streaming: false }]
    }
    if (m.role === 'user' && m.sessionKey === sessionKey && m.content.trim() === '') {
      return []
    }
    if (m.role === 'tool' && m.sessionKey === sessionKey) {
      return [{ ...m, streaming: false }]
    }
    return [m]
  })
}

/** Mark the optimistic user message for a session as failed. */
export function markOptimisticUserFailed(
  current: ChatMessage[],
  sessionKey: string,
): ChatMessage[] {
  return current.map((m) =>
    m.role === 'user' && m.optimistic && m.sessionKey === sessionKey
      ? { ...m, failed: true, streaming: false }
      : m,
  )
}

/** Mark all actively-streaming assistants for a session as errored. */
export function markStreamingAssistantsErrored(
  current: ChatMessage[],
  sessionKey: string,
  error: string,
): ChatMessage[] {
  return current.map((m) =>
    m.sessionKey === sessionKey && m.role === 'assistant' && m.streaming
      ? { ...m, streaming: false, error }
      : m,
  )
}

/** Mark every message as no longer streaming (used by cancel.ack). */
export function stopAllStreaming(current: ChatMessage[]): ChatMessage[] {
  return current.map((m) => ({ ...m, streaming: false }))
}

/** Attach files to the most recent assistant message and stop its streaming. */
export function attachToLastAssistant(
  current: ChatMessage[],
  attachments: ChatMessage['attachments'],
): ChatMessage[] {
  const idx = [...current].reverse().findIndex((m) => m.role === 'assistant')
  if (idx < 0) return current
  const targetIndex = current.length - idx - 1
  return current.map((m, i) => (i === targetIndex ? { ...m, attachments, streaming: false } : m))
}
