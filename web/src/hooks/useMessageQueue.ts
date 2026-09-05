import { useCallback, useRef, useState } from 'react'

/**
 * A message the user submitted while the agent was already busy with a turn.
 *
 * The queue is client-side only and lives in memory: it is intentionally lost on
 * reload (a stale unsent message after a page refresh is more confusing than
 * helpful). Each entry remembers the session it was typed in, so a busy session
 * never drains its backlog into whichever chat happens to be visible later.
 */
export type QueuedMessage = {
  id: string
  content: string
  attachments: string[]
  sessionKey: string
  createdAt: number
}

/** Maximum number of messages held per session. Beyond it, enqueue is refused. */
export const QUEUE_CAP = 10

function createQueueId(): string {
  const cryptoRef = typeof globalThis !== 'undefined' ? globalThis.crypto : undefined
  if (cryptoRef && typeof cryptoRef.randomUUID === 'function') return cryptoRef.randomUUID()
  return `q-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

/**
 * FIFO, per-session, in-memory queue of messages waiting for the agent's turn to
 * end. Pure state container: it does NOT decide when to flush — useAppLogic pops
 * one entry per processing falling edge, so the drain stays paced by real turns.
 */
export function useMessageQueue() {
  const [queuedMessages, setQueuedMessages] = useState<QueuedMessage[]>([])

  // Mirror of the state for reads that must not go stale inside a callback
  // (peek/dequeue run in an effect that must not re-subscribe on every tick).
  const queueRef = useRef<QueuedMessage[]>(queuedMessages)

  const commit = useCallback((next: QueuedMessage[]) => {
    queueRef.current = next
    setQueuedMessages(next)
  }, [])

  /**
   * Append to the back of the session's queue. Returns false (and changes
   * nothing) when that session already holds QUEUE_CAP entries.
   */
  const enqueueMessage = useCallback(
    (sessionKey: string, content: string, attachments: string[] = []): boolean => {
      if (!sessionKey) return false
      const normalized = content.trim()
      if (!normalized && attachments.length === 0) return false

      const current = queueRef.current
      let count = 0
      for (const item of current) {
        if (item.sessionKey === sessionKey) count += 1
      }
      if (count >= QUEUE_CAP) return false

      commit([
        ...current,
        {
          id: createQueueId(),
          content: normalized,
          attachments,
          sessionKey,
          createdAt: Date.now(),
        },
      ])
      return true
    },
    [commit],
  )

  const removeQueuedMessage = useCallback(
    (id: string) => {
      const current = queueRef.current
      const next = current.filter((item) => item.id !== id)
      if (next.length !== current.length) commit(next)
    },
    [commit],
  )

  /** Drop every entry for a session (used by the "clear queue" button). */
  const clearQueue = useCallback(
    (sessionKey: string) => {
      const current = queueRef.current
      const next = current.filter((item) => item.sessionKey !== sessionKey)
      if (next.length !== current.length) commit(next)
    },
    [commit],
  )

  const queueCount = useCallback((sessionKey: string | null) => {
    if (!sessionKey) return 0
    let count = 0
    for (const item of queueRef.current) {
      if (item.sessionKey === sessionKey) count += 1
    }
    return count
  }, [])

  /** Front of the session's queue without removing it. */
  const peekNext = useCallback((sessionKey: string | null): QueuedMessage | undefined => {
    if (!sessionKey) return undefined
    return queueRef.current.find((item) => item.sessionKey === sessionKey)
  }, [])

  /** Remove and return the front of the session's queue. */
  const dequeueNext = useCallback(
    (sessionKey: string | null): QueuedMessage | undefined => {
      if (!sessionKey) return undefined
      const current = queueRef.current
      const index = current.findIndex((item) => item.sessionKey === sessionKey)
      if (index === -1) return undefined
      // Never mutate the array held in state — build a fresh one.
      const next = current.slice(0, index).concat(current.slice(index + 1))
      commit(next)
      return current[index]
    },
    [commit],
  )

  return {
    queuedMessages,
    enqueueMessage,
    removeQueuedMessage,
    clearQueue,
    queueCount,
    peekNext,
    dequeueNext,
  }
}
