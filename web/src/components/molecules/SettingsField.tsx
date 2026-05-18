import { useTranslation } from 'react-i18next'
import { Badge } from '../atoms/Badge'

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
    <div className="space-y-2 mb-4">
      <div className="flex items-center gap-2">
        <label htmlFor={path} className="text-sm font-medium text-text-secondary">
          {label}
          {required && <span className="ml-1 text-state-error">*</span>}
        </label>
        {isDirty && <Badge variant="info">{t('settings.modified')}</Badge>}
      </div>
      {description && <p className="text-xs text-text-tertiary">{description}</p>}
      <div className="mt-1.5">{children}</div>
      {error && <p className="text-xs text-state-error">{error}</p>}
    </div>
  )
}
