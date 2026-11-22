import { useTranslation } from 'react-i18next'
import { SKILLS_VIEWS, type SkillsView, viewHintKey } from '../../lib/skillsScope'
import type { Agent } from '../../lib/types'
import { SegmentedControl } from '../molecules/SegmentedControl'
import { SelectInput } from '../molecules/SelectInput'

type Props = {
  view: SkillsView
  onViewChange: (view: SkillsView) => void
  agents: Agent[]
  selectedAgentId: string | null
  onAgentChange: (agentId: string) => void
}

const HINT_DEFAULTS: Record<ReturnType<typeof viewHintKey>, string> = {
  'skills.viewAllHint':
    "Everything the default workspace, the shared global directory and lele's built-ins provide.",
  'skills.viewGlobalHint': 'Shared by every agent — installed in ~/.lele/skills.',
  'skills.viewAgentHint':
    'What {{agent}} can use: its own workspace plus the shared skills. Enable/disable applies to this agent only.',
}

const VIEW_LABEL_DEFAULTS: Record<SkillsView, string> = {
  all: 'All',
  global: 'Global',
  agent: 'By agent',
}

export function SkillsScopeBar({
  view,
  onViewChange,
  agents,
  selectedAgentId,
  onAgentChange,
}: Props) {
  const { t } = useTranslation()

  const hintKey = viewHintKey(view)
  const selectedAgent = agents.find((a) => a.id === selectedAgentId)

  const agentName = selectedAgent
    ? selectedAgent.name || selectedAgent.id
    : t('skills.selectAgent', 'Agent')

  const hint = t(hintKey, { defaultValue: HINT_DEFAULTS[hintKey], agent: agentName })

  return (
    <div data-testid="skills-scope-bar" className="mb-4 flex flex-wrap items-center gap-3">
      <div data-testid="skills-scope-tabs">
        <SegmentedControl
          id="skills-view"
          size="sm"
          ariaLabel={t('skills.scopeLabel', 'Filter skills')}
          value={view}
          onChange={onViewChange}
          options={SKILLS_VIEWS.map((v) => ({
            value: v,
            label: t(`skills.view.${v}`, VIEW_LABEL_DEFAULTS[v]),
          }))}
        />
      </div>

      {view === 'agent' && agents.length > 0 && (
        <div className="flex items-center gap-2">
          <label htmlFor="skills-agent" className="text-xs text-text-tertiary">
            {t('skills.selectAgent', 'Agent')}
          </label>
          <div className="w-[220px]">
            <SelectInput
              id="skills-agent"
              value={selectedAgentId ?? ''}
              onChange={onAgentChange}
              options={agents.map((a) => ({ value: a.id, label: a.name || a.id }))}
            />
          </div>
        </div>
      )}

      <p data-testid="skills-scope-hint" className="w-full text-xs text-text-tertiary">
        {hint}
      </p>
    </div>
  )
}
