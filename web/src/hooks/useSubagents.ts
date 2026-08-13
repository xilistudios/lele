import { useCallback, useEffect, useState } from 'react'
import { useAuthContext } from '../contexts/AuthContext'
import type { SubagentTaskInfo } from '../lib/types'

export type { SubagentTaskInfo as SubagentInfo }

export function useSubagents(sessionKey: string | null, pollIntervalMs = 5000) {
  const { api } = useAuthContext()
  const [subagents, setSubagents] = useState<SubagentTaskInfo[]>([])
  const [loading, setLoading] = useState(false)
  const [hasRunning, setHasRunning] = useState(false)

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

  // Fetch on mount and when session changes
  useEffect(() => {
    fetchSubagents()
  }, [fetchSubagents])

  // Track running state so the polling effect can react to it.
  useEffect(() => {
    setHasRunning(subagents.some((s) => s.status === 'running'))
  }, [subagents])

  // Poll every 5s while any subagent is running
  useEffect(() => {
    if (!hasRunning) return

    const id = setInterval(() => {
      fetchSubagents()
    }, pollIntervalMs)
    return () => clearInterval(id)
  }, [fetchSubagents, hasRunning, pollIntervalMs])

  return { subagents, loading, refresh: fetchSubagents }
}
