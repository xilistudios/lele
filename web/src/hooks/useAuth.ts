import { useCallback, useMemo, useState } from 'react'
import { createApiClient } from '../lib/api'
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

  const persistSession = useCallback((nextSession: AuthSession | null) => {
    setSession(nextSession)
    if (nextSession) {
      saveSession(nextSession)
    } else {
      clearSession()
    }
  }, [])

  const api = useMemo(() => {
    const client = createApiClient(apiUrl)

    if (session?.token && session.refresh_token) {
      client.setToken(session.token, session.refresh_token, (nextSession) => {
        persistSession({
          ...session,
          ...nextSession,
          client_id: session.client_id,
          device_name: session.device_name,
        })
      })
    }

    return client
  }, [apiUrl, persistSession, session])

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
