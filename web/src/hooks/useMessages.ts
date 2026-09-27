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
import { buildChatHistoryQueryKey } from './useChatHistory'
import { useGroupState } from './useGroupState'
import { useProcessingSessions } from './useProcessingSessions'
import { useStreamQueues } from './useStreamQueues'

export { parseAttachmentsFromContent, parseSubagentSessionKey, toChatMessages }

type ClientEvent = { event: string; data: unknown }
type SendFn = (event: ClientCommand['event'], data: Record<string, unknown>) => void

/**
 * Throttle window (ms) for the sidebar session-list refresh: leading + trailing,
 * with at most ONE queued trailing call per window.
 *
 * WHY 5 s: the sidebar list is a coarse, low-stakes view — it does not need
 * sub-second consistency — and every refresh walks the WHOLE paginated
 * `GET /api/v1/chat/sessions/meta` loop (N/200 requests). The backend emits
 * `history.updated` once per outbound agent message (plus `message.ack` and
 * `message.complete` per turn), so a single turn with tools fires this trigger
 * several times in a row: that is what produced 5+ full paginated passes for
 * one turn.
 *
 * WHY trailing: a burst can create/rename a session in its LAST events, so pure
 * leading throttling (what this hook used to do, 300 ms) would drop them and
 * leave the sidebar stale until some unrelated later event. One trailing call
 * at the end of the window guarantees the FINAL state after the burst is
 * reflected while still collapsing the burst into at most two passes.
 */
const SESSION_REFRESH_THROTTLE_MS = 5_000

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
  /**
   * When the current throttle window opened (the leading refresh's timestamp);
   * null once the window is over. Anchoring the window here (instead of in the
   * timer) is what lets the trailing timer be armed LAZILY.
   */
  const sessionRefreshWindowStartRef = useRef<number | null>(null)
  /**
   * Pending trailing timer. It exists ONLY when a trigger was actually
   * swallowed inside an open window, so an idle tab holds no timer at all
   * (the leading edge arms none). Exactly one exists at a time, which is what
   * makes "one trailing per window" structural — and the leading edge also
   * DISARMS any trailing still pending from the previous window, so a late
   * timer can never fire a second refresh into a window it does not belong to.
   */
  const sessionRefreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  /**
   * Latest `onSessionUpdated`, read at CALL time by the throttle below: the
   * trailing refresh must invoke the callback of the current render, never a
   * closure captured when the window opened (same pattern as eventContextRef).
   */
  const onSessionUpdatedRef = useRef(onSessionUpdated)
  /** False after unmount: blocks a refresh scheduled by a late WS event. */
  const mountedRef = useRef(true)
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

  onSessionUpdatedRef.current = onSessionUpdated

  /**
   * Leading + trailing throttle for `onSessionUpdated` (session-list refresh).
   * See SESSION_REFRESH_THROTTLE_MS for the why.
   *
   * - First trigger of a window runs IMMEDIATELY (leading) so a brand-new chat
   *   shows up in the sidebar at once, and opens the window.
   * - Triggers inside the window do not run. The FIRST swallowed trigger arms
   *   the single trailing timer for the rest of the window; later ones reuse
   *   it, so a burst costs one trailing call, never one per event.
   * - The trailing timer is armed LAZILY: a lone leading call arms none, and
   *   the leading edge cancels any trailing still pending from the previous
   *   window, so an idle tab (or an idle hook, which lives as long as the app)
   *   holds nothing. The window itself is anchored on
   *   `sessionRefreshWindowStartRef` and simply expires.
   * - When the trailing fires the window is over (both refs go back to null),
   *   so the next trigger is leading again.
   */
  const debouncedSessionRefresh = useCallback(() => {
    if (!mountedRef.current) return

    const now = Date.now()
    const windowStart = sessionRefreshWindowStartRef.current

    if (windowStart === null || now - windowStart >= SESSION_REFRESH_THROTTLE_MS) {
      // Leading edge: refresh now and open the window. A trailing timer from
      // the PREVIOUS window may still be pending (it fires late when a long
      // task delays it, and this trigger beat it to the new window) — drop it:
      // this leading call already reflects state at least as fresh as the
      // trailing one would, and keeping it would fire two refreshes for one
      // window and reset the window anchor mid-flight.
      if (sessionRefreshTimerRef.current !== null) {
        clearTimeout(sessionRefreshTimerRef.current)
        sessionRefreshTimerRef.current = null
      }
      sessionRefreshWindowStartRef.current = now
      onSessionUpdatedRef.current?.()
      return
    }

    // Inside an open window: swallow this trigger, and owe exactly one
    // trailing refresh at the end of the window.
    if (sessionRefreshTimerRef.current === null) {
      sessionRefreshTimerRef.current = setTimeout(
        () => {
          sessionRefreshTimerRef.current = null
          sessionRefreshWindowStartRef.current = null
          // A timer scheduled before unmount must not refresh afterwards.
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

  /**
   * Snapshot of the HTTP history cache taken at send time. Used to decide when
   * an optimistic user bubble has been confirmed by the server.
   *
   * `userCount` is the legacy signal (see optimisticBaseCount). `lastUserId` is
   * the id of the newest confirmed user message in the cache — the anchor that
   * stays valid when the history window slides and the count saturates.
   */
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

      // Snapshot BOTH confirmation signals once, before the optimistic bubble
      // enters streaming state (the snapshot must not see its own message).
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

      wsSend('message', {
        content: normalizedContent,
        session_key: sessionKey,
        agent_id: agentId ?? undefined,
        attachments: attachments.length > 0 ? attachments : undefined,
      })
    },
    [wsSend, getHistorySendAnchor],
  )

  // ── Retry failed message ──────────────────────────────────────────────

  const retryMessage = useCallback(
    (failedMessage: ChatMessage) => {
      const sessionKey = failedMessage.sessionKey
      if (!sessionKey) return

      // Remove the failed message from streaming state
      setStreamingMessages((current) => current.filter((m) => m.id !== failedMessage.id))
      // Re-send
      const attachmentPaths = (failedMessage.attachments ?? [])
        .map((a) => a.path ?? '')
        .filter(Boolean)
      sendMessage(failedMessage.content, attachmentPaths, sessionKey, null)
    },
    [sendMessage],
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

  // Cleanup timers on unmount: neither the typing indicator nor a trailing
  // session refresh may fire after the hook is gone (and no setTimeout is left
  // hanging behind).
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
    // Exposed so processing-indicator safety nets (useAppLogic HTTP-poll
    // backstop) can finalize stale streaming flags without going through a
    // WebSocket event. Prefer the pure helpers in streamingOpsLocal.ts.
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
  }
}
