import { useCallback, useEffect, useRef, useState } from 'react'
import { Navigate, Route, Routes, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { AuthPage } from './components/pages/AuthPage'
import { ChatPage } from './components/pages/ChatPage'
import { SettingsPage } from './components/pages/SettingsPage'
import { useAppLogic } from './hooks/useAppLogic'
import { useAuth, defaultApiUrlFromWindow } from './hooks/useAuth'
import { useSocket } from './hooks/useSocket'
import type { ClientEvent } from './lib/types'

const defaultApiUrl = defaultApiUrlFromWindow()

// Auth wrapper component to handle auto-pairing from URL params
function AuthRoute() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [autoAuthAttempted, setAutoAuthAttempted] = useState(false)
  const [autoAuthError, setAutoAuthError] = useState<string | null>(null)
  const { apiUrl, session, handleAuth } = useAuth(defaultApiUrl)
  const [authError, setAuthError] = useState<string | null>(null)
  const [isAutoAuthenticating, setIsAutoAuthenticating] = useState(false)

  const codeFromUrl = searchParams.get('code')
  const deviceName = 'My Desktop'

  // Auto-pair if code is provided and no session exists
  useEffect(() => {
    if (codeFromUrl && !session?.token && !autoAuthAttempted && !isAutoAuthenticating) {
      setIsAutoAuthenticating(true)
      setAutoAuthAttempted(true)

      const autoAuth = async () => {
        try {
          setAutoAuthError(null)
          await handleAuth({ apiUrl, pin: codeFromUrl, deviceName })
          // Navigate to home on success with replace to avoid back-button issues
          navigate('/', { replace: true })
        } catch (err) {
          setAutoAuthError((err as Error).message)
          setIsAutoAuthenticating(false)
        }
      }

      autoAuth()
    }
  }, [codeFromUrl, session?.token, autoAuthAttempted, isAutoAuthenticating, apiUrl, handleAuth, navigate])

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
  const { session } = useAuth(defaultApiUrl)

  if (!session?.token) {
    return <Navigate to="/pair" replace />
  }

  return <>{children}</>
}

// Chat route component
function ChatRoute() {
  const { chat_id } = useParams<{ chat_id?: string }>()
  const navigate = useNavigate()
  const { api, apiUrl, session, persistSession } = useAuth(defaultApiUrl)
  const token = session?.token ?? null
  const clientId = session?.client_id ?? null
  const deviceName = session?.device_name ?? ''
  const eventHandlerRef = useRef<(event: ClientEvent) => void>(() => {})

  const {
    status: wsStatus,
    send: wsSend,
    close: wsClose,
  } = useSocket(apiUrl, token, {
    onEvent: (event) => eventHandlerRef.current(event),
  })

  const app = useAppLogic(api, token, clientId, wsStatus, wsSend, wsClose, (s) => persistSession(s))

  eventHandlerRef.current = app.handleEvent

  // Sync URL param with session selection - only runs when chat_id changes
  useEffect(() => {
    if (!chat_id) return

    const availableKeys = new Set(app.sessions.map((s) => s.key))
    if (availableKeys.has(chat_id)) {
      // URL session exists, select it (only if different from current)
      if (app.currentSessionKey !== chat_id) {
        app.onSelectSession(chat_id)
      }
    } else if (app.sessions.length > 0) {
      // Chat ID doesn't exist, redirect to home
      navigate('/', { replace: true })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [chat_id]) // Only run when chat_id changes from URL

  // Sync session selection with URL - only runs when currentSessionKey changes
  useEffect(() => {
    if (!app.currentSessionKey) return

    const currentPath = chat_id ? `/chat/${chat_id}` : '/'
    const newPath = `/chat/${app.currentSessionKey}`

    if (currentPath !== newPath) {
      navigate(newPath, { replace: true })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [app.currentSessionKey]) // Only run when currentSessionKey changes

  const handleCreateSession = useCallback(() => {
    const newKey = app.onCreateSession()
    if (newKey !== null) {
      navigate(`/chat/${newKey}`, { replace: true })
    }
  }, [app, navigate])

  const handleDeleteSession = useCallback(
    async (key: string) => {
      await app.onDeleteSession(key)
      // Navigation is handled by useChatSessions internally
    },
    [app.onDeleteSession]
  )

  const handleClearSession = useCallback(() => {
    void app.onClearSession()
    // Stay on the same route, just clear the messages
  }, [app.onClearSession])

  const handleLogout = useCallback(() => {
    app.onLogout()
    navigate('/pair', { replace: true })
  }, [app.onLogout, navigate])

  return (
    <ChatPage
      apiUrl={apiUrl}
      deviceName={deviceName}
      wsStatus={wsStatus}
      sessions={app.sessions}
      agents={app.agents}
      currentSessionKey={app.currentSessionKey}
      currentAgent={app.currentAgent}
      diagnostics={app.diagnostics}
      diagnosticsOpen={app.diagnosticsOpen}
      error={app.error}
      modelState={app.modelState}
      messages={app.messages}
      approvalRequest={app.approvalRequest}
      pendingAttachments={app.pendingAttachments}
      toolStatus={app.toolStatus}
      isStreaming={app.isStreaming}
      onApprove={app.onApprove}
      onUploadAttachments={app.onUploadAttachments}
      onAttachmentsChange={app.onAttachmentsChange}
      onCancel={app.onCancel}
      onClearSession={handleClearSession}
      onCreateSession={handleCreateSession}
      onLogout={handleLogout}
      onSend={app.onSend}
      onSelectAgent={app.onSelectAgent}
      onSelectModel={app.onSelectModel}
      onSelectSession={app.onSelectSession}
      onDeleteSession={handleDeleteSession}
      onToggleDiagnostics={app.onToggleDiagnostics}
    />
  )
}

// Settings route component
function SettingsRoute() {
  const navigate = useNavigate()
  const { api, apiUrl, session, persistSession } = useAuth(defaultApiUrl)
  const token = session?.token ?? null
  const clientId = session?.client_id ?? null
  const eventHandlerRef = useRef<(event: ClientEvent) => void>(() => {})

  const {
    status: wsStatus,
    send: wsSend,
    close: wsClose,
  } = useSocket(apiUrl, token, {
    onEvent: (event) => eventHandlerRef.current(event),
  })

  const app = useAppLogic(api, token, clientId, wsStatus, wsSend, wsClose, (s) => persistSession(s))

  eventHandlerRef.current = app.handleEvent

  const handleLogout = useCallback(() => {
    app.onLogout()
    navigate('/pair', { replace: true })
  }, [app.onLogout, navigate])

  return <SettingsPage diagnostics={app.diagnostics} onLogout={handleLogout} />
}

function App() {
  const { session } = useAuth(defaultApiUrl)
  const navigate = useNavigate()

  // Redirect authenticated users away from /pair
  useEffect(() => {
    if (session?.token && window.location.pathname === '/pair') {
      navigate('/', { replace: true })
    }
  }, [session?.token, navigate])

  return (
    <Routes>
      {/* Public routes */}
      <Route path="/pair" element={<AuthRoute />} />

      {/* Protected routes */}
      <Route
        path="/"
        element={
          <ProtectedRoute>
            <ChatRoute />
          </ProtectedRoute>
        }
      />
      <Route
        path="/chat/:chat_id"
        element={
          <ProtectedRoute>
            <ChatRoute />
          </ProtectedRoute>
        }
      />
      <Route
        path="/settings"
        element={
          <ProtectedRoute>
            <SettingsRoute />
          </ProtectedRoute>
        }
      />

      {/* Fallback */}
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}

export default App
