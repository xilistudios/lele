import { useCallback, useRef } from 'react'
import { createAssistantMessage } from '../lib/chatMessageBuilder'
import type { ChatMessage } from '../lib/types'

// Interval between characters when animating streaming text in the UI.
const STREAM_CHAR_INTERVAL_MS = 12

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
        return [
          ...current,
          createAssistantMessage({
            id: messageId,
            sessionKey,
            content: chunk,
            streaming: !isDone,
          }),
        ]
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
