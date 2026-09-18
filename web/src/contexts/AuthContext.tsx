import {
  type ReactNode,
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { bootstrapDesktopSession, createApiClient } from '../lib/api'
import { getDesktopApiUrl } from '../lib/desktop'
import { clearSession, loadApiUrl, loadSession, saveApiUrl, saveSession } from '../lib/storage'
import type { AuthSession } from '../lib/types'

export const defaultApiUrlFromWindow = () =>
  import.meta.env.VITE_LELE_API_URL ?? window.location.origin

export type AuthContextValue = {
  api: ReturnType<typeof createApiClient>
  apiUrl: string
  session: AuthSession | null
  setApiUrl: (url: string) => void
  persistSession: (session: AuthSession | null) => void
  handleAuth: (input: { apiUrl: string; pin: string; deviceName: string }) => Promise<AuthSession>
  ensureSession: (baseSession: AuthSession) => Promise<AuthSession | null>
  isLoading: boolean
}

// Exported for tests that need to mount a consumer without the full provider tree.
export const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({
  children,
  defaultApiUrl,
}: { children: ReactNode; defaultApiUrl: string }) {
  const [apiUrl, setApiUrlState] = useState(() => getDesktopApiUrl() ?? loadApiUrl(defaultApiUrl))
  // Restore a persisted session first; in the desktop shell, fall back to the
  // token injected by Tauri so the app starts authenticated without a PIN.
  const [session, setSession] = useState<AuthSession | null>(
    () => loadSession() ?? bootstrapDesktopSession(),
  )
  const [isLoading, setIsLoading] = useState(false)

  // Keep a mutable ref to the latest session so callbacks captured by the
  // api memo can read it without becoming memo dependencies.
  const sessionRef = useRef(session)
  useEffect(() => {
    sessionRef.current = session
  }, [session])

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

  // Stable api client: only recreated when apiUrl changes.
  // Auth callbacks read from sessionRef to avoid depending on `session`.
  const persistRef = useRef(persistSession)
  useEffect(() => {
    persistRef.current = persistSession
  }, [persistSession])

  const api = useMemo(() => {
    const client = createApiClient(apiUrl)

    client.setAuthFailureHandler(() => {
      persistRef.current(null)
    })

    return client
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiUrl])

  // Sync token separately so token changes don't recreate the client.
  useEffect(() => {
    if (session?.token) {
      api.setToken(session.token, session.refresh_token, (nextSession) => {
        const prev = sessionRef.current
        persistRef.current({
          ...prev,
          ...nextSession,
          client_id: prev?.client_id,
          device_name: prev?.device_name,
        })
      })
    } else {
      api.clearToken()
    }
  }, [api, session])

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
