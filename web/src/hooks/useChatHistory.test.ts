import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import React from 'react'
import { createApiClient } from '../lib/api'
import type { ChatMessage } from '../lib/types'
import { useChatHistory } from './useChatHistory'

const originalFetch = globalThis.fetch

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  globalThis.fetch = originalFetch
  localStorage.clear()
})

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
