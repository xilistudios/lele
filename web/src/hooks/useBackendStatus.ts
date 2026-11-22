import { useCallback, useEffect, useRef, useState } from 'react'
import { invokeDesktop } from '../lib/desktop'
import { usePageVisible } from './usePageVisible'

export type BackendStatus = {
  running: boolean
  pid: number
  port: number
  uptime_secs: number
  url: string
}

const POLL_INTERVAL_MS = 3000

export function useBackendStatus(enabled: boolean) {
  const [status, setStatus] = useState<BackendStatus | null>(null)
  const [restarting, setRestarting] = useState(false)
  const seenRunning = useRef(false)
  const pageVisible = usePageVisible()

  useEffect(() => {
    if (!enabled) {
      setStatus(null)
      seenRunning.current = false
      return
    }

    if (!pageVisible) return

    let cancelled = false

    const poll = async () => {
      const result = await invokeDesktop<BackendStatus>('backend_status')
      if (cancelled) return
      if (!result) return
      setStatus(result)
      if (result.running) {
        seenRunning.current = true
      }
    }

    void poll()

    const intervalId = setInterval(poll, POLL_INTERVAL_MS)

    return () => {
      cancelled = true
      clearInterval(intervalId)
    }
  }, [enabled, pageVisible])

  const restart = useCallback(async () => {
    if (!enabled) return
    setRestarting(true)
    try {
      await invokeDesktop('restart_backend')
      seenRunning.current = false
    } finally {
      setRestarting(false)
    }
  }, [enabled])

  const disconnected = seenRunning.current && status !== null && !status.running

  return { status, restart, restarting, disconnected }
}
