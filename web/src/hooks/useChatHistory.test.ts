import { afterEach, beforeEach, describe, expect, jest, mock, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import React from 'react'
import { createApiClient } from '../lib/api'
import type { ChatMessage } from '../lib/types'
import { useChatHistory } from './useChatHistory'

const originalFetch = globalThis.fetch

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  cleanup()
  globalThis.fetch = originalFetch
  localStorage.clear()
  // Always leave the file on real timers and visible: a hidden state (or a
  // pending fake timer) left behind would silently change the next test.
  jest.useRealTimers()
  setVisibility('visible')
})

/**
 * jsdom neither fires `visibilitychange` nor lets `visibilityState` be set, so
 * both are simulated the way a browser does (same approach as
 * `services/ws/client.test.ts`).
 */
function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, 'visibilityState', { value: state, configurable: true })
  document.dispatchEvent(new window.Event('visibilitychange'))
}

/** Number of `GET /api/v1/chat/sessions/{key}/history` requests the mock saw. */
function historyFetchCalls(fetchMock: ReturnType<typeof mock>, sessionKey = 'session-1'): number {
  const path = `/api/v1/chat/sessions/${sessionKey}/history`
  return fetchMock.mock.calls.filter((call) => String(call[0]).includes(path)).length
}

/**
 * Settle the mocked fetch chain, React Query's notifyManager (a `setTimeout(0)`
 * scheduler, so a timer tick is required) and the resulting re-renders.
 *
 * `waitFor` cannot observe anything here: on fake timers its own timeout never
 * elapses. Advances are 0 ms, so they never push the query past its 4000 ms
 * reconciliation interval by accident.
 */
async function flushEffects() {
  await act(async () => {
    for (let i = 0; i < 5; i += 1) {
      await Promise.resolve()
      jest.advanceTimersByTime(0)
      await Promise.resolve()
    }
  })
}

/** Fetch mock answering the history endpoint with a live (processing) turn. */
function processingHistoryFetchMock(getMessages: () => Array<Record<string, unknown>>) {
  const fetchMock = mock(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.includes('/api/v1/chat/sessions/session-1/history')) {
      return new Response(
        JSON.stringify({
          session_key: 'session-1',
          messages: getMessages(),
          has_more: false,
          processing: true,
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    }
    return new Response(JSON.stringify({ error: 'not found' }), { status: 404 })
  })
  globalThis.fetch = fetchMock as unknown as typeof fetch
  return fetchMock
}

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  })
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client: queryClient }, children)
  }
}

describe('useChatHistory', () => {
  test('initial load populates messages and hasMore correctly', async () => {
    const rawMessages = [
      { id: 'msg-1', role: 'user' as const, content: 'Hello 1' },
      { id: 'msg-2', role: 'assistant' as const, content: 'Hi 1' },
    ]

    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/api/v1/chat/sessions/session-1/history')) {
        return new Response(
          JSON.stringify({
            session_key: 'session-1',
            messages: rawMessages,
            has_more: true,
            processing: false,
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        )
      }
      return new Response(JSON.stringify({ error: 'not found' }), { status: 404 })
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const streamingMessages: ChatMessage[] = []
    const { result } = renderHook(
      () => useChatHistory(api, 'session-1', 'token', streamingMessages),
      { wrapper: createWrapper() },
    )

    await waitFor(() => {
      expect(result.current.messages.length).toBe(2)
      expect(result.current.hasMore).toBe(true)
    })

    expect(result.current.messages[0].content).toBe('Hello 1')
    expect(result.current.messages[1].content).toBe('Hi 1')
  })

  test('loadMore fetches older messages using oldest raw message id as cursor and prepends them', async () => {
    const page2 = [
      { id: 'msg-3', role: 'user' as const, content: 'Hello 3' },
      { id: 'msg-4', role: 'assistant' as const, content: 'Hi 4' },
    ]
    const page1 = [
      { id: 'msg-1', role: 'user' as const, content: 'Hello 1' },
      { id: 'msg-2', role: 'assistant' as const, content: 'Hi 2' },
    ]

    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/api/v1/chat/sessions/session-1/history')) {
        const parsed = new URL(url)
        const beforeId = parsed.searchParams.get('before_id')
        if (beforeId === 'msg-3') {
          return new Response(
            JSON.stringify({
              session_key: 'session-1',
              messages: page1,
              has_more: false,
              processing: false,
            }),
            { status: 200, headers: { 'Content-Type': 'application/json' } },
          )
        }
        return new Response(
          JSON.stringify({
            session_key: 'session-1',
            messages: page2,
            has_more: true,
            processing: false,
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        )
      }
      return new Response(JSON.stringify({ error: 'not found' }), { status: 404 })
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const streamingMessages: ChatMessage[] = []
    const { result } = renderHook(
      () => useChatHistory(api, 'session-1', 'token', streamingMessages),
      { wrapper: createWrapper() },
    )

    await waitFor(() => {
      expect(result.current.messages.length).toBe(2)
      expect(result.current.hasMore).toBe(true)
    })

    // Trigger loadMore
    await act(async () => {
      await result.current.loadMore()
    })

    await waitFor(() => {
      expect(result.current.messages.length).toBe(4)
      expect(result.current.hasMore).toBe(false)
    })

    // Older messages should be prepended before page2
    expect(result.current.messages[0].id).toBe('msg-1')
    expect(result.current.messages[1].id).toBe('msg-2')
    expect(result.current.messages[2].id).toBe('msg-3')
    expect(result.current.messages[3].id).toBe('msg-4')
  })
})
/**
 * Visibility gating of the HTTP history poll.
 *
 * `GET /api/v1/chat/history` is the most expensive read on the gateway. React
 * Query already skips the FETCH of an interval tick that fires while the
 * document is unfocused (`focusManager.isFocused()` gate), so the request
 * saving for this query is defence in depth; what the change really adds is
 * (a) no timer is armed at all while hidden and (b) exactly one immediate
 * refresh at the visibility transition, instead of waiting up to a full 4 s
 * interval for the next tick. The contract these tests pin:
 *   1. while hidden: zero requests and no armed reconciliation timer,
 *   2. on becoming visible: exactly ONE immediate refetch — which must JOIN any
 *      in-flight request instead of cancelling and restarting it,
 *   3. the 4 s cadence resumes while processing, and stops once it ends.
 */
describe('useChatHistory visibility gating', () => {
  test('does not poll while hidden and refreshes exactly once when shown again', async () => {
    jest.useFakeTimers()
    const fetchMock = processingHistoryFetchMock(() => [
      { id: 'msg-1', role: 'user' as const, content: 'Hello' },
    ])

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')
    const streamingMessages: ChatMessage[] = []
    const { result } = renderHook(
      () => useChatHistory(api, 'session-1', 'token', streamingMessages),
      { wrapper: createWrapper() },
    )

    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(1)
    expect(result.current.processing).toBe(true)

    // Polling is live while the session is processing.
    act(() => {
      jest.advanceTimersByTime(4000)
    })
    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(2)

    // Hidden: nothing may hit the endpoint for the whole time the user is away.
    // (React Query's focusManager would also skip the fetch of a tick that is
    // still armed; the test below pins the stronger guarantee that this change
    // adds — the timer itself is cleared.)
    act(() => setVisibility('hidden'))
    await flushEffects()
    act(() => {
      jest.advanceTimersByTime(60_000)
    })
    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(2)

    // Shown again: one immediate refresh, not a wait for the next tick.
    act(() => setVisibility('visible'))
    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(3)

    // ...then the 4 s reconciliation cadence resumes.
    act(() => {
      jest.advanceTimersByTime(4000)
    })
    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(4)
  })

  /**
   * The one-refresh-on-visible contract has a second half: the refresh must not
   * THROW AWAY a request that is already on the wire. React Query's default for
   * `refetch()` is `cancelRefetch: true`, which aborts the in-flight request
   * (thereby starting a second GET for the same data — the double fetch a tab
   * return used to produce, since the focus path already refetches with
   * `cancelRefetch: false`). Here the interval tick's request is left pending
   * across a hidden → visible transition and the refresh must join it: same
   * request count, no abort, and the pending answer is the one that lands.
   */
  test('the visibility refresh joins an in-flight request instead of cancelling it', async () => {
    jest.useFakeTimers()
    let calls = 0
    const inFlight: Array<(response: Response) => void> = []
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.includes('/api/v1/chat/sessions/session-1/history')) {
        return new Response(JSON.stringify({ error: 'not found' }), { status: 404 })
      }
      calls += 1
      if (calls === 1) {
        // Initial load: answered immediately so the query has data to poll for.
        return new Response(
          JSON.stringify({
            session_key: 'session-1',
            messages: [{ id: 'msg-1', role: 'user', content: 'Hello' }],
            has_more: false,
            processing: true,
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        )
      }
      // Every later request is left on the wire until the test answers it.
      return new Promise<Response>((resolve) => inFlight.push(resolve))
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')
    const streamingMessages: ChatMessage[] = []
    const { result } = renderHook(
      () => useChatHistory(api, 'session-1', 'token', streamingMessages),
      { wrapper: createWrapper() },
    )

    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(1)
    expect(result.current.processing).toBe(true)

    // A reconciliation tick fires while the session is processing and its
    // request is still pending when the user comes back to the tab.
    act(() => {
      jest.advanceTimersByTime(4000)
    })
    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(2)
    expect(inFlight.length).toBe(1)

    // React Query cancels an in-flight fetch by aborting its controller, so a
    // cancelling refetch is visible here as an abort — and as an extra GET.
    const realAbort = AbortController.prototype.abort
    const aborted: unknown[] = []
    AbortController.prototype.abort = function patchedAbort(this: AbortController) {
      aborted.push(this)
      return realAbort.call(this)
    } as typeof AbortController.prototype.abort

    try {
      act(() => setVisibility('hidden'))
      await flushEffects()
      act(() => setVisibility('visible'))
      await flushEffects()

      // Still two requests: the refresh joined the pending one.
      expect(historyFetchCalls(fetchMock)).toBe(2)
      // …and nothing was aborted on the way.
      expect(aborted).toEqual([])
    } finally {
      AbortController.prototype.abort = realAbort
    }

    // The joined request's answer is the one that lands — the refresh was not
    // lost, and the page does not stay stuck on "processing".
    act(() => {
      inFlight[0](
        new Response(
          JSON.stringify({
            session_key: 'session-1',
            messages: [
              { id: 'msg-1', role: 'user', content: 'Hello' },
              { id: 'msg-2', role: 'assistant', content: 'Done while away' },
            ],
            has_more: false,
            processing: false,
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        ),
      )
    })
    await flushEffects()
    expect(result.current.messages.length).toBe(2)
    expect(result.current.processing).toBe(false)
  })

  test('picks up a turn that finished while hidden with that single refresh', async () => {
    jest.useFakeTimers()
    let finalized = false
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/api/v1/chat/sessions/session-1/history')) {
        return new Response(
          JSON.stringify({
            session_key: 'session-1',
            messages: finalized
              ? [
                  { id: 'msg-1', role: 'user', content: 'Hello' },
                  { id: 'msg-2', role: 'assistant', content: 'Done while hidden' },
                ]
              : [{ id: 'msg-1', role: 'user', content: 'Hello' }],
            has_more: false,
            // The backend turns `processing` off when the turn ends.
            processing: !finalized,
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        )
      }
      return new Response(JSON.stringify({ error: 'not found' }), { status: 404 })
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')
    const streamingMessages: ChatMessage[] = []
    const { result } = renderHook(
      () => useChatHistory(api, 'session-1', 'token', streamingMessages),
      { wrapper: createWrapper() },
    )

    await flushEffects()
    expect(result.current.messages.length).toBe(1)
    expect(result.current.processing).toBe(true)

    act(() => setVisibility('hidden'))
    await flushEffects()
    const callsWhenHidden = historyFetchCalls(fetchMock)

    // The turn ends on the server while the user is on another tab; the client
    // cannot know (the interval is dead and the WS event may be dropped).
    finalized = true
    act(() => {
      jest.advanceTimersByTime(60_000)
    })
    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(callsWhenHidden)

    act(() => setVisibility('visible'))
    await flushEffects()

    // Exactly one request — and it carries the finished turn, so the page is
    // never left showing "processing" forever.
    expect(historyFetchCalls(fetchMock)).toBe(callsWhenHidden + 1)
    expect(result.current.processing).toBe(false)
    expect(result.current.messages.length).toBe(2)

    // Nothing more to reconcile: the interval stays off.
    act(() => {
      jest.advanceTimersByTime(4000)
    })
    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(callsWhenHidden + 1)
  })

  /**
   * The request-count tests above would still pass without the gate, because
   * React Query's focusManager skips the *fetch* of a tick that fires while
   * `document.visibilityState === 'hidden'` — the timer itself kept running
   * (and kept being re-armed on every query update). This test observes the
   * timers, which is exactly what the change is about: while hidden the 4 s
   * reconciliation interval must not exist at all.
   *
   * `setInterval`/`clearInterval` are wrapped AFTER `jest.useFakeTimers()` so
   * the fake timer implementation is what gets recorded, and restored in the
   * `finally` so no other test sees the wrappers.
   */
  test('clears the 4 s reconciliation timer while hidden and re-arms it when shown', async () => {
    jest.useFakeTimers()
    const fetchMock = processingHistoryFetchMock(() => [
      { id: 'msg-1', role: 'user' as const, content: 'Hello' },
    ])

    const realSetInterval = globalThis.setInterval
    const realClearInterval = globalThis.clearInterval
    const armed: unknown[] = []
    const cleared: unknown[] = []

    globalThis.setInterval = ((handler: TimerHandler, timeout?: number, ...args: unknown[]) => {
      const id = realSetInterval(handler as never, timeout as never, ...(args as never[]))
      if (timeout === 4000) armed.push(id)
      return id
    }) as unknown as typeof globalThis.setInterval
    globalThis.clearInterval = ((id?: number) => {
      cleared.push(id)
      return realClearInterval(id as never)
    }) as unknown as typeof globalThis.clearInterval

    try {
      const api = createApiClient('http://127.0.0.1:18793')
      api.setToken('token', 'refresh')
      const streamingMessages: ChatMessage[] = []
      renderHook(() => useChatHistory(api, 'session-1', 'token', streamingMessages), {
        wrapper: createWrapper(),
      })

      await flushEffects()
      // Processing reached the client: the reconciliation timer is armed.
      expect(armed.length).toBe(1)
      const armedWhileVisible = armed[0]

      act(() => setVisibility('hidden'))
      await flushEffects()

      // The armed timer was explicitly torn down...
      expect(cleared).toContain(armedWhileVisible)
      const armedWhenHidden = armed.length

      // ...and nothing re-arms it while the tab stays hidden.
      act(() => {
        jest.advanceTimersByTime(60_000)
      })
      await flushEffects()
      expect(armed.length).toBe(armedWhenHidden)

      act(() => setVisibility('visible'))
      await flushEffects()

      // Shown again: a fresh 4 s timer is armed (polling resumed), and the
      // immediate refresh did not wait for it.
      expect(armed.length).toBeGreaterThan(armedWhenHidden)
      expect(historyFetchCalls(fetchMock)).toBe(2)
    } finally {
      globalThis.setInterval = realSetInterval
      globalThis.clearInterval = realClearInterval
    }
  })
})
/**
 * Loading an older page across the history-id digest change.
 *
 * Server context: message ids in `GET /api/v1/chat/sessions/{key}/history` are
 * content-derived, and the derivation was replaced (content digest → bounded
 * hash) while keeping the same id FORMAT. A cursor minted before that change no
 * longer resolves, and the server's documented fallback then answers with the
 * NEWEST page — every message of which is already on screen under its previous
 * id. Deduping by `id` alone cannot see that, so the whole page used to be
 * prepended a second time (duplicate bubbles) until a reload.
 *
 * The merge therefore recognises a re-served page by IDENTITY (role + content +
 * tool identity — the payload carries no timestamp and no per-message uuid, see
 * countReServedMessages in useChatHistory.ts) and drops it when it provably
 * repeats the whole displayed window. The negative tests below pin what must
 * NOT be collapsed by that identity: genuinely distinct messages that merely
 * repeat role + content still render once each (the issue-#324
 * duplicate-content contract).
 */
describe('useChatHistory across the history-id digest change', () => {
  /** Page 1 as served BEFORE the upgrade: digest-derived ids. */
  const preUpgradePage = [
    { id: 'old-u1', role: 'user', content: 'Question one' },
    { id: 'old-a1', role: 'assistant', content: 'Answer one' },
    { id: 'old-u2', role: 'user', content: 'Question two' },
    { id: 'old-a2', role: 'assistant', content: 'Answer two' },
  ]

  /**
   * History mock whose response depends on the `before_id` cursor, so a test
   * can model the server's "cursor no longer resolves → newest page" fallback.
   */
  function cursorHistoryFetchMock(
    page: (beforeId: string | null) => {
      messages: Array<Record<string, unknown>>
      has_more: boolean
    },
  ) {
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.includes('/api/v1/chat/sessions/session-1/history')) {
        return new Response(JSON.stringify({ error: 'not found' }), { status: 404 })
      }
      const { messages, has_more } = page(new URL(url).searchParams.get('before_id'))
      return new Response(
        JSON.stringify({ session_key: 'session-1', messages, has_more, processing: false }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch
    return fetchMock
  }

  function mountHistory() {
    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')
    const streamingMessages: ChatMessage[] = []
    return renderHook(() => useChatHistory(api, 'session-1', 'token', streamingMessages), {
      wrapper: createWrapper(),
    })
  }

  test('loadMore with a cursor that no longer resolves duplicates nothing', async () => {
    cursorHistoryFetchMock((beforeId) =>
      beforeId
        ? {
            // The fallback: the newest page again, now under bounded-hash ids.
            messages: preUpgradePage.map((m, i) => ({ ...m, id: `new-${i}` })),
            has_more: true,
          }
        : { messages: preUpgradePage, has_more: true },
    )
    const { result } = mountHistory()

    await waitFor(() => expect(result.current.messages.length).toBe(4))
    const idsBefore = result.current.messages.map((m) => m.id)
    expect(idsBefore).toEqual(['old-u1', 'old-a1', 'old-u2', 'old-a2'])

    await act(async () => {
      await result.current.loadMore()
    })

    // Same four bubbles, same order, same ids — nothing was prepended.
    expect(result.current.messages.length).toBe(4)
    expect(result.current.messages.map((m) => m.id)).toEqual(idsBefore)
    // And pagination stops instead of re-requesting the same page forever.
    expect(result.current.hasMore).toBe(false)
  })

  test('a partial overlap is merged as before — content alone is not proof', async () => {
    cursorHistoryFetchMock((beforeId) =>
      beforeId
        ? {
            // The newest page after two more messages arrived: it starts at the
            // third message we display, so it does NOT repeat our whole window.
            // Content identity cannot prove those two overlapping messages are
            // the same logical ones (no timestamp/uuid in the payload), and
            // dropping them on content alone would lose genuinely older repeated
            // messages, so only a full re-serve is deduped (test above).
            messages: [
              { id: 'new-u2', role: 'user', content: 'Question two' },
              { id: 'new-a2', role: 'assistant', content: 'Answer two' },
              { id: 'new-u3', role: 'user', content: 'Question three' },
              { id: 'new-a3', role: 'assistant', content: 'Answer three' },
            ],
            has_more: true,
          }
        : { messages: preUpgradePage, has_more: true },
    )
    const { result } = mountHistory()

    await waitFor(() => expect(result.current.messages.length).toBe(4))

    await act(async () => {
      await result.current.loadMore()
    })

    // Nothing is LOST: every incoming message is merged, the two new ones
    // included — and the two that overlap by content are NOT dropped on that
    // evidence alone.
    const ids = result.current.messages.map((m) => m.id)
    expect(ids).toContain('new-u2')
    expect(ids).toContain('new-a2')
    expect(ids).toContain('new-u3')
    expect(ids).toContain('new-a3')
    const contents = result.current.messages.map((m) => m.content)
    expect(contents.filter((c) => c === 'Question one').length).toBe(1)
    expect(contents.filter((c) => c === 'Answer one').length).toBe(1)
  })

  test('two identical-content messages are still two bubbles (never deduped)', async () => {
    const answer = 'Repeated answer'
    cursorHistoryFetchMock((beforeId) =>
      beforeId
        ? {
            // An older page with two answers that repeat the text ALREADY on
            // screen. The server occurrence-indexes repeated content, so these
            // are three distinct messages with three distinct ids.
            messages: [
              { id: 'hash', role: 'assistant', content: answer },
              { id: 'hash-1', role: 'assistant', content: answer },
            ],
            has_more: false,
          }
        : {
            messages: [
              { id: 'old-u1', role: 'user', content: 'Question' },
              { id: 'old-a1', role: 'assistant', content: answer },
            ],
            has_more: true,
          },
    )
    const { result } = mountHistory()

    await waitFor(() => expect(result.current.messages.length).toBe(2))

    await act(async () => {
      await result.current.loadMore()
    })

    // 2 identical older messages + the identical one already displayed.
    expect(result.current.messages.length).toBe(4)
    expect(result.current.messages.filter((m) => m.content === answer).length).toBe(3)
    expect(new Set(result.current.messages.map((m) => m.id)).size).toBe(4)
  })

  test('an older page that repeats displayed content is not mistaken for a re-serve', async () => {
    // Repetitive content ("ping"/"pong") is the shape where a content-based
    // dedupe is most dangerous: the older page starts with text we already
    // display, but it neither starts at our newest message nor matches it in
    // order, so it must load in full.
    const older = [
      { id: 'older-u1', role: 'user', content: 'pong' },
      { id: 'older-a1', role: 'assistant', content: 'ping' },
    ]
    cursorHistoryFetchMock((beforeId) =>
      beforeId
        ? { messages: older, has_more: false }
        : {
            messages: [
              { id: 'old-u1', role: 'user', content: 'ping' },
              { id: 'old-a1', role: 'assistant', content: 'pong' },
              { id: 'old-u2', role: 'user', content: 'pong' },
              { id: 'old-a2', role: 'assistant', content: 'ping' },
            ],
            has_more: true,
          },
    )
    const { result } = mountHistory()

    await waitFor(() => expect(result.current.messages.length).toBe(4))

    await act(async () => {
      await result.current.loadMore()
    })

    expect(result.current.messages.length).toBe(6)
    expect(result.current.messages.map((m) => m.id).slice(0, 2)).toEqual(['older-u1', 'older-a1'])
  })
})
