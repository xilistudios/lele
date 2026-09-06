import { describe, expect, mock, test } from 'bun:test'
import { renderHook, waitFor } from '@testing-library/react'
import type { SlashCommandInfo } from '../lib/types'
import { useSlashCommands } from './useSlashCommands'

const clear: SlashCommandInfo = { name: '/clear', description: 'Clear.', usage: '/clear' }
const compact: SlashCommandInfo = { name: '/compact', description: 'Compact.', usage: '/compact' }

describe('useSlashCommands', () => {
  test('carga comandos exitosamente', async () => {
    const mockApi = {
      chatCommands: mock(() => Promise.resolve({ commands: [clear, compact] })),
    }

    const { result } = renderHook(() => useSlashCommands(mockApi as never))

    await waitFor(() => expect(result.current.loading).toBe(false))

    expect(result.current.commands).toEqual([clear, compact])
    expect(result.current.error).toBeNull()
    expect(mockApi.chatCommands).toHaveBeenCalledTimes(1)
  })

  test('en error degrada a lista vacía', async () => {
    const mockApi = {
      chatCommands: mock(() => Promise.reject(new Error('Network error'))),
    }

    const { result } = renderHook(() => useSlashCommands(mockApi as never))

    await waitFor(() => expect(result.current.loading).toBe(false))

    expect(result.current.commands).toEqual([])
    expect(result.current.error).toBe('Network error')
  })

  test('responde sin campo commands sin romper nada', async () => {
    const mockApi = { chatCommands: mock(() => Promise.resolve({})) }

    const { result } = renderHook(() => useSlashCommands(mockApi as never))

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.commands).toEqual([])
    expect(result.current.error).toBeNull()
  })

  test('con api null no hace fetch', () => {
    const { result } = renderHook(() => useSlashCommands(null))

    expect(result.current.loading).toBe(true)
    expect(result.current.commands).toEqual([])
  })

  test('custom commands (with source) flow through untouched', async () => {
    const builtin: SlashCommandInfo = { name: '/clear', description: 'Clear.', usage: '/clear' }
    const custom: SlashCommandInfo = {
      name: '/review',
      description: 'Review the current diff',
      usage: '/review [args]',
      source: 'workspace',
    }
    const mockApi = {
      chatCommands: mock(() => Promise.resolve({ commands: [builtin, custom] })),
    }

    const { result } = renderHook(() => useSlashCommands(mockApi as never))

    await waitFor(() => expect(result.current.loading).toBe(false))

    // The hook must not whitelist fields: `source` reaches the palette as-is
    // (built-ins keep it undefined, custom ones carry their discovery level).
    expect(result.current.commands).toEqual([builtin, custom])
    expect(result.current.commands[0].source).toBeUndefined()
    expect(result.current.commands[1].source).toBe('workspace')
    expect(result.current.commands[1].usage).toBe('/review [args]')
  })
})
