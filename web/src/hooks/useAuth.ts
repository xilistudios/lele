import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { createApiClient } from '../lib/api'
import { PROACTIVE_REFRESH_INTERVAL_MS, shouldProactivelyRefresh } from '../lib/sessionTiming'
import { clearSession, loadApiUrl, loadSession, saveApiUrl, saveSession } from '../lib/storage'
import type { AuthSession } from '../lib/types'

export const defaultApiUrlFromWindow = () =>
  import.meta.env.VITE_LELE_API_URL ??
  `${window.location.protocol}//${window.location.hostname}:18793`

export function useAuth(defaultApiUrl: string) {
  const [apiUrl, setApiUrlState] = useState(() => loadApiUrl(defaultApiUrl))
  const [session, setSession] = useState<AuthSession | null>(() => loadSession())

  const setApiUrl = useCallback((nextApiUrl: string) => {
    setApiUrlState(nextApiUrl)
    saveApiUrl(nextApiUrl)
  }, [])

  // Drop the session from this React root only, leaving the shared
  // localStorage slot alone: it may hold another client's session.
  const detachSession = useCallback(() => {
    setSession(null)
  }, [])

  const persistSession = useCallback((nextSession: AuthSession | null) => {
    setSession(nextSession)
    if (nextSession) {
      saveSession(nextSession)
    } else {
      clearSession()
    }
  }, [])

  // Keep a mutable ref to the latest session so callbacks captured by the
  // api memo can read it without becoming memo dependencies.
  const sessionRef = useRef(session)
  useEffect(() => {
    sessionRef.current = session
  }, [session])

  // Stable api client: only recreated when apiUrl changes.
  const persistRef = useRef(persistSession)
  useEffect(() => {
    persistRef.current = persistSession
  }, [persistSession])

  const api = useMemo(() => {
    const client = createApiClient(apiUrl)

    client.setAuthFailureHandler((mayClearStorage) => {
      // Only a session of our own may be erased from shared storage; otherwise
      // this tab just disconnects.
      if (mayClearStorage) {
        persistRef.current(null)
      } else {
        detachSession()
      }
    })

    return client
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiUrl, detachSession])

  // Sync token separately so token changes don't recreate the client.
  useEffect(() => {
    if (session?.token && session.refresh_token) {
      api.setToken(
        session.token,
        session.refresh_token,
        (nextSession) => {
          const prev = sessionRef.current
          persistRef.current({
            ...prev,
            ...nextSession,
            client_id: prev?.client_id ?? '',
            device_name: prev?.device_name ?? '',
          })
        },
        session.client_id,
      )
    } else {
      api.clearToken()
    }
  }, [api, session])

  // Renew before the deadline instead of waiting for a 401 (see AuthContext for
  // the full rationale). Same helper, same timer discipline: only `api` is a
  // dependency, and the latest session is read through the ref.
  const proactiveTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  useEffect(() => {
    const renewIfDue = () => {
      if (!shouldProactivelyRefresh(sessionRef.current, Date.now())) return
      void api.refreshNow().catch(() => null)
    }

    renewIfDue()
    proactiveTimerRef.current = setInterval(renewIfDue, PROACTIVE_REFRESH_INTERVAL_MS)

    return () => {
      if (proactiveTimerRef.current !== null) {
        clearInterval(proactiveTimerRef.current)
        proactiveTimerRef.current = null
      }
    }
  }, [api])

  const handleAuth = useCallback(
    async (input: { apiUrl: string; pin: string; deviceName: string }) => {
      const nextApiUrl = input.apiUrl.trim()
      const nextApi = createApiClient(nextApiUrl)
      const sessionData = await nextApi.pair(input.pin, input.deviceName)
      const nextSession: AuthSession = {
        ...sessionData,
        device_name: input.deviceName.trim(),
      }

      setApiUrl(nextApiUrl)
      persistSession(nextSession)
      return nextSession
    },
    [persistSession, setApiUrl],
  )

  const ensureSession = useCallback(
    async (baseSession: AuthSession): Promise<AuthSession | null> => {
      try {
        const status = await api.status(baseSession.token)
        if (status.valid) {
          if (status.device_name && status.device_name !== baseSession.device_name) {
            const nextSession = { ...baseSession, device_name: status.device_name }
            persistSession(nextSession)
            return nextSession
          }

          return baseSession
        }
      } catch {
        // Fall through to refresh when the status probe fails.
      }

      try {
        const refreshed = await api.refresh(baseSession.refresh_token)
        const nextSession: AuthSession = {
          ...baseSession,
          ...refreshed,
          client_id: baseSession.client_id,
          device_name: baseSession.device_name,
        }
        persistSession(nextSession)
        return nextSession
      } catch {
        persistSession(null)
        return null
      }
    },
    [api, persistSession],
  )

  return {
    api,
    apiUrl,
    setApiUrl,
    session,
    setSession,
    persistSession,
    handleAuth,
    ensureSession,
  }
}
