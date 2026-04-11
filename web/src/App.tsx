import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Navigate,
  Outlet,
  Route,
  Routes,
  useLocation,
  useNavigate,
  useParams,
  useSearchParams,
} from 'react-router-dom'
import { AuthPage } from './components/pages/AuthPage'
import { ChatPage } from './components/pages/ChatPage'
import { SettingsPage } from './components/pages/SettingsPage'
import { AppLogicProvider, useAppLogicContext } from './contexts/AppLogicContext'
import { AuthProvider, defaultApiUrlFromWindow, useAuthContext } from './contexts/AuthContext'

const defaultApiUrl = defaultApiUrlFromWindow()

// Auth wrapper component to handle auto-pairing from URL params
function AuthRoute() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [autoAuthAttempted, setAutoAuthAttempted] = useState(false)
  const [autoAuthError, setAutoAuthError] = useState<string | null>(null)
  const { apiUrl, session, handleAuth, isLoading } = useAuthContext()
  const [authError, setAuthError] = useState<string | null>(null)
  const isAutoAuthenticating = isLoading && !autoAuthAttempted

  const codeFromUrl = searchParams.get('code')
  const deviceName = 'My Desktop'

  // Auto-pair if code is provided and no session exists
  useEffect(() => {
    if (codeFromUrl && !session?.token && !autoAuthAttempted && !isLoading) {
      setAutoAuthAttempted(true)

      const autoAuth = async () => {
        try {
          setAutoAuthError(null)
          await handleAuth({ apiUrl, pin: codeFromUrl, deviceName })
          // Navigate to home on success with replace to avoid back-button issues
          navigate('/', { replace: true })
        } catch (err) {
          setAutoAuthError((err as Error).message)
        }
      }

      autoAuth()
    }
  }, [codeFromUrl, session?.token, autoAuthAttempted, isLoading, apiUrl, handleAuth, navigate])

  const handleAuthSubmit = useCallback(
    async (input: { apiUrl: string; pin: string; deviceName: string }) => {
      try {
        setAuthError(null)
        await handleAuth(input)
        navigate('/', { replace: true })
      } catch (err) {
        setAuthError((err as Error).message)
      }
    },
    [handleAuth, navigate],
  )

  // Pre-fill PIN from URL if available
  const initialPin = codeFromUrl ?? ''

  if (isAutoAuthenticating && !autoAuthError) {
    // Show loading state during auto-auth
    return (
      <main className="flex min-h-screen items-center justify-center px-4 py-12">
        <div className="w-full max-w-md space-y-5 rounded-3xl border border-slate-800 bg-slate-900/80 p-6 shadow-2xl shadow-sky-950/30">
          <div className="flex items-center justify-center py-8">
            <div className="h-8 w-8 animate-spin rounded-full border-2 border-sky-500 border-t-transparent" />
          </div>
          <p className="text-center text-slate-300">Connecting...</p>
        </div>
      </main>
    )
  }

  return (
    <AuthPage
      apiUrl={apiUrl}
      error={authError ?? autoAuthError}
      initialPin={initialPin}
      onSubmit={handleAuthSubmit}
    />
  )
}

// Protected route wrapper
function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { session } = useAuthContext()

  if (!session?.token) {
    return <Navigate to="/pair" replace />
  }

  return <>{children}</>
}

// Chat route component
function ChatRoute() {
  const { chat_id, parent_chat_id, child_chat_id } = useParams<{
    chat_id?: string
    parent_chat_id?: string
    child_chat_id?: string
  }>()
  const navigate = useNavigate()
  const location = useLocation()
  const { sessions, currentSessionKey, parentSessionKey, onSelectSession } = useAppLogicContext()
  const targetSessionKey = child_chat_id ?? chat_id
  const derivedParentSessionKey = child_chat_id ? (parent_chat_id ?? null) : null
  const availableKeys = useMemo(() => new Set(sessions.map((s) => s.key)), [sessions])

  useEffect(() => {
    if (!targetSessionKey) return

    if (currentSessionKey === targetSessionKey && parentSessionKey === derivedParentSessionKey) {
      return
    }

    const isNestedSubagent = Boolean(child_chat_id)
    const hasValidParent = !isNestedSubagent || (derivedParentSessionKey ? availableKeys.has(derivedParentSessionKey) : false)
    const hasValidTarget = isNestedSubagent
      ? targetSessionKey.startsWith('subagent:')
      : !targetSessionKey.startsWith('subagent:') && availableKeys.has(targetSessionKey)

    if (hasValidParent && hasValidTarget) {
      void onSelectSession(targetSessionKey, { parentSessionKey: derivedParentSessionKey })
    } else if (sessions.length > 0) {
      navigate('/', { replace: true })
    }
  }, [
    targetSessionKey,
    child_chat_id,
    derivedParentSessionKey,
    sessions,
    availableKeys,
    currentSessionKey,
    parentSessionKey,
    onSelectSession,
    navigate,
  ])

  useEffect(() => {
    if (!currentSessionKey) return

    if (targetSessionKey && currentSessionKey !== targetSessionKey) {
      return
    }

    if (derivedParentSessionKey && parentSessionKey !== derivedParentSessionKey) {
      return
    }

    const newPath = parentSessionKey
      ? `/chat/${encodeURIComponent(parentSessionKey)}/subagent/${encodeURIComponent(currentSessionKey)}`
      : `/chat/${encodeURIComponent(currentSessionKey)}`

    if (location.pathname !== newPath) {
      navigate(newPath, { replace: true })
    }
  }, [
    currentSessionKey,
    parentSessionKey,
    targetSessionKey,
    derivedParentSessionKey,
    location.pathname,
    navigate,
  ])

  // Note: onCreateSession, onDeleteSession, onClearSession, and onLogout are handled
  // directly within the ChatPage components via context hooks

  return <ChatPage />
}

function ProtectedLayout() {
  return (
    <ProtectedRoute>
      <AppLogicProvider>
        <Outlet />
      </AppLogicProvider>
    </ProtectedRoute>
  )
}

// Settings route component
function SettingsRoute() {
  const navigate = useNavigate()
  const { onLogout } = useAppLogicContext()

  const handleLogout = useCallback(() => {
    onLogout()
    navigate('/pair', { replace: true })
  }, [onLogout, navigate])

  return <SettingsPage onLogout={handleLogout} />
}

function AppContent() {
  const { session } = useAuthContext()
  const navigate = useNavigate()
  const location = useLocation()

  // Redirect authenticated users away from /pair
  useEffect(() => {
    if (session?.token && location.pathname === '/pair') {
      navigate('/', { replace: true })
    }
  }, [location.pathname, session?.token, navigate])

  return (
    <Routes>
      {/* Public routes */}
      <Route path="/pair" element={<AuthRoute />} />

      {/* Protected routes */}
      <Route path="/" element={<ProtectedLayout />}>
        <Route index element={<ChatRoute />} />
        <Route path="chat/:chat_id" element={<ChatRoute />} />
        <Route path="chat/:parent_chat_id/subagent/:child_chat_id" element={<ChatRoute />} />
        <Route path="settings" element={<SettingsRoute />} />
      </Route>

      {/* Fallback */}
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}

function App() {
  return (
    <AuthProvider defaultApiUrl={defaultApiUrl}>
      <AppContent />
    </AuthProvider>
  )
}

export default App
