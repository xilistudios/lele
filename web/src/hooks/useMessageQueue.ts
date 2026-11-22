import { useCallback, useRef, useState } from 'react'

export type QueuedMessage = {
  id: string
  content: string
  attachments: string[]
  sessionKey: string
  createdAt: number
}

export const QUEUE_CAP = 10

function createQueueId(): string {
  const cryptoRef = typeof globalThis !== 'undefined' ? globalThis.crypto : undefined
  if (cryptoRef && typeof cryptoRef.randomUUID === 'function') return cryptoRef.randomUUID()
  return `q-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

export function useMessageQueue() {
  const [queuedMessages, setQueuedMessages] = useState<QueuedMessage[]>([])

  const queueRef = useRef<QueuedMessage[]>(queuedMessages)

  const commit = useCallback((next: QueuedMessage[]) => {
    queueRef.current = next
    setQueuedMessages(next)
  }, [])

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

  const takeQueuedMessage = useCallback(
    (id: string): QueuedMessage | undefined => {
      const current = queueRef.current
      const index = current.findIndex((item) => item.id === id)
      if (index === -1) return undefined
      const next = current.slice(0, index).concat(current.slice(index + 1))
      commit(next)
      return current[index]
    },
    [commit],
  )

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

  const peekNext = useCallback((sessionKey: string | null): QueuedMessage | undefined => {
    if (!sessionKey) return undefined
    return queueRef.current.find((item) => item.sessionKey === sessionKey)
  }, [])

  const dequeueNext = useCallback(
    (sessionKey: string | null): QueuedMessage | undefined => {
      if (!sessionKey) return undefined
      const current = queueRef.current
      const index = current.findIndex((item) => item.sessionKey === sessionKey)
      if (index === -1) return undefined
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
    takeQueuedMessage,
    clearQueue,
    queueCount,
    peekNext,
    dequeueNext,
  }
}
