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

function skillsKeys(locale: Json): string[] {
  const skills = locale.skills
  if (!skills || typeof skills !== 'object') return []
  return flatten('skills', skills).sort()
}

function missing(a: string[], b: string[]): string {
  const set = new Set(b)
  return a.filter((key) => !set.has(key)).join('\n')
}

const enKeys = skillsKeys(en as Json)
const esKeys = skillsKeys(es as Json)
const ptKeys = skillsKeys(pt as Json)

describe('skills.* i18n key parity', () => {
  test('the three locales carry at least one skills.* key', () => {
    expect(enKeys.length).toBeGreaterThan(0)
  })

  test('en, es and pt expose the exact same skills.* key set (both directions)', () => {
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
