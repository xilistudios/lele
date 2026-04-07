import { useCallback, useEffect, useState } from 'react'
import { Navigate, Route, Routes, useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom'
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
    [handleAuth, navigate]
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
  const { chat_id } = useParams<{ chat_id?: string }>()
  const navigate = useNavigate()
  const { sessions, currentSessionKey, onSelectSession } = useAppLogicContext()

  // Sync URL param with session selection - only runs when chat_id changes
  useEffect(() => {
    if (!chat_id) return

    const availableKeys = new Set(sessions.map((s) => s.key))
    if (availableKeys.has(chat_id)) {
      // URL session exists, select it (only if different from current)
      if (currentSessionKey !== chat_id) {
        onSelectSession(chat_id)
      }
    } else if (sessions.length > 0) {
      // Chat ID doesn't exist, redirect to home
      navigate('/', { replace: true })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [chat_id])

  // Sync session selection with URL - only runs when currentSessionKey changes
  useEffect(() => {
    if (!currentSessionKey) return

    const currentPath = chat_id ? `/chat/${chat_id}` : '/'
    const newPath = `/chat/${currentSessionKey}`

    if (currentPath !== newPath) {
      navigate(newPath, { replace: true })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentSessionKey])

  // Note: onCreateSession, onDeleteSession, onClearSession, and onLogout are handled
  // directly within the ChatPage components via context hooks

  return <ChatPage />
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
      <Route
        path="/"
        element={
          <ProtectedRoute>
            <AppLogicProvider>
              <ChatRoute />
            </AppLogicProvider>
          </ProtectedRoute>
        }
      />
      <Route
        path="/chat/:chat_id"
        element={
          <ProtectedRoute>
            <AppLogicProvider>
              <ChatRoute />
            </AppLogicProvider>
          </ProtectedRoute>
        }
      />
      <Route
        path="/settings"
        element={
          <ProtectedRoute>
            <AppLogicProvider>
              <SettingsRoute />
            </AppLogicProvider>
          </ProtectedRoute>
        }
      />

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
