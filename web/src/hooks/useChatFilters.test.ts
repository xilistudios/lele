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
  }
}

describe('useChatFilters', () => {
  it('drops sessions with invalid dates', () => {
    const sessions = [
      makeSession({ key: 'chat-1' }),
      makeSession({ key: 'bad', updated: 'invalid-date-string' }),
    ]
    const { result } = renderHook(() => useChatFilters(sessions))
    expect(result.current.filteredSessions.map((s) => s.key)).toEqual(['chat-1'])
  })

  it('filters by query across kind sessions', () => {
    const sessions = [
      makeSession({ key: 'cron-abc', name: 'Daily report', kind: 'cron' }),
      makeSession({ key: 'chat-2', name: 'Talk' }),
    ]
    const { result } = renderHook(() => useChatFilters(sessions))
    act(() => {
      result.current.setQuery('report')
    })
    expect(result.current.filteredSessions.map((s) => s.key)).toEqual(['cron-abc'])
  })

  it('supports sort modes', () => {
    const sessions = [
      makeSession({
        key: 'a',
        name: 'Alpha',
        updated: '2026-01-02T00:00:00.000Z',
      }),
      makeSession({
        key: 'b',
        name: 'Beta',
        updated: '2026-01-01T00:00:00.000Z',
      }),
    ]
    const { result } = renderHook(() => useChatFilters(sessions))
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