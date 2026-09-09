import { useSettings } from '../../../contexts/SettingsContext'
import { getErrorForPath, isDirtyPath } from '../../../hooks/useSettingsHelpers'
import type { EditableAgentConfig, SubagentsConfig } from '../../../lib/types'
import { AgentChipMultiSelect } from '../../molecules/AgentChipMultiSelect'
import { BooleanInput } from '../../molecules/BooleanInput'
import { NumberInput } from '../../molecules/NumberInput'
import { SearchableSelect } from '../../molecules/SearchableSelect'
import { SettingsField } from '../../molecules/SettingsField'
import { SettingsSection } from '../../molecules/SettingsSection'

/**
 * `tab=subagents` — delegation (spec §4.7).
 *
 * The whole section hangs off ONE config node, `agents.list.{i}.subagents`:
 * the feature is on when the object is present and off when it is `undefined`.
 * `SubagentsConfig` on the wire is `{ allow_agents, model, timeout_minutes,
 * max_concurrent, max_iterations }` (`omitempty` on every field), so:
 *
 * - enabling writes `{ allow_agents: [] }` — an empty allowlist means "nobody
 *   yet", which is the safe default; the chips below are how you add some;
 * - every write is an immutable spread of the node that is already there, so
 *   changing one field can never drop the others;
 * - the three numbers keep `0` as a real value (the backend reads 0 as "no
 *   limit"), they are never normalised to `undefined`;
 * - an empty subagent model writes `undefined` for `model` — NOT `{}` — so the
 *   agent inherits its own model.
 */

type Props = {
  agent: EditableAgentConfig
  /** Position in `agents.list` — paths are positional. */
  index: number
  /** Full list, used to build the "who may I delegate to" chips. */
  agents: EditableAgentConfig[]
}

export function AgentSubagentsSection({ agent, index, agents }: Props) {
  const {
    t,
    updateField,
    dirtyPaths,
    validationErrors,
    getOptionsForAgent,
    getGroupsForAgent,
    isLoadingModels,
  } = useSettings()

  const base = `agents.list.${index}.subagents`
  const subagents = agent.subagents
  const enabled = subagents !== undefined && subagents !== null

  const dirty = (field: string) => isDirtyPath(dirtyPaths, field)
  const error = (field: string) => getErrorForPath(validationErrors, field)

  /** Write a new `subagents` node derived from the current one. */
  const write = (patch: Partial<SubagentsConfig>) =>
    updateField(base, { ...subagents, ...patch } as SubagentsConfig)

  const toggleEnabled = (value: boolean) => {
    // OFF removes the node entirely (`undefined` => `omitempty` drops it).
    if (!value) updateField(base, undefined)
    else write({ allow_agents: subagents?.allow_agents ?? [] })
  }

  // Only the OTHER agents are candidates: an agent delegating to itself would
  // recurse. Order follows `agents.list` (positional dirty paths).
  const otherAgents = agents.filter((entry) => entry.id !== agent.id)

  const allowlist = subagents?.allow_agents ?? []
  const subagentModel =
    typeof subagents?.model === 'string' ? subagents.model : (subagents?.model?.primary ?? '')

  const emptyLabel = isLoadingModels ? t('settings.loading') : t('settings.noModels')

  const numbers: Array<{ field: keyof SubagentsConfig; labelKey: string; helpKey: string }> = [
    { field: 'timeout_minutes', labelKey: 'subagentsTimeout', helpKey: 'subagentsTimeoutHelp' },
    {
      field: 'max_concurrent',
      labelKey: 'subagentsConcurrent',
      helpKey: 'subagentsConcurrentHelp',
    },
    {
      field: 'max_iterations',
      labelKey: 'subagentsIterations',
      helpKey: 'subagentsIterationsHelp',
    },
  ]

  return (
    <div className="space-y-6">
      <SettingsSection title={t('settings.sections.subagents')}>
        <SettingsField
          label={t('settings.agentPage.subagentsEnable')}
          path={base}
          isDirty={dirty(base)}
          error={error(base)}
        >
          <BooleanInput
            id={`${base}.enabled`}
            value={enabled}
            onChange={toggleEnabled}
            label={t('settings.agentPage.subagentsEnable')}
          />
        </SettingsField>

        {enabled && (
          <>
            <SettingsField
              label={t('settings.agentPage.subagentsAllowed')}
              description={t('settings.agentPage.subagentsAllowedHelp')}
              path={`${base}.allow_agents`}
              isDirty={dirty(`${base}.allow_agents`)}
              error={error(`${base}.allow_agents`)}
            >
              <AgentChipMultiSelect
                id={base}
                agents={otherAgents}
                value={allowlist}
                onChange={(value) => write({ allow_agents: value })}
                empty={t('settings.agentPage.noOtherAgents')}
              />
            </SettingsField>

            <SettingsField
              label={t('settings.agentPage.subagentsModel')}
              description={t('settings.agentPage.subagentsModelHelp')}
              path={`${base}.model`}
              isDirty={dirty(`${base}.model`)}
              error={error(`${base}.model`)}
            >
              <SearchableSelect
                ariaLabel={t('settings.agentPage.subagentsModel')}
                buttonLabel={subagentModel || t('settings.agentPage.inherit')}
                direction="down"
                emptyLabel={emptyLabel}
                groups={getGroupsForAgent}
                onChange={(value) =>
                  write({ model: value ? { ...subagents?.model, primary: value } : undefined })
                }
                options={getOptionsForAgent}
                placeholder={subagentModel || t('settings.selectModel')}
                searchAriaLabel={`${t('settings.agentPage.subagentsModel')} search`}
                searchPlaceholder={t('settings.agentPage.subagentsModel')}
                value={subagentModel}
              />
            </SettingsField>

            <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
              {numbers.map(({ field, labelKey, helpKey }) => {
                const path = `${base}.${field}`
                return (
                  <SettingsField
                    key={field}
                    label={t(`settings.agentPage.${labelKey}`)}
                    description={t(`settings.agentPage.${helpKey}`)}
                    path={path}
                    isDirty={dirty(path)}
                    error={error(path)}
                  >
                    <NumberInput
                      // `?? 0`: an unset limit shows as 0, which is exactly its
                      // meaning on the wire ("no limit").
                      value={Number(subagents?.[field] ?? 0)}
                      min={0}
                      onChange={(value) => write({ [field]: value } as Partial<SubagentsConfig>)}
                      id={path}
                    />
                  </SettingsField>
                )
              })}
            </div>
          </>
        )}
      </SettingsSection>
    </div>
  )
}
