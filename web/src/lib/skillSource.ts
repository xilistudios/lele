/**
 * Skill source presentation (spec §4.5.3).
 *
 * Single source of truth for the badge that says WHERE a skill comes from
 * (`workspace` / `global` / `builtin`). It used to live inline in
 * `organisms/SkillsList.tsx`; the per-agent Skills tab
 * (`organisms/agents/AgentSkillsSection.tsx`) renders the same badge, so both
 * consumers now import it from here instead of duplicating the palette.
 *
 * Values are literal — they must stay byte-identical to what `SkillsList`
 * shipped before the extraction (no visual change on the Skills page).
 */
import type { SkillSource } from './types'

/** Badge classes per skill source (Tailwind tokens only, no new CSS). */
export const SOURCE_COLORS: Record<string, string> = {
  workspace: 'bg-state-info-light text-state-info border-state-info/30',
  global: 'bg-state-success-light text-state-success border-state-success/30',
  builtin: 'bg-surface-muted text-text-tertiary border-border/50',
}

/** Display label per skill source. Kept as-is: proper nouns of the loader. */
export const SOURCE_LABELS: Record<string, string> = {
  workspace: 'Workspace',
  global: 'Global',
  builtin: 'Built-in',
}

/** Neutral fallback for a source the frontend does not know yet. */
export const SOURCE_FALLBACK = 'bg-surface-muted text-text-tertiary border-border/50'

/** Badge classes for a source; unknown sources fall back to the neutral style. */
export function sourceBadgeClasses(source?: SkillSource | string): string {
  return (source && SOURCE_COLORS[source]) || SOURCE_FALLBACK
}

/** Badge label for a source; unknown sources are shown verbatim (never hidden). */
export function sourceBadgeLabel(source?: SkillSource | string): string {
  return (source && SOURCE_LABELS[source]) || source || ''
}

/**
 * The full class string of a source badge, identical to what
 * `organisms/SkillsList.tsx` renders (base chrome + per-source colors).
 */
export function sourceBadgeClassNames(source?: SkillSource | string): string {
  return `inline-flex items-center rounded-md px-1.5 py-0.5 text-[10px] font-medium border ${sourceBadgeClasses(source)}`
}
