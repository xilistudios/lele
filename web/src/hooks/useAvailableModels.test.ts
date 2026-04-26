import { describe, expect, mock, test } from 'bun:test'
import { renderHook, waitFor } from '@testing-library/react'
import { useAvailableModels } from './useAvailableModels'

describe('useAvailableModels', () => {
  test('carga modelos exitosamente', async () => {
    const mockApi = {
      models: mock(() =>
        Promise.resolve({
          agent_id: 'main',
          model: 'gpt-4',
          models: ['gpt-4', 'gpt-4o-mini'],
          model_groups: [
            {
              provider: 'openai',
              models: [
                { value: 'gpt-4', label: 'GPT-4' },
                { value: 'gpt-4o-mini', label: 'GPT-4 Mini' },
              ],
            },
          ],
        }),
      ),
    }

    const { result } = renderHook(() => useAvailableModels(mockApi as never))

    await waitFor(() => expect(result.current.isLoading).toBe(false))

    expect(result.current.available).toEqual(['gpt-4', 'gpt-4o-mini'])
    expect(result.current.groups).toHaveLength(1)
    expect(result.current.groups[0].provider).toBe('openai')
    expect(result.current.error).toBeNull()
  })

  test('maneja error en la carga de modelos', async () => {
    const mockApi = {
      models: mock(() => Promise.reject(new Error('Network error'))),
    }

    const { result } = renderHook(() => useAvailableModels(mockApi as never))

    await waitFor(() => expect(result.current.isLoading).toBe(false))

    expect(result.current.available).toHaveLength(0)
    expect(result.current.error).toBe('Network error')
  })

  test('no hace nada si api es null', () => {
    const { result } = renderHook(() => useAvailableModels(null))

    expect(result.current.isLoading).toBe(true)
    expect(result.current.available).toHaveLength(0)
  })
})
