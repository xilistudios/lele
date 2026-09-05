import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { CloseIcon } from '../atoms/Icons'

/**
 * Strip listing the messages waiting for the current session's turn to end.
 * Owns its own visibility: renders nothing while the session's queue is empty,
 * so the composer stays untouched in the common case.
 */
export function QueuedMessages() {
  const { t } = useTranslation()
  const { queuedMessages, currentSessionKey, removeQueuedMessage, clearQueue } =
    useAppLogicContext()

  if (!currentSessionKey) return null
  const sessionQueue = queuedMessages.filter((item) => item.sessionKey === currentSessionKey)
  if (sessionQueue.length === 0) return null

  return (
    <div
      className="mb-2 flex flex-col gap-1 rounded-lg border border-border bg-background-secondary p-2"
      data-testid="queued-messages"
      aria-label={t('chat.queueHeader', { count: sessionQueue.length })}
    >
      <div className="flex items-center justify-between gap-2 px-1">
        <span className="text-xs font-medium text-text-secondary">
          {t('chat.queueHeader', { count: sessionQueue.length })}
        </span>
        <button
          type="button"
          onClick={() => clearQueue(currentSessionKey)}
          title={t('chat.queueClear')}
          aria-label={t('chat.queueClear')}
          className="rounded px-1 py-0.5 text-[10px] text-text-tertiary transition-colors hover:bg-background-tertiary hover:text-text-primary"
        >
          {t('chat.queueClear')}
        </button>
      </div>
      {sessionQueue.map((item) => (
        <div
          key={item.id}
          data-testid="queued-message"
          className="flex min-w-0 items-center gap-2 rounded-md bg-background-tertiary px-2 py-1"
        >
          <span className="min-w-0 flex-1 truncate text-xs text-text-primary" title={item.content}>
            {item.content}
          </span>
          {item.attachments.length > 0 && (
            <span className="flex-shrink-0 text-[10px] text-text-tertiary">
              📎{item.attachments.length}
            </span>
          )}
          <button
            type="button"
            onClick={() => removeQueuedMessage(item.id)}
            title={t('chat.queueRemove')}
            aria-label={t('chat.queueRemove')}
            className="flex h-4 w-4 flex-shrink-0 items-center justify-center rounded-full text-text-tertiary transition-colors hover:bg-background-secondary hover:text-text-primary"
          >
            <CloseIcon size={10} />
          </button>
        </div>
      ))}
    </div>
  )
}
