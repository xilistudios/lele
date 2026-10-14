import { describe, expect, test } from 'bun:test'
import en from './locales/en.json'
import es from './locales/es.json'
import pt from './locales/pt.json'

type Json = Record<string, unknown>

/** Flatten a nested object into dotted paths ("auth.button", "editor.save", …). */
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

/** Sorted list of every `mcp.*` key (nested ones included) of a locale. */
function mcpKeys(locale: Json): string[] {
  const mcp = locale.mcp
  if (!mcp || typeof mcp !== 'object') return []
  return flatten('mcp', mcp).sort()
}

/** Keys present in `a` but not in `b`, one per line ('' when there are none). */
function missing(a: string[], b: string[]): string {
  const set = new Set(b)
  return a.filter((key) => !set.has(key)).join('\n')
}

const enKeys = mcpKeys(en as Json)
const esKeys = mcpKeys(es as Json)
const ptKeys = mcpKeys(pt as Json)

describe('mcp.* i18n key parity', () => {
  test('the three locales carry at least one mcp.* key', () => {
    // Guards the whole suite against a silently empty flatten (e.g. a renamed
    // top-level key), which would make every parity assertion vacuously true.
    expect(enKeys.length).toBeGreaterThan(0)
  })

  test('en, es and pt expose the exact same mcp.* key set (both directions)', () => {
    // Web i18n parity is currently unenforced and `fallbackLng` is 'es'
    // (src/i18n/index.ts): a key added to en.json but forgotten in es.json
    // renders as the raw key in the locale everyone actually boots, and a key
    // missing from pt.json is invisible locally because the Spanish fallback
    // answers for it silently. This test is the only thing that catches either
    // before it ships — same rationale as skills-keys.test.ts.
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

  test('the mcp.auth group is present in all three locales', () => {
    // The OAuth keys are the ones this task adds; the parity test above would
    // stay green if ALL THREE locales lost the group together, so pin it.
    for (const keys of [enKeys, esKeys, ptKeys]) {
      expect(keys.filter((key) => key.startsWith('mcp.auth.')).length).toBeGreaterThan(0)
    }
  })
})
