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
import type { ClientCommand } from '../services/ws/events'
import { type MessageEventContext, dispatchMessageEvent } from './messageEventHandlers'
import { useApprovals } from './useApprovals'
import { chatHistoryQueryKey } from './useChatHistory'
import { useGroupState } from './useGroupState'
import { useProcessingSessions } from './useProcessingSessions'
import { useStreamQueues } from './useStreamQueues'

export { parseAttachmentsFromContent, parseSubagentSessionKey, toChatMessages }

type ClientEvent = { event: string; data: unknown }
type SendFn = (event: ClientCommand['event'], data: Record<string, unknown>) => void

export function useMessages(
  wsSend: SendFn,
  _currentSessionKey: string | null,
  currentSessionKeyRef: React.MutableRefObject<string | null>,
  onSessionUpdated?: () => void,
  parentSessionKey?: string | null,
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
  const lastSessionRefreshRef = useRef<number>(0)
  const typingTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const queryClient = useQueryClient()

  // ── Parent session key ref for subagent-aware cache operations ──────────
  const parentSessionKeyRef = useRef<string | null>(null)
  parentSessionKeyRef.current = parentSessionKey ?? null

  // ── Sub-hooks ────────────────────────────────────────────────────────────

  const streamQueues = useStreamQueues(setStreamingMessages)
  const approvals = useApprovals()
  const processing = useProcessingSessions()

  // ── Derived helpers ──────────────────────────────────────────────────────

  useEffect(() => {
    streamingRef.current = streamingMessages
  }, [streamingMessages])

  const debouncedSessionRefresh = useCallback(() => {
    const now = Date.now()
    if (now - lastSessionRefreshRef.current < 300) return
    lastSessionRefreshRef.current = now
    onSessionUpdated?.()
  }, [onSessionUpdated])

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

  const getHistoryUserCount = useCallback(
    (sessionKey: string) => {
      const history = queryClient.getQueryData<{ messages?: ChatMessage[] }>(
        chatHistoryQueryKey(sessionKey),
      )
      return history?.messages?.filter((m) => m.role === 'user').length ?? 0
    },
    [queryClient],
  )

  // ── Event handling ───────────────────────────────────────────────────────

  const eventContextRef = useRef<MessageEventContext>({} as MessageEventContext)

  // Keep context in sync with latest values (avoids stale closures in the
  // event handler while keeping a stable function reference for the WS layer).
  eventContextRef.current = {
    currentSessionKeyRef,
    parentSessionKeyRef,
    queryClient,
    debouncedSessionRefresh,
    setStreamingMessages,
    setToolStatus,
    setPendingAttachments,
    setApprovalRequest: approvals.setApprovalRequest as (req: unknown) => void,
    showApprovalResult: approvals.showResult,
    enqueueChunk: streamQueues.enqueueChunk,
    clearQueue: streamQueues.clearQueue,
    clearAllQueues: streamQueues.clearAllQueues,
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
    dispatchMessageEvent(eventContextRef.current, event)
  }, [])

  // ── Send message ─────────────────────────────────────────────────────────

  const sendMessage = useCallback(
    async (content: string, attachments: string[], sessionKey: string, agentId: string | null) => {
      if (!sessionKey) return

      const normalizedContent = content.trim()
      if (normalizedContent.length === 0) return

      const userMessage = createUserMessage({
        id: createOptimisticUserId(),
        sessionKey,
        content: normalizedContent,
        optimistic: true,
        optimisticBaseCount: getHistoryUserCount(sessionKey),
        attachments: attachments.map((path) => ({
          path,
          name: path.split('/').pop() ?? path,
          kind: 'file' as const,
        })),
      })

      setStreamingMessages((current) => [...current, userMessage])
      setPendingAttachments([])

      // Optimistic cache update — rollback happens on message.error
      const previousCache = queryClient.getQueryData<{ messages?: ChatMessage[] }>(
        chatHistoryQueryKey(sessionKey),
      )
      if (previousCache) {
        queryClient.setQueryData(chatHistoryQueryKey(sessionKey), {
          ...previousCache,
          messages: [...(previousCache.messages ?? []), userMessage],
        })
      } else {
        queryClient.setQueryData(chatHistoryQueryKey(sessionKey), {
          sessionKey,
          messages: [userMessage],
          rawMessages: [],
          processing: false,
        })
      }

      wsSend('message', {
        content: normalizedContent,
        session_key: sessionKey,
        agent_id: agentId ?? undefined,
        attachments: attachments.length > 0 ? attachments : undefined,
      })
    },
    [wsSend, getHistoryUserCount, queryClient],
  )

  // ── Retry failed message ──────────────────────────────────────────────

  const retryMessage = useCallback(
    (failedMessage: ChatMessage) => {
      const sessionKey = failedMessage.sessionKey
      if (!sessionKey) return

      // Remove the failed message from streaming state
      setStreamingMessages((current) => current.filter((m) => m.id !== failedMessage.id))
      // Remove from query cache too
      const cached = queryClient.getQueryData<{ messages?: ChatMessage[] }>(
        chatHistoryQueryKey(sessionKey),
      )
      if (cached) {
        queryClient.setQueryData(chatHistoryQueryKey(sessionKey), {
          ...cached,
          messages: (cached.messages ?? []).filter((m) => m.id !== failedMessage.id),
        })
      }
      // Re-send
      const attachmentPaths = (failedMessage.attachments ?? [])
        .map((a) => a.path ?? '')
        .filter(Boolean)
      sendMessage(failedMessage.content, attachmentPaths, sessionKey, null)
    },
    [queryClient, sendMessage],
  )

  // ── Cleanup helpers ──────────────────────────────────────────────────────

  const clearStreaming = useCallback(() => {
    streamQueues.clearAllQueues()
    setStreamingMessages([])
    setToolStatus(null)
    approvals.clear()
    setPendingAttachments([])
    groupState.clearGroups()
    processing.processingSessionKeyRef.current = null
  }, [
    streamQueues.clearAllQueues,
    approvals,
    processing.processingSessionKeyRef,
    groupState.clearGroups,
  ])

  const clearAll = useCallback(() => {
    streamQueues.clearAllQueues()
    setStreamingMessages([])
    setToolStatus(null)
    approvals.clear()
    setPendingAttachments([])
    groupState.clearGroups()
    processing.clearAll()
  }, [streamQueues.clearAllQueues, approvals, processing, groupState.clearGroups])

  // Cleanup on unmount
  useEffect(() => {
    return () => streamQueues.clearAllQueues()
  }, [streamQueues.clearAllQueues])

  // Cleanup typing timeout on unmount
  useEffect(() => {
    return () => {
      if (typingTimeoutRef.current) clearTimeout(typingTimeoutRef.current)
    }
  }, [])

  return {
    streamingMessages,
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
  }
}
