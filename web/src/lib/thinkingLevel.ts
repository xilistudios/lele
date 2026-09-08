/**
 * Shared helpers for the per-agent / global "thinking level" setting
 * (`agents.list[i].thinking_level` and `agents.defaults.thinking_level`).
 *
 * Wire contract (backend `pkg/config`):
 *   - absent (undefined)  -> inherit: use the model's own reasoning configuration
 *   - "off"               -> reasoning explicitly disabled
 *   - "low"|"medium"|"high" -> reasoning enabled with that effort
 *
 * The UI represents "inherit" as the empty string '' (same sentinel as the TUI
 * selector's "(default)" option) and clears the field by writing `undefined`
 * through `updateField(path, v || undefined)`.
 */

/** Accepted non-empty values for thinking_level. */
export const THINKING_LEVELS = ['off', 'low', 'medium', 'high'] as const

export type ThinkingLevel = (typeof THINKING_LEVELS)[number]

type Translate = (key: string, options?: Record<string, unknown>) => string

export type ThinkingLevelOption = { value: string; label: string }

/**
 * Build the option list for the thinking-level select. The first entry
 * ('' = inherit/default) must stay first so the field can be cleared back to
 * "not set" (mirrors the TUI `(default)` sentinel, review item R4).
 */
export function thinkingLevelOptions(t: Translate): ThinkingLevelOption[] {
  return [
    { value: '', label: t('settings.options.thinkingInherit') },
    { value: 'off', label: t('settings.options.thinkingOff') },
    { value: 'low', label: t('settings.options.thinkingLow') },
    { value: 'medium', label: t('settings.options.thinkingMedium') },
    { value: 'high', label: t('settings.options.thinkingHigh') },
  ]
}

/** Human-readable label for a stored value (''/unknown -> inherit). */
export function thinkingLevelLabel(t: Translate, value?: string): string {
  if (!value) return t('settings.options.thinkingInherit')
  const opt = thinkingLevelOptions(t).find((o) => o.value === value)
  return opt ? opt.label : value
}
