import { useState } from 'react'
import { useTranslation } from 'react-i18next'

type Props = {
  message: string
  onDismiss?: () => void
}

export function ErrorBanner({ message, onDismiss }: Props) {
  const { t } = useTranslation()
  const [dismissed, setDismissed] = useState(false)
  if (dismissed || !message) return null

  const handleDismiss = () => {
    setDismissed(true)
    onDismiss?.()
  }

  const display =
    message === 'Failed to fetch' || message === 'NetworkError when attempting to fetch resource.'
      ? t('errors.network')
      : message

  return (
    <div
      role="alert"
      className="mx-4 mt-3 flex items-start justify-between gap-3 rounded-lg border border-state-error/30 bg-state-error-light px-4 py-2.5 text-xs text-state-error md:mx-6"
    >
      <span className="min-w-0 flex-1 break-words">{display}</span>
      <button
        type="button"
        onClick={handleDismiss}
        aria-label={t('common.dismiss', 'Dismiss')}
        className="flex h-5 w-5 shrink-0 items-center justify-center rounded text-state-error/80 hover:bg-state-error/10 hover:text-state-error"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" aria-hidden="true">
          <line x1="18" y1="6" x2="6" y2="18" />
          <line x1="6" y1="6" x2="18" y2="18" />
        </svg>
      </button>
    </div>
  )
}
