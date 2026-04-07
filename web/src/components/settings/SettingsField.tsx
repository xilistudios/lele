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
        <label htmlFor={path} className="text-xs font-medium text-[#ccc]">
          {label}
          {required && <span className="ml-1 text-rose-400">*</span>}
        </label>
        {isDirty && (
          <span className="rounded bg-blue-500/20 px-1.5 py-0.5 text-[10px] text-blue-400">
            {t('settings.modified')}
          </span>
        )}
      </div>
      {description && (
        <p className="text-[11px] text-[#666]">{description}</p>
      )}
      <div className="mt-1">{children}</div>
      {error && (
        <p className="text-[11px] text-rose-400">{error}</p>
      )}
    </div>
  )
}
