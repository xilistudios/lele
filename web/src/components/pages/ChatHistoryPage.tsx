import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useChatFilters } from '../../hooks/useChatFilters'
import { SidebarToggleIcon } from '../atoms/Icons'
import { ChatSearchBar } from '../molecules/ChatSearchBar'
import { ChatListView } from '../organisms/ChatListView'
import { Sidebar } from '../organisms/Sidebar'

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

  const { query, setQuery, sortMode, setSortMode, grouped, filteredSessions } =
    useChatFilters(sessions)

  const handleSelect = useCallback(
    (key: string) => {
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
                {t('chat.totalChats', { count: sessions.length })}
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

        {/* Content */}
        <div className="flex-1 overflow-y-auto px-6 py-4">
          {filteredSessions.length === 0 ? (
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
            </div>
          )}
        </div>
      </main>
    </div>
  )
}
