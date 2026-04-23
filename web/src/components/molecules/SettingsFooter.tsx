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
          <span className="text-xs text-green-400">{t('settings.saved')}</span>
        )}
        {saveState === 'error' && saveError && (
          <span className="text-xs text-rose-400">{saveError}</span>
        )}
        {hasErrors && (
          <span className="text-xs text-amber-400">
            {t('settings.validationErrors', { count: validationErrorsCount })}
          </span>
        )}
        {isDirty && <span className="text-xs text-blue-400">{t('settings.unsavedChanges')}</span>}
      </div>

      <div className="flex items-center gap-2">
        <button
          onClick={onReset}
          disabled={!isDirty || saveState === 'saving'}
          type="button"
          className="rounded border border-border bg-transparent px-4 py-2 text-xs text-text-secondary transition-colors hover:bg-surface-card disabled:opacity-50"
        >
          {t('common.reset')}
        </button>
        <button
          onClick={onSave}
          disabled={!isDirty || saveState === 'saving'}
          type="button"
          className="rounded bg-blue-600 px-4 py-2 text-xs text-white transition-colors hover:bg-blue-500 disabled:opacity-50"
        >
          {saveState === 'saving' ? t('common.saving') : t('common.save')}
        </button>
      </div>
    </div>
  )
}
