import { useTranslation } from 'react-i18next'

type Props = {
  label: string
  path: string
  description?: string
  children: React.ReactNode
  isDirty?: boolean
  error?: string
  required?: boolean
}

export function SettingsField({
  label,
  path,
  description,
  children,
  isDirty,
  error,
  required,
}: Props) {
  const { t } = useTranslation()

  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-2">
        <label htmlFor={path} className="text-xs font-medium text-text-secondary">
          {label}
          {required && <span className="ml-1 text-state-error">*</span>}
        </label>
        {isDirty && (
          <span className="rounded bg-state-info-light px-1.5 py-0.5 text-[10px] text-state-info">
            {t('settings.modified')}
          </span>
        )}
      </div>
      {description && <p className="text-[11px] text-text-tertiary">{description}</p>}
      <div className="mt-1">{children}</div>
      {error && <p className="text-[11px] text-state-error">{error}</p>}
    </div>
  )
}
