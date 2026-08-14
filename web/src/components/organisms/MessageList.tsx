import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { getModeTheme } from '../../lib/modeTheme'
import type { ChatMessage, GroupInfo, GroupToolCall, GroupTurn } from '../../lib/types'
import { MarkdownText } from '../molecules/MarkdownText'
import { ToolCallDisplay } from '../molecules/ToolCallDisplay'
import { MessageBubble } from './MessageBubble'

/** Collapsible tool-call item rendered under a group turn. */
function GroupToolCallItem({ tc }: { tc: GroupToolCall }) {
  const [expanded, setExpanded] = useState(false)
  return (
    <div className="mt-1">
      <ToolCallDisplay
        toolName={tc.tool}
        toolArgs={tc.arguments}
        toolResult={tc.result}
        toolStatus={tc.status}
        expanded={expanded}
        onToggleExpand={() => setExpanded((e) => !e)}
      />
    </div>
  )
}

type RenderItem =
  | { type: 'message'; message: ChatMessage; index: number }
  | { type: 'group'; group: GroupInfo }

const START_INDEX = 10000

export function MessageList() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { apiUrl, session } = useAuthContext()
  const {
    messages,
    approvalRequest,
    approvalResult,
    onApprove,
    onRetry,
    currentSessionKey,
    isProcessing,
    loadMore,
    hasMore,
    isLoadingMore,
    chatMode,
    groups,
    typingIndicator,
  } = useAppLogicContext()

  const virtuosoRef = useRef<VirtuosoHandle>(null)
  const [firstItemIndex, setFirstItemIndex] = useState(START_INDEX)
  const [atBottom, setAtBottom] = useState(true)
  const prevSessionKeyRef = useRef(currentSessionKey)

  const visibleMessages = messages.filter(
    (m) => !m.content.startsWith('⚠️ GUIDANCE:') && !m.content.startsWith('GUIDANCE:'),
  )

  const prevMessagesLengthRef = useRef(visibleMessages.length)

  // Handle session change — reset tracking
  useEffect(() => {
    if (prevSessionKeyRef.current !== currentSessionKey) {
      prevSessionKeyRef.current = currentSessionKey
      setFirstItemIndex(START_INDEX)
      prevMessagesLengthRef.current = visibleMessages.length
      setAtBottom(true)
    }
  }, [currentSessionKey, visibleMessages.length])

  // When older messages are prepended (loadMore), shift firstItemIndex backward
  // so Virtuoso preserves scroll position without jumping or blanking the viewport.
  useEffect(() => {
    if (
      prevMessagesLengthRef.current > 0 &&
      visibleMessages.length > prevMessagesLengthRef.current
    ) {
      const added = visibleMessages.length - prevMessagesLengthRef.current
      if (isLoadingMore) {
        setFirstItemIndex((prev) => prev - added)
      }
    }
    prevMessagesLengthRef.current = visibleMessages.length
  }, [visibleMessages.length, isLoadingMore])

  const handleNavigateToSession = useCallback(
    (sessionKey: string) => {
      if (!currentSessionKey) return
      navigate(
        `/chat/${encodeURIComponent(currentSessionKey)}/subagent/${encodeURIComponent(sessionKey)}`,
      )
    },
    [currentSessionKey, navigate],
  )

  const handleStartReached = useCallback(() => {
    if (hasMore && !isLoadingMore) {
      loadMore()
    }
  }, [hasMore, isLoadingMore, loadMore])

  const hasActiveGroup = Array.from(groups.values()).some((g) => g.status === 'started')

  // ── Build a merged timeline of messages and group blocks ──
  const renderItems: RenderItem[] = useMemo(() => {
    const items: RenderItem[] = []
    for (let i = 0; i < visibleMessages.length; i++) {
      items.push({ type: 'message', message: visibleMessages[i], index: i })
    }
    for (const group of groups.values()) {
      items.push({ type: 'group', group })
    }
    return items
  }, [visibleMessages, groups])

  const renderItem = useCallback(
    (_index: number, item: RenderItem) => {
      if (item.type === 'message') {
        return (
          <MessageBubble
            key={item.message.id}
            message={item.message}
            isLast={item.index === visibleMessages.length - 1}
            onNavigateToSession={handleNavigateToSession}
            apiUrl={apiUrl}
            onRetry={onRetry}
          />
        )
      }

      // ── Group block: header + turns + synthesis ──
      const group = item.group
      const groupTheme = getModeTheme('group')
      const highestTurn = group.turns.reduce(
        (max, t) => (t.turnIndex > (max?.turnIndex ?? -1) ? t : max),
        undefined as GroupTurn | undefined,
      )
      const speakingSpeaker =
        group.status === 'started' && highestTurn && highestTurn.content === ''
          ? highestTurn.speaker
          : null

      return (
        <div key={`group-block-${group.groupID}`}>
          {/* Group header */}
          <div className="py-2">
            <div
              className={`rounded-lg border ${groupTheme.border} ${groupTheme.softBg} px-3 py-2`}
            >
              <div className="flex items-center gap-2 flex-wrap">
                <span
                  className={`inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium ${groupTheme.chip}`}
                >
                  {group.strategy || t('groups.strategy')}
                </span>
                <span className="text-xs text-text-tertiary">
                  {t('groups.participants')}: {group.participants}
                </span>
                <span className="text-xs text-text-tertiary">
                  {t('groups.layers')}: {group.layers}
                </span>
                <span className="text-xs text-text-tertiary">
                  {group.totalTokens} {t('groups.tokens')}
                </span>
                <span
                  className={`inline-flex items-center rounded-md px-1.5 py-0.5 text-[10px] font-medium ${
                    group.status === 'done'
                      ? 'bg-state-success-light text-state-success'
                      : group.status === 'error'
                        ? 'bg-state-error-light text-state-error'
                        : group.status === 'stopped'
                          ? 'bg-surface-hover text-text-tertiary'
                          : `${groupTheme.softBg} ${groupTheme.text}`
                  }`}
                >
                  {t(`groups.status.${group.status}`)}
                </span>
              </div>
            </div>
          </div>

          {/* Group turns */}
          {group.turns.map((turn) => (
            <div key={`group-turn-${turn.groupID}-${turn.turnIndex}`} className="py-2">
              <div className="mb-1 flex items-center gap-2">
                {speakingSpeaker === turn.speaker && (
                  <span className="inline-block h-2 w-2 rounded-full bg-brand-naranja animate-pulse" />
                )}
                <span className="text-sm font-semibold text-text-primary">{turn.label}</span>
                <span className="rounded px-1.5 py-0.5 text-[10px] font-medium bg-surface-hover text-text-tertiary">
                  {turn.role}
                </span>
              </div>
              <div className="text-sm text-text-secondary">
                <MarkdownText content={turn.content} />
              </div>
              {turn.toolCalls?.map((tc) => (
                <GroupToolCallItem key={tc.tool_call_id} tc={tc} />
              ))}
            </div>
          ))}

          {/* Group synthesis */}
          {group.synthesis && (
            <div className="py-2">
              <div className="mb-1 flex items-center gap-2">
                <span className="text-sm font-semibold text-text-primary">
                  ✨ {t('groups.finalSynthesis')}
                </span>
              </div>
              <div className="text-sm text-text-secondary">
                <MarkdownText content={group.synthesis} />
              </div>
            </div>
          )}
        </div>
      )
    },
    [visibleMessages, handleNavigateToSession, apiUrl, onRetry, t],
  )

  const computeItemKey = useCallback((index: number, item: RenderItem) => {
    if (item.type === 'message') return item.message.id || `msg-${index}`
    return `group-block-${item.group.groupID}`
  }, [])

  const scrollToBottom = useCallback(() => {
    virtuosoRef.current?.scrollToIndex({
      index: firstItemIndex + renderItems.length - 1,
      behavior: 'smooth',
    })
  }, [firstItemIndex, renderItems])

  // ── Empty state (AFTER all hooks to satisfy Rules of Hooks) ──
  if (renderItems.length === 0) {
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

  return (
    <div className="relative h-full w-full">
      <Virtuoso
        ref={virtuosoRef}
        key={currentSessionKey ?? 'default'}
        className="mx-auto max-w-3xl h-full w-full"
        firstItemIndex={firstItemIndex}
        initialTopMostItemIndex={Math.max(0, renderItems.length - 1)}
        data={renderItems}
        itemContent={renderItem}
        computeItemKey={computeItemKey}
        followOutput={(isAtBottom) => (isAtBottom ? 'smooth' : false)}
        atBottomStateChange={setAtBottom}
        atBottomThreshold={300}
        startReached={handleStartReached}
        components={{
          Header: () => (
            <>
              {isLoadingMore && (
                <div className="flex justify-center py-2">
                  <div className="h-5 w-5 animate-spin rounded-full border-2 border-primary border-t-transparent" />
                </div>
              )}
              {!isLoadingMore && hasMore && (
                <p className="text-center py-2 text-xs text-text-tertiary">
                  {t('chat.scrollUpForMore')}
                </p>
              )}
            </>
          ),
          Footer: () => (
            <>
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
              {/* Typing indicator from another device */}
              {typingIndicator && typingIndicator.deviceId !== session?.client_id && (
                <div className="flex items-center gap-2 px-4 py-2 text-xs text-text-tertiary animate-pulse">
                  <span className="flex gap-0.5">
                    <span
                      className="h-1.5 w-1.5 rounded-full bg-text-tertiary animate-bounce"
                      style={{ animationDelay: '0ms' }}
                    />
                    <span
                      className="h-1.5 w-1.5 rounded-full bg-text-tertiary animate-bounce"
                      style={{ animationDelay: '150ms' }}
                    />
                    <span
                      className="h-1.5 w-1.5 rounded-full bg-text-tertiary animate-bounce"
                      style={{ animationDelay: '300ms' }}
                    />
                  </span>
                  <span>{typingIndicator.deviceName} is typing...</span>
                </div>
              )}
            </>
          ),
        }}
      />
      {!atBottom && renderItems.length > 0 && (
        <button
          type="button"
          onClick={scrollToBottom}
          aria-label="Scroll to bottom"
          className="absolute bottom-4 left-1/2 -translate-x-1/2 z-10 flex h-9 w-9 items-center justify-center rounded-full border border-border bg-background-secondary shadow-md transition-opacity hover:bg-background-tertiary"
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 16 16"
            fill="none"
            xmlns="http://www.w3.org/2000/svg"
            aria-hidden="true"
          >
            <path
              d="M8 3v10m0 0l-4-4m4 4l4-4"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </button>
      )}
    </div>
  )
}
