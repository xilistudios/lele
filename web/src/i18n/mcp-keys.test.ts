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

function mcpKeys(locale: Json): string[] {
  const mcp = locale.mcp
  if (!mcp || typeof mcp !== 'object') return []
  return flatten('mcp', mcp).sort()
}

function missing(a: string[], b: string[]): string {
  const set = new Set(b)
  return a.filter((key) => !set.has(key)).join('\n')
}

const enKeys = mcpKeys(en as Json)
const esKeys = mcpKeys(es as Json)
const ptKeys = mcpKeys(pt as Json)

describe('mcp.* i18n key parity', () => {
  test('the three locales carry at least one mcp.* key', () => {
    expect(enKeys.length).toBeGreaterThan(0)
  })

  test('en, es and pt expose the exact same mcp.* key set (both directions)', () => {
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
    for (const keys of [enKeys, esKeys, ptKeys]) {
      expect(keys.filter((key) => key.startsWith('mcp.auth.')).length).toBeGreaterThan(0)
    }
  })
})
