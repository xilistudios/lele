import { describe, expect, test } from 'bun:test'
import en from './locales/en.json'
import es from './locales/es.json'
import pt from './locales/pt.json'

type Json = Record<string, unknown>

function flatten(prefix: string, value: unknown, out: string[] = []): string[] {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    for (const [key, child] of Object.entries(value as Json)) {
      flatten(prefix ? `${prefix}.${key}` : key, child, out)
    }
  } else {
    out.push(prefix)
  }
  return out
}

function settingsKeys(locale: Json): string[] {
  const settings = locale.settings
  if (!settings || typeof settings !== 'object') return []
  return flatten('settings', settings).sort()
}

function settingsValues(locale: Json): Map<string, unknown> {
  const values = new Map<string, unknown>()
  const settings = locale.settings
  if (!settings || typeof settings !== 'object') return values
  const walk = (prefix: string, value: unknown): void => {
    if (value && typeof value === 'object' && !Array.isArray(value)) {
      for (const [key, child] of Object.entries(value as Json)) {
        walk(`${prefix}.${key}`, child)
      }
      return
    }
    values.set(prefix, value)
  }
  walk('settings', settings)
  return values
}

function missing(a: string[], b: string[]): string {
  const set = new Set(b)
  return a.filter((key) => !set.has(key)).join('\n')
}

const LOCALES: Record<string, Json> = { en: en as Json, es: es as Json, pt: pt as Json }
const enKeys = settingsKeys(en as Json)
const esKeys = settingsKeys(es as Json)
const ptKeys = settingsKeys(pt as Json)

const NEW_TAB_KEYS = [
  'settings.tabs.subagents',
  'settings.tabs.mcp',
  'settings.sections.subagentDefaults',
  'settings.fields.subagentTimeout',
  'settings.fields.subagentMaxConcurrent',
  'settings.fields.subagentMaxRetries',
  'settings.fields.subagentMaxIterations',
  'settings.fields.subagentRetention',
  'settings.descriptions.subagentDefaults',
  'settings.descriptions.subagentTimeout',
  'settings.descriptions.subagentMaxConcurrent',
  'settings.descriptions.subagentMaxRetries',
  'settings.descriptions.subagentMaxIterations',
  'settings.descriptions.subagentRetention',
  'settings.subagentsPage.agentLabel',
  'settings.subagentsPage.openAgent',
  'settings.subagentsPage.noAgents',
  'settings.mcpPage.agentLabel',
  'settings.mcpPage.intro',
  'settings.mcpPage.unsavedAgent',
  'settings.mcpPage.implicitMain',
]

describe('settings.* i18n key parity', () => {
  test('the three locales carry at least one settings.* key', () => {
    expect(enKeys.length).toBeGreaterThan(0)
    expect(esKeys.length).toBeGreaterThan(0)
    expect(ptKeys.length).toBeGreaterThan(0)
  })

  test('en, es and pt expose the exact same settings.* key set (both directions)', () => {
    const diffs = {
      'en → es': missing(enKeys, esKeys),
      'es → en': missing(esKeys, enKeys),
      'en → pt': missing(enKeys, ptKeys),
      'pt → en': missing(ptKeys, enKeys),
      'es → pt': missing(esKeys, ptKeys),
      'pt → es': missing(ptKeys, esKeys),
    }
    const report = Object.entries(diffs)
      .filter(([, keys]) => keys !== '')
      .map(([direction, keys]) => `${direction} missing:\n${keys}`)
      .join('\n\n')

    expect(report).toBe('')
  })

  test('every key the new settings pages render is present in all three locales', () => {
    for (const keys of [enKeys, esKeys, ptKeys]) {
      for (const key of NEW_TAB_KEYS) expect(keys).toContain(key)
    }
  })

  test('every key the new settings pages render is a non-empty string in all three locales', () => {
    const problems: string[] = []
    for (const [name, locale] of Object.entries(LOCALES)) {
      const values = settingsValues(locale)
      for (const key of NEW_TAB_KEYS) {
        const value = values.get(key)
        if (typeof value !== 'string' || value.trim() === '') problems.push(`${name}: ${key}`)
      }
    }
    expect(problems.join('\n')).toBe('')
  })
})