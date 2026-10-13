import { useTranslation } from 'react-i18next'
import { SKILLS_VIEWS, type SkillsView, viewHintKey } from '../../lib/skillsScope'
import type { Agent } from '../../lib/types'
// `SegmentedControl` is intentionally NOT re-exported from `molecules/index.ts`
// (it is a spec-internal molecule), so it is imported from its own module —
// same import in the rest of the codebase. `SelectInput` is exported, but we
// keep both from the source file for consistency (plan §4.3).
import { SegmentedControl } from '../molecules/SegmentedControl'
import { SelectInput } from '../molecules/SelectInput'

/**
 * The scope bar of the `/skills` page (plan T4): the All/Global/By-agent
 * segmented control, the agent selector shown only in the agent view, and the
 * one-line hint under both.
 *
 * The component is fully CONTROLLED and deliberately dumb — it owns no data
 * fetching, no URL state and no view semantics (those live in
 * `lib/skillsScope.ts` and in `SkillsPage`, which is T5). Everything it needs
 * arrives as props; everything the user does goes back out as a callback.
 */
type Props = {
  view: SkillsView
  onViewChange: (view: SkillsView) => void
  agents: Agent[]
  selectedAgentId: string | null
  onAgentChange: (agentId: string) => void
}

/**
 * Inline English defaults (house style: every `t()` carries its fallback).
 * The `{{agent}}` placeholder in `skills.viewAgentHint` is filled below.
 */
const HINT_DEFAULTS: Record<ReturnType<typeof viewHintKey>, string> = {
  'skills.viewAllHint':
    "Everything the default workspace, the shared global directory and lele's built-ins provide.",
  'skills.viewGlobalHint': 'Shared by every agent — installed in ~/.lele/skills.',
  'skills.viewAgentHint':
    'What {{agent}} can use: its own workspace plus the shared skills. Enable/disable applies to this agent only.',
}

/** Inline English defaults for the three tab labels. */
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

  // The agent hint interpolates the display name (`name || id`, the same rule
  // as the <option> labels). With no resolved agent we must NOT leave the raw
  // `{{agent}}` placeholder on screen, and there is no browser-default to fall
  // back on — so we feed the neutral `skills.selectAgent` label ("Agent"). The
  // page owns the real empty state for "no agents" (D11), not this bar.
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
          {/* The visible label is the select's ONLY accessible name:
              `SelectInput` takes no `aria-label`/`data-testid` (C8). */}
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
