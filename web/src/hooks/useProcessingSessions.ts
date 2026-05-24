import { useCallback, useRef, useState } from 'react'

/**
 * Tracks which sessions are currently being processed by the backend.
 * Used to show spinners in the sidebar.
 */
export function useProcessingSessions() {
  const [processingSessions, setProcessingSessions] = useState<Set<string>>(new Set())
  const processingSessionKeyRef = useRef<string | null>(null)

  const addSession = useCallback((sessionKey: string) => {
    if (!sessionKey) return
    setProcessingSessions((prev) => {
      if (prev.has(sessionKey)) return prev
      const next = new Set(prev)
      next.add(sessionKey)
      return next
    })
    processingSessionKeyRef.current = sessionKey
  }, [])

  const removeSession = useCallback((sessionKey: string) => {
    if (!sessionKey) return
    setProcessingSessions((prev) => {
      if (!prev.has(sessionKey)) return prev
      const next = new Set(prev)
      next.delete(sessionKey)
      return next
    })
  }, [])

  const syncSession = useCallback((sessionKey: string, processing: boolean) => {
    if (!sessionKey) return
    setProcessingSessions((prev) => {
      const has = prev.has(sessionKey)
      if (processing && !has) {
        const next = new Set(prev)
        next.add(sessionKey)
        return next
      }
      if (!processing && has) {
        const next = new Set(prev)
        next.delete(sessionKey)
        return next
      }
      return prev
    })
  }, [])

  const clearAll = useCallback(() => {
    setProcessingSessions(new Set())
    processingSessionKeyRef.current = null
  }, [])

  return {
    processingSessions,
    setProcessingSessions,
    processingSessionKeyRef,
    addSession,
    removeSession,
    syncSession,
    clearAll,
  }
}
