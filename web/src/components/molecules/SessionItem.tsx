import { useTranslation } from 'react-i18next'
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
}: Props) {
  const { t } = useTranslation()

  if (collapsed) {
    return (
      <button
        onClick={onSelect}
        type="button"
        title={formatSessionTitle(sessionKey, sessionName, messageCount)}
        className={`relative flex w-full items-center justify-center rounded-md p-2 transition-colors ${
          selected
            ? 'bg-surface-card-hover text-text-primary'
            : 'text-text-secondary hover:bg-surface-card hover:text-text-secondary'
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
    <button
      onClick={onSelect}
      type="button"
      className={`group flex w-full items-start gap-2 rounded-md px-3 py-2 text-left transition-colors ${
        selected
          ? 'bg-surface-card-hover text-text-primary'
          : 'text-text-secondary hover:bg-surface-card hover:text-text-secondary'
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
        onClick={(event) => {
          event.stopPropagation()
          onDelete()
        }}
        type="button"
        aria-label={t('chat.deleteSession')}
        className="ml-auto text-text-tertiary opacity-0 transition-opacity hover:text-state-error group-hover:opacity-100"
      >
        <TrashIcon />
      </button>
    </button>
  )
}
