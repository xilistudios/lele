import { describe, expect, test } from 'bun:test'
import en from './locales/en.json'
import es from './locales/es.json'
import pt from './locales/pt.json'

type Json = Record<string, unknown>

const CHAT_KEYS = ['viewImage', 'previousImage', 'nextImage'] as const

function chatKeys(locale: Json): string[] {
  const chat = locale.chat
  if (!chat || typeof chat !== 'object') return []
  const record = chat as Json
  return CHAT_KEYS.filter((key) => typeof record[key] === 'string').sort()
}

function chatValue(locale: Json, key: string): string {
  return ((locale.chat ?? {}) as Json)[key] as string
}

const contexts = [
  { name: 'en', locale: en as Json },
  { name: 'es', locale: es as Json },
  { name: 'pt', locale: pt as Json },
]

describe.each(contexts)('$name chat image keys', ({ locale }) => {
  test('carries every chat image key', () => {
    expect(chatKeys(locale)).toEqual([...CHAT_KEYS].sort())
  })

  test('every chat image string is non-empty', () => {
    for (const key of CHAT_KEYS) {
      expect(chatValue(locale, key).trim().length).toBeGreaterThan(0)
    }
  })
})

describe('chat image key parity across locales', () => {
  test('en, es and pt expose the exact same chat image key set', () => {
    const enKeys = chatKeys(en as Json)
    const esKeys = chatKeys(es as Json)
    const ptKeys = chatKeys(pt as Json)

    expect(enKeys).toEqual(esKeys)
    expect(enKeys).toEqual(ptKeys)
  })
})
