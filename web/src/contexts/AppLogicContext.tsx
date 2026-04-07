import { createContext, useContext, useRef, type ReactNode, type MutableRefObject } from 'react'
import { useSocket, type SocketStatus } from '../hooks/useSocket'
import { useAppLogic as useAppLogicHook } from '../hooks/useAppLogic'
import type { ClientEvent, AuthSession } from '../lib/types'
import { useAuthContext } from './AuthContext'

// Re-export types for convenience
export type { SocketStatus }
export type SendFn = (event: string, data: Record<string, unknown>) => void

export type AppLogicContextValue = {
  // Connection & socket
  wsStatus: SocketStatus
  wsSend: SendFn
  wsClose: () => void

  // State from useAppLogic
  error: string | null
  agents: ReturnType<typeof useAppLogicHook>['agents']
  currentAgent: ReturnType<typeof useAppLogicHook>['currentAgent']
  diagnostics: ReturnType<typeof useAppLogicHook>['diagnostics']
  diagnosticsOpen: ReturnType<typeof useAppLogicHook>['diagnosticsOpen']
  modelState: ReturnType<typeof useAppLogicHook>['modelState']
  isStreaming: ReturnType<typeof useAppLogicHook>['isStreaming']
  sessions: ReturnType<typeof useAppLogicHook>['sessions']
  currentSessionKey: ReturnType<typeof useAppLogicHook>['currentSessionKey']
  messages: ReturnType<typeof useAppLogicHook>['messages']
  approvalRequest: ReturnType<typeof useAppLogicHook>['approvalRequest']
  pendingAttachments: ReturnType<typeof useAppLogicHook>['pendingAttachments']
  toolStatus: ReturnType<typeof useAppLogicHook>['toolStatus']

  // Handlers from useAppLogic
  handleEvent: ReturnType<typeof useAppLogicHook>['handleEvent']
  onSend: ReturnType<typeof useAppLogicHook>['onSend']
  onApprove: ReturnType<typeof useAppLogicHook>['onApprove']
  onCancel: ReturnType<typeof useAppLogicHook>['onCancel']
  onSelectSession: ReturnType<typeof useAppLogicHook>['onSelectSession']
  onCreateSession: ReturnType<typeof useAppLogicHook>['onCreateSession']
  onDeleteSession: ReturnType<typeof useAppLogicHook>['onDeleteSession']
  onClearSession: ReturnType<typeof useAppLogicHook>['onClearSession']
  onSelectAgent: ReturnType<typeof useAppLogicHook>['onSelectAgent']
  onSelectModel: ReturnType<typeof useAppLogicHook>['onSelectModel']
  onUploadAttachments: ReturnType<typeof useAppLogicHook>['onUploadAttachments']
  onAttachmentsChange: ReturnType<typeof useAppLogicHook>['onAttachmentsChange']
  onLogout: ReturnType<typeof useAppLogicHook>['onLogout']
  onToggleDiagnostics: ReturnType<typeof useAppLogicHook>['onToggleDiagnostics']

  // For event handler ref access
  eventHandlerRef: MutableRefObject<(event: ClientEvent) => void>
}

const AppLogicContext = createContext<AppLogicContextValue | null>(null)

export function AppLogicProvider({ children }: { children: ReactNode }) {
  const { api, apiUrl, session, persistSession } = useAuthContext()

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

  const app = useAppLogicHook(api, token, clientId, wsStatus, wsSend, wsClose, (s) => persistSession(s as AuthSession | null))

  // Expose the event handler via ref
  eventHandlerRef.current = app.handleEvent

  const value: AppLogicContextValue = {
    // Connection & socket
    wsStatus,
    wsSend,
    wsClose,

    // State from useAppLogic
    error: app.error,
    agents: app.agents,
    currentAgent: app.currentAgent,
    diagnostics: app.diagnostics,
    diagnosticsOpen: app.diagnosticsOpen,
    modelState: app.modelState,
    isStreaming: app.isStreaming,
    sessions: app.sessions,
    currentSessionKey: app.currentSessionKey,
    messages: app.messages,
    approvalRequest: app.approvalRequest,
    pendingAttachments: app.pendingAttachments,
    toolStatus: app.toolStatus,

    // Handlers from useAppLogic
    handleEvent: app.handleEvent,
    onSend: app.onSend,
    onApprove: app.onApprove,
    onCancel: app.onCancel,
    onSelectSession: app.onSelectSession,
    onCreateSession: app.onCreateSession,
    onDeleteSession: app.onDeleteSession,
    onClearSession: app.onClearSession,
    onSelectAgent: app.onSelectAgent,
    onSelectModel: app.onSelectModel,
    onUploadAttachments: app.onUploadAttachments,
    onAttachmentsChange: app.onAttachmentsChange,
    onLogout: app.onLogout,
    onToggleDiagnostics: app.onToggleDiagnostics,

    // Ref for internal wiring
    eventHandlerRef,
  }

  return <AppLogicContext.Provider value={value}>{children}</AppLogicContext.Provider>
}

export function useAppLogicContext(): AppLogicContextValue {
  const context = useContext(AppLogicContext)
  if (!context) {
    throw new Error('useAppLogicContext must be used within an AppLogicProvider')
  }
  return context
}
