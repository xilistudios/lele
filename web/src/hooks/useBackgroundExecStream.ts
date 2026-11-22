import { useCallback, useEffect, useRef, useState } from 'react'
import { useAuthContext } from '../contexts/AuthContext'

export function useBackgroundExecStream(processId: string | null) {
  const { session, apiUrl } = useAuthContext()
  const [output, setOutput] = useState('')
  const [status, setStatus] = useState('')
  const [elapsedMs, setElapsedMs] = useState(0)
  const [done, setDone] = useState(false)
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)

  useEffect(() => {
    if (!processId || !session?.token) {
      setOutput('')
      setStatus('')
      setElapsedMs(0)
      setDone(false)
      return
    }

    let cancelled = false

    const fetchInitial = async () => {
      try {
        const resp = await fetch(
          `${apiUrl}/api/v1/background-exec/${encodeURIComponent(processId)}/output?tail=10000`,
          { headers: { Authorization: `Bearer ${session.token}` } },
        )
        if (resp.ok) {
          const data = await resp.json()
          if (!cancelled) {
            setOutput(data.output || '')
            setStatus(data.status || '')
            setElapsedMs(data.elapsed_ms || 0)
            if (data.status !== 'running') {
              setDone(true)
            }
          }
        }
      } catch (err) {
        console.warn('[useBackgroundExecStream] Initial fetch failed:', err)
      }
    }
    fetchInitial()

    pollRef.current = setInterval(async () => {
      if (cancelled) return
      try {
        const resp = await fetch(
          `${apiUrl}/api/v1/background-exec/${encodeURIComponent(processId)}/output?tail=10000`,
          { headers: { Authorization: `Bearer ${session.token}` } },
        )
        if (!resp.ok) return
        const data = await resp.json()
        if (cancelled) return
        setOutput(data.output || '')
        setStatus(data.status || '')
        setElapsedMs(data.elapsed_ms || 0)
        if (data.status && data.status !== 'running') {
          setDone(true)
          if (pollRef.current) {
            clearInterval(pollRef.current)
            pollRef.current = null
          }
        }
      } catch {}
    }, 1000)

    return () => {
      cancelled = true
      if (pollRef.current) {
        clearInterval(pollRef.current)
        pollRef.current = null
      }
    }
  }, [processId, session?.token, apiUrl])

  const reset = useCallback(() => {
    setOutput('')
    setStatus('')
    setElapsedMs(0)
    setDone(false)
  }, [])

  return { output, status, elapsedMs, done, reset }
}
