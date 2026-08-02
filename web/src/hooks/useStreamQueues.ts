import { useCallback, useEffect, useRef } from 'react'
import { createAssistantMessage } from '../lib/chatMessageBuilder'
import type { ChatMessage } from '../lib/types'

// Assistant insertion ordering lives in messageInsertion.ts (shared with
// the tool handlers). Re-exported here to preserve the existing import
// path used by messageOrdering.test.ts.
import { computeAssistantInsertIndex } from './messageInsertion'
export { computeAssistantInsertIndex }

// ── Animation tuning ────────────────────────────────────────────────────────

// One tick of the shared animation loop. ~60fps.
const TICK_MS = 16

// Base characters rendered per message per tick (the typewriter pace).
const CHARS_PER_TICK = 2

// When a message falls behind (e.g. the tab was backgrounded and ticks were
// throttled), the per-tick budget grows proportionally so the backlog drains
// over roughly CATCHUP_FRAMES frames. This smooth catch-up replaces the old
// hard FLUSH_THRESHOLD band-aid.
const CATCHUP_FRAMES = 8

type StreamQueue = {
  sessionKey: string
  /** Buffered characters not yet rendered. */
  pending: string
  /** True once the server signalled the stream is done. */
  done: boolean
}

type SetStreamingMessages = React.Dispatch<React.SetStateAction<ChatMessage[]>>

/**
 * Manages the typewriter animation for streaming assistant responses.
 *
 * A SINGLE shared interval drives every active message: each tick drains a
 * small budget of buffered characters from each queue and applies all updates
 * in ONE batched setState. This avoids the per-message timers and per-character
 * re-renders of the previous implementation while keeping the same smooth
 * typewriter feel.
 */
export function useStreamQueues(setStreamingMessages: SetStreamingMessages) {
  const queuesRef = useRef<Map<string, StreamQueue>>(new Map())
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const stopTimer = useCallback(() => {
    if (timerRef.current) {
      clearInterval(timerRef.current)
      timerRef.current = null
    }
  }, [])

  /**
   * Insert (or update) an assistant message. Used directly by message.ack,
   * message.thinking, and the done-with-chunk path — i.e. anywhere we need the
   * placeholder to exist immediately rather than waiting for the next tick.
   */
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
        const arr = [...current]
        arr.splice(computeAssistantInsertIndex(current), 0, newMsg)
        return arr
      })
    },
    [setStreamingMessages],
  )

  // One tick drains a budget of characters from EVERY active queue and applies
  // all the updates (creation + append + finalization) in a single setState.
  const tick = useCallback(() => {
    const queues = queuesRef.current
    if (queues.size === 0) {
      stopTimer()
      return
    }

    const appends = new Map<string, { sessionKey: string; text: string }>()
    const finished = new Set<string>()

    for (const [messageId, queue] of queues) {
      if (queue.pending.length > 0) {
        const budget = Math.max(CHARS_PER_TICK, Math.ceil(queue.pending.length / CATCHUP_FRAMES))
        appends.set(messageId, {
          sessionKey: queue.sessionKey,
          text: queue.pending.slice(0, budget),
        })
        queue.pending = queue.pending.slice(budget)
      }
      if (queue.pending.length === 0 && queue.done) {
        finished.add(messageId)
      }
    }

    for (const messageId of finished) queues.delete(messageId)
    if (queues.size === 0) stopTimer()
    if (appends.size === 0 && finished.size === 0) return

    setStreamingMessages((current) => {
      // Ensure every message that receives an append exists. Creation can be
      // load-bearing: when message.ack is lost/delayed, tools arrive first and
      // the assistant must be created here, ordered before those tools.
      let next = current
      for (const [messageId, { sessionKey }] of appends) {
        if (!next.some((m) => m.id === messageId)) {
          const arr = [...next]
          arr.splice(
            computeAssistantInsertIndex(next),
            0,
            createAssistantMessage({ id: messageId, sessionKey, content: '', streaming: true }),
          )
          next = arr
        }
      }

      return next.map((m) => {
        const append = appends.get(m.id)
        const isFinished = finished.has(m.id)
        if (!append && !isFinished) return m
        return {
          ...m,
          content: append ? m.content + append.text : m.content,
          sessionKey: append ? append.sessionKey : m.sessionKey,
          streaming: isFinished ? false : m.streaming,
        }
      })
    })
  }, [setStreamingMessages, stopTimer])

  const startTimer = useCallback(() => {
    if (!timerRef.current) {
      timerRef.current = setInterval(tick, TICK_MS)
    }
  }, [tick])

  const enqueueChunk = useCallback(
    (messageId: string, sessionKey: string, chunk: string, done: boolean) => {
      if (!messageId || !sessionKey) return

      let queue = queuesRef.current.get(messageId)
      if (!queue) {
        queue = { sessionKey, pending: '', done: false }
        queuesRef.current.set(messageId, queue)
      }

      queue.sessionKey = sessionKey
      if (chunk) queue.pending += chunk
      if (done) queue.done = true

      startTimer()
    },
    [startTimer],
  )

  const clearQueue = useCallback(
    (messageId: string) => {
      queuesRef.current.delete(messageId)
      if (queuesRef.current.size === 0) stopTimer()
    },
    [stopTimer],
  )

  const clearAllQueues = useCallback(() => {
    queuesRef.current.clear()
    stopTimer()
  }, [stopTimer])

  // Never leak the shared timer past unmount.
  useEffect(() => stopTimer, [stopTimer])

  return {
    enqueueChunk,
    clearQueue,
    clearAllQueues,
    ensureAssistantPlaceholder,
  }
}
