import { useCallback, useEffect, useRef, useState } from 'react'
import { useAuthContext } from '../contexts/AuthContext'
import type { CronJob, CronJobInput, CronStatus } from '../lib/types'
import { useOnPageVisible, usePageVisible } from './usePageVisible'

const POLL_INTERVAL_MS = 5000

export function useCronJobs() {
  const { api } = useAuthContext()
  const [jobs, setJobs] = useState<CronJob[]>([])
  const [status, setStatus] = useState<CronStatus | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(true)
  const pageVisible = usePageVisible()

  const fetchJobs = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const data = await api.cron.list(true)
      if (!mountedRef.current) return
      setJobs(data?.jobs ?? [])
      setStatus(data?.status ?? null)
    } catch (err) {
      console.warn('[useCronJobs] Failed to fetch:', err)
      if (mountedRef.current) {
        setError((err as Error).message)
      }
    } finally {
      if (mountedRef.current) setLoading(false)
    }
  }, [api])

  // Silent poll: same read as `fetchJobs` but without the loading flag or the
  // error banner, so a periodic/visibility refresh never flashes the UI. Used
  // by the interval and by the "tab became visible" refresh.
  const pollJobs = useCallback(async () => {
    try {
      const data = await api.cron.list(true)
      if (!mountedRef.current) return
      setJobs(data?.jobs ?? [])
      setStatus(data?.status ?? null)
    } catch {
      // ignore transient polling errors
    }
  }, [api])

  // Initial load
  useEffect(() => {
    mountedRef.current = true
    fetchJobs()
    return () => {
      mountedRef.current = false
    }
  }, [fetchJobs])

  // Poll to keep next-run / last-status fresh.
  //
  // Visibility-gated: a hidden tab must not poll the gateway, so this effect
  // returns before creating the interval and is re-run by the visibility change
  // when the tab comes back.
  useEffect(() => {
    if (!pageVisible) return
    const id = setInterval(() => {
      void pollJobs()
    }, POLL_INTERVAL_MS)
    return () => clearInterval(id)
  }, [pageVisible, pollJobs])

  // Refresh once when the tab becomes visible again. While hidden the interval
  // above was cleared, so waiting for the next tick would leave next-run /
  // last-status stale for up to POLL_INTERVAL_MS after the user returns.
  useOnPageVisible(() => {
    void pollJobs()
  })

  const toggleEnabled = useCallback(
    async (job: CronJob) => {
      try {
        if (job.enabled) {
          await api.cron.disable(job.id)
        } else {
          await api.cron.enable(job.id)
        }
        await fetchJobs()
      } catch (err) {
        console.warn('[useCronJobs] Failed to toggle:', err)
        setError((err as Error).message)
      }
    },
    [api, fetchJobs],
  )

  const removeJob = useCallback(
    async (id: string) => {
      try {
        await api.cron.remove(id)
        await fetchJobs()
      } catch (err) {
        console.warn('[useCronJobs] Failed to remove:', err)
        setError((err as Error).message)
      }
    },
    [api, fetchJobs],
  )

  const runJob = useCallback(
    async (id: string) => {
      try {
        await api.cron.run(id)
        await fetchJobs()
      } catch (err) {
        console.warn('[useCronJobs] Failed to run:', err)
        setError((err as Error).message)
      }
    },
    [api, fetchJobs],
  )

  const createJob = useCallback(
    async (input: CronJobInput) => {
      const resp = await api.cron.create(input)
      await fetchJobs()
      return resp.job
    },
    [api, fetchJobs],
  )

  const updateJob = useCallback(
    async (id: string, input: CronJobInput) => {
      const resp = await api.cron.update(id, input)
      await fetchJobs()
      return resp.job
    },
    [api, fetchJobs],
  )

  return {
    jobs,
    status,
    loading,
    error,
    refresh: fetchJobs,
    toggleEnabled,
    removeJob,
    runJob,
    createJob,
    updateJob,
  }
}
