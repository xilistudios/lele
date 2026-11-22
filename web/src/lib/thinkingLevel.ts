export const THINKING_LEVELS = ['off', 'low', 'medium', 'high'] as const

export type ThinkingLevel = (typeof THINKING_LEVELS)[number]

type Translate = (key: string, options?: Record<string, unknown>) => string

export type ThinkingLevelOption = { value: string; label: string }

export function thinkingLevelOptions(t: Translate): ThinkingLevelOption[] {
  return [
    { value: '', label: t('settings.options.thinkingInherit') },
    { value: 'off', label: t('settings.options.thinkingOff') },
    { value: 'low', label: t('settings.options.thinkingLow') },
    { value: 'medium', label: t('settings.options.thinkingMedium') },
    { value: 'high', label: t('settings.options.thinkingHigh') },
  ]
}

export function thinkingLevelLabel(t: Translate, value?: string): string {
  if (!value) return t('settings.options.thinkingInherit')
  const opt = thinkingLevelOptions(t).find((o) => o.value === value)
  return opt ? opt.label : value
}
