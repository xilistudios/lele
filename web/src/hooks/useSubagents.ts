import { useCallback, useEffect, useState } from 'react'
import { useAuthContext } from '../contexts/AuthContext'
import type { SubagentTaskInfo } from '../lib/types'
import { useOnPageVisible, usePageVisible } from './usePageVisible'

export type { SubagentTaskInfo as SubagentInfo }

export const MIN_POLL_MS = 1000

export const NO_POLL_MS = 0

const subagentListeners = new Set<() => void>()

export function notifySubagentsChanged(): void {
  for (const listener of subagentListeners) listener()
}

export function useSubagents(sessionKey: string | null, pollIntervalMs = 5000, active = false) {
  const { api } = useAuthContext()
  const [subagents, setSubagents] = useState<SubagentTaskInfo[]>([])
  const [loading, setLoading] = useState(false)
  const [hasRunning, setHasRunning] = useState(false)
  const pageVisible = usePageVisible()

  const fetchSubagents = useCallback(async () => {
    if (!sessionKey) {
      setSubagents([])
      return
    }
    setLoading(true)
    try {
      const data = await api.sessionSubagents(sessionKey)
      setSubagents(data?.subagents ?? [])
    } catch (err) {
      console.warn('[useSubagents] Failed to fetch:', err)
      setSubagents([])
    } finally {
      setLoading(false)
    }
  }, [sessionKey, api])

  useEffect(() => {
    fetchSubagents()
  }, [fetchSubagents])

  useEffect(() => {
    const listener = () => {
      void fetchSubagents()
    }
    subagentListeners.add(listener)
    return () => {
      subagentListeners.delete(listener)
    }
  }, [fetchSubagents])

  useEffect(() => {
    setHasRunning(subagents.some((s) => s.status === 'running' || s.status === 'pending'))
  }, [subagents])

  const shouldPoll = hasRunning || active
  useEffect(() => {
    if (!shouldPoll || pollIntervalMs <= 0) return
    if (!pageVisible) return

    const intervalMs =
      Number.isFinite(pollIntervalMs) && pollIntervalMs > 0
        ? Math.max(pollIntervalMs, MIN_POLL_MS)
        : MIN_POLL_MS

    const id = setInterval(() => {
      fetchSubagents()
    }, intervalMs)
    return () => clearInterval(id)
  }, [fetchSubagents, shouldPoll, pollIntervalMs, pageVisible])

  useOnPageVisible(() => {
    if (!shouldPoll || pollIntervalMs <= 0) return
    void fetchSubagents()
  })

  return { subagents, loading, refresh: fetchSubagents }
}
