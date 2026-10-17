/**
 * Shared types for message event handlers.
 *
 * Contains the `ClientEvent` envelope type and the `MessageEventContext`
 * that every handler function receives.
 */
import type { QueryClient } from '@tanstack/react-query'
import type { ChatMessage, GroupInfo, ToolStatus } from '../../lib/types'
import type { StreamMetrics } from '../useStreamMetrics'

export type ClientEvent = { event: string; data: unknown }

export type MessageEventContext = {
  currentSessionKeyRef: React.MutableRefObject<string | null>
  parentSessionKeyRef: React.MutableRefObject<string | null>
  queryClient: QueryClient
  debouncedSessionRefresh: () => void
  setStreamingMessages: React.Dispatch<React.SetStateAction<ChatMessage[]>>
  setToolStatus: React.Dispatch<React.SetStateAction<ToolStatus | null>>
  setPendingAttachments: React.Dispatch<React.SetStateAction<string[]>>
  /**
   * Surface a user-visible failure in the chat's error banner
   * (`useAppLogic.error` -> `ChatPage.tsx` `ErrorBanner`). Used by the handler
   * for the server's `error` event, so a refused send is never silent.
   */
  setError: (message: string) => void
  setApprovalRequest: (req: unknown) => void
  showApprovalResult: (requestId: string, approved: boolean, command: string) => void
  enqueueChunk: (messageId: string, sessionKey: string, chunk: string, done: boolean) => void
  clearQueue: (messageId: string) => void
  clearAllQueues: () => void
  /** Store the latest `message.stream` metrics (token speed) for the UI. */
  setStreamMetrics: (metrics: StreamMetrics) => void
  /** Drop the current metrics (welcome/reconnect: the backend re-emits none). */
  clearStreamMetrics: () => void
  ensureAssistantPlaceholder: (
    messageId: string,
    sessionKey: string,
    chunk?: string,
    isDone?: boolean,
  ) => void
  addProcessingSession: (sessionKey: string) => void
  removeProcessingSession: (sessionKey: string) => void
  syncProcessingSession: (sessionKey: string, processing: boolean) => void
  processingSessionKeyRef: React.MutableRefObject<string | null>
  upsertGroup: (groupId: string, updater: (existing: GroupInfo | undefined) => GroupInfo) => void
  hydrateGroups: (infos: GroupInfo[]) => void
  markActiveGroupsStopped: () => void
  setGroupsEnabled: (enabled: boolean) => void
  setTypingIndicator: (
    indicator: { deviceId: string; deviceName: string; timestamp: number } | null,
  ) => void
}
