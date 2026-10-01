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

  // Drop the session from THIS React root only, leaving the shared
  // localStorage slot alone. Required when the credential that failed is not
  // ours: the stored session belongs to another client and must survive.
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

  // Stable api client: only recreated when apiUrl changes.
  // Auth callbacks read from sessionRef to avoid depending on `session`.
  const persistRef = useRef(persistSession)
  useEffect(() => {
    persistRef.current = persistSession
  }, [persistSession])

  // biome-ignore lint/correctness/useExhaustiveDependencies: the seed reads this render's session on purpose -- deps stay [apiUrl, detachSession] so the client keeps its identity
  const api = useMemo(() => {
    // React flushes CHILD passive effects before PARENT ones, so the effect
    // below that calls `setToken` runs after every descendant's boot effect
    // has already fired its requests. Seeding THIS RENDER's session closes
    // that window (the ref still holds the previous commit's session when
    // `apiUrl` and the session change in one batch), so the first wave
    // leaves authenticated. Session CHANGES still arrive through the effect below.
    const current = session
    const client = createApiClient(
      apiUrl,
      current?.token
        ? { token: current.token, refreshToken: current.refresh_token, clientId: current.client_id }
        : undefined,
    )

    client.setAuthFailureHandler((mayClearStorage) => {
      // The client only reports that its own credential died. Whether that
      // entitles us to erase the browser-wide session is a separate question:
      // when localStorage holds another client's session, wiping it would log
      // a device out that never failed here.
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

  // Renew before the deadline, instead of only after a request was refused.
  //
  // The credentials live for weeks and the server only pushes their deadline
  // forward on a refresh, while the 401 path cannot fire before a request is
  // already rejected. A browser left idle for longer than the session's life
  // therefore came back to an expired refresh token, a client the server had
  // just deleted, and a forced logout -- the 'My Desktop' session paired on the
  // 5th and expired on the 4th, without a single refresh. Checking the stored
  // deadline on mount (and once a day after that) renews such a session while
  // it is still renewable.
  //
  // The timer is kept in a ref and the effect only depends on `api`, so a
  // session update does not restart the interval; the check reads the latest
  // session through sessionRef. A renewal that succeeds persists through the
  // client's own `onTokenRefresh` callback above -- nothing extra to write
  // here -- and one that fails needs no handling: a transient failure has
  // already backed the client off, and a dead credential still surfaces as the
  // natural 401.
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
