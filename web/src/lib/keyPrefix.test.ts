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
    expect(KEY_PREFIXES.some((row) => row.prefix === 'sk-')).toBe(false)
    expect(detectProviderFromKey('sk-abcdef')).toBeNull()
    expect(detectProviderFromKey('sk_live_x')).toBeNull()
    expect(detectProviderFromKey('sk-')).toBeNull()
    expect(detectProviderFromKey('')).toBeNull()
    expect(detectProviderFromKey('   ')).toBeNull()
    expect(detectProviderFromKey('ANTHROPIC_API_KEY')).toBeNull()
    expect(detectProviderFromKey('export ANTHROPIC_API_KEY=sk-ant-xyz')).toBeNull()
  })

  test('first match wins: overlapping rows keep their intended types', () => {
    expect(detectProviderFromKey('sk-ant-1')).toBe('anthropic')
    expect(detectProviderFromKey('sk-proj-1')).toBe('openai')
    expect(detectProviderFromKey('sk-svcacct-1')).toBe('openai')
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
