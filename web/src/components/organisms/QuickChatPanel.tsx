import { useCallback, useEffect, useRef, useState } from 'react'
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

  // Animation state: track if component should render (for exit animation)
  const [closing, setClosing] = useState(false)
  const [mounted, setMounted] = useState(false)

  // On open
  useEffect(() => {
    if (isOpen) {
      setMounted(true)
      setClosing(false)
    }
  }, [isOpen])

  // Focus search on open
  useEffect(() => {
    if (mounted && !closing && initialFocusSearch && searchRef.current) {
      searchRef.current.focus()
    }
  }, [mounted, closing, initialFocusSearch])

  // Keyboard handler
  const handleClose = useCallback(() => {
    setClosing(true)
    setTimeout(() => {
      setMounted(false)
      setClosing(false)
      onClose()
    }, 200) // match animation duration
  }, [onClose])

  useEffect(() => {
    if (!mounted || closing) return
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') handleClose()
    }
    document.addEventListener('keydown', handleKey)
    return () => document.removeEventListener('keydown', handleKey)
  }, [mounted, closing, handleClose])

  const handleSelect = useCallback(
    (key: string) => {
      onSelectSession(key)
      handleClose()
    },
    [onSelectSession, handleClose],
  )

  const handleDelete = useCallback(
    async (key: string) => {
      await onDeleteSession(key)
    },
    [onDeleteSession],
  )

  const goToAdmin = useCallback(() => {
    handleClose()
    navigate('/chats')
  }, [handleClose, navigate])

  if (!mounted) return null

  return (
    <div
      className={`fixed inset-0 z-50 flex ${closing ? 'animate-panel-out' : 'animate-panel-in'}`}
    >
      {/* Overlay — left side */}
      <div
        className={`flex-1 cursor-pointer ${closing ? 'modal-backdrop-out' : 'modal-backdrop-in'}`}
        onClick={handleClose}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') handleClose()
        }}
        role="button"
        tabIndex={-1}
        aria-hidden="true"
      />

      {/* Panel — right side */}
      <aside
        className={`flex h-full w-full max-w-sm flex-shrink-0 flex-col border-l border-border bg-background-primary shadow-2xl ${closing ? 'animate-panel-slide-out' : 'animate-panel-slide-in'}`}
      >
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
