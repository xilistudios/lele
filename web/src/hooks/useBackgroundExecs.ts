import { useCallback, useEffect, useRef, useState } from 'react'
import { useAuthContext } from '../contexts/AuthContext'
import i18n from '../i18n'
import type { BackgroundExecInfo } from '../lib/types'

export function useBackgroundExecs() {
  const { api } = useAuthContext()
  const [processes, setProcesses] = useState<BackgroundExecInfo[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const hasRunningRef = useRef(false)

  const fetchProcesses = useCallback(async () => {
    setLoading(true)
    try {
      const data = await api.backgroundExecs.list(true)
      setProcesses(data?.processes ?? [])
      setError(null)
    } catch (err) {
      console.warn('[useBackgroundExecs] Failed to fetch:', err)
      setError(
        err instanceof Error && err.message ? err.message : i18n.t('backgroundExecs.loadError'),
      )
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
  }, [fetchProcesses, processes])

  const stopProcess = useCallback(
    async (id: string) => {
      try {
        await api.backgroundExecs.stop(id)
        setError(null)
        await fetchProcesses()
      } catch (err) {
        console.warn('[useBackgroundExecs] Failed to stop:', err)
        setError(
          err instanceof Error && err.message ? err.message : i18n.t('backgroundExecs.stopError'),
        )
      }
    },
    [api, fetchProcesses],
  )

  return { processes, loading, error, refresh: fetchProcesses, stopProcess }
}
