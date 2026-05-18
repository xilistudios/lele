import { useCallback, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useChatFilters } from '../../hooks/useChatFilters'
import { ChatSearchBar } from '../molecules/ChatSearchBar'
import { ChatListView } from './ChatListView'

interface QuickChatPanelProps {
  isOpen: boolean
  onClose: () => void
  initialFocusSearch?: boolean
}

export function QuickChatPanel({
  isOpen,
  onClose,
  initialFocusSearch = false,
}: QuickChatPanelProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { sessions, currentSessionKey, processingSessions, onSelectSession, onDeleteSession } =
    useAppLogicContext()
  const { query, setQuery, sortMode, setSortMode, grouped } = useChatFilters(sessions)
  const searchRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (isOpen && initialFocusSearch && searchRef.current) {
      searchRef.current.focus()
    }
  }, [isOpen, initialFocusSearch])

  useEffect(() => {
    if (!isOpen) return
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKey)
    return () => document.removeEventListener('keydown', handleKey)
  }, [isOpen, onClose])

  const handleSelect = useCallback(
    (key: string) => {
      onSelectSession(key)
      onClose()
    },
    [onSelectSession, onClose],
  )

  const handleDelete = useCallback(
    async (key: string) => {
      await onDeleteSession(key)
    },
    [onDeleteSession],
  )

  const goToAdmin = useCallback(() => {
    onClose()
    navigate('/chats')
  }, [onClose, navigate])

  if (!isOpen) return null

  return (
    <div className="fixed inset-0 z-50 flex">
      {/* Overlay */}
      <div
        className="flex-1 bg-black/20 transition-opacity cursor-pointer"
        onClick={onClose}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') onClose()
        }}
        role="button"
        tabIndex={-1}
        aria-hidden="true"
      />

      {/* Panel */}
      <aside className="flex h-full w-full max-w-sm flex-shrink-0 flex-col border-r border-border bg-background-primary shadow-2xl">
        {/* Header */}
        <div className="flex items-center gap-3 border-b border-border px-4 py-3">
          <ChatSearchBar
            query={query}
            onQueryChange={setQuery}
            sortMode={sortMode}
            onSortChange={setSortMode}
            className="flex-1"
          />
          <button
            type="button"
            onClick={onClose}
            className="rounded-md p-1.5 text-text-tertiary hover:bg-background-secondary"
            aria-label={t('chat.closePanel')}
          >
            <svg
              width="18"
              height="18"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              aria-hidden="true"
            >
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>

        {/* List */}
        <div className="flex-1 overflow-y-auto">
          <ChatListView
            groups={grouped}
            selectedKey={currentSessionKey}
            processingSessions={processingSessions}
            onSelect={handleSelect}
            onDelete={handleDelete}
          />
        </div>

        {/* Footer */}
        <div className="border-t border-border px-4 py-3">
          <button
            type="button"
            onClick={goToAdmin}
            className="w-full rounded-lg border border-border bg-background-secondary px-4 py-2.5 text-sm font-medium text-text-primary hover:bg-background-secondary/80 transition-colors"
          >
            {t('chat.manageChats')}
          </button>
        </div>
      </aside>
    </div>
  )
}
