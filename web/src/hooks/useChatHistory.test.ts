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
  jest.useRealTimers()
  setVisibility('visible')
})

function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, 'visibilityState', { value: state, configurable: true })
  document.dispatchEvent(new window.Event('visibilitychange'))
}

function historyFetchCalls(fetchMock: ReturnType<typeof mock>, sessionKey = 'session-1'): number {
  const path = `/api/v1/chat/sessions/${sessionKey}/history`
  return fetchMock.mock.calls.filter((call) => String(call[0]).includes(path)).length
}

async function flushEffects() {
  await act(async () => {
    for (let i = 0; i < 5; i += 1) {
      await Promise.resolve()
      jest.advanceTimersByTime(0)
      await Promise.resolve()
    }
  })
}

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

    await act(async () => {
      await result.current.loadMore()
    })

    await waitFor(() => {
      expect(result.current.messages.length).toBe(4)
      expect(result.current.hasMore).toBe(false)
    })

    expect(result.current.messages[0].id).toBe('msg-1')
    expect(result.current.messages[1].id).toBe('msg-2')
    expect(result.current.messages[2].id).toBe('msg-3')
    expect(result.current.messages[3].id).toBe('msg-4')
  })
})
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

    act(() => {
      jest.advanceTimersByTime(4000)
    })
    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(2)

    act(() => setVisibility('hidden'))
    await flushEffects()
    act(() => {
      jest.advanceTimersByTime(60_000)
    })
    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(2)

    act(() => setVisibility('visible'))
    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(3)

    act(() => {
      jest.advanceTimersByTime(4000)
    })
    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(4)
  })

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

    act(() => {
      jest.advanceTimersByTime(4000)
    })
    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(2)
    expect(inFlight.length).toBe(1)

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

      expect(historyFetchCalls(fetchMock)).toBe(2)
      expect(aborted).toEqual([])
    } finally {
      AbortController.prototype.abort = realAbort
    }

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

    finalized = true
    act(() => {
      jest.advanceTimersByTime(60_000)
    })
    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(callsWhenHidden)

    act(() => setVisibility('visible'))
    await flushEffects()

    expect(historyFetchCalls(fetchMock)).toBe(callsWhenHidden + 1)
    expect(result.current.processing).toBe(false)
    expect(result.current.messages.length).toBe(2)

    act(() => {
      jest.advanceTimersByTime(4000)
    })
    await flushEffects()
    expect(historyFetchCalls(fetchMock)).toBe(callsWhenHidden + 1)
  })

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
      expect(armed.length).toBe(1)
      const armedWhileVisible = armed[0]

      act(() => setVisibility('hidden'))
      await flushEffects()

      expect(cleared).toContain(armedWhileVisible)
      const armedWhenHidden = armed.length

      act(() => {
        jest.advanceTimersByTime(60_000)
      })
      await flushEffects()
      expect(armed.length).toBe(armedWhenHidden)

      act(() => setVisibility('visible'))
      await flushEffects()

      expect(armed.length).toBeGreaterThan(armedWhenHidden)
      expect(historyFetchCalls(fetchMock)).toBe(2)
    } finally {
      globalThis.setInterval = realSetInterval
      globalThis.clearInterval = realClearInterval
    }
  })
})
describe('useChatHistory across the history-id digest change', () => {
  const preUpgradePage = [
    { id: 'old-u1', role: 'user', content: 'Question one' },
    { id: 'old-a1', role: 'assistant', content: 'Answer one' },
    { id: 'old-u2', role: 'user', content: 'Question two' },
    { id: 'old-a2', role: 'assistant', content: 'Answer two' },
  ]

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

    expect(result.current.messages.length).toBe(4)
    expect(result.current.messages.map((m) => m.id)).toEqual(idsBefore)
    expect(result.current.hasMore).toBe(false)
  })

  test('a partial overlap is merged as before — content alone is not proof', async () => {
    cursorHistoryFetchMock((beforeId) =>
      beforeId
        ? {
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

    expect(result.current.messages.length).toBe(4)
    expect(result.current.messages.filter((m) => m.content === answer).length).toBe(3)
    expect(new Set(result.current.messages.map((m) => m.id)).size).toBe(4)
  })

  test('an older page that repeats displayed content is not mistaken for a re-serve', async () => {
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
