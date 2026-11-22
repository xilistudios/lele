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
import { PROACTIVE_REFRESH_INTERVAL_MS, shouldProactivelyRefresh } from '../lib/sessionTiming'
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

export const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({
  children,
  defaultApiUrl,
}: { children: ReactNode; defaultApiUrl: string }) {
  const [apiUrl, setApiUrlState] = useState(() => getDesktopApiUrl() ?? loadApiUrl(defaultApiUrl))
  const [session, setSession] = useState<AuthSession | null>(
    () => loadSession() ?? bootstrapDesktopSession(),
  )
  const [isLoading, setIsLoading] = useState(false)

  const sessionRef = useRef(session)
  useEffect(() => {
    sessionRef.current = session
  }, [session])

  const setApiUrl = useCallback((nextApiUrl: string) => {
    setApiUrlState(nextApiUrl)
    saveApiUrl(nextApiUrl)
  }, [])

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

  const persistRef = useRef(persistSession)
  useEffect(() => {
    persistRef.current = persistSession
  }, [persistSession])

  // biome-ignore lint/correctness/useExhaustiveDependencies: the seed reads this render's session on purpose -- deps stay [apiUrl, detachSession] so the client keeps its identity
  const api = useMemo(() => {
    const current = session
    const client = createApiClient(
      apiUrl,
      current?.token
        ? { token: current.token, refreshToken: current.refresh_token, clientId: current.client_id }
        : undefined,
    )

    client.setAuthFailureHandler((mayClearStorage) => {
      if (mayClearStorage) {
        persistRef.current(null)
      } else {
        detachSession()
      }
    })

    return client
  }, [apiUrl, detachSession])

  useEffect(() => {
    if (session?.token) {
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
      } catch {}

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
