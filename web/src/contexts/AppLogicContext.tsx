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
import type { StreamMetrics } from '../hooks/useStreamMetrics'
import type { AuthSession, ChatMode, ClientEvent } from '../lib/types'
import type { ClientCommand } from '../services/ws/events'
import { useAuthContext } from './AuthContext'

export type { SocketStatus }
export type SendFn = (event: ClientCommand['event'], data: Record<string, unknown>) => void

type UseApp = ReturnType<typeof useAppLogicHook>

export type AppLogicContextValue = {
  wsStatus: SocketStatus
  wsSend: SendFn
  wsClose: () => void

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
  sessionFolder: UseApp['sessionFolder']
  isProcessing: UseApp['isProcessing']
  processingSessions: UseApp['processingSessions']
  sessions: UseApp['sessions']
  currentSessionKey: UseApp['currentSessionKey']
  parentSessionKey: UseApp['parentSessionKey']
  approvalRequest: UseApp['approvalRequest']
  approvalResult: UseApp['approvalResult']
  pendingAttachments: UseApp['pendingAttachments']
  queuedMessages: UseApp['queuedMessages']
  groups: UseApp['groups']
  groupsEnabled: boolean
  sendTyping: UseApp['sendTyping']

  handleEvent: UseApp['handleEvent']
  onSend: UseApp['onSend']
  removeQueuedMessage: UseApp['removeQueuedMessage']
  sendNowQueuedMessage: UseApp['sendNowQueuedMessage']
  clearQueue: UseApp['clearQueue']
  queueCount: UseApp['queueCount']
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
  onSelectFolder: UseApp['onSelectFolder']
  onClearFolder: UseApp['onClearFolder']
  onUploadAttachments: UseApp['onUploadAttachments']
  onAttachmentsChange: UseApp['onAttachmentsChange']
  onLogout: UseApp['onLogout']
  onToggleDiagnostics: UseApp['onToggleDiagnostics']
  onToggleSidebar: UseApp['onToggleSidebar']
  onOpenMobileSidebar: UseApp['onOpenMobileSidebar']
  onCloseMobileSidebar: UseApp['onCloseMobileSidebar']

  loadMore: UseApp['loadMore']
  hasMore: UseApp['hasMore']
  isLoadingMore: UseApp['isLoadingMore']
  isHistoryLoading: UseApp['isHistoryLoading']

  eventHandlerRef: MutableRefObject<(event: ClientEvent) => void>
}

export type AppStreamingContextValue = {
  messages: UseApp['messages']
  toolStatus: UseApp['toolStatus']
  typingIndicator: UseApp['typingIndicator']
}

export const AppLogicContext = createContext<AppLogicContextValue | null>(null)
export const AppStreamingContext = createContext<AppStreamingContextValue | null>(null)

export const StreamMetricsContext = createContext<StreamMetrics | null>(null)

export function useStreamMetricsContext(): StreamMetrics | null {
  return useContext(StreamMetricsContext)
}

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

  eventHandlerRef.current = app.handleEvent

  const value = useMemo<AppLogicContextValue>(
    () => ({
      wsStatus,
      wsSend,
      wsClose,

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
      sessionFolder: app.sessionFolder,
      isProcessing: app.isProcessing,
      processingSessions: app.processingSessions,
      sessions: app.sessions,
      currentSessionKey: app.currentSessionKey,
      parentSessionKey: app.parentSessionKey,
      approvalRequest: app.approvalRequest,
      approvalResult: app.approvalResult,
      pendingAttachments: app.pendingAttachments,
      queuedMessages: app.queuedMessages,
      groups: app.groups,
      groupsEnabled: app.groupsEnabled,
      sendTyping: app.sendTyping,

      handleEvent: app.handleEvent,
      onSend: app.onSend,
      removeQueuedMessage: app.removeQueuedMessage,
      sendNowQueuedMessage: app.sendNowQueuedMessage,
      clearQueue: app.clearQueue,
      queueCount: app.queueCount,
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
      onSelectFolder: app.onSelectFolder,
      onClearFolder: app.onClearFolder,
      onUploadAttachments: app.onUploadAttachments,
      onAttachmentsChange: app.onAttachmentsChange,
      onLogout: app.onLogout,
      onToggleDiagnostics: app.onToggleDiagnostics,
      onToggleSidebar: app.onToggleSidebar,
      onOpenMobileSidebar: app.onOpenMobileSidebar,
      onCloseMobileSidebar: app.onCloseMobileSidebar,

      loadMore: app.loadMore,
      hasMore: app.hasMore,
      isLoadingMore: app.isLoadingMore,
      isHistoryLoading: app.isHistoryLoading,

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
      app.sessionFolder,
      app.isProcessing,
      app.processingSessions,
      app.sessions,
      app.currentSessionKey,
      app.parentSessionKey,
      app.approvalRequest,
      app.approvalResult,
      app.pendingAttachments,
      app.queuedMessages,
      app.groups,
      app.groupsEnabled,
      app.sendTyping,
      app.handleEvent,
      app.onSend,
      app.removeQueuedMessage,
      app.sendNowQueuedMessage,
      app.clearQueue,
      app.queueCount,
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
      app.onSelectFolder,
      app.onClearFolder,
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
      app.isHistoryLoading,
    ],
  )

  const streamingValue = useMemo<AppStreamingContextValue>(
    () => ({
      messages: app.messages,
      toolStatus: app.toolStatus,
      typingIndicator: app.typingIndicator,
    }),
    [app.messages, app.toolStatus, app.typingIndicator],
  )

  return (
    <AppLogicContext.Provider value={value}>
      <AppStreamingContext.Provider value={streamingValue}>
        <StreamMetricsContext.Provider value={app.streamMetrics}>
          {children}
        </StreamMetricsContext.Provider>
      </AppStreamingContext.Provider>
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
