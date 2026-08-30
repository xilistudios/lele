import { useTranslation } from 'react-i18next'
import { Badge } from '../atoms/Badge'

type Props = {
  title: string
  description?: string
  children: React.ReactNode
  isRestartRequired?: boolean
}

export function SettingsSection({ title, description, children, isRestartRequired }: Props) {
  const { t } = useTranslation()

  return (
    <section className="rounded-lg border border-border bg-background-primary p-4 md:p-6">
      <div className="mb-4 flex items-center gap-2">
        <h2 className="text-sm font-medium text-text-primary">{title}</h2>
        {isRestartRequired && <Badge variant="warning">{t('settings.requiresRestart')}</Badge>}
      </div>
      {description && <p className="mb-4 text-xs text-text-secondary">{description}</p>}
      <div className="space-y-4">{children}</div>
    </section>
  )
}
