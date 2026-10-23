/**
 * Tests for the key-prefix preselection helper (spec §2.4, plan T1).
 *
 * Mutation pin: the "spec prefixes" test below hardcodes the prefix→type
 * pairs instead of reading them from KEY_PREFIXES. Deleting (or renaming
 * the prefix of) a row from the table turns that test red — the
 * table-driven test above it cannot catch a deletion on its own because it
 * iterates whatever rows remain and would pass vacuously. Verified by hand
 * (2026-10-22): removing the `sk-ant-` row fails `maps every unambiguous
 * spec prefix to its provider type`.
 */
import { describe, expect, test } from 'bun:test'
import { KEY_PREFIXES, detectProviderFromKey } from './keyPrefix'

describe('detectProviderFromKey', () => {
  test('every row of KEY_PREFIXES maps to its own type (table-driven)', () => {
    for (const { prefix, type } of KEY_PREFIXES) {
      expect(detectProviderFromKey(`${prefix}abc123`)).toBe(type)
    }
  })

  test('maps every unambiguous spec prefix to its provider type', () => {
    expect(detectProviderFromKey('sk-ant-xyz')).toBe('anthropic')
    expect(detectProviderFromKey('sk-or-v1-xyz')).toBe('openrouter')
    expect(detectProviderFromKey('sk-proj-xyz')).toBe('openai')
    expect(detectProviderFromKey('gsk_xyz')).toBe('groq')
    expect(detectProviderFromKey('AIzaSyD-xyz')).toBe('gemini')
    expect(detectProviderFromKey('xai-xyz')).toBe('xai')
    expect(detectProviderFromKey('nvapi-xyz')).toBe('nvidia')
    expect(detectProviderFromKey('pplx-xyz')).toBe('perplexity')
  })

  test('ambiguous or empty keys detect nothing', () => {
    // A bare `sk-` must return null BY CONSTRUCTION: no row may have it.
    expect(KEY_PREFIXES.some((row) => row.prefix === 'sk-')).toBe(false)
    expect(detectProviderFromKey('sk-abcdef')).toBeNull()
    expect(detectProviderFromKey('sk_live_x')).toBeNull()
    expect(detectProviderFromKey('sk-')).toBeNull()
    expect(detectProviderFromKey('')).toBeNull()
    expect(detectProviderFromKey('   ')).toBeNull()
    // An env *name* is not a key; a pasted `export …` line is not supported.
    expect(detectProviderFromKey('ANTHROPIC_API_KEY')).toBeNull()
    expect(detectProviderFromKey('export ANTHROPIC_API_KEY=sk-ant-xyz')).toBeNull()
  })

  test('first match wins: overlapping rows keep their intended types', () => {
    expect(detectProviderFromKey('sk-ant-1')).toBe('anthropic') // not openai
    expect(detectProviderFromKey('sk-proj-1')).toBe('openai') // not openrouter
    expect(detectProviderFromKey('sk-svcacct-1')).toBe('openai')
    // The `sk-or-v1-` row shadows `sk-or-`; both answer openrouter anyway —
    // the test pins the outcome, not the row used.
    expect(detectProviderFromKey('sk-or-v1-abc')).toBe('openrouter')
    expect(detectProviderFromKey('sk-or-abc')).toBe('openrouter')
  })

  test('trims before matching (pasted keys carry whitespace)', () => {
    expect(detectProviderFromKey('  gsk_abc\n')).toBe('groq')
    expect(detectProviderFromKey(' xai-abc\n')).toBe('xai')
  })

  test('matching is case-sensitive', () => {
    expect(detectProviderFromKey('aiza')).toBeNull()
    expect(detectProviderFromKey('AIZA-xyz')).toBeNull()
    expect(detectProviderFromKey('SK-ANT-xyz')).toBeNull()
    expect(detectProviderFromKey('GSK_xyz')).toBeNull()
  })
})
