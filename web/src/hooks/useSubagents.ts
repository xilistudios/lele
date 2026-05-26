import { useCallback, useEffect, useRef, useState } from 'react'
import { useAuthContext } from '../contexts/AuthContext'
import type { SubagentTaskInfo } from '../lib/types'

export type { SubagentTaskInfo as SubagentInfo }

export function useSubagents(sessionKey: string | null) {
  const { api } = useAuthContext()
  const [subagents, setSubagents] = useState<SubagentTaskInfo[]>([])
  const [loading, setLoading] = useState(false)
  const hasRunningRef = useRef(false)

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

  // Track running state in a ref so the polling effect doesn't re-trigger
  // on every subagents change (avoids timer churn).
  useEffect(() => {
    hasRunningRef.current = subagents.some((s) => s.status === 'running')
  }, [subagents])

  // Poll every 5s while any subagent is running
  useEffect(() => {
    if (!hasRunningRef.current) return

    const id = setInterval(() => {
      if (hasRunningRef.current) {
        fetchSubagents()
      }
    }, 5000)
    return () => clearInterval(id)
  }, [fetchSubagents])

  return { subagents, loading, refresh: fetchSubagents }
}
