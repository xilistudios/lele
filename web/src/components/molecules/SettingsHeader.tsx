import { useTranslation } from 'react-i18next'

type Props = {
  configPath?: string
  title?: string
}

export function SettingsHeader({ configPath, title }: Props) {
  const { t } = useTranslation()

  return (
    <div className="flex items-center justify-between border-b border-border px-6 py-4">
      <div className="flex items-center gap-4">
        <h1 className="text-xl font-semibold text-text-primary">
          {title ?? t('chat.settings')}
        </h1>
        {configPath && (
          <span className="text-xs text-text-tertiary">{configPath}</span>
        )}
      </div>
    </div>
  )
}
