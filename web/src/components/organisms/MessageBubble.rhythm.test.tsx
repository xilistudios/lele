/**
 * Regression tests for the chat log's VERTICAL RHYTHM (lib/chatRowSpacing.ts as
 * seen through the real component).
 *
 * Each row wrapper ([data-testid="message"]) used to hardcode its own vertical
 * padding — user `py-1`, tool and system notice `py-1.5`, assistant `py-3` even
 * when it held nothing but a thinking card or the typing dots — so the gap
 * between two consecutive rows depended on which kinds happened to be adjacent
 * (10 / 12 / 16 / 18 / 24px). All four wrappers now ask chatRowPadding(message),
 * which yields exactly two rhythms: compact `py-1.5` (tool, user, system notice,
 * assistant with no visible text) and content `py-3` (assistant with text).
 *
 * These cases render the component so a wrapper that forgets the helper, or
 * reorders it, goes red — the pure unit tests in lib/chatRowSpacing.test.ts
 * cannot see the JSX. Which wrappers that actually means: the helper only
 * CHANGES the user row (`py-1` → compact) and the assistant row (`py-3`, plus
 * compact once it holds no visible text), so those are the ones a regression
 * here catches. The tool row and the system notice were already `py-1.5`, so
 * hard-coding that value instead of calling the helper would still be green —
 * their cases document the rhythm, they are not mutation tripwires.
 *
 * NOTE on the assertions: `toHaveClass` is a jest-dom matcher and bun's
 * `expect` does not carry its typings (tsconfig types = bun-types), so the
 * class checks go through classList.contains below — the same convention the
 * suite already uses (settings/AddProviderModal.test.tsx). They are strict
 * token checks: `py-1` never matches the `py-1.5` class.
 *
 * Conventions (imports/mocks/i18n/helpers) mirror
 * MessageBubble.visibility.test.tsx in this folder.
 */
import '../../test/setup'
import '../../test/i18n'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { render } from '@testing-library/react'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { CHAT_ROW_PADDING, chatRowPadding } from '../../lib/chatRowSpacing'
import type { ChatMessage } from '../../lib/types'
import { MessageBubble } from './MessageBubble'

const originalCreateObjectURL = URL.createObjectURL
const originalRevokeObjectURL = URL.revokeObjectURL
const originalIntersectionObserver = globalThis.IntersectionObserver

function makeAuthCtx(): AuthContextValue {
  const fileBlob = mock(async () => new Blob(['test'], { type: 'image/png' }))
  return {
    api: { fileBlob },
    apiUrl: 'http://127.0.0.1:18793',
    session: null,
    setApiUrl: () => {},
    persistSession: () => {},
    handleAuth: async () => ({ token: '', refresh_token: '', device_name: '', client_id: '' }),
    ensureSession: async () => null,
    isLoading: false,
  } as unknown as AuthContextValue
}

function renderBubble(message: ChatMessage, ctx: AuthContextValue, isLast = false) {
  return render(
    <AuthContext.Provider value={ctx}>
      <MessageBubble message={message} isLast={isLast} onNavigateToSession={() => {}} />
    </AuthContext.Provider>,
  )
}

function makeMessage(overrides: Partial<ChatMessage> & { role: ChatMessage['role'] }): ChatMessage {
  return {
    id: `msg-${overrides.role}`,
    content: '',
    streaming: false,
    createdAt: new Date().toISOString(),
    ...overrides,
  }
}

/** The row wrapper that carries the vertical padding. */
function rowWrapper(getByTestId: (id: string) => HTMLElement): HTMLElement {
  return getByTestId('message')
}

/** Assert the wrapper's padding token is exactly `expected`, and that the other
 *  rhythm is absent — a row must never mix both. `expected` is always a literal
 *  here on purpose: deriving it from chatRowPadding() would make the check
 *  circular (test 6 is the one that pins component ↔ helper agreement). */
function expectPadding(row: HTMLElement, expected: 'py-1.5' | 'py-3'): void {
  const paddings = row.className
    .split(/\s+/)
    .filter((c) => c === CHAT_ROW_PADDING.compact || c === CHAT_ROW_PADDING.content)
  expect(paddings).toEqual([expected])
}

const ctx = makeAuthCtx()

beforeEach(() => {
  // @ts-expect-error removing so AuthedImage loads immediately in tests
  globalThis.IntersectionObserver = undefined
  URL.createObjectURL = mock((_blob: Blob) => 'blob:test-url/msg') as typeof URL.createObjectURL
  URL.revokeObjectURL = mock(() => {}) as typeof URL.revokeObjectURL
})

afterEach(() => {
  URL.createObjectURL = originalCreateObjectURL
  URL.revokeObjectURL = originalRevokeObjectURL
  globalThis.IntersectionObserver = originalIntersectionObserver
})

describe('MessageBubble — vertical rhythm of the row wrappers', () => {
  test('1. tool row → compact (py-1.5), never the prose rhythm', () => {
    const message = makeMessage({
      role: 'tool',
      content: 'exit 0',
      toolName: 'exec',
      toolStatus: 'completed',
    })
    const { getByTestId } = renderBubble(message, ctx)

    const row = rowWrapper(getByTestId)
    expect(row.classList.contains('py-1.5')).toBe(true)
    expect(row.classList.contains('py-3')).toBe(false)
    expectPadding(row, 'py-1.5')
  })

  test('2. assistant row with text → content (py-3), never compact', () => {
    const message = makeMessage({ role: 'assistant', content: 'hello' })
    const { getByTestId } = renderBubble(message, ctx)

    const row = rowWrapper(getByTestId)
    expect(row.classList.contains('py-3')).toBe(true)
    expect(row.classList.contains('py-1.5')).toBe(false)
    expectPadding(row, 'py-3')
  })

  test('3. assistant reasoning-only row → compact: a thinking card is not prose', () => {
    const { getByTestId } = renderBubble(
      makeMessage({ role: 'assistant', content: '', reasoningContent: 'why' }),
      ctx,
    )

    const row = rowWrapper(getByTestId)
    expect(row.classList.contains('py-1.5')).toBe(true)
    expect(row.classList.contains('py-3')).toBe(false)
    expectPadding(row, 'py-1.5')
  })

  test('3b. assistant row showing only the typing dots → compact', () => {
    const { getByTestId } = renderBubble(
      makeMessage({ role: 'assistant', content: '', streaming: true }),
      ctx,
      true,
    )

    const row = rowWrapper(getByTestId)
    expect(row.classList.contains('py-1.5')).toBe(true)
    expect(row.classList.contains('py-3')).toBe(false)
    expectPadding(row, 'py-1.5')
  })

  test('4. user row → compact, and keeps its alignment classes', () => {
    const message = makeMessage({ role: 'user', content: 'hola' })
    const { getByTestId } = renderBubble(message, ctx)

    const row = rowWrapper(getByTestId)
    expect(row.classList.contains('py-1.5')).toBe(true)
    expect(row.classList.contains('py-3')).toBe(false)
    expectPadding(row, 'py-1.5')
    // The old user row was `flex justify-end py-1`; only the padding may change,
    // and the removed token must not survive as a leftover of the substitution.
    expect(row.classList.contains('flex')).toBe(true)
    expect(row.classList.contains('justify-end')).toBe(true)
    expect(row.classList.contains('py-1')).toBe(false)
  })

  test('5. system notice (role user + systemNotice) → compact', () => {
    const message = makeMessage({
      role: 'user',
      content: '[System: subagent] Subagent status update.',
      systemNotice: { sender: 'subagent', body: 'Subagent status update.' },
    })
    const { getByTestId } = renderBubble(message, ctx)

    const row = rowWrapper(getByTestId)
    expect(row.getAttribute('data-system-notice')).toBe('true')
    expect(row.classList.contains('py-1.5')).toBe(true)
    expect(row.classList.contains('py-3')).toBe(false)
    expectPadding(row, 'py-1.5')
  })

  test('6. the wrapper agrees with the helper, and never mixes rhythms', () => {
    const cases: ChatMessage[] = [
      makeMessage({ role: 'tool', content: 'x', toolName: 'exec' }),
      makeMessage({ role: 'user', content: 'hola' }),
      makeMessage({ role: 'assistant', content: 'hello there' }),
      makeMessage({ role: 'assistant', content: '', reasoningContent: 'why' }),
    ]

    for (const message of cases) {
      const { getByTestId, unmount } = renderBubble(message, ctx)
      const row = rowWrapper(getByTestId)
      // Agreement with the pure helper…
      expect(row.classList.contains(chatRowPadding(message))).toBe(true)
      // …and exactly one rhythm token on the row.
      const paddings = row.className
        .split(/\s+/)
        .filter((c) => c === CHAT_ROW_PADDING.compact || c === CHAT_ROW_PADDING.content)
      expect(paddings).toHaveLength(1)
      unmount()
    }
  })
})
