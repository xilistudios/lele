/**
 * Unit tests for the chat row rhythm (lib/chatRowSpacing.ts).
 *
 * The contract under test is the padding MATRIX, not the implementation:
 *   - tool rows and user rows (bubbles and system notices alike) are compact;
 *   - an assistant row is content ONLY when it carries visible text — a row
 *     holding just a thinking card or the typing dots is activity, not prose,
 *     and must not steal the prose rhythm (that was the irregularity).
 *
 * `ChatMessage['content']` is a plain string (lib/types.ts), so the fixtures
 * below are valid `Pick<ChatMessage, 'role' | 'content'>` values and no message
 * builder is needed.
 */
import { describe, expect, test } from 'bun:test'
import { CHAT_ROW_PADDING, type ChatRowKind, chatRowKind, chatRowPadding } from './chatRowSpacing'

/** Every case the row padding must cover, as data. */
const matrix: ReadonlyArray<{
  name: string
  role: 'user' | 'assistant' | 'tool'
  content: string
  kind: ChatRowKind
  padding: string
}> = [
  { name: 'tool with content', role: 'tool', content: 'x', kind: 'compact', padding: 'py-1.5' },
  { name: 'tool without content', role: 'tool', content: '', kind: 'compact', padding: 'py-1.5' },
  { name: 'user with content', role: 'user', content: 'hola', kind: 'compact', padding: 'py-1.5' },
  {
    name: 'user without content',
    role: 'user',
    content: '',
    kind: 'compact',
    padding: 'py-1.5',
  },
  {
    name: 'user whitespace only',
    role: 'user',
    content: ' \n ',
    kind: 'compact',
    padding: 'py-1.5',
  },
  {
    name: 'assistant prose',
    role: 'assistant',
    content: 'hello',
    kind: 'content',
    padding: 'py-3',
  },
  {
    name: 'assistant prose padded with whitespace',
    role: 'assistant',
    content: '  hello  ',
    kind: 'content',
    padding: 'py-3',
  },
  {
    name: 'assistant empty (thinking-only row)',
    role: 'assistant',
    content: '',
    kind: 'compact',
    padding: 'py-1.5',
  },
  {
    name: 'assistant whitespace only',
    role: 'assistant',
    content: ' \n ',
    kind: 'compact',
    padding: 'py-1.5',
  },
]

describe('chatRowPadding — row kind matrix', () => {
  for (const { name, role, content, kind, padding } of matrix) {
    test(`${name} → ${padding}`, () => {
      expect(chatRowPadding({ role, content })).toBe(padding)
      expect(chatRowKind({ role, content })).toBe(kind)
    })
  }
})

describe('chatRowPadding — tool rows ignore content', () => {
  for (const content of ['x', '', '  \n  ', 'a'.repeat(500)]) {
    test(`tool + ${JSON.stringify(content)} → compact`, () => {
      expect(chatRowPadding({ role: 'tool', content })).toBe(CHAT_ROW_PADDING.compact)
    })
  }
})

describe('chatRowPadding — user rows ignore content', () => {
  for (const content of ['hola', '', ' \n ']) {
    test(`user + ${JSON.stringify(content)} → compact`, () => {
      expect(chatRowPadding({ role: 'user', content })).toBe(CHAT_ROW_PADDING.compact)
    })
  }
})

describe('chatRowPadding — assistant rows depend on VISIBLE text', () => {
  test('"hello" is prose → py-3', () => {
    expect(chatRowPadding({ role: 'assistant', content: 'hello' })).toBe('py-3')
  })

  test('"  hello  " trims to prose → py-3', () => {
    expect(chatRowPadding({ role: 'assistant', content: '  hello  ' })).toBe('py-3')
  })

  test('"" (thinking card / typing dots only) → py-1.5', () => {
    expect(chatRowPadding({ role: 'assistant', content: '' })).toBe('py-1.5')
  })

  test('" \\n " renders nothing → py-1.5', () => {
    expect(chatRowPadding({ role: 'assistant', content: ' \n ' })).toBe('py-1.5')
  })

  test('"\\n\\n\\n" renders nothing → py-1.5', () => {
    expect(chatRowPadding({ role: 'assistant', content: '\n\n\n' })).toBe('py-1.5')
  })
})

describe('CHAT_ROW_PADDING — the two rhythms stay distinct', () => {
  test('compact and content must never be silently unified', () => {
    expect(CHAT_ROW_PADDING.compact).not.toBe(CHAT_ROW_PADDING.content)
    expect(CHAT_ROW_PADDING).toEqual({ compact: 'py-1.5', content: 'py-3' })
  })

  /** Pixels out of a tailwind vertical-padding class ('py-1.5' → 1.5). Parsing
   *  the tail with slice() would drop the integer part ('py-3.5'.slice(4) is
   *  '.5'), so an inversion like content < compact could slip through. */
  const px = (cls: string) => Number.parseFloat(cls.replace('py-', ''))

  test('both are real vertical paddings (a typo like "p-1.5" would not set the rhythm)', () => {
    for (const padding of [CHAT_ROW_PADDING.compact, CHAT_ROW_PADDING.content]) {
      expect(padding.startsWith('py-')).toBe(true)
    }
    // content > compact, otherwise prose rows would sit tighter than activity.
    expect(px(CHAT_ROW_PADDING.content)).toBeGreaterThan(px(CHAT_ROW_PADDING.compact))
  })
})

describe('chatRowKind — agrees with the padding mapping', () => {
  test('for each role, chatRowKind maps to chatRowPadding through CHAT_ROW_PADDING', () => {
    for (const role of ['user', 'assistant', 'tool'] as const) {
      for (const content of ['', 'x', ' \n ']) {
        const message = { role, content }
        // widened to string: CHAT_ROW_PADDING[] is the literal union, while
        // chatRowPadding's declared return type is string.
        const fromKind: string = CHAT_ROW_PADDING[chatRowKind(message)]
        expect(fromKind).toBe(chatRowPadding(message))
      }
    }
  })

  test('kind is one of the two declared values', () => {
    for (const { role, content } of matrix) {
      const kind = chatRowKind({ role, content })
      expect(kind === 'compact' || kind === 'content').toBe(true)
    }
  })

  test('the matrix above is itself self-consistent', () => {
    for (const { role, content, kind, padding } of matrix) {
      expect(chatRowKind({ role, content })).toBe(kind)
      expect(chatRowPadding({ role, content })).toBe(padding)
    }
  })
})
