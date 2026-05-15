import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { MessageBubble } from '../MessageBubble'

const SCROLL_THRESHOLD = 100
const DEBOUNCE_MS = 150

export function MessageList() {
  const navigate = useNavigate()
  const { apiUrl } = useAuthContext()
  const {
    messages,
    approvalRequest,
    onApprove,
    currentSessionKey,
    loadMore,
    hasMore,
    isLoadingMore,
  } = useAppLogicContext()
  const containerRef = useRef<HTMLDivElement>(null)
  const sentinelRef = useRef<HTMLDivElement | null>(null)
  const [showSentinel, setShowSentinel] = useState(false)
  const debounceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const isLoadingMoreRef = useRef(false)
  const lastMessageCountRef = useRef(0)
  const shouldScrollToBottomRef = useRef(false)

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
    if (!container || !hasMore || isLoadingMoreRef.current) return

    // Check if user scrolled near the top
    if (container.scrollTop < SCROLL_THRESHOLD) {
      if (debounceTimerRef.current) {
        clearTimeout(debounceTimerRef.current)
      }

      debounceTimerRef.current = setTimeout(() => {
        // Insert sentinel at current scroll position before loading
        setShowSentinel(true)
        isLoadingMoreRef.current = true
        shouldScrollToBottomRef.current = false
        loadMore()
      }, DEBOUNCE_MS)
    }
  }, [hasMore, loadMore])

  // Restore scroll position after loading older messages using sentinel
  // biome-ignore lint/correctness/useExhaustiveDependencies: messages.length change triggers scroll restore
  useEffect(() => {
    if (!isLoadingMore && isLoadingMoreRef.current) {
      isLoadingMoreRef.current = false
      // Restore scroll using sentinel
      if (sentinelRef.current) {
        sentinelRef.current.scrollIntoView()
        setShowSentinel(false)
      }
    }
  }, [isLoadingMore, messages.length])

  // Track new messages and decide if we should scroll to bottom
  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    // Check if new messages were added (not from loading more)
    if (messages.length > lastMessageCountRef.current && !isLoadingMoreRef.current) {
      const lastMessage = messages[messages.length - 1]
      // Only scroll to bottom if it's a new user message or streaming message
      if (lastMessage && (lastMessage.role === 'user' || lastMessage.streaming)) {
        shouldScrollToBottomRef.current = true
      }
    }
    lastMessageCountRef.current = messages.length
  }, [messages])

  // Scroll to bottom when needed
  useEffect(() => {
    if (shouldScrollToBottomRef.current) {
      const container = containerRef.current
      if (container) {
        container.scrollTop = container.scrollHeight
      }
      shouldScrollToBottomRef.current = false
    }
  }, [])

  // Cleanup debounce timer
  useEffect(() => {
    return () => {
      if (debounceTimerRef.current) {
        clearTimeout(debounceTimerRef.current)
      }
    }
  }, [])

  // Reset refs when session changes
  // biome-ignore lint/correctness/useExhaustiveDependencies: refs are intentionally used for mutation
  useEffect(() => {
    isLoadingMoreRef.current = false
    lastMessageCountRef.current = 0
    shouldScrollToBottomRef.current = false
    setShowSentinel(false)
  }, [currentSessionKey])

  if (messages.length === 0) {
    return (
      <div className="flex h-full items-center justify-center">
        <p className="text-sm text-text-tertiary">Start a conversation</p>
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
      className="mx-auto max-w-3xl space-y-1 overflow-y-auto h-full"
    >
      {showSentinel && <div ref={sentinelRef} />}
      {isLoadingMore && (
        <div className="flex justify-center py-2">
          <div className="h-5 w-5 animate-spin rounded-full border-2 border-primary border-t-transparent" />
        </div>
      )}
      {!isLoadingMore && hasMore && (
        <p className="text-center py-2 text-xs text-text-tertiary">Scroll up for more</p>
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
      {approvalRequest && (
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
                Approve
              </button>
              <button
                type="button"
                onClick={() => onApprove(false)}
                className="rounded-md bg-state-error-light px-3 py-1.5 text-xs text-state-error hover:bg-state-error-light/80"
              >
                Reject
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
