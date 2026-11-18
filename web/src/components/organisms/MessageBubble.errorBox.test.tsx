/**
 * Regression tests for the LLM-failure error box.
 *
 * DEFECT: when an LLM request failed after every retry, the WebUI flashed the
 * backend's plain "❌ Error …" text as a normal assistant bubble and then the
 * history refetch DELETED it (the failure was never persisted) — so the user saw
 * the error appear and vanish. The backend now persists the failure with
 * `error_detail` and the builder maps it onto `ChatMessage.error`; this row
 * renders it as the red error box instead of a normal bubble.
 *
 * What MUST hold:
 *   - the box carries the failure text (data-testid="llm-error-box"),
 *   - normal content/reasoning/tool rendering is skipped (the legacy ❌ text is
 *     not repeated, the box replaces the bubble),
 *   - the "archived from context" label is skipped (the message is excluded from
 *     context precisely because it is an error),
 *   - an error-only message with EMPTY content is not null-rendered (that is the
 *     disappearing-error bug this path exists to fix).
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

function renderBubble(message: ChatMessage, ctx: AuthContextValue) {
  return render(
    <AuthContext.Provider value={ctx}>
      <MessageBubble message={message} isLast={false} onNavigateToSession={() => {}} />
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
    sessionKey: 'native:s1',
    ...overrides,
  }
}

const DETAIL = 'LLM call failed after retries: 429 Too Many Requests'

beforeEach(() => {
  // @ts-expect-error removing for test
  globalThis.IntersectionObserver = undefined
  URL.createObjectURL = mock((_blob: Blob) => 'blob:test-url/err') as typeof URL.createObjectURL
  URL.revokeObjectURL = mock(() => {}) as typeof URL.revokeObjectURL
})

afterEach(() => {
  URL.createObjectURL = originalCreateObjectURL
  URL.revokeObjectURL = originalRevokeObjectURL
  globalThis.IntersectionObserver = originalIntersectionObserver
})

describe('MessageBubble — LLM error box', () => {
  test('renders the red error box with the failure detail', () => {
    const ctx = makeAuthCtx()
    const { container, getByTestId } = renderBubble(
      makeAssistantMessage({
        content:
          '❌ Error processing message: LLM call failed after retries: 429 Too Many Requests',
        error: DETAIL,
        excludeFromContext: true,
      }),
      ctx,
    )

    const box = getByTestId('llm-error-box')
    expect(box.textContent).toContain(DETAIL)
    // The box rides inside the normal message row so ordering/anchors hold.
    expect(box.closest('[data-testid="message"]')).not.toBeNull()
    expect(container.querySelector('[data-role="assistant"]')).not.toBeNull()
  })

  test('skips the normal content and the archived label', () => {
    const ctx = makeAuthCtx()
    const { container, getByTestId } = renderBubble(
      makeAssistantMessage({
        content: '❌ Error processing message: boom',
        error: DETAIL,
        excludeFromContext: true,
        // Reasoning must be dropped too: the box replaces the bubble.
        reasoningContent: 'thinking about the request',
      }),
      ctx,
    )

    const box = getByTestId('llm-error-box')
    expect(box.textContent).toContain(DETAIL)
    // The legacy ❌ text is not rendered anywhere in the row.
    expect(container.textContent ?? '').not.toContain('❌ Error processing message')
    // Neither is the reasoning/thinking card.
    expect(container.textContent ?? '').not.toContain('thinking about the request')
    // Nor the "archived from context" marker (the row is an error, not context).
    expect(container.textContent ?? '').not.toContain('Archived from context')
  })

  test('does not null-render an error-only message with empty content', () => {
    const ctx = makeAuthCtx()
    const { container, getByTestId } = renderBubble(
      makeAssistantMessage({ content: '', error: DETAIL, excludeFromContext: true }),
      ctx,
    )

    expect(getByTestId('llm-error-box').textContent).toContain(DETAIL)
    // A null render would leave the container empty: that was the disappearing
    // error bug, now impossible.
    expect(container.querySelector('[data-testid="message"]')).not.toBeNull()
  })

  test('a message without error keeps the normal assistant render', () => {
    const ctx = makeAuthCtx()
    const { container } = renderBubble(makeAssistantMessage({ content: 'a normal answer' }), ctx)

    expect(container.querySelector('[data-testid="llm-error-box"]')).toBeNull()
    expect(container.textContent ?? '').toContain('a normal answer')
  })
})
