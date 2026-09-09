import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { AgentAvatar } from '../atoms/AgentAvatar'

/**
 * Agent chip multi-select (spec §7.6) — "which agents may this one delegate
 * to?".
 *
 * Replaces the generic `StringListEditor` here: the candidate set is small and
 * static, so showing every agent as a chip and tapping it is strictly better
 * than adding names one by one from a dropdown.
 *
 * Order follows `agents.list` — never re-sorted, because dirty paths are
 * positional (`agents.list.{index}.…`).
 */

type Props = {
  /** DOM id of the group (also used to build each chip's id). */
  id: string
  /** Other agents (never the agent being edited). */
  agents: Array<{ id: string; name?: string }>
  value: string[]
  onChange: (value: string[]) => void
  disabled?: boolean
  /** Block shown when there are no candidates (dashed empty state). */
  empty?: ReactNode
  /** Accessible name of the group; defaults to the i18n "Allowed agents". */
  ariaLabel?: string
}

const WRAPPER_CLS = 'flex flex-wrap gap-1.5'

const CHIP_BASE_CLS =
  'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition-colors duration-fast'
const CHIP_ON_CLS = 'border-interaction-primary/40 bg-accent-subtle text-text-primary'
const CHIP_OFF_CLS =
  'border-border bg-background-secondary text-text-tertiary hover:border-border-strong'
const CHIP_DISABLED_CLS = 'opacity-40 cursor-not-allowed'

export function AgentChipMultiSelect({
  id,
  agents,
  value,
  onChange,
  disabled = false,
  empty,
  ariaLabel,
}: Props) {
  const { t } = useTranslation()

  if (agents.length === 0) {
    return (
      <div
        className="rounded-lg border-2 border-dashed border-border bg-background-secondary/20 px-4 py-6 text-center text-xs text-text-secondary"
        data-testid={`${id}-empty`}
      >
        {empty ??
          t('settings.agentPage.noOtherAgents', {
            defaultValue: 'No other agents to delegate to yet.',
          })}
      </div>
    )
  }

  const toggle = (agentId: string) => {
    if (disabled) return
    const next = value.includes(agentId)
      ? value.filter((entry) => entry !== agentId)
      : [...value, agentId]
    onChange(next)
  }

  return (
    <div
      id={id}
      // biome-ignore lint/a11y/useSemanticElements: spec §7.6 groups role=checkbox chips; a <fieldset> cannot carry the flex-wrap pill-row styling and its <legend> would add a visible caption the design does not want
      role="group"
      aria-label={
        ariaLabel ?? t('settings.agentPage.subagentsAllowed', { defaultValue: 'Allowed agents' })
      }
      className={WRAPPER_CLS}
    >
      {agents.map((agent) => {
        const checked = value.includes(agent.id)
        return (
          <button
            key={agent.id}
            id={`${id}-${agent.id}`}
            type="button"
            // biome-ignore lint/a11y/useSemanticElements: spec §7.6 — the chip embeds an avatar element, which is invalid inside a native <input type=checkbox>; it is a toggle, not a form field
            role="checkbox"
            aria-checked={checked}
            aria-label={agent.name ? `${agent.id} (${agent.name})` : agent.id}
            title={agent.name ?? agent.id}
            disabled={disabled}
            onClick={() => toggle(agent.id)}
            className={`${CHIP_BASE_CLS} ${checked ? CHIP_ON_CLS : CHIP_OFF_CLS} ${disabled ? CHIP_DISABLED_CLS : ''}`}
          >
            <AgentAvatar id={agent.id} name={agent.name} size="sm" />
            <span className="font-mono">{agent.id}</span>
          </button>
        )
      })}
    </div>
  )
}
