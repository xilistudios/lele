import { describe, expect, test } from 'bun:test'
import en from './locales/en.json'
import es from './locales/es.json'
import pt from './locales/pt.json'

type Json = Record<string, unknown>

/** Flatten a nested object into dotted paths ("view.all", "source.global", …). */
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

/** Sorted list of every `settings.addProviderModal.*` key of a locale. */
function addProviderKeys(locale: Json): string[] {
  const settings = locale.settings
  if (!settings || typeof settings !== 'object') return []
  const modal = (settings as Json).addProviderModal
  if (!modal || typeof modal !== 'object') return []
  return flatten('settings.addProviderModal', modal).sort()
}

/** Keys present in `a` but not in `b`, one per line ('' when there are none). */
function missing(a: string[], b: string[]): string {
  const set = new Set(b)
  return a.filter((key) => !set.has(key)).join('\n')
}

const enKeys = addProviderKeys(en as Json)
const esKeys = addProviderKeys(es as Json)
const ptKeys = addProviderKeys(pt as Json)

describe('settings.addProviderModal.* i18n key parity', () => {
  test('the three locales carry at least one settings.addProviderModal.* key', () => {
    // Guards the whole suite against a silently empty flatten (e.g. a renamed
    // top-level key), which would make every parity assertion vacuously true.
    expect(enKeys.length).toBeGreaterThan(0)
  })

  test('en, es and pt expose the exact same settings.addProviderModal.* key set (both directions)', () => {
    // Tests boot in Spanish, so a key missing from es.json renders as the raw
    // key; a key missing from pt.json is invisible locally. This is the only
    // thing that catches either before it ships.
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
})
