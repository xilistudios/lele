import { useTranslation } from 'react-i18next'

type Props = {
  title: string
  description?: string
  children: React.ReactNode
  isRestartRequired?: boolean
}

export function SettingsSection({ title, description, children, isRestartRequired }: Props) {
  const { t } = useTranslation()

  return (
    <section className="rounded-lg border border-border bg-background-primary p-6">
      <div className="mb-4 flex items-center gap-2">
        <h2 className="text-sm font-medium text-white">{title}</h2>
        {isRestartRequired && (
          <span className="rounded bg-amber-500/20 px-1.5 py-0.5 text-[10px] text-amber-400">
            {t('settings.requiresRestart')}
          </span>
        )}
      </div>
      {description && <p className="mb-4 text-xs text-text-secondary">{description}</p>}
      <div className="space-y-4">{children}</div>
    </section>
  )
}
