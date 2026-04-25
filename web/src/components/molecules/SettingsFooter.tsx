import { useTranslation } from 'react-i18next'
import type { SaveState } from '../../hooks/useSettingsConfig'

type Props = {
  saveState: SaveState
  saveError: string | null
  hasErrors: boolean
  isDirty: boolean
  validationErrorsCount: number
  onReset: () => void
  onSave: () => void
}

export function SettingsFooter({
  saveState,
  saveError,
  hasErrors,
  isDirty,
  validationErrorsCount,
  onReset,
  onSave,
}: Props) {
  const { t } = useTranslation()

  return (
    <div className="flex items-center justify-between border-t border-border bg-background-secondary px-6 py-4">
      <div className="flex items-center gap-2">
        {saveState === 'saved' && (
          <span className="text-xs text-state-success">{t('settings.saved')}</span>
        )}
        {saveState === 'error' && saveError && (
          <span className="text-xs text-state-error">{saveError}</span>
        )}
        {hasErrors && (
          <span className="text-xs text-state-warning">
            {t('settings.validationErrors', { count: validationErrorsCount })}
          </span>
        )}
        {isDirty && <span className="text-xs text-state-info">{t('settings.unsavedChanges')}</span>}
      </div>

      <div className="flex items-center gap-3">
        <button
          onClick={onReset}
          disabled={!isDirty || saveState === 'saving'}
          type="button"
          className="rounded-md border border-border/70 bg-transparent px-5 py-2.5 text-sm text-text-secondary transition-all hover:bg-surface-hover hover:text-text-primary disabled:opacity-40"
        >
          {t('common.reset')}
        </button>
        <button
          onClick={onSave}
          disabled={!isDirty || saveState === 'saving'}
          type="button"
          className="rounded-md bg-accent-primary px-5 py-2.5 text-sm text-text-on-accent transition-all hover:bg-accent-hover shadow-sm disabled:opacity-40"
        >
          {saveState === 'saving' ? t('common.saving') : t('common.save')}
        </button>
      </div>
    </div>
  )
}
