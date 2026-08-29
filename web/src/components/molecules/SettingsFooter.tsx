import { useTranslation } from 'react-i18next'
import type { SaveState } from '../../hooks/useSettingsConfig'
import { Button } from '../atoms'

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
    <div className="flex items-center justify-between border-t border-border bg-background-secondary px-4 py-3 md:px-6 md:py-4">
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
        <Button
          variant="secondary"
          size="lg"
          onClick={onReset}
          disabled={!isDirty || saveState === 'saving'}
          type="button"
        >
          {t('common.reset')}
        </Button>
        <Button
          variant="primary"
          size="lg"
          onClick={onSave}
          loading={saveState === 'saving'}
          disabled={!isDirty}
          type="button"
        >
          {saveState === 'saving' ? t('common.saving') : t('common.save')}
        </Button>
      </div>
    </div>
  )
}
