import { memo, useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { TimeGroup } from '../../hooks/useChatFilters'
import { useClickOutside } from '../../hooks/useClickOutside'
import type { ChatSession } from '../../lib/types'

interface ChatListViewProps {
  groups: TimeGroup[]
  selectedKey?: string | null
  processingSessions?: Set<string>
  onSelect: (sessionKey: string) => void
  onDelete?: (sessionKey: string) => void
  onClear?: (sessionKey: string) => void
  onRename?: (sessionKey: string, name: string) => void
}

function formatDateRelative(
  dateStr: string,
  t: (s: string, o?: Record<string, unknown>) => string,
): string {
  const d = new Date(dateStr)
  if (Number.isNaN(d.getTime())) return ''

  const now = new Date()
  const diffMs = now.getTime() - d.getTime()
  const diffMins = Math.floor(diffMs / 60000)
  const diffHours = Math.floor(diffMs / 3600000)

  if (diffMins < 1) return t('chat.justNow')
  if (diffMins < 60) return t('chat.minutesAgo', { count: diffMins })
  if (diffHours < 24) return t('chat.hoursAgo', { count: diffHours })
  return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })
}

const RenameForm = memo(function RenameForm({
  value,
  onChange,
  onSubmit,
}: {
  value: string
  onChange: (val: string) => void
  onSubmit: () => void
}) {
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    inputRef.current?.focus()
    inputRef.current?.select()
  }, [])

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        onSubmit()
      }}
      className="flex items-center gap-2"
    >
      <input
        ref={inputRef}
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onBlur={onSubmit}
        className="w-full rounded-md border border-border bg-background-primary px-2 py-1 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-accent"
      />
    </form>
  )
})

const ChatListItem = memo(function ChatListItem({
  session,
  isSelected,
  isProcessing,
  onSelect,
  onDelete,
  onClear,
  onRename,
}: {
  session: ChatSession
  isSelected: boolean
  isProcessing: boolean
  onSelect: (key: string) => void
  onDelete?: (key: string) => void
  onClear?: (key: string) => void
  onRename?: (key: string, name: string) => void
}) {
  const { t } = useTranslation()
  const [renaming, setRenaming] = useState(false)
  const [renameValue, setRenameValue] = useState(session.name ?? '')
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useClickOutside<HTMLDivElement>(() => setMenuOpen(false), menuOpen)

  const handleRenameSubmit = useCallback(() => {
    const trimmed = renameValue.trim()
    if (trimmed && trimmed !== session.name) {
      onRename?.(session.key, trimmed)
    }
    setRenaming(false)
  }, [renameValue, session.name, session.key, onRename])

  const handleSelectClick = useCallback(() => {
    if (!renaming && !menuOpen) onSelect(session.key)
  }, [renaming, menuOpen, onSelect, session.key])

  const toggleMenu = useCallback(() => setMenuOpen((v) => !v), [])

  const startRename = useCallback(() => {
    setRenaming(true)
    setRenameValue(session.name ?? '')
    setMenuOpen(false)
  }, [session.name])

  const handleClearClick = useCallback(() => {
    onClear?.(session.key)
    setMenuOpen(false)
  }, [onClear, session.key])

  const handleDeleteClick = useCallback(() => {
    onDelete?.(session.key)
    setMenuOpen(false)
  }, [onDelete, session.key])

  return (
    <div
      className={`
        group flex items-center gap-2.5 rounded-lg px-3 py-2.5
        transition-colors duration-150
        ${
          isSelected
            ? 'bg-accent/10 text-accent'
            : 'hover:bg-background-secondary/80 text-text-primary'
        }
      `}
    >
      <button type="button" className="min-w-0 flex-1 text-left" onClick={handleSelectClick}>
        {renaming ? (
          <div
            onClick={(e) => e.stopPropagation()}
            onKeyDown={(e) => e.stopPropagation()}
            role="presentation"
          >
            <RenameForm
              value={renameValue}
              onChange={setRenameValue}
              onSubmit={handleRenameSubmit}
            />
          </div>
        ) : (
          <div className="flex items-center justify-between gap-2">
            <div className="min-w-0">
              <p
                className={`truncate text-sm font-medium ${
                  isSelected ? 'text-accent' : 'text-text-primary'
                } ${!session.name ? 'italic text-text-tertiary' : ''}`}
              >
                {session.name || t('chat.unnamedSession', { key: session.key.slice(-8) })}
              </p>
              <div className="flex items-center gap-2 text-xs text-text-tertiary">
                <span>{t('chat.messageCount', { count: session.message_count })}</span>
                <span className="h-1 w-1 rounded-full bg-text-tertiary/40" />
                <span>{formatDateRelative(session.updated, t)}</span>
                {isProcessing && (
                  <span className="ml-1 inline-block h-3 w-3 animate-spin rounded-full border-2 border-accent border-t-transparent" />
                )}
              </div>
            </div>
          </div>
        )}
      </button>

      {(onDelete || onClear || onRename) && (
        <div
          ref={menuRef}
          className="relative flex items-center opacity-0 group-hover:opacity-100 transition-opacity"
        >
          <button
            type="button"
            onClick={toggleMenu}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') toggleMenu()
            }}
            className="rounded-md p-1.5 hover:bg-background-secondary text-text-tertiary"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              aria-hidden="true"
            >
              <circle cx="12" cy="5" r="1" />
              <circle cx="12" cy="12" r="1" />
              <circle cx="12" cy="19" r="1" />
            </svg>
          </button>
          {menuOpen && (
            <div className="absolute right-0 top-8 z-50 w-44 rounded-lg border border-border bg-background-primary shadow-lg py-1">
              {onRename && (
                <button
                  type="button"
                  className="flex w-full items-center gap-2 px-3 py-2 text-sm text-text-primary hover:bg-background-secondary"
                  onClick={startRename}
                >
                  {t('chat.rename')}
                </button>
              )}
              {onClear && (
                <button
                  type="button"
                  className="flex w-full items-center gap-2 px-3 py-2 text-sm text-text-primary hover:bg-background-secondary"
                  onClick={handleClearClick}
                >
                  {t('chat.clearSession')}
                </button>
              )}
              {onDelete && (
                <button
                  type="button"
                  className="flex w-full items-center gap-2 px-3 py-2 text-sm text-warning hover:bg-background-secondary"
                  onClick={handleDeleteClick}
                >
                  {t('chat.deleteSession')}
                </button>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  )
})

export const ChatListView = memo(function ChatListView({
  groups,
  selectedKey,
  processingSessions,
  onSelect,
  onDelete,
  onClear,
  onRename,
}: ChatListViewProps) {
  const { t } = useTranslation()

  if (!groups.length) {
    return (
      <div className="px-3 py-8 text-center text-sm text-text-tertiary">
        {t('chat.noSearchResults')}
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-1 pb-4">
      {groups.map((group) => (
        <div key={group.key} className="flex flex-col gap-0.5">
          <div className="px-3 py-2 text-xs font-semibold uppercase tracking-wide text-text-tertiary/70">
            {t(group.label, { defaultValue: group.label })}
          </div>
          <div className="flex flex-col gap-0.5">
            {group.sessions.map((session) => (
              <ChatListItem
                key={session.key}
                session={session}
                isSelected={selectedKey === session.key}
                isProcessing={!!processingSessions?.has(session.key)}
                onSelect={onSelect}
                onDelete={onDelete}
                onClear={onClear}
                onRename={onRename}
              />
            ))}
          </div>
        </div>
      ))}
    </div>
  )
})
