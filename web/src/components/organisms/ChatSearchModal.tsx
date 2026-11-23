import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useChatFilters } from '../../hooks/useChatFilters'
import { useFocusTrap } from '../../hooks/useFocusTrap'
import { ChatSearchBar } from '../molecules/ChatSearchBar'
import { ChatListView } from './ChatListView'

const EXIT_ANIMATION_MS = 200

interface ChatSearchModalProps {
  isOpen: boolean
  onClose: () => void
}

/**
 * Centered chat-search dialog. Rendered while `isOpen` is true and for
 * EXIT_ANIMATION_MS after it turns false, so the closing animation plays
 * before unmount.
 */
export function ChatSearchModal({ isOpen, onClose }: ChatSearchModalProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { sessions, currentSessionKey, processingSessions, onSelectSession, onDeleteSession } =
    useAppLogicContext()
  const { query, setQuery, sortMode, setSortMode, grouped } = useChatFilters(sessions)

  const dialogRef = useRef<HTMLDialogElement>(null)
  const searchInputRef = useRef<HTMLInputElement>(null)
  const invokerRef = useRef<HTMLElement | null>(null)

  const [mounted, setMounted] = useState(isOpen)
  const [closing, setClosing] = useState(false)

  useEffect(() => {
    if (isOpen) {
      setMounted(true)
      setClosing(false)
      return
    }
    if (!mounted) return

    setClosing(true)
    const timer = setTimeout(() => {
      setMounted(false)
      setClosing(false)
    }, EXIT_ANIMATION_MS)
    return () => clearTimeout(timer)
  }, [isOpen, mounted])

  useFocusTrap(dialogRef, mounted && !closing)

  useEffect(() => {
    if (!mounted || closing) return
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const active = document.activeElement
    if (active instanceof HTMLElement && active !== document.body) invokerRef.current = active
    searchInputRef.current?.focus()
    return () => {
      document.body.style.overflow = previousOverflow
    }
  }, [mounted, closing])

  useEffect(() => {
    if (!closing) return
    const invoker = invokerRef.current
    invokerRef.current = null
    if (invoker?.isConnected) invoker.focus()
  }, [closing])

  useEffect(() => {
    if (!mounted || closing) return
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKey)
    return () => document.removeEventListener('keydown', handleKey)
  }, [mounted, closing, onClose])

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

  if (!mounted) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center px-4 pt-[max(1rem,env(safe-area-inset-top,0px))] pb-[max(1rem,env(safe-area-inset-bottom,0px))]">
      <div
        className={`absolute inset-0 bg-overlay backdrop-blur-sm ${closing ? 'modal-backdrop-out' : 'modal-backdrop-in'}`}
        onClick={onClose}
        onKeyDown={(e) => {
          if (e.key === 'Escape') onClose()
        }}
        aria-hidden="true"
      />

      <dialog
        ref={dialogRef}
        open
        aria-modal="true"
        aria-label={t('chat.search')}
        tabIndex={-1}
        className={`relative z-10 flex max-h-[min(80vh,44rem)] w-full max-w-lg flex-col overflow-hidden rounded-xl border border-border bg-background-primary shadow-2xl outline-none ${
          closing ? 'modal-content-out' : 'modal-content-in'
        }`}
      >
        <div className="flex items-center gap-3 border-b border-border px-4 py-3">
          <ChatSearchBar
            query={query}
            onQueryChange={setQuery}
            sortMode={sortMode}
            onSortChange={setSortMode}
            inputRef={searchInputRef}
            className="flex-1"
          />
          <button
            type="button"
            onClick={onClose}
            className="rounded-md p-1.5 text-text-tertiary transition-colors hover:bg-background-secondary"
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

        <div className="min-h-0 flex-1 overflow-y-auto">
          <ChatListView
            groups={grouped}
            selectedKey={currentSessionKey}
            processingSessions={processingSessions}
            onSelect={handleSelect}
            onDelete={handleDelete}
          />
        </div>

        <div className="border-t border-border px-4 py-3">
          <button
            type="button"
            onClick={goToAdmin}
            className="w-full rounded-lg border border-border bg-background-secondary px-4 py-2.5 text-sm font-medium text-text-primary transition-colors hover:bg-background-secondary/80"
          >
            {t('chat.manageChats')}
          </button>
        </div>
      </dialog>
    </div>
  )
}
