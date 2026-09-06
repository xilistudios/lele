/**
 * Regression cover for the merge filter in useChatHistory.
 *
 * Symptom: tool cards disappear during a live turn and come back after a
 * reload. The backend emits tool events with the resolved conversation alias
 * (`base:chat:N`, pkg/channels/native.go dispatchOutboundMessage →
 * agentLoop.ResolveSessionKey). The handlers now re-tag those messages with
 * the current UI key (effectiveSessionKey), but the merge filter is also made
 * alias-tolerant as defense-in-depth: a transient message that still carries
 * an alias of the session on screen must render, not silently vanish.
 */
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import React from 'react'
import { createApiClient } from '../lib/api'
import type { ChatMessage } from '../lib/types'
import { buildChatHistoryQueryKey, useChatHistory } from './useChatHistory'

const originalFetch = globalThis.fetch

beforeEach(() => localStorage.clear())
afterEach(() => {
  globalThis.fetch = originalFetch
  localStorage.clear()
})

function createWrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client: queryClient }, children)
  }
}

function emptyHistory(sessionKey: string) {
  return new Response(
    JSON.stringify({ session_key: sessionKey, messages: [], has_more: false, processing: false }),
    { status: 200, headers: { 'Content-Type': 'application/json' } },
  )
}

function tool(id: string, sessionKey: string): ChatMessage {
  return {
    id,
    role: 'tool',
    content: '',
    streaming: false,
    createdAt: new Date().toISOString(),
    sessionKey,
    toolName: 'exec',
    toolStatus: 'completed',
    toolCallId: `call_${id}`,
  }
}

function assistant(id: string, sessionKey: string): ChatMessage {
  return {
    id,
    role: 'assistant',
    content: 'working…',
    streaming: true,
    createdAt: new Date().toISOString(),
    sessionKey,
  }
}

async function renderWithStreaming(streaming: ChatMessage[], sessionKey = 'native:A') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  globalThis.fetch = mock(async () => emptyHistory(sessionKey)) as unknown as typeof fetch
  const api = createApiClient('http://localhost')
  const { result } = renderHook(
    () => useChatHistory(api, sessionKey, 'token', streaming, undefined, () => {}),
    { wrapper: createWrapper(queryClient) },
  )
  await waitFor(() => {
    expect(queryClient.getQueryData(buildChatHistoryQueryKey(sessionKey))).toBeDefined()
  })
  return result
}

describe('useChatHistory streaming filter (conversation aliases)', () => {
  test('message tagged with the session alias is still rendered', async () => {
    const result = await renderWithStreaming([tool('t1', 'native:A:chat:3')])
    expect(result.current.messages.map((m) => m.id)).toContain('t1')
  })

  test('message tagged with the current key renders (unchanged behaviour)', async () => {
    const result = await renderWithStreaming([tool('t1', 'native:A')])
    expect(result.current.messages.map((m) => m.id)).toContain('t1')
  })

  test('message from an unrelated session is NOT rendered', async () => {
    const result = await renderWithStreaming([
      tool('mine', 'native:A'),
      tool('theirs', 'native:B:chat:1'),
      assistant('theirs-a', 'native:B'),
    ])
    const ids = result.current.messages.map((m) => m.id)
    expect(ids).toContain('mine')
    expect(ids).not.toContain('theirs')
    expect(ids).not.toContain('theirs-a')
  })

  test('non-alias suffix is not tolerated (native:A:chat:x ≠ native:A)', async () => {
    const result = await renderWithStreaming([tool('weird', 'native:A:chat:x')])
    expect(result.current.messages.map((m) => m.id)).not.toContain('weird')
  })
})
