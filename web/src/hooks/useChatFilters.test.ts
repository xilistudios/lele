import { describe, expect, it } from 'bun:test'
import { act, renderHook } from '@testing-library/react'
import type { ChatSession } from '../lib/types'
import { useChatFilters } from './useChatFilters'

function makeSession(overrides: Partial<ChatSession> & { key: string }): ChatSession {
  return {
    key: overrides.key,
    name: overrides.name,
    kind: overrides.kind,
    created: overrides.created ?? '2026-01-01T00:00:00.000Z',
    updated: overrides.updated ?? '2026-01-01T00:00:00.000Z',
    message_count: overrides.message_count ?? 1,
  }
}

describe('useChatFilters', () => {
  it('drops empty sessions (0 messages) by default', () => {
    const sessions = [
      makeSession({ key: 'chat-1', message_count: 3 }),
      makeSession({ key: 'heartbeat', message_count: 0 }),
    ]
    const { result } = renderHook(() => useChatFilters(sessions))
    expect(result.current.filteredSessions.map((s) => s.key)).toEqual(['chat-1'])
  })

  it('keeps empty sessions when includeEmpty is true', () => {
    const sessions = [
      makeSession({ key: 'chat-1', message_count: 3 }),
      makeSession({ key: 'heartbeat', message_count: 0, kind: 'heartbeat' }),
    ]
    const { result } = renderHook(() => useChatFilters(sessions, { includeEmpty: true }))
    expect(result.current.filteredSessions.map((s) => s.key).sort()).toEqual([
      'chat-1',
      'heartbeat',
    ])
  })

  it('filters by query across kind sessions', () => {
    const sessions = [
      makeSession({ key: 'cron-abc', name: 'Daily report', kind: 'cron', message_count: 0 }),
      makeSession({ key: 'chat-2', name: 'Talk', message_count: 2 }),
    ]
    const { result } = renderHook(() => useChatFilters(sessions, { includeEmpty: true }))
    act(() => {
      result.current.setQuery('report')
    })
    expect(result.current.filteredSessions.map((s) => s.key)).toEqual(['cron-abc'])
  })

  it('supports sort modes on empty sessions', () => {
    const sessions = [
      makeSession({
        key: 'a',
        name: 'Alpha',
        message_count: 0,
        updated: '2026-01-02T00:00:00.000Z',
      }),
      makeSession({
        key: 'b',
        name: 'Beta',
        message_count: 0,
        updated: '2026-01-01T00:00:00.000Z',
      }),
    ]
    const { result } = renderHook(() => useChatFilters(sessions, { includeEmpty: true }))
    expect(result.current.filteredSessions.map((s) => s.key)).toEqual(['a', 'b'])
    act(() => {
      result.current.setSortMode('name')
    })
    expect(result.current.filteredSessions.map((s) => s.key)).toEqual(['a', 'b'])
    act(() => {
      result.current.setSortMode('recent')
    })
    expect(result.current.filteredSessions.map((s) => s.key)).toEqual(['a', 'b'])
  })
})
