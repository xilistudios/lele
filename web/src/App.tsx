import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
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
import { AgentFilesPage } from './components/pages/AgentFilesPage'
import { AgentsPage } from './components/pages/AgentsPage'
import { AuthPage } from './components/pages/AuthPage'
import { ChatHistoryPage } from './components/pages/ChatHistoryPage'
import { ChatPage } from './components/pages/ChatPage'
import { ProvidersPage } from './components/pages/ProvidersPage'
import { SettingsPage } from './components/pages/SettingsPage'
import { SkillsPage } from './components/pages/SkillsPage'
import { AppLogicProvider, useAppLogicContext } from './contexts/AppLogicContext'
import { AuthProvider, defaultApiUrlFromWindow, useAuthContext } from './contexts/AuthContext'
import { wsDebug } from './lib/debug'

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
        <div className="w-full max-w-md space-y-5 rounded-3xl border border-border bg-background-secondary p-6 shadow-2xl shadow-interaction-primary/10">
          <div className="flex items-center justify-center py-8">
            <div className="h-8 w-8 animate-spin rounded-full border-2 border-interaction-primary border-t-transparent" />
          </div>
          <p className="text-center text-text-secondary">Connecting...</p>
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
  const { sessions, currentSessionKey, parentSessionKey, onSelectSession, createSession } =
    useAppLogicContext()
  const targetSessionKey = child_chat_id ?? chat_id
  const derivedParentSessionKey = child_chat_id ? (parent_chat_id ?? null) : null
  const availableKeys = useMemo(() => new Set(sessions.map((s) => s.key)), [sessions])
  const creatingRef = useRef(false)

  useEffect(() => {
    if (chat_id === 'new') {
      if (creatingRef.current) return
      creatingRef.current = true
      wsDebug('[ChatRoute] Creating new session...')
      const sessionKey = createSession()
      wsDebug('[ChatRoute] createSession returned:', sessionKey)
      if (sessionKey) {
        wsDebug(`[ChatRoute] Navigating to /chat/${sessionKey}`)
        navigate(`/chat/${sessionKey}`, { replace: true })
        return
      }
      navigate('/', { replace: true })
      return
    }
    creatingRef.current = false

    if (!targetSessionKey) return

    if (currentSessionKey === targetSessionKey && parentSessionKey === derivedParentSessionKey) {
      return
    }

    const isNestedSubagent = Boolean(child_chat_id)
    const hasValidParent =
      !isNestedSubagent ||
      (derivedParentSessionKey ? availableKeys.has(derivedParentSessionKey) : false)
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
    chat_id,
    child_chat_id,
    derivedParentSessionKey,
    sessions,
    availableKeys,
    currentSessionKey,
    parentSessionKey,
    onSelectSession,
    navigate,
    createSession,
  ])

  useEffect(() => {
    // Skip when creating new session — first useEffect handles this
    if (chat_id === 'new') return

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
    chat_id,
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

// Settings route component (layout wrapper with shared state)
function SettingsRoute() {
  return <SettingsPage />
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
        <Route path="agents" element={<AgentsPage />} />
        <Route path="providers" element={<ProvidersPage />} />
        <Route path="skills" element={<SkillsPage />} />
        <Route path="chats" element={<ChatHistoryPage />} />
        <Route path="settings/:tab?" element={<SettingsRoute />} />
        <Route path="settings/agent/:agentId" element={<AgentFilesPage />} />
        <Route path="settings/agents" element={<Navigate to="/agents" replace />} />
        <Route path="settings/providers" element={<Navigate to="/providers" replace />} />
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
