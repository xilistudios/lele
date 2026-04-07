import { createContext, useContext, useMemo, useCallback, useState, type ReactNode } from 'react'
import { createApiClient } from '../lib/api'
import { clearSession, loadApiUrl, loadSession, saveApiUrl, saveSession } from '../lib/storage'
import type { AuthSession } from '../lib/types'

export const defaultApiUrlFromWindow = () =>
  import.meta.env.VITE_LELE_API_URL ??
  `${window.location.protocol}//${window.location.hostname}:18793`

type AuthContextValue = {
  api: ReturnType<typeof createApiClient>
  apiUrl: string
  session: AuthSession | null
  setApiUrl: (url: string) => void
  persistSession: (session: AuthSession | null) => void
  handleAuth: (input: { apiUrl: string; pin: string; deviceName: string }) => Promise<AuthSession>
  ensureSession: (baseSession: AuthSession) => Promise<AuthSession | null>
  isLoading: boolean
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children, defaultApiUrl }: { children: ReactNode; defaultApiUrl: string }) {
  const [apiUrl, setApiUrlState] = useState(() => loadApiUrl(defaultApiUrl))
  const [session, setSession] = useState<AuthSession | null>(() => loadSession())
  const [isLoading, setIsLoading] = useState(false)

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
      setIsLoading(true)
      try {
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
      } finally {
        setIsLoading(false)
      }
    },
    [persistSession, setApiUrl]
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
    [api, persistSession]
  )

  const value: AuthContextValue = {
    api,
    apiUrl,
    session,
    setApiUrl,
    persistSession,
    handleAuth,
    ensureSession,
    isLoading,
  }

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuthContext(): AuthContextValue {
  const context = useContext(AuthContext)
  if (!context) {
    throw new Error('useAuthContext must be used within an AuthProvider')
  }
  return context
}
