import { memo } from 'react'
import { useTranslation } from 'react-i18next'
import type { SortMode } from '../../hooks/useChatFilters'

interface ChatSearchBarProps {
  query: string
  onQueryChange: (value: string) => void
  sortMode: SortMode
  onSortChange: (mode: SortMode) => void
  className?: string
  inputClassName?: string
}

export const ChatSearchBar = memo(function ChatSearchBar({
  query,
  onQueryChange,
  sortMode,
  onSortChange,
  className = '',
  inputClassName = '',
}: ChatSearchBarProps) {
  const { t } = useTranslation()

  return (
    <div className={`flex items-center gap-3 ${className}`}>
      <div className="relative flex-1">
        <SearchIcon />
        <input
          type="text"
          value={query}
          onChange={(e) => onQueryChange(e.target.value)}
          placeholder={t('chat.searchChats')}
          className={`w-full rounded-lg border border-border bg-background-secondary py-2 pl-9 pr-3 text-sm text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-accent ${inputClassName}`}
        />
      </div>
      <select
        value={sortMode}
        onChange={(e) => onSortChange(e.target.value as SortMode)}
        className="rounded-lg border border-border bg-background-secondary px-2 py-2 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-accent"
        aria-label={t('chat.sortBy')}
      >
        <option value="recent">{t('chat.sortRecent')}</option>
        <option value="name">{t('chat.sortName')}</option>
      </select>
    </div>
  )
})

function SearchIcon() {
  return (
    <svg
      className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-text-tertiary"
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      aria-hidden="true"
    >
      <circle cx="11" cy="11" r="8" />
      <line x1="21" y1="21" x2="16.65" y2="16.65" />
    </svg>
  )
}
