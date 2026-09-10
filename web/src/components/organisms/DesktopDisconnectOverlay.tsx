import { useId } from 'react'
import { useTranslation } from 'react-i18next'

type Props = {
  onRestart: () => void
  restarting: boolean
}

/**
 * Full-screen overlay shown when the desktop backend (sidecar gateway) died.
 * Simple presentational component — all state lives in the parent hook.
 */
export function DesktopDisconnectOverlay({ onRestart, restarting }: Props) {
  const { t } = useTranslation()
  const titleId = useId()

  return (
    <div
      role="alertdialog"
      aria-modal="true"
      aria-labelledby={titleId}
      className="fixed inset-0 z-[100] flex items-center justify-center bg-overlay px-4 backdrop-blur-sm"
    >
      <div className="flex max-w-md flex-col items-center rounded-3xl border border-border bg-background-secondary p-8 text-center shadow-2xl">
        <div className="mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-state-error-light text-4xl">
          ⚠️
        </div>
        <h2 id={titleId} className="text-xl font-semibold text-text-primary">
          {t('desktop.disconnected')}
        </h2>
        <p className="mt-2 text-sm text-text-secondary">{t('desktop.disconnectedMessage')}</p>
        <button
          type="button"
          onClick={onRestart}
          disabled={restarting}
          className="mt-6 rounded-lg bg-accent-primary px-6 py-2.5 text-sm font-medium text-white hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-60 transition-colors"
        >
          {restarting ? t('desktop.restarting') : t('desktop.restart')}
        </button>
      </div>
    </div>
  )
}
