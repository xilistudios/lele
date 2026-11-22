import { useTranslation } from 'react-i18next'
import { shortModelName } from '../../../lib/modelName'
import type { EditableAgentConfig } from '../../../lib/types'

type Props = {
  agent: EditableAgentConfig
  isModified: boolean
  defaultsModel: string
  hideInheritsOnSmall?: boolean
}

export function AgentBadges({
  agent,
  isModified,
  defaultsModel,
  hideInheritsOnSmall = false,
}: Props) {
  const { t } = useTranslation()
  const inherits = !getPrimary(agent) && !!defaultsModel

  return (
    <span className="ml-2 inline-flex flex-none items-center gap-1.5">
      {agent.default && (
        <span className="rounded-md bg-accent-subtle px-1.5 py-0.5 text-2xs font-medium text-accent-primary">
          {t('settings.defaultBadge')}
        </span>
      )}
      {isModified && (
        <span
          data-testid="badge-modified"
          className="rounded-md bg-state-info/10 px-1.5 py-0.5 text-2xs font-medium text-state-info"
        >
          {t('settings.modifiedBadge')}
        </span>
      )}
      {inherits && (
        <span
          data-testid="badge-inherits"
          className={`${
            hideInheritsOnSmall ? 'hidden sm:inline-flex' : 'inline-flex'
          } rounded-md border border-border bg-background-tertiary px-1.5 py-0.5 text-2xs font-medium text-text-tertiary`}
        >
          {t('settings.agentPage.inheritsModel', { model: shortModelName(defaultsModel) })}
        </span>
      )}
    </span>
  )
}

function getPrimary(agent: EditableAgentConfig): string {
  if (!agent.model) return ''
  return typeof agent.model === 'string' ? agent.model : agent.model.primary || ''
}

export function MetaDot() {
  return (
    <span aria-hidden="true" className="mx-1.5">
      ·
    </span>
  )
}
