import { useCallback, useEffect, useRef, useState } from 'react'
import { useAuthContext } from '../contexts/AuthContext'
import type { BackgroundExecInfo } from '../lib/types'

export function useBackgroundExecs() {
  const { api } = useAuthContext()
  const [processes, setProcesses] = useState<BackgroundExecInfo[]>([])
  const [loading, setLoading] = useState(false)
  const hasRunningRef = useRef(false)

  const fetchProcesses = useCallback(async () => {
    setLoading(true)
    try {
      const data = await api.backgroundExecs.list(true)
      setProcesses(data?.processes ?? [])
    } catch (err) {
      console.warn('[useBackgroundExecs] Failed to fetch:', err)
      setProcesses([])
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    fetchProcesses()
  }, [fetchProcesses])

  useEffect(() => {
    hasRunningRef.current = processes.some((p) => p.status === 'running')
  }, [processes])

  // Poll every 3s while any process is running
  useEffect(() => {
    if (!hasRunningRef.current) return
    const id = setInterval(() => {
      if (hasRunningRef.current) {
        fetchProcesses()
      }
    }, 3000)
    return () => clearInterval(id)
  }, [fetchProcesses])

  const stopProcess = useCallback(
    async (id: string) => {
      try {
        await api.backgroundExecs.stop(id)
        await fetchProcesses()
      } catch (err) {
        console.warn('[useBackgroundExecs] Failed to stop:', err)
      }
    },
    [api, fetchProcesses],
  )

  return { processes, loading, refresh: fetchProcesses, stopProcess }
}
