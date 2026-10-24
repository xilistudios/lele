/**
 * Regression tests for MessageBubble VISIBILITY (the "dead space between chat
 * messages" bug).
 *
 * An assistant row must never render when it has nothing visible: raw
 * `message.content` / `message.reasoningContent` truthiness is not enough
 * because whitespace-only strings (" ", "\n", "\n\n\n") are truthy yet paint
 * nothing in the row.
 *
 * These rows are invisible by construction: nothing inside them has height the
 * user can see, yet the row still occupies its padding (and, for whitespace on
 * interior lines, the blank blocks `MarkdownText` emits) — which is how dead
 * bands appeared between two tool cards in a live turn.
 *
 * What MUST keep rendering: the streaming typing dots (a streaming bubble whose
 * visible content is still empty is *doing* something, so it is not dead space),
 * reasoning-only rows, rows with visible text, the archived-from-context banner,
 * attachments, user rows (the guard is assistant-only), and the streaming caret —
 * which is gated on the same trimmed content test, so it never floats over a
 * payload that paints nothing.
 *
 * Conventions (imports/mocks/i18n/helpers) mirror MessageBubble.test.tsx.
 */
import '../../test/setup'
import '../../test/i18n'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { render } from '@testing-library/react'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
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

function makeAssistantMessage(overrides: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: 'msg-assistant',
    role: 'assistant',
    content: '',
    streaming: false,
    createdAt: new Date().toISOString(),
    ...overrides,
  }
}

function makeUserMessage(overrides: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: 'msg-user',
    role: 'user',
    content: '',
    streaming: false,
    createdAt: new Date().toISOString(),
    ...overrides,
  }
}

/** The three pulsing dots of the typing indicator (no data-testid on purpose:
 *  the caret and the "thinking…" pulse also carry `animate-pulse`, but neither
 *  of them is reachable while the dots render — content and reasoning are both
 *  empty there — so counting the spans pins the indicator exactly. */
function typingDotCount(container: HTMLElement) {
  return container.querySelectorAll('span.animate-pulse').length
}

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

describe('MessageBubble — invisible assistant rows are not rendered', () => {
  const ctx = makeAuthCtx()

  test('1. empty content, not streaming → renders nothing', () => {
    const { container, queryByTestId } = renderBubble(makeAssistantMessage({ content: '' }), ctx)

    expect(queryByTestId('message')).toBeNull()
    expect(container.innerHTML).toBe('')
  })

  test('2. whitespace-only content (" \\n "), not streaming → renders nothing', () => {
    const { container, queryByTestId } = renderBubble(
      makeAssistantMessage({ content: ' \n ' }),
      ctx,
    )

    expect(queryByTestId('message')).toBeNull()
    expect(container.innerHTML).toBe('')
  })

  test('3. newlines only ("\\n\\n\\n"), finalized (not streaming) → renders nothing: the 84px ghost row', () => {
    // The band measured between two tool cards: content that is truthy but
    // paints nothing. parseBlocks() trims every text block, so "\n\n\n" is not
    // even a markdown payload — what the row was paying was pure layout, and it
    // disappeared on reload because the persisted copy is empty.
    const { container, queryByTestId } = renderBubble(
      makeAssistantMessage({ content: '\n\n\n' }),
      ctx,
    )

    expect(queryByTestId('message')).toBeNull()
    expect(container.innerHTML).toBe('')
  })

  test('4. whitespace-only reasoning does not produce an empty Thinking card', () => {
    const { container, queryByTestId, queryByText } = renderBubble(
      makeAssistantMessage({ content: '', reasoningContent: '  ' }),
      ctx,
    )

    expect(queryByTestId('message')).toBeNull()
    expect(container.innerHTML).toBe('')
    expect(queryByText(/Pensamiento|Thinking/)).toBeNull()
    expect(container.querySelectorAll('button[aria-expanded]')).toHaveLength(0)
  })
})

describe('MessageBubble — rows that are visible keep rendering', () => {
  const ctx = makeAuthCtx()

  test('5. streaming with no content yet → the three typing dots are rendered', () => {
    const { container, getByTestId } = renderBubble(
      makeAssistantMessage({ content: '', streaming: true }),
      ctx,
      true,
    )

    expect(getByTestId('message')).toBeTruthy()
    expect(typingDotCount(container)).toBe(3)
  })

  test('5b. whitespace-only content while streaming → typing dots, never blank markdown lines', () => {
    // ADAPTED expectation: `showsTypingDots` is measured on the TRIMMED content,
    // so a streaming bubble that has received nothing but whitespace is "no
    // visible content yet" and takes the dots branch instead of the markdown
    // one. The blank blocks a whitespace payload *can* produce come from
    // MarkdownText (whitespace on interior lines), not from parseBlocks — and
    // this fixture is not such a case, which is what the assertion below pins:
    // nothing but the indicator leaks into the row. The row is visible (dots),
    // so it is not dead space; the moment it finalizes as whitespace it hits the
    // early return (case 3) and disappears.
    const { container, getByTestId } = renderBubble(
      makeAssistantMessage({ content: '\n\n\n', streaming: true }),
      ctx,
      true,
    )

    expect(getByTestId('message')).toBeTruthy()
    expect(typingDotCount(container)).toBe(3)
    // No invisible markdown payload leaked through the dots branch.
    expect(container.querySelectorAll('div.h-2')).toHaveLength(0)
    expect(container.textContent).toBe('')
  })

  test('6. reasoning-only row survives and exposes the Thinking toggle', () => {
    const { getByTestId, getByRole } = renderBubble(
      makeAssistantMessage({ content: '  ', reasoningContent: 'real reasoning' }),
      ctx,
    )

    expect(getByTestId('message')).toBeTruthy()
    const toggle = getByRole('button', { expanded: false })
    expect(toggle.textContent).toMatch(/Pensamiento|Thinking/)
  })

  test('7. leading blank lines with real text → row renders and shows the text', () => {
    const { getByTestId, getByText } = renderBubble(
      makeAssistantMessage({ content: '\n\nhello' }),
      ctx,
    )

    expect(getByTestId('message')).toBeTruthy()
    expect(getByText('hello')).toBeTruthy()
  })

  test('8. excludeFromContext banner keeps the row alive even with empty content', () => {
    const { container, getByTestId } = renderBubble(
      makeAssistantMessage({ content: '', excludeFromContext: true }),
      ctx,
    )

    expect(getByTestId('message')).toBeTruthy()
    expect(container.textContent).toMatch(/Archivado del contexto|Archived from context/)
  })

  test('9. attachment-only row renders the attachment chip', () => {
    const { container, getByTestId } = renderBubble(
      makeAssistantMessage({
        content: '',
        attachments: [{ path: '/tmp/lele/report.txt', name: 'report.txt' }],
      }),
      ctx,
    )

    expect(getByTestId('message')).toBeTruthy()
    expect(container.textContent).toContain('report.txt')
  })

  test('10. user rows are NOT guarded — an empty user bubble still renders', () => {
    const { container, getByTestId } = renderBubble(makeUserMessage({ content: '' }), ctx)

    expect(getByTestId('message')).toBeTruthy()
    expect(container.innerHTML).not.toBe('')
  })
})

describe('MessageBubble — the streaming caret follows the trimmed content test', () => {
  const ctx = makeAuthCtx()

  /** The caret is the row's only `h-4 w-0.5` element (the typing dots are
   *  `h-2 w-2`, the thinking pulse lives in the collapsed card header), so this
   *  selector pins the caret and nothing else. The `.` of the tailwind token
   *  `w-0.5` is escaped — unescaped, CSS reads it as a `5` type selector. */
  function caret(container: HTMLElement) {
    return container.querySelector('span.h-4.w-0\\.5')
  }

  test('11. streaming + isLast + visible text → the caret is rendered', () => {
    const { container, getByTestId } = renderBubble(
      makeAssistantMessage({ content: 'hi', streaming: true }),
      ctx,
      true,
    )

    expect(getByTestId('message')).toBeTruthy()
    expect(caret(container)).not.toBeNull()
    // The dots and the caret are mutually exclusive: text has arrived.
    expect(container.querySelectorAll('span.h-2.w-2')).toHaveLength(0)
  })

  test('12. streaming + isLast + whitespace-only text → no caret, dots instead', () => {
    // The caret used to be gated on `message.content !== ''`, so a payload of
    // whitespace alone drew a blinking bar next to nothing while the row itself
    // was invisible content. `hasContent` is trimmed, so the indicator wins.
    const { container, getByTestId } = renderBubble(
      makeAssistantMessage({ content: '  ', streaming: true }),
      ctx,
      true,
    )

    expect(getByTestId('message')).toBeTruthy()
    expect(caret(container)).toBeNull()
    expect(typingDotCount(container)).toBe(3)
  })
})
