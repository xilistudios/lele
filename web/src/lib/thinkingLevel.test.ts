import { describe, expect, test } from 'bun:test'
import { THINKING_LEVELS, thinkingLevelLabel, thinkingLevelOptions } from './thinkingLevel'

// t() echoes the key so tests assert on key usage, not on translations.
const t = (key: string) => key

describe('thinkingLevelOptions', () => {
  test('first option is the inherit sentinel (empty value)', () => {
    const opts = thinkingLevelOptions(t)
    expect(opts[0]).toEqual({ value: '', label: 'settings.options.thinkingInherit' })
  })

  test('offers exactly inherit + off/low/medium/high in order', () => {
    const opts = thinkingLevelOptions(t)
    expect(opts.map((o) => o.value)).toEqual(['', ...THINKING_LEVELS])
    expect(opts.map((o) => o.label)).toEqual([
      'settings.options.thinkingInherit',
      'settings.options.thinkingOff',
      'settings.options.thinkingLow',
      'settings.options.thinkingMedium',
      'settings.options.thinkingHigh',
    ])
  })
})

describe('thinkingLevelLabel', () => {
  test('empty/undefined render as inherit', () => {
    expect(thinkingLevelLabel(t, '')).toBe('settings.options.thinkingInherit')
    expect(thinkingLevelLabel(t, undefined)).toBe('settings.options.thinkingInherit')
  })

  test('known levels resolve to their option label', () => {
    expect(thinkingLevelLabel(t, 'off')).toBe('settings.options.thinkingOff')
    expect(thinkingLevelLabel(t, 'high')).toBe('settings.options.thinkingHigh')
  })

  test('unknown value falls back to the raw string (never crashes)', () => {
    expect(thinkingLevelLabel(t, 'turbo')).toBe('turbo')
  })
})
