import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useChatFilters } from '../../hooks/useChatFilters'
import type { ChatSession, SessionKind } from '../../lib/types'
import { SidebarToggleIcon } from '../atoms/Icons'
import { ChatSearchBar } from '../molecules/ChatSearchBar'
import { ChatListView } from '../organisms/ChatListView'
import { Sidebar } from '../organisms/Sidebar'

type KindTab = 'all' | SessionKind

const KIND_TABS: KindTab[] = ['all', 'chat', 'heartbeat', 'cron', 'cron-spawn', 'subagent']

export function ChatHistoryPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { api } = useAuthContext()
  const {
    sessions,
    currentSessionKey,
    sidebarOpen,
    onToggleSidebar,
    processingSessions,
    onSelectSession,
    onDeleteSession,
  } = useAppLogicContext()

  const [activeKind, setActiveKind] = useState<KindTab>('all')
  const [allFetchedSessions, setAllFetchedSessions] = useState<ChatSession[]>([])
  const [loading, setLoading] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [hasMore, setHasMore] = useState(false)
  const [total, setTotal] = useState(0)

  const PAGE_SIZE = 200 // backend max

  // Fetch the first page of persisted sessions on mount or when kind filter changes.
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setLoadError(null)
    setAllFetchedSessions([])
    setHasMore(false)
    setTotal(0)
    const kindParam = activeKind === 'all' ? undefined : activeKind
    api
      .sessions(undefined, kindParam, true, { offset: 0, limit: PAGE_SIZE })
      .then((data) => {
        if (!cancelled) {
          setAllFetchedSessions(data?.sessions ?? [])
          setHasMore(data?.has_more ?? false)
          setTotal(data?.total ?? 0)
        }
      })
      .catch((err) => {
        if (!cancelled) setLoadError((err as Error).message)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [api, activeKind])

  const loadMore = useCallback(async () => {
    if (loadingMore || !hasMore) return
    setLoadingMore(true)
    try {
      const kindParam = activeKind === 'all' ? undefined : activeKind
      const data = await api.sessions(undefined, kindParam, true, {
        offset: allFetchedSessions.length,
        limit: PAGE_SIZE,
      })
      if (data?.sessions?.length) {
        setAllFetchedSessions((prev) => [...prev, ...data.sessions])
      }
      setHasMore(data?.has_more ?? false)
      setTotal(data?.total ?? 0)
    } catch (err) {
      setLoadError((err as Error).message)
    } finally {
      setLoadingMore(false)
    }
  }, [api, activeKind, allFetchedSessions.length, hasMore, loadingMore])

  const allSessions = useMemo(() => {
    // Filter context sessions by active kind — without this, live sessions
    // bypass the kind tab filter because they are merged before API results.
    const kindFiltered =
      activeKind === 'all' ? sessions : sessions.filter((s) => s.kind === activeKind)
    const byKey = new Map<string, ChatSession>()
    for (const s of kindFiltered) byKey.set(s.key, s)
    for (const s of allFetchedSessions) {
      if (!byKey.has(s.key)) byKey.set(s.key, s)
    }
    return Array.from(byKey.values())
  }, [sessions, allFetchedSessions, activeKind])

  const { query, setQuery, sortMode, setSortMode, grouped, filteredSessions } = useChatFilters(
    allSessions,
    { includeEmpty: true },
  )

  const handleSelect = useCallback(
    (key: string) => {
      // Subagent sessions are only reachable via their parent chat's nested route
      if (key.startsWith('subagent:')) return
      onSelectSession(key)
      navigate(`/chat/${encodeURIComponent(key)}`)
    },
    [onSelectSession, navigate],
  )

  const handleDelete = useCallback(
    async (key: string) => {
      await onDeleteSession(key)
    },
    [onDeleteSession],
  )

  const handleClear = useCallback(
    async (key: string) => {
      try {
        await api.clearSession(key)
      } catch {
        // Silently ignore; session may already be gone
      }
    },
    [api],
  )

  return (
    <div className="flex h-screen overflow-hidden bg-background-primary text-text-primary">
      <Sidebar
        collapsed={!sidebarOpen}
        mobileOpen={sidebarOpen}
        onClose={() => onToggleSidebar()}
      />
      <main className="flex flex-1 flex-col overflow-hidden">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border px-4 py-4 md:px-6">
          <div className="flex items-center gap-2 md:gap-3 min-w-0">
            <button
              type="button"
              onClick={onToggleSidebar}
              className="flex md:hidden items-center justify-center rounded-md p-1.5 text-text-secondary hover:bg-surface-hover hover:text-text-primary transition-colors mr-1"
              aria-label={t('chat.toggleSidebar')}
            >
              <SidebarToggleIcon size={20} />
            </button>
            <div className="min-w-0">
              <h1 className="text-lg font-semibold text-text-primary truncate">
                {t('chat.allChats')}
              </h1>
              <p className="text-sm text-text-tertiary truncate">
                {t('chat.totalSessions', { count: total || allSessions.length })}
              </p>
            </div>
          </div>
          <ChatSearchBar
            query={query}
            onQueryChange={setQuery}
            sortMode={sortMode}
            onSortChange={setSortMode}
            inputClassName="w-64"
          />
        </div>

        {/* Kind tabs */}
        <div className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-2 md:px-6">
          {KIND_TABS.map((kind) => (
            <button
              key={kind}
              type="button"
              onClick={() => setActiveKind(kind)}
              className={`rounded-full border px-3 py-1 text-xs font-medium transition-colors ${
                activeKind === kind
                  ? 'border-interaction-primary/50 bg-interaction-primary/20 text-interaction-primary'
                  : 'border-border bg-background-secondary text-text-secondary hover:bg-surface-hover hover:text-text-primary'
              }`}
            >
              {t(`chat.filterKind.${kind}`)}
            </button>
          ))}
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto px-6 py-4">
          {loading && allSessions.length === 0 ? (
            <div className="flex h-64 items-center justify-center">
              <div className="h-8 w-8 animate-spin rounded-full border-2 border-interaction-primary border-t-transparent" />
            </div>
          ) : loadError ? (
            <div className="flex h-64 items-center justify-center text-sm text-warning">
              {t('chat.loadError', { error: loadError })}
            </div>
          ) : filteredSessions.length === 0 ? (
            <div className="flex h-64 items-center justify-center text-sm text-text-tertiary">
              {t('chat.noSearchResults')}
            </div>
          ) : (
            <div className="mx-auto max-w-4xl">
              <ChatListView
                groups={grouped}
                selectedKey={currentSessionKey}
                processingSessions={processingSessions}
                onSelect={handleSelect}
                onDelete={handleDelete}
                onClear={handleClear}
              />
              {hasMore && (
                <div className="flex justify-center py-6">
                  <button
                    type="button"
                    onClick={loadMore}
                    disabled={loadingMore}
                    className="rounded-lg border border-border bg-background-secondary px-5 py-2.5 text-sm font-medium text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary disabled:opacity-50"
                  >
                    {loadingMore ? (
                      <span className="flex items-center gap-2">
                        <span className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-interaction-primary border-t-transparent" />
                        {t('chat.loadingMore')}
                      </span>
                    ) : (
                      t('chat.loadMore', {
                        loaded: filteredSessions.length,
                        total: total || filteredSessions.length,
                      })
                    )}
                  </button>
                </div>
              )}
            </div>
          )}
        </div>
      </main>
    </div>
  )
}
