import { describe, expect, test } from 'bun:test'
import en from './locales/en.json'
import es from './locales/es.json'
import pt from './locales/pt.json'

type Json = Record<string, unknown>

/**
 * Parity for the per-model reasoning override copy added to the provider model
 * editor (settings.fields.modelReasoning*, their settings.descriptions.*
 * siblings and settings.options.reasoningDefault).
 *
 * Same rationale as add-provider-keys.test.ts: web i18n falls back to 'es', so
 * a key missing from es.json renders the raw key for every local user, and a
 * key missing from pt.json is invisible because the Spanish fallback answers
 * silently. This is the only thing that catches either before it ships.
 */

const PREFIXES = ['settings.fields.modelReasoning', 'settings.descriptions.modelReasoning'] as const
const OPTION_KEYS = ['settings.options.reasoningDefault'] as const

/** Flatten a nested object into dotted paths ("a.b", "a.c", …). */
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

/** Every locale key of the reasoning group plus the shared option key. */
function reasoningKeys(locale: Json): string[] {
  const all = flatten('', locale)
  return all
    .filter((key) => PREFIXES.some((prefix) => key.startsWith(prefix)))
    .concat(all.filter((key) => OPTION_KEYS.some((key2) => key === key2)))
    .sort()
}

/** Keys present in `a` but not in `b`, one per line ('' when there are none). */
function missing(a: string[], b: string[]): string {
  const set = new Set(b)
  return a.filter((key) => !set.has(key)).join('\n')
}

const enKeys = reasoningKeys(en as Json)
const esKeys = reasoningKeys(es as Json)
const ptKeys = reasoningKeys(pt as Json)

// The four controls the model editor renders, one key each.
const EXPECTED = [
  'settings.fields.modelReasoningEffort',
  'settings.fields.modelReasoningSummary',
  'settings.fields.modelReasoningMaxTokens',
  'settings.fields.modelReasoningExclude',
]

describe('settings.*.modelReasoning* i18n key parity', () => {
  test('the three locales carry every reasoning editor key', () => {
    for (const [name, keys] of [
      ['en', enKeys],
      ['es', esKeys],
      ['pt', ptKeys],
    ] as const) {
      expect({ locale: name, missing: missing(EXPECTED, keys) }).toEqual({
        locale: name,
        missing: '',
      })
    }
  })

  test('en, es and pt expose the exact same reasoning key set (both directions)', () => {
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

  test('the option and description keys exist in all three locales', () => {
    for (const keys of [enKeys, esKeys, ptKeys]) {
      expect(keys).toContain('settings.options.reasoningDefault')
      expect(
        keys.filter((key) => key.startsWith('settings.descriptions.modelReasoning')),
      ).toHaveLength(4)
    }
  })
})
