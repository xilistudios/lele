import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getModeTheme } from '../../lib/modeTheme'
import type { ChatMode } from '../../lib/types'
import { formatSessionTitle } from '../../lib/utils'
import { TrashIcon } from '../atoms/Icons'
import { Spinner } from '../atoms/Spinner'

type Props = {
  sessionKey: string
  sessionName?: string
  messageCount: number
  selected?: boolean
  isProcessing?: boolean
  onSelect: () => void
  onDelete: () => void
  collapsed?: boolean
  mode?: ChatMode
}

export function SessionItem({
  sessionKey,
  sessionName,
  messageCount,
  selected = false,
  isProcessing = false,
  onSelect,
  onDelete,
  collapsed = false,
  mode,
}: Props) {
  const { t } = useTranslation()
  const [confirmingDelete, setConfirmingDelete] = useState(false)

  if (collapsed) {
    return (
      <button
        onClick={onSelect}
        type="button"
        title={formatSessionTitle(sessionKey, sessionName, messageCount)}
        className={`relative flex w-full items-center justify-center rounded-md p-2 transition-colors ${
          selected
            ? getModeTheme(mode).selectedItem
            : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
        }`}
      >
        <span className="text-xs">
          {sessionName?.[0]?.toUpperCase() ?? sessionKey[0]?.toUpperCase() ?? '#'}
        </span>
        {isProcessing && (
          <span className="absolute -right-0.5 -top-0.5 h-2 w-2 rounded-full bg-accent animate-pulse" />
        )}
      </button>
    )
  }

  return (
    <div
      onClick={onSelect}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onSelect()
        }
      }}
      // biome-ignore lint/a11y/useSemanticElements: div needed because it contains a nested <button> (delete), which would be invalid HTML
      role="button"
      tabIndex={0}
      className={`group flex w-full items-center gap-2 rounded-md px-3 py-2 text-left transition-colors cursor-pointer ${
        selected
          ? getModeTheme(mode).selectedItem
          : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
      }`}
    >
      {isProcessing && (
        <span className="flex-shrink-0 mt-1">
          <Spinner size="sm" className="text-accent" />
        </span>
      )}
      <span className="min-w-0 flex-1">
        <span className="block truncate text-xs leading-5">
          {formatSessionTitle(sessionKey, sessionName, messageCount)}
        </span>
        <span className="block text-[10px] text-text-tertiary">
          {messageCount === 1
            ? t('chat.messageCount_one', { count: messageCount })
            : t('chat.messageCount_other', { count: messageCount })}
        </span>
      </span>
      <button
        type="button"
        onClick={(event) => {
          event.stopPropagation()
          if (confirmingDelete) {
            setConfirmingDelete(false)
            onDelete()
          } else {
            setConfirmingDelete(true)
          }
        }}
        onBlur={() => setConfirmingDelete(false)}
        aria-label={confirmingDelete ? t('chat.confirmDelete') : t('chat.deleteSession')}
        className={`ml-auto flex items-center justify-center rounded-md p-2 transition-all ${
          confirmingDelete
            ? 'opacity-100 bg-state-error-light text-state-error'
            : 'text-text-tertiary opacity-0 hover:bg-state-error-light hover:text-state-error group-hover:opacity-100'
        }`}
      >
        <TrashIcon size={12} />
      </button>
    </div>
  )
}
