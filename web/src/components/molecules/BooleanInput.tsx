import { useTranslation } from 'react-i18next'

type Props = {
  id: string
  value: boolean
  onChange: (value: boolean) => void
  disabled?: boolean
  label?: string
}

export function BooleanInput({ id, value, onChange, disabled, label }: Props) {
  const { t } = useTranslation()

  return (
    <label className="flex cursor-pointer items-center gap-2">
      <input
        id={id}
        type="checkbox"
        checked={value}
        onChange={(e) => onChange(e.target.checked)}
        disabled={disabled}
        className="h-4 w-4 rounded border-border bg-background-primary text-blue-600 focus:ring-blue-500 focus:ring-offset-background-primary disabled:opacity-50"
      />
      <span className="text-xs text-text-secondary">
        {label ?? (value ? t('common.enabled') : t('common.disabled'))}
      </span>
    </label>
  )
}
