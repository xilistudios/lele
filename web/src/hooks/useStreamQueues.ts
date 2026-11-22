import { useCallback, useEffect, useRef } from 'react'
import { createAssistantMessage } from '../lib/chatMessageBuilder'
import type { ChatMessage } from '../lib/types'

import { computeAssistantInsertIndex } from './messageInsertion'
export { computeAssistantInsertIndex }

const TICK_MS = 32

const CHARS_PER_TICK = 4

const CATCHUP_FRAMES = 8

type StreamQueue = {
  sessionKey: string
  pending: string
  done: boolean
}

type SetStreamingMessages = React.Dispatch<React.SetStateAction<ChatMessage[]>>

export function useStreamQueues(setStreamingMessages: SetStreamingMessages) {
  const queuesRef = useRef<Map<string, StreamQueue>>(new Map())
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const stopTimer = useCallback(() => {
    if (timerRef.current) {
      clearInterval(timerRef.current)
      timerRef.current = null
    }
  }, [])

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
        const updated = current.map((m) =>
          m.role === 'assistant' && m.sessionKey === sessionKey && m.streaming
            ? { ...m, streaming: false }
            : m,
        )
        const newMsg = createAssistantMessage({
          id: messageId,
          sessionKey,
          content: chunk,
          streaming: !isDone,
        })
        const arr = [...updated]
        arr.splice(computeAssistantInsertIndex(updated), 0, newMsg)
        return arr
      })
    },
    [setStreamingMessages],
  )

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

  useEffect(() => stopTimer, [stopTimer])

  return {
    enqueueChunk,
    clearQueue,
    clearAllQueues,
    ensureAssistantPlaceholder,
  }
}
