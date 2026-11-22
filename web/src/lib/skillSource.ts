import type { SkillSource } from './types'

export const SOURCE_COLORS: Record<string, string> = {
  workspace: 'bg-state-info/10 text-state-info border-state-info/30',
  global: 'bg-state-success/10 text-state-success border-state-success/30',
  builtin: 'bg-background-tertiary text-text-tertiary border-border',
}

export const SOURCE_LABELS: Record<string, string> = {
  workspace: 'Workspace',
  global: 'Global',
  builtin: 'Built-in',
}

export const SOURCE_FALLBACK = 'bg-background-tertiary text-text-tertiary border-border'

export function sourceBadgeClasses(source?: SkillSource | string): string {
  return (source && SOURCE_COLORS[source]) || SOURCE_FALLBACK
}

export function sourceBadgeLabel(source?: SkillSource | string): string {
  return (source && SOURCE_LABELS[source]) || source || ''
}

export function sourceBadgeClassNames(source?: SkillSource | string): string {
  return `inline-flex items-center rounded-md px-1.5 py-0.5 text-2xs font-medium border ${sourceBadgeClasses(source)}`
}
