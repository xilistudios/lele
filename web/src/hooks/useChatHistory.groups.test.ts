import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import React from 'react'
import { createApiClient } from '../lib/api'
import type { ChatMessage, GroupInfo, GroupSnapshot } from '../lib/types'
import { buildChatHistoryQueryKey, useChatHistory } from './useChatHistory'
import { useGroupState } from './useGroupState'

const originalFetch = globalThis.fetch

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  globalThis.fetch = originalFetch
  localStorage.clear()
})

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        staleTime: 10_000,
      },
    },
  })
}

function createWrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client: queryClient }, children)
  }
}

function makeHistoryResponse(
  sessionKey: string,
  messages: Array<{ id: string; role: 'user' | 'assistant'; content: string }>,
  hasMore: boolean,
  groups?: GroupSnapshot[],
) {
  const body: Record<string, unknown> = {
    session_key: sessionKey,
    messages,
    has_more: hasMore,
    processing: false,
  }
  if (groups !== undefined) body.groups = groups
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

function makeSnapshot(groupId: string, overrides: Partial<GroupSnapshot> = {}): GroupSnapshot {
  return {
    group_id: groupId,
    status: 'done',
    strategy: 'moa',
    participants: 'agent-a,agent-b',
    layers: 2,
    total_tokens: 1500,
    created_at: '2026-08-30T00:00:00Z',
    synthesis: `synthesis of ${groupId}`,
    turns: [
      {
        turn_index: 0,
        speaker: 'agent-a',
        label: 'Agent A',
        role: 'proposer',
        layer: 0,
        content: 'Proposal A',
      },
      {
        turn_index: 1,
        speaker: 'agent-b',
        label: 'Agent B',
        role: 'proposer',
        layer: 0,
        content: 'Proposal B',
        tool_calls: [
          { tool_call_id: 'tc-1', tool: 'web_search', status: 'completed', result: 'results' },
        ],
      },
    ],
    ...overrides,
  }
}

function makeMessages(
  n: number,
  prefix = 'm',
): Array<{ id: string; role: 'user' | 'assistant'; content: string }> {
  const out: Array<{ id: string; role: 'user' | 'assistant'; content: string }> = []
  for (let i = 1; i <= n; i++) {
    out.push({
      id: `${prefix}${i}`,
      role: i % 2 === 1 ? 'user' : 'assistant',
      content: `${prefix}${i}`,
    })
  }
  return out
}

function setupApi() {
  const api = createApiClient('http://127.0.0.1:18793')
  api.setToken('token', 'refresh')
  return api
}

const streaming: ChatMessage[] = []

describe('useChatHistory group rehydration (#239)', () => {
  test('cache-hit remount rehydrates group cards without re-running queryFn', async () => {
    const queryClient = createTestQueryClient()
    const wrapper = createWrapper(queryClient)
    let fetchCount = 0

    globalThis.fetch = mock(async () => {
      fetchCount++
      return makeHistoryResponse('session-1', makeMessages(2), false, [makeSnapshot('g1')])
    }) as unknown as typeof fetch

    const api = setupApi()

    const first = renderHook(
      () => {
        const gs = useGroupState()
        const hist = useChatHistory(
          api,
          'session-1',
          'token',
          streaming,
          undefined,
          gs.hydrateGroups,
        )
        return { groups: gs.groups, hist }
      },
      { wrapper },
    )
    await waitFor(() => {
      expect(first.result.current.groups.size).toBe(1)
    })
    expect(fetchCount).toBe(1)

    first.unmount()
    const second = renderHook(
      () => {
        const gs = useGroupState()
        const hist = useChatHistory(
          api,
          'session-1',
          'token',
          streaming,
          undefined,
          gs.hydrateGroups,
        )
        return { groups: gs.groups, hist }
      },
      { wrapper },
    )

    await waitFor(() => {
      expect(second.result.current.groups.size).toBe(1)
    })
    expect(fetchCount).toBe(1)

    const g = second.result.current.groups.get('g1') as GroupInfo
    expect(g.status).toBe('done')
    expect(g.turns.length).toBe(2)
    second.unmount()
  })

  test('queryFn result includes groups converted to GroupInfo shape', async () => {
    const queryClient = createTestQueryClient()
    const wrapper = createWrapper(queryClient)

    globalThis.fetch = mock(async () =>
      makeHistoryResponse('session-1', makeMessages(2), false, [makeSnapshot('g1')]),
    ) as unknown as typeof fetch
    const api = setupApi()

    const hydrate = mock((_infos: GroupInfo[]) => {})
    const { result } = renderHook(
      () => useChatHistory(api, 'session-1', 'token', streaming, undefined, hydrate),
      { wrapper },
    )
    await waitFor(() => {
      expect(hydrate).toHaveBeenCalledTimes(1)
    })

    const data = queryClient.getQueryData<{ groups?: GroupInfo[] }>(
      buildChatHistoryQueryKey('session-1'),
    )
    expect(data?.groups).toBeDefined()
    expect(data?.groups?.length).toBe(1)
    const g = data?.groups?.[0] as GroupInfo
    expect(g.groupID).toBe('g1')
    expect(g.status).toBe('done')
    expect(g.totalTokens).toBe(1500)
    expect(g.synthesis).toBe('synthesis of g1')
    expect(g.turns[0].groupID).toBe('g1')
    expect(g.turns[1].turnIndex).toBe(1)
    expect(g.turns[1].toolCalls?.[0]?.tool).toBe('web_search')
    expect(hydrate.mock.calls[0][0][0].groupID).toBe('g1')
    expect(result.current.messages.length).toBe(2)
  })

  test('missing or empty history.groups does not call hydrateGroups and stores []', async () => {
    const queryClient = createTestQueryClient()
    const wrapper = createWrapper(queryClient)

    globalThis.fetch = mock(async () =>
      makeHistoryResponse('session-1', makeMessages(2), false),
    ) as unknown as typeof fetch
    const api = setupApi()

    const hydrate = mock((_infos: GroupInfo[]) => {})
    renderHook(() => useChatHistory(api, 'session-1', 'token', streaming, undefined, hydrate), {
      wrapper,
    })
    await waitFor(() => {
      expect(queryClient.getQueryData(buildChatHistoryQueryKey('session-1'))).toBeDefined()
    })
    expect(hydrate).not.toHaveBeenCalled()
    const data = queryClient.getQueryData<{ groups?: GroupInfo[] }>(
      buildChatHistoryQueryKey('session-1'),
    )
    expect(data?.groups).toEqual([])
  })

  test('paginated-merge branch (poll after loadMore-sized cache) keeps groups', async () => {
    const queryClient = createTestQueryClient()
    const wrapper = createWrapper(queryClient)

    globalThis.fetch = mock(async () =>
      makeHistoryResponse('session-1', makeMessages(51), true, [makeSnapshot('g1')]),
    ) as unknown as typeof fetch
    const api = setupApi()

    const hydrate = mock((_infos: GroupInfo[]) => {})
    const { result } = renderHook(
      () => useChatHistory(api, 'session-1', 'token', streaming, undefined, hydrate),
      { wrapper },
    )
    await waitFor(() => {
      expect(result.current.messages.length).toBe(51)
    })
    expect(hydrate).toHaveBeenCalledTimes(1)

    await act(async () => {
      await result.current.refetch()
    })
    await waitFor(() => {
      expect(hydrate).toHaveBeenCalledTimes(2)
    })

    const data = queryClient.getQueryData<{ groups?: GroupInfo[]; messages?: unknown[] }>(
      buildChatHistoryQueryKey('session-1'),
    )
    expect(data?.groups?.map((g) => g.groupID)).toEqual(['g1'])
    expect(data?.messages?.length).toBe(51)
  })

  test('loadMore keeps cached groups and appends groups from older pages', async () => {
    const queryClient = createTestQueryClient()
    const wrapper = createWrapper(queryClient)

    const page2 = makeMessages(2, 'p2-')
    const page1 = makeMessages(2, 'p1-')
    globalThis.fetch = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      const beforeId = new URL(url).searchParams.get('before_id')
      if (beforeId) {
        return makeHistoryResponse('session-1', page1, false, [
          makeSnapshot('g1'),
          makeSnapshot('g0'),
        ])
      }
      return makeHistoryResponse('session-1', page2, true, [makeSnapshot('g1')])
    }) as unknown as typeof fetch
    const api = setupApi()

    const hydrate = mock((_infos: GroupInfo[]) => {})
    const { result } = renderHook(
      () => useChatHistory(api, 'session-1', 'token', streaming, undefined, hydrate),
      { wrapper },
    )
    await waitFor(() => {
      expect(result.current.messages.length).toBe(2)
    })

    await act(async () => {
      await result.current.loadMore()
    })
    await waitFor(() => {
      expect(result.current.messages.length).toBe(4)
    })

    const data = queryClient.getQueryData<{ groups?: GroupInfo[] }>(
      buildChatHistoryQueryKey('session-1'),
    )
    const ids = (data?.groups ?? []).map((g) => g.groupID).sort()
    expect(ids).toEqual(['g0', 'g1'])
  })

  test('hydrate overwrites per group and coexists with WS upserts (key symmetry)', async () => {
    const queryClient = createTestQueryClient()
    const wrapper = createWrapper(queryClient)

    globalThis.fetch = mock(async () =>
      makeHistoryResponse('session-1', makeMessages(2), false, [
        makeSnapshot('g1', { status: 'started', synthesis: '' }),
      ]),
    ) as unknown as typeof fetch
    const api = setupApi()

    const { result } = renderHook(
      () => {
        const gs = useGroupState()
        const hist = useChatHistory(
          api,
          'session-1',
          'token',
          streaming,
          undefined,
          gs.hydrateGroups,
        )
        return { groups: gs.groups, upsertGroup: gs.upsertGroup, hist }
      },
      { wrapper },
    )
    await waitFor(() => {
      expect(result.current.groups.size).toBe(1)
    })
    expect(result.current.groups.get('g1')?.status).toBe('started')

    act(() => {
      result.current.upsertGroup('g1', (existing) => ({
        ...(existing as GroupInfo),
        status: 'done',
        synthesis: 'live ws synthesis',
      }))
    })
    expect(result.current.groups.size).toBe(1)
    expect(result.current.groups.get('g1')?.status).toBe('done')
  })
})
