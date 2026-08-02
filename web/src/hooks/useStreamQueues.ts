import { useCallback, useRef } from 'react'
import { createAssistantMessage } from '../lib/chatMessageBuilder'
import type { ChatMessage } from '../lib/types'

// Interval between characters when animating streaming text in the UI.
const STREAM_CHAR_INTERVAL_MS = 12

// If the queue accumulates more than this many characters (e.g., because the
// tab was backgrounded and setInterval was throttled), flush them all at once
// instead of animating one-by-one. This prevents the UI from appearing "stuck"
// after the user returns to the tab.
const FLUSH_THRESHOLD = 80

/**
 * Compute the index at which a new assistant message should be inserted into
 * the current streaming message array so it lands in the correct chronological
 * position relative to user messages, prior assistants, and tools.
 *
 * Case 1 — user message is AFTER the last assistant:
 *   e.g. [a1-completed, u2-opt] — a1 was retained from a previous turn (HTTP
 *   cache hasn't caught up yet). The new assistant responds to u2, so it must
 *   go AFTER u2, not after a1.
 *
 * Case 2 — last assistant is AFTER the last user (or no user exists):
 *   e.g. [u1-opt, a1, t1] — the new assistant is a continuation of the same turn
 *   (post-tool-call response). Insert after the last assistant and its trailing
 *   tools.
 *
 * Case 3 — no assistant exists:
 *   e.g. [t1, t2] — tools arrived before message.stream (ack delayed). Insert
 *   BEFORE the first tool so order is: assistant → tool calls.
 *
 * Exported as a pure function so the ordering rules can be unit-tested directly
 * (see messageOrdering.test.ts) without rendering React or driving a queue.
 */
export function computeAssistantInsertIndex(current: ChatMessage[]): number {
  const lastUserRevIdx = [...current].reverse().findIndex((m) => m.role === 'user')
  const lastAsstRevIdx = [...current].reverse().findIndex((m) => m.role === 'assistant')
  const lastUserPos = lastUserRevIdx >= 0 ? current.length - 1 - lastUserRevIdx : -1
  const lastAsstPos = lastAsstRevIdx >= 0 ? current.length - 1 - lastAsstRevIdx : -1

  if (lastUserPos > lastAsstPos) {
    // User message is after the last assistant — insert right after it
    return lastUserPos + 1
  }

  if (lastAsstPos >= 0) {
    // Continuation of the current turn — insert after assistant + its tools
    let insertIdx = lastAsstPos + 1
    while (insertIdx < current.length && current[insertIdx].role === 'tool') {
      insertIdx++
    }
    return insertIdx
  }

  // No assistant exists — insert before all tool messages
  const firstToolIdx = current.findIndex((m) => m.role === 'tool')
  return firstToolIdx >= 0 ? firstToolIdx : current.length
}

// NOTE: For O(1) lookups by message ID in consumer components, use the
// useMessageIndex hook from './useMessageIndex'. The setState callbacks
// below still use array scans because they need the latest closure state,
// but external handlers (e.g. event-handlers) can avoid repeated .find()
// calls by calling useMessageIndex(streamingMessages) once per render.

type StreamQueue = {
  sessionKey: string
  chars: string[]
  done: boolean
  timer: ReturnType<typeof setInterval> | null
}

type SetStreamingMessages = React.Dispatch<React.SetStateAction<ChatMessage[]>>

/**
 * Manages per-message character animation queues for streaming assistant responses.
 * Each message gets its own queue that drains one character at a time for a
 * smooth typewriter effect.
 */
export function useStreamQueues(setStreamingMessages: SetStreamingMessages) {
  const queuesRef = useRef<Map<string, StreamQueue>>(new Map())

  const ensureAssistantPlaceholder = useCallback(
    (messageId: string, sessionKey: string, chunk = '', isDone = false) => {
      setStreamingMessages((current) => {
        const existing = current.find((m) => m.id === messageId)
        if (existing) {
          return current.map((m) =>
            m.id === messageId
              ? {
                  ...m,
                  content: isDone ? chunk || m.content : chunk ? `${m.content}${chunk}` : m.content,
                  streaming: !isDone,
                  sessionKey,
                }
              : m,
          )
        }
        const newMsg = createAssistantMessage({
          id: messageId,
          sessionKey,
          content: chunk,
          streaming: !isDone,
        })

        // Compute the chronological insertion point (see computeAssistantInsertIndex
        // for the full case breakdown) and splice the new assistant in.
        const insertIdx = computeAssistantInsertIndex(current)
        const arr = [...current]
        arr.splice(insertIdx, 0, newMsg)
        return arr
      })
    },
    [setStreamingMessages],
  )

  const clearQueue = useCallback((messageId: string) => {
    const queue = queuesRef.current.get(messageId)
    if (queue?.timer) clearInterval(queue.timer)
    queuesRef.current.delete(messageId)
  }, [])

  const clearAllQueues = useCallback(() => {
    for (const queue of queuesRef.current.values()) {
      if (queue.timer) clearInterval(queue.timer)
    }
    queuesRef.current.clear()
  }, [])

  const drainQueue = useCallback(
    (messageId: string) => {
      const queue = queuesRef.current.get(messageId)
      if (!queue) return

      // If the queue has accumulated too many characters (tab was throttled),
      // flush them all at once to catch up immediately.
      if (queue.chars.length > FLUSH_THRESHOLD) {
        const bulk = queue.chars.splice(0, queue.chars.length).join('')
        ensureAssistantPlaceholder(messageId, queue.sessionKey, bulk, false)
        if (queue.done) {
          clearQueue(messageId)
          ensureAssistantPlaceholder(messageId, queue.sessionKey, '', true)
        }
        return
      }

      const nextChar = queue.chars.shift()
      if (nextChar) {
        ensureAssistantPlaceholder(messageId, queue.sessionKey, nextChar, false)
        return
      }

      if (queue.done) {
        clearQueue(messageId)
        ensureAssistantPlaceholder(messageId, queue.sessionKey, '', true)
      }
    },
    [clearQueue, ensureAssistantPlaceholder],
  )

  const enqueueChunk = useCallback(
    (messageId: string, sessionKey: string, chunk: string, done: boolean) => {
      if (!messageId || !sessionKey) return

      let queue = queuesRef.current.get(messageId)
      if (!queue) {
        queue = { sessionKey, chars: [], done: false, timer: null }
        queuesRef.current.set(messageId, queue)
      }

      queue.sessionKey = sessionKey
      if (chunk) queue.chars.push(...Array.from(chunk))
      if (done) queue.done = true

      if (!queue.timer) {
        queue.timer = setInterval(() => drainQueue(messageId), STREAM_CHAR_INTERVAL_MS)
      }
    },
    [drainQueue],
  )

  return {
    enqueueChunk,
    clearQueue,
    clearAllQueues,
    ensureAssistantPlaceholder,
  }
}
