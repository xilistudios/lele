import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'

export function DiagnosticsPanel() {
  const { t } = useTranslation()
  const { diagnostics } = useAppLogicContext()

  return (
    <section className="mx-6 mt-3 rounded-lg border border-border bg-background-primary p-4 text-xs text-text-secondary">
      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-2">
          <p className="text-[10px] uppercase tracking-[0.2em] text-text-tertiary">
            {t('chat.systemStatus')}
          </p>
          <p>{diagnostics.status?.status ?? '-'}</p>
          <p>{diagnostics.status?.uptime ?? '-'}</p>
          <p>{diagnostics.status?.version ?? '-'}</p>
        </div>
        <div className="space-y-2">
          <p className="text-[10px] uppercase tracking-[0.2em] text-text-tertiary">
            {t('chat.agentInfo')}
          </p>
          <p>{diagnostics.agentInfo?.name ?? '-'}</p>
          <p>{diagnostics.agentInfo?.model ?? '-'}</p>
          <p>{diagnostics.agentInfo?.workspace ?? '-'}</p>
          <p>{diagnostics.agentInfo?.status ?? '-'}</p>
        </div>
        <div className="space-y-2">
          <p className="text-[10px] uppercase tracking-[0.2em] text-text-tertiary">
            {t('chat.channels')}
          </p>
          {diagnostics.channels.map((channel) => (
            <p key={channel.name}>
              {channel.name} · {channel.running ? t('chat.running') : t('chat.stopped')}
            </p>
          ))}
        </div>
        <div className="space-y-2">
          <p className="text-[10px] uppercase tracking-[0.2em] text-text-tertiary">
            {t('chat.tools')}
          </p>
          {diagnostics.tools.map((tool) => (
            <p key={tool.name}>
              {tool.name} · {tool.enabled ? t('chat.enabled') : t('chat.disabled')}
            </p>
          ))}
        </div>
      </div>
      <details className="mt-4 rounded border border-border bg-background-primary p-3">
        <summary className="cursor-pointer text-[10px] uppercase tracking-[0.2em] text-text-tertiary">
          {t('chat.config')}
        </summary>
        <pre className="mt-3 overflow-x-auto text-[11px] leading-5 text-text-secondary">
          {JSON.stringify(diagnostics.config?.config ?? {}, null, 2)}
        </pre>
      </details>
    </section>
  )
}
