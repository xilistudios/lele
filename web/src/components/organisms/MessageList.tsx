import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { getModeTheme } from '../../lib/modeTheme'
import type { GroupTurn } from '../../lib/types'
import { MarkdownText } from '../molecules/MarkdownText'
import { MessageBubble } from './MessageBubble'

const SCROLL_THRESHOLD = 300
const DEBOUNCE_MS = 350

export function MessageList() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { apiUrl } = useAuthContext()
  const {
    messages,
    approvalRequest,
    approvalResult,
    onApprove,
    currentSessionKey,
    isProcessing,
    loadMore,
    hasMore,
    isLoadingMore,
    chatMode,
    groups,
  } = useAppLogicContext()
  const containerRef = useRef<HTMLDivElement>(null)
  const sentinelRef = useRef<HTMLDivElement | null>(null)
  const [showSentinel, setShowSentinel] = useState(false)
  const debounceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const scrollDebounceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const isLoadingMoreRef = useRef(false)
  const isScrollRestoringRef = useRef(false) // guard against scroll events during sentinel restoration
  const anchorMessageIdRef = useRef<string | null>(null) // message to scroll to after loadMore
  const lastMessageCountRef = useRef(0)
  const streamingRef = useRef(false) // track previous streaming state to detect end of stream
  const prevSessionKeyRef = useRef(currentSessionKey)

  const isNearBottom = useCallback(() => {
    const container = containerRef.current
    if (!container) return true
    return container.scrollHeight - container.scrollTop - container.clientHeight < SCROLL_THRESHOLD
  }, [])

  const scrollToBottomSmooth = useCallback(() => {
    const container = containerRef.current
    if (!container) return
    container.scrollTo({
      top: container.scrollHeight,
      behavior: 'smooth',
    })
  }, [])

  const debouncedScrollToBottomSmooth = useCallback(() => {
    if (scrollDebounceTimerRef.current) {
      clearTimeout(scrollDebounceTimerRef.current)
    }
    scrollDebounceTimerRef.current = setTimeout(() => {
      const container = containerRef.current
      if (!container) return
      container.scrollTo({
        top: container.scrollHeight,
        behavior: 'smooth',
      })
    }, DEBOUNCE_MS)
  }, [])

  const handleNavigateToSession = useCallback(
    (sessionKey: string) => {
      if (!currentSessionKey) return
      navigate(
        `/chat/${encodeURIComponent(currentSessionKey)}/subagent/${encodeURIComponent(sessionKey)}`,
      )
    },
    [currentSessionKey, navigate],
  )

  // Handle scroll to load more messages
  const handleScroll = useCallback(() => {
    const container = containerRef.current
    if (!container || !hasMore || isLoadingMoreRef.current || isScrollRestoringRef.current) return

    // Check if user scrolled near the top
    if (container.scrollTop < SCROLL_THRESHOLD) {
      if (debounceTimerRef.current) {
        clearTimeout(debounceTimerRef.current)
      }

      debounceTimerRef.current = setTimeout(() => {
        // Save the first visible message as scroll anchor before loading more
        const el = containerRef.current
        if (el) {
          const firstMsg = el.querySelector('[data-message-id]')
          if (firstMsg) {
            anchorMessageIdRef.current = firstMsg.getAttribute('data-message-id')
          }
        }
        // Insert sentinel at current scroll position before loading
        setShowSentinel(true)
        isLoadingMoreRef.current = true
        loadMore()
      }, DEBOUNCE_MS)
    }
  }, [hasMore, loadMore])

  // Restore scroll position after loading older messages:
  // scroll to the message that was first (oldest) before loadMore,
  // so the user sees where old and new messages join.
  // biome-ignore lint/correctness/useExhaustiveDependencies: messages.length change triggers scroll restore
  useEffect(() => {
    if (!isLoadingMore && isLoadingMoreRef.current) {
      // Set scroll-restoring guard BEFORE scrollIntoView to prevent
      // the resulting scroll event from triggering another loadMore.
      isScrollRestoringRef.current = true

      const anchorId = anchorMessageIdRef.current
      const anchorEl = anchorId
        ? containerRef.current?.querySelector(`[data-message-id="${anchorId}"]`)
        : null
      ;(anchorEl || sentinelRef.current)?.scrollIntoView()
      anchorMessageIdRef.current = null
      setShowSentinel(false)

      // Clear guards after scroll restoration is complete.
      // Use rAF to ensure the scroll event has been processed.
      window.requestAnimationFrame(() => {
        isScrollRestoringRef.current = false
        isLoadingMoreRef.current = false
      })
    }
  }, [isLoadingMore, messages.length])

  // Handle session changes, new messages, and streaming end in one effect.
  useEffect(() => {
    const container = containerRef.current
    if (!container) return
    if (isLoadingMoreRef.current) return

    const isNewSession = prevSessionKeyRef.current !== currentSessionKey
    prevSessionKeyRef.current = currentSessionKey

    // Reset per-session tracking when switching conversations
    if (isNewSession) {
      isLoadingMoreRef.current = false
      lastMessageCountRef.current = 0
      streamingRef.current = false
      setShowSentinel(false)
    }

    const lastMessage = messages[messages.length - 1]
    const isStreaming = !!lastMessage?.streaming
    const prevCount = lastMessageCountRef.current
    const prevStreaming = streamingRef.current

    streamingRef.current = isStreaming
    lastMessageCountRef.current = messages.length

    // After switching sessions, force scroll once messages are present
    if (isNewSession && messages.length > 0) {
      window.requestAnimationFrame(() => scrollToBottomSmooth())
      return
    }

    // Only scroll if user hasn't scrolled up to read history
    if (!isNearBottom()) return

    // During streaming: don't force scroll — let user scroll freely
    if (isStreaming) return

    // Streaming just ended OR new complete message: debounced smooth scroll
    if (prevStreaming || messages.length > prevCount) {
      debouncedScrollToBottomSmooth()
    }
  }, [
    messages,
    currentSessionKey,
    isNearBottom,
    debouncedScrollToBottomSmooth,
    scrollToBottomSmooth,
  ])

  // Cleanup timers
  useEffect(() => {
    return () => {
      if (debounceTimerRef.current) clearTimeout(debounceTimerRef.current)
      if (scrollDebounceTimerRef.current) clearTimeout(scrollDebounceTimerRef.current)
    }
  }, [])

  // Flatten all group turns sorted by turnIndex for inline rendering
  const allGroupTurns: GroupTurn[] = []
  const groupSyntheses: { groupID: string; synthesis: string; status: string }[] = []
  for (const group of groups.values()) {
    allGroupTurns.push(...group.turns)
    if (group.synthesis) {
      groupSyntheses.push({
        groupID: group.groupID,
        synthesis: group.synthesis,
        status: group.status,
      })
    }
  }
  allGroupTurns.sort((a, b) => a.turnIndex - b.turnIndex)
  const hasGroupContent = allGroupTurns.length > 0 || groupSyntheses.length > 0
  const hasActiveGroup = Array.from(groups.values()).some((g) => g.status === 'started')

  if (messages.length === 0 && !hasGroupContent) {
    const modeTheme = getModeTheme(chatMode)
    const EmptyIcon = modeTheme.Icon
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 px-4 text-center">
        <div
          className={`flex h-14 w-14 items-center justify-center rounded-2xl ${modeTheme.iconCircle}`}
        >
          <EmptyIcon size={26} />
        </div>
        <div className="space-y-1">
          <p className="text-sm font-medium text-text-primary">{t(modeTheme.labelKey)}</p>
          <p className="max-w-xs text-xs text-text-tertiary">{t(modeTheme.descKey)}</p>
        </div>
      </div>
    )
  }

  const visibleMessages = messages.filter(
    (m) => !m.content.startsWith('⚠️ GUIDANCE:') && !m.content.startsWith('GUIDANCE:'),
  )

  return (
    <div
      ref={containerRef}
      onScroll={handleScroll}
      className="mx-auto max-w-3xl space-y-1 overflow-y-auto h-full w-full"
    >
      {showSentinel && <div ref={sentinelRef} />}
      {isLoadingMore && (
        <div className="flex justify-center py-2">
          <div className="h-5 w-5 animate-spin rounded-full border-2 border-primary border-t-transparent" />
        </div>
      )}
      {!isLoadingMore && hasMore && (
        <p className="text-center py-2 text-xs text-text-tertiary">{t('chat.scrollUpForMore')}</p>
      )}
      {visibleMessages.map((message, index) => (
        <MessageBubble
          key={message.id}
          message={message}
          isLast={index === visibleMessages.length - 1}
          onNavigateToSession={handleNavigateToSession}
          apiUrl={apiUrl}
        />
      ))}
      {/* Group turns rendered inline */}
      {allGroupTurns.map((turn) => (
        <div key={`group-turn-${turn.groupID}-${turn.turnIndex}`} className="py-2">
          <div className="mb-1 flex items-center gap-2">
            <span className="text-sm font-semibold text-text-primary">{turn.label}</span>
            <span className="rounded px-1.5 py-0.5 text-[10px] font-medium bg-surface-hover text-text-tertiary">
              {turn.role}
            </span>
          </div>
          <div className="text-sm text-text-secondary">
            <MarkdownText content={turn.content} />
          </div>
        </div>
      ))}
      {/* Group synthesis */}
      {groupSyntheses.map((g) => (
        <div key={`group-synthesis-${g.groupID}`} className="py-2">
          <div className="mb-1 flex items-center gap-2">
            <span className="text-sm font-semibold text-text-primary">✨ Síntesis</span>
          </div>
          <div className="text-sm text-text-secondary">
            <MarkdownText content={g.synthesis} />
          </div>
        </div>
      ))}
      {approvalRequest && !approvalResult && (
        <div className="py-2">
          <div className="rounded-lg border border-border bg-background-primary p-4">
            <p className="text-sm font-medium text-text-secondary mb-2">
              {approvalRequest.command}
            </p>
            <p className="text-xs text-text-secondary mb-4">{approvalRequest.reason}</p>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => onApprove(true)}
                className="rounded-md bg-state-success-light px-3 py-1.5 text-xs text-state-success hover:bg-state-success-light/80"
              >
                {t('approval.approve')}
              </button>
              <button
                type="button"
                onClick={() => onApprove(false)}
                className="rounded-md bg-state-error-light px-3 py-1.5 text-xs text-state-error hover:bg-state-error-light/80"
              >
                {t('approval.reject')}
              </button>
            </div>
          </div>
        </div>
      )}
      {approvalResult && (
        <div className="py-2">
          <div
            className={`rounded-lg border p-4 ${
              approvalResult.approved
                ? 'border-state-success bg-state-success-light/10'
                : 'border-state-error bg-state-error-light/10'
            }`}
          >
            <div className="flex items-center gap-2">
              <span className="text-lg">{approvalResult.approved ? '✅' : '❌'}</span>
              <span
                className={`text-sm font-medium ${
                  approvalResult.approved ? 'text-state-success' : 'text-state-error'
                }`}
              >
                {approvalResult.approved
                  ? t('approval.commandApproved')
                  : t('approval.commandRejected')}
              </span>
            </div>
            {approvalResult.command && (
              <pre className="mt-2 text-xs text-text-secondary whitespace-pre-wrap break-all">
                {approvalResult.command}
              </pre>
            )}
          </div>
        </div>
      )}
      {/* Group execution loading indicator — shown when a group is actively running */}
      {hasActiveGroup && !messages.some((m) => m.streaming) && (
        <div className="flex items-center gap-2 py-3 text-text-tertiary text-sm">
          <span className="inline-block h-2 w-2 rounded-full bg-brand-naranja animate-pulse" />
          <span className="inline-block h-2 w-2 rounded-full bg-brand-naranja animate-pulse [animation-delay:0.2s]" />
          <span className="inline-block h-2 w-2 rounded-full bg-brand-naranja animate-pulse [animation-delay:0.4s]" />
          <span className="ml-1 text-xs">{t('groups.executing')}</span>
        </div>
      )}
      {/* Regular loading dots — hidden when a group is active to avoid duplication */}
      {isProcessing && !hasActiveGroup && !messages.some((m) => m.streaming) && (
        <div className="flex items-center gap-2 py-3 text-text-tertiary text-sm">
          <span className="inline-block h-2 w-2 rounded-full bg-text-tertiary animate-pulse" />
          <span className="inline-block h-2 w-2 rounded-full bg-text-tertiary animate-pulse [animation-delay:0.2s]" />
          <span className="inline-block h-2 w-2 rounded-full bg-text-tertiary animate-pulse [animation-delay:0.4s]" />
        </div>
      )}
    </div>
  )
}
