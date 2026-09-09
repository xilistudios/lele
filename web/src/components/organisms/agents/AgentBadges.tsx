import { useTranslation } from 'react-i18next'
import { shortModelName } from '../../../lib/modelName'
import type { EditableAgentConfig } from '../../../lib/types'

/**
 * The three identity badges of an agent (spec §3.4.2, reused by the detail
 * header §4.2.2): `default`, `modified` and `inherits model`.
 *
 * Class strings are copied verbatim from the master-list card so the same
 * badge never looks different in the two places it appears. `AgentCard` keeps
 * its own inline copy for now (it ships with its own test suite); switching it
 * to this component is a follow-up, not part of this change.
 */
type Props = {
  agent: EditableAgentConfig
  /** Any dirty path starts with `agents.list.{index}.` (§5.3). */
  isModified: boolean
  /** `agents.defaults.model` — the badge names it when the agent has none. */
  defaultsModel: string
  /** Hide the inherited-model badge below `sm` (list cards do this at §3.9). */
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
        <span className="rounded-md bg-accent-subtle px-1.5 py-0.5 text-[10px] font-medium text-accent-primary">
          {t('settings.defaultBadge')}
        </span>
      )}
      {isModified && (
        <span
          data-testid="badge-modified"
          className="rounded-md bg-state-info-light px-1.5 py-0.5 text-[10px] font-medium text-state-info"
        >
          {t('settings.modifiedBadge')}
        </span>
      )}
      {inherits && (
        <span
          data-testid="badge-inherits"
          className={`${
            hideInheritsOnSmall ? 'hidden sm:inline-flex' : 'inline-flex'
          } rounded-md border border-border bg-surface-muted px-1.5 py-0.5 text-[10px] font-medium text-text-tertiary`}
        >
          {t('settings.agentPage.inheritsModel', { model: shortModelName(defaultsModel) })}
        </span>
      )}
    </span>
  )
}

/** `model` is `string | {primary}` in the wire format; '' when unset. */
function getPrimary(agent: EditableAgentConfig): string {
  if (!agent.model) return ''
  return typeof agent.model === 'string' ? agent.model : agent.model.primary || ''
}

/** Decorative ` · ` separator between meta segments. */
export function MetaDot() {
  return (
    <span aria-hidden="true" className="mx-1.5">
      ·
    </span>
  )
}
