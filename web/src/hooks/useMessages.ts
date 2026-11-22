import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  createOptimisticUserId,
  createUserMessage,
  parseAttachmentsFromContent,
  parseSubagentSessionKey,
  toChatMessages,
} from '../lib/chatMessageBuilder'
import type { ChatMessage, ToolStatus } from '../lib/types'
import { installOrderingMonitorGlobals, noteWsEvent } from '../lib/orderingMonitor'
import type { ClientCommand } from '../services/ws/events'
import { type MessageEventContext, dispatchMessageEvent } from './messageEventHandlers'
import { useApprovals } from './useApprovals'
import { buildChatHistoryQueryKey } from './useChatHistory'
import { useGroupState } from './useGroupState'
import { useProcessingSessions } from './useProcessingSessions'
import { useStreamMetrics } from './useStreamMetrics'
import { useStreamQueues } from './useStreamQueues'

export { parseAttachmentsFromContent, parseSubagentSessionKey, toChatMessages }

type ClientEvent = { event: string; data: unknown }
type SendFn = (event: ClientCommand['event'], data: Record<string, unknown>) => void

const SESSION_REFRESH_THROTTLE_MS = 5_000

export function useMessages(
  wsSend: SendFn,
  _currentSessionKey: string | null,
  currentSessionKeyRef: React.MutableRefObject<string | null>,
  onSessionUpdated?: () => void,
  parentSessionKey?: string | null,
  onError?: (message: string) => void,
) {
  const [streamingMessages, setStreamingMessages] = useState<ChatMessage[]>([])
  const [toolStatus, setToolStatus] = useState<ToolStatus | null>(null)
  const [pendingAttachments, setPendingAttachments] = useState<string[]>([])
  const groupState = useGroupState()
  const [groupsEnabled, setGroupsEnabled] = useState(false)
  const [typingIndicator, setTypingIndicator] = useState<{
    deviceId: string
    deviceName: string
    timestamp: number
  } | null>(null)
  const streamingRef = useRef(streamingMessages)
  const sessionRefreshWindowStartRef = useRef<number | null>(null)
  const sessionRefreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const onSessionUpdatedRef = useRef(onSessionUpdated)
  const mountedRef = useRef(true)
  const typingTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const queryClient = useQueryClient()

  const parentSessionKeyRef = useRef<string | null>(null)
  parentSessionKeyRef.current = parentSessionKey ?? null

  const streamQueues = useStreamQueues(setStreamingMessages)
  const streamMetrics = useStreamMetrics()
  const approvals = useApprovals()
  const processing = useProcessingSessions()

  useEffect(() => {
    streamingRef.current = streamingMessages
  }, [streamingMessages])

  onSessionUpdatedRef.current = onSessionUpdated

  const debouncedSessionRefresh = useCallback(() => {
    if (!mountedRef.current) return

    const now = Date.now()
    const windowStart = sessionRefreshWindowStartRef.current

    if (windowStart === null || now - windowStart >= SESSION_REFRESH_THROTTLE_MS) {
      if (sessionRefreshTimerRef.current !== null) {
        clearTimeout(sessionRefreshTimerRef.current)
        sessionRefreshTimerRef.current = null
      }
      sessionRefreshWindowStartRef.current = now
      onSessionUpdatedRef.current?.()
      return
    }

    if (sessionRefreshTimerRef.current === null) {
      sessionRefreshTimerRef.current = setTimeout(
        () => {
          sessionRefreshTimerRef.current = null
          sessionRefreshWindowStartRef.current = null
          if (!mountedRef.current) return
          onSessionUpdatedRef.current?.()
        },
        SESSION_REFRESH_THROTTLE_MS - (now - windowStart),
      )
    }
  }, [])

  const setTypingWithTimeout = useCallback(
    (indicator: { deviceId: string; deviceName: string; timestamp: number } | null) => {
      if (typingTimeoutRef.current) {
        clearTimeout(typingTimeoutRef.current)
        typingTimeoutRef.current = null
      }
      setTypingIndicator(indicator)
      if (indicator) {
        typingTimeoutRef.current = setTimeout(() => {
          setTypingIndicator(null)
        }, 5000)
      }
    },
    [],
  )

  const sendTyping = useCallback(
    (sessionKey: string) => {
      wsSend('typing', { session_key: sessionKey })
    },
    [wsSend],
  )

  const getHistorySendAnchor = useCallback(
    (sessionKey: string): { userCount: number; lastUserId: string | undefined } => {
      const queryKey = buildChatHistoryQueryKey(
        sessionKey,
        parentSessionKeyRef.current ?? undefined,
      )
      const history = queryClient.getQueryData<{ messages?: ChatMessage[] }>(queryKey)
      const confirmedUsers = (history?.messages ?? []).filter(
        (m) => m.role === 'user' && !m.optimistic,
      )
      return {
        userCount: confirmedUsers.length,
        lastUserId:
          confirmedUsers.length > 0 ? confirmedUsers[confirmedUsers.length - 1].id : undefined,
      }
    },
    [queryClient],
  )

  const eventContextRef = useRef<MessageEventContext>({} as MessageEventContext)

  eventContextRef.current = {
    currentSessionKeyRef,
    parentSessionKeyRef,
    queryClient,
    debouncedSessionRefresh,
    setStreamingMessages,
    setToolStatus,
    setPendingAttachments,
    streamingMessagesRef: streamingRef,
    setError: (message: string) => onError?.(message),
    setApprovalRequest: approvals.setApprovalRequest as (req: unknown) => void,
    showApprovalResult: approvals.showResult,
    enqueueChunk: streamQueues.enqueueChunk,
    clearQueue: streamQueues.clearQueue,
    clearAllQueues: streamQueues.clearAllQueues,
    setStreamMetrics: streamMetrics.reportMetrics,
    clearStreamMetrics: streamMetrics.clearMetrics,
    ensureAssistantPlaceholder: streamQueues.ensureAssistantPlaceholder,
    addProcessingSession: processing.addSession,
    removeProcessingSession: processing.removeSession,
    syncProcessingSession: processing.syncSession,
    processingSessionKeyRef: processing.processingSessionKeyRef,
    upsertGroup: groupState.upsertGroup,
    hydrateGroups: groupState.hydrateGroups,
    markActiveGroupsStopped: groupState.markActiveGroupsStopped,
    setGroupsEnabled,
    setTypingIndicator: setTypingWithTimeout,
  }

  const handleEvent = useCallback((event: ClientEvent) => {
    noteWsEvent(event.event, event.data)
    dispatchMessageEvent(eventContextRef.current, event)
  }, [])

  const sendMessage = useCallback(
    async (
      content: string,
      attachments: string[],
      sessionKey: string,
      agentId: string | null,
    ): Promise<boolean> => {
      if (!sessionKey) return false

      const normalizedContent = content.trim()
      if (normalizedContent.length === 0 && attachments.length === 0) return false

      const anchor = getHistorySendAnchor(sessionKey)

      const userMessage = createUserMessage({
        id: createOptimisticUserId(),
        sessionKey,
        content: normalizedContent,
        optimistic: true,
        optimisticBaseCount: anchor.userCount,
        optimisticAnchorId: anchor.lastUserId,
        attachments: attachments.map((path) => ({
          path,
          name: path.split('/').pop() ?? path,
          kind: 'file' as const,
        })),
      })

      setStreamingMessages((current) => [...current, userMessage])
      setPendingAttachments([])
      streamMetrics.clearMetrics()

      wsSend('message', {
        content: normalizedContent,
        session_key: sessionKey,
        agent_id: agentId ?? undefined,
        attachments: attachments.length > 0 ? attachments : undefined,
      })
      return true
    },
    [wsSend, getHistorySendAnchor, streamMetrics.clearMetrics],
  )

  const retryMessage = useCallback(
    (failedMessage: ChatMessage) => {
      const sessionKey = failedMessage.sessionKey
      if (!sessionKey) return

      setStreamingMessages((current) => current.filter((m) => m.id !== failedMessage.id))
      const attachmentPaths = (failedMessage.attachments ?? [])
        .map((a) => a.path ?? '')
        .filter(Boolean)
      void sendMessage(failedMessage.content, attachmentPaths, sessionKey, null)
    },
    [sendMessage],
  )

  const clearStreaming = useCallback(() => {
    streamQueues.clearAllQueues()
    setStreamingMessages([])
    setToolStatus(null)
    approvals.clear()
    setPendingAttachments([])
    groupState.clearGroups()
    processing.processingSessionKeyRef.current = null
    streamMetrics.clearMetrics()
  }, [
    streamQueues.clearAllQueues,
    approvals,
    processing.processingSessionKeyRef,
    groupState.clearGroups,
    streamMetrics.clearMetrics,
  ])

  const clearAll = useCallback(() => {
    streamQueues.clearAllQueues()
    setStreamingMessages([])
    setToolStatus(null)
    approvals.clear()
    setPendingAttachments([])
    groupState.clearGroups()
    processing.clearAll()
    streamMetrics.clearMetrics()
  }, [
    streamQueues.clearAllQueues,
    approvals,
    processing,
    groupState.clearGroups,
    streamMetrics.clearMetrics,
  ])

  useEffect(() => {
    return () => streamQueues.clearAllQueues()
  }, [streamQueues.clearAllQueues])

  useEffect(() => {
    installOrderingMonitorGlobals()
  }, [])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      if (typingTimeoutRef.current) clearTimeout(typingTimeoutRef.current)
      if (sessionRefreshTimerRef.current !== null) {
        clearTimeout(sessionRefreshTimerRef.current)
        sessionRefreshTimerRef.current = null
      }
      sessionRefreshWindowStartRef.current = null
    }
  }, [])

  return {
    streamingMessages,
    setStreamingMessages,
    streamingRef,
    toolStatus,
    approvalRequest: approvals.approvalRequest,
    approvalResult: approvals.approvalResult,
    pendingAttachments,
    groups: groupState.groups,
    groupsEnabled,
    hydrateGroups: groupState.hydrateGroups,
    processingSessions: processing.processingSessions,
    setProcessingSessions: processing.setProcessingSessions,
    processingSessionKeyRef: processing.processingSessionKeyRef,
    ensureAssistantPlaceholder: streamQueues.ensureAssistantPlaceholder,
    sendMessage,
    retryMessage,
    handleEvent,
    approveRequest: approvals.approveRequest,
    setPendingAttachments,
    clearStreaming,
    clearAll,
    typingIndicator,
    sendTyping,
    streamMetrics: streamMetrics.metrics,
  }
}
