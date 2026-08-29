import {
  type MutableRefObject,
  type ReactNode,
  createContext,
  useContext,
  useMemo,
  useRef,
} from 'react'
import { useAppLogic as useAppLogicHook } from '../hooks/useAppLogic'
import { type SocketStatus, useSocket } from '../hooks/useSocket'
import type { AuthSession, ChatMode, ClientEvent } from '../lib/types'
import type { ClientCommand } from '../services/ws/events'
import { useAuthContext } from './AuthContext'

// Re-export types for convenience
export type { SocketStatus }
export type SendFn = (event: ClientCommand['event'], data: Record<string, unknown>) => void

type UseApp = ReturnType<typeof useAppLogicHook>

/**
 * COLD context — exposed to every consumer under the ProtectedLayout. Contains
 * app state and handlers that change rarely (sessions, agents, sidebar, model
 * selection, handlers). The `value` object is memoized so that cold-only
 * consumers (Sidebar, Settings, ChatHeader, composer, ...) do NOT re-render
 * when streaming hot state changes every tick.
 */
export type AppLogicContextValue = {
  // Connection & socket
  wsStatus: SocketStatus
  wsSend: SendFn
  wsClose: () => void

  // State from useAppLogic
  error: string | null
  agents: UseApp['agents']
  currentAgent: UseApp['currentAgent']
  diagnostics: UseApp['diagnostics']
  diagnosticsOpen: UseApp['diagnosticsOpen']
  sidebarOpen: UseApp['sidebarOpen']
  mobileSidebarOpen: UseApp['mobileSidebarOpen']
  chatMode: ChatMode
  onSelectMode: (mode: ChatMode) => void
  modelState: UseApp['modelState']
  thinkLevel: UseApp['thinkLevel']
  isProcessing: UseApp['isProcessing']
  processingSessions: UseApp['processingSessions']
  sessions: UseApp['sessions']
  currentSessionKey: UseApp['currentSessionKey']
  parentSessionKey: UseApp['parentSessionKey']
  approvalRequest: UseApp['approvalRequest']
  approvalResult: UseApp['approvalResult']
  pendingAttachments: UseApp['pendingAttachments']
  groups: UseApp['groups']
  groupsEnabled: boolean
  sendTyping: UseApp['sendTyping']

  // Handlers from useAppLogic
  handleEvent: UseApp['handleEvent']
  onSend: UseApp['onSend']
  onRetry: (message: import('../lib/types').ChatMessage) => void
  onApprove: UseApp['onApprove']
  onCancel: UseApp['onCancel']
  onSelectSession: UseApp['onSelectSession']
  onCreateSession: UseApp['onCreateSession']
  createSession: UseApp['createSession']
  onDeleteSession: UseApp['onDeleteSession']
  onClearSession: UseApp['onClearSession']
  onSelectAgent: UseApp['onSelectAgent']
  onSelectModel: UseApp['onSelectModel']
  onSelectThinkLevel: UseApp['onSelectThinkLevel']
  onUploadAttachments: UseApp['onUploadAttachments']
  onAttachmentsChange: UseApp['onAttachmentsChange']
  onLogout: UseApp['onLogout']
  onToggleDiagnostics: UseApp['onToggleDiagnostics']
  onToggleSidebar: UseApp['onToggleSidebar']
  onOpenMobileSidebar: UseApp['onOpenMobileSidebar']
  onCloseMobileSidebar: UseApp['onCloseMobileSidebar']

  // Pagination
  loadMore: UseApp['loadMore']
  hasMore: UseApp['hasMore']
  isLoadingMore: UseApp['isLoadingMore']

  // For event handler ref access
  eventHandlerRef: MutableRefObject<(event: ClientEvent) => void>
}

/**
 * HOT context — perched in a nested Provider so only the consumers that truly
 * need per-tick streaming data (MessageList, ChatPageContext) subscribe to it.
 * These fields change on every typewriter tick; keeping them out of the cold
 * value prevents a re-render of the whole tree on each tick.
 */
export type AppStreamingContextValue = {
  messages: UseApp['messages']
  toolStatus: UseApp['toolStatus']
  typingIndicator: UseApp['typingIndicator']
}

const AppLogicContext = createContext<AppLogicContextValue | null>(null)
const AppStreamingContext = createContext<AppStreamingContextValue | null>(null)

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

  const app = useAppLogicHook(api, token, clientId, wsStatus, wsSend, wsClose, (s) =>
    persistSession(s as AuthSession | null),
  )

  // Expose the event handler via ref
  eventHandlerRef.current = app.handleEvent

  // ── COLD value: memoized so cold-only consumers skip re-renders when the
  //    streaming hot state (messages/toolStatus/typingIndicator) updates.
  const value = useMemo<AppLogicContextValue>(
    () => ({
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
      sidebarOpen: app.sidebarOpen,
      mobileSidebarOpen: app.mobileSidebarOpen,
      chatMode: app.chatMode,
      onSelectMode: app.onSelectMode,
      modelState: app.modelState,
      thinkLevel: app.thinkLevel,
      isProcessing: app.isProcessing,
      processingSessions: app.processingSessions,
      sessions: app.sessions,
      currentSessionKey: app.currentSessionKey,
      parentSessionKey: app.parentSessionKey,
      approvalRequest: app.approvalRequest,
      approvalResult: app.approvalResult,
      pendingAttachments: app.pendingAttachments,
      groups: app.groups,
      groupsEnabled: app.groupsEnabled,
      sendTyping: app.sendTyping,

      // Handlers from useAppLogic
      handleEvent: app.handleEvent,
      onSend: app.onSend,
      onRetry: app.retryMessage,
      onApprove: app.onApprove,
      onCancel: app.onCancel,
      onSelectSession: app.onSelectSession,
      onCreateSession: app.onCreateSession,
      createSession: app.createSession,
      onDeleteSession: app.onDeleteSession,
      onClearSession: app.onClearSession,
      onSelectAgent: app.onSelectAgent,
      onSelectModel: app.onSelectModel,
      onSelectThinkLevel: app.onSelectThinkLevel,
      onUploadAttachments: app.onUploadAttachments,
      onAttachmentsChange: app.onAttachmentsChange,
      onLogout: app.onLogout,
      onToggleDiagnostics: app.onToggleDiagnostics,
      onToggleSidebar: app.onToggleSidebar,
      onOpenMobileSidebar: app.onOpenMobileSidebar,
      onCloseMobileSidebar: app.onCloseMobileSidebar,

      // Pagination
      loadMore: app.loadMore,
      hasMore: app.hasMore,
      isLoadingMore: app.isLoadingMore,

      // Ref for internal wiring
      eventHandlerRef,
    }),
    [
      wsStatus,
      wsSend,
      wsClose,
      app.error,
      app.agents,
      app.currentAgent,
      app.diagnostics,
      app.diagnosticsOpen,
      app.sidebarOpen,
      app.mobileSidebarOpen,
      app.chatMode,
      app.onSelectMode,
      app.modelState,
      app.thinkLevel,
      app.isProcessing,
      app.processingSessions,
      app.sessions,
      app.currentSessionKey,
      app.parentSessionKey,
      app.approvalRequest,
      app.approvalResult,
      app.pendingAttachments,
      app.groups,
      app.groupsEnabled,
      app.sendTyping,
      app.handleEvent,
      app.onSend,
      app.retryMessage,
      app.onApprove,
      app.onCancel,
      app.onSelectSession,
      app.onCreateSession,
      app.createSession,
      app.onDeleteSession,
      app.onClearSession,
      app.onSelectAgent,
      app.onSelectModel,
      app.onSelectThinkLevel,
      app.onUploadAttachments,
      app.onAttachmentsChange,
      app.onLogout,
      app.onToggleDiagnostics,
      app.onToggleSidebar,
      app.onOpenMobileSidebar,
      app.onCloseMobileSidebar,
      app.loadMore,
      app.hasMore,
      app.isLoadingMore,
    ],
  )

  // ── HOT value: streaming fields only. Its identity changes every tick, but
  //    only MessageList & ChatPageContext subscribe to it.
  const streamingValue: AppStreamingContextValue = {
    messages: app.messages,
    toolStatus: app.toolStatus,
    typingIndicator: app.typingIndicator,
  }

  return (
    <AppLogicContext.Provider value={value}>
      <AppStreamingContext.Provider value={streamingValue}>{children}</AppStreamingContext.Provider>
    </AppLogicContext.Provider>
  )
}

export function useAppLogicContext(): AppLogicContextValue {
  const context = useContext(AppLogicContext)
  if (!context) {
    throw new Error('useAppLogicContext must be used within an AppLogicProvider')
  }
  return context
}

export function useAppStreamingContext(): AppStreamingContextValue {
  const context = useContext(AppStreamingContext)
  if (!context) {
    throw new Error('useAppStreamingContext must be used within an AppLogicProvider')
  }
  return context
}
