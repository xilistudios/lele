import { describe, expect, it, mock } from 'bun:test'
import { act, renderHook, waitFor } from '@testing-library/react'
import { useModels } from './useModels'

describe('useModels', () => {
  it('loadModels carga modelos del servidor', async () => {
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
      sessionModel: mock(() =>
        Promise.resolve({
          session_key: 'native:client:1',
          model: 'gpt-4o',
          models: ['gpt-4o', 'gpt-4'],
          model_groups: [],
        }),
      ),
      updateSessionModel: mock(() =>
        Promise.resolve({
          session_key: 'native:client:1',
          model: 'gpt-4o',
          models: ['gpt-4o', 'gpt-4'],
          model_groups: [],
        }),
      ),
    }

    const { result } = renderHook(() => useModels(mockApi as never, 'token-123'))

    // Estado inicial
    expect(result.current.modelState.current).toBe('')
    expect(result.current.modelState.available).toHaveLength(0)

    // Cargar modelos sin sesión (usa models())
    await act(async () => {
      await result.current.loadModels('main', null, false)
    })

    await waitFor(() => {
      expect(result.current.modelState.available).toEqual(['gpt-4', 'gpt-4o-mini'])
    })
    expect(result.current.modelState.current).toBe('gpt-4')

    // Cargar modelos con sesión y conversación (usa sessionModel())
    await act(async () => {
      await result.current.loadModels('main', 'native:client:1', true)
    })

    await waitFor(() => {
      expect(result.current.modelState.available).toEqual(['gpt-4o', 'gpt-4'])
    })
    expect(result.current.modelState.current).toBe('gpt-4o')
  })

  it('selectModel actualiza el modelo en la sesión', async () => {
    const mockApi = {
      models: mock(() => Promise.resolve({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] })),
      sessionModel: mock(() =>
        Promise.resolve({
          session_key: 'native:client:1',
          model: 'gpt-4',
          models: ['gpt-4'],
          model_groups: [],
        }),
      ),
      updateSessionModel: mock(() =>
        Promise.resolve({
          session_key: 'native:client:1',
          model: 'gpt-4o',
          models: ['gpt-4o', 'gpt-4'],
          model_groups: [],
        }),
      ),
    }

    const { result } = renderHook(() => useModels(mockApi as never, 'token-123'))

    await act(async () => {
      await result.current.selectModel('gpt-4o', 'native:client:1')
    })

    await waitFor(() => {
      expect(result.current.modelState.current).toBe('gpt-4o')
    })

    expect(mockApi.updateSessionModel).toHaveBeenCalledWith('native:client:1', 'gpt-4o')
    expect(result.current.modelState.available).toEqual(['gpt-4o', 'gpt-4'])
  })

  it('reset limpia el estado', async () => {
    const mockApi = {
      models: mock(() => Promise.resolve({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] })),
      sessionModel: mock(() => Promise.resolve({ session_key: 'x', model: '', models: [] })),
      updateSessionModel: mock(() => Promise.resolve({ session_key: 'x', model: '', models: [] })),
    }

    const { result } = renderHook(() => useModels(mockApi as never, 'token'))

    // Cargar algo
    await act(async () => {
      await result.current.loadModels('main', null, false)
    })

    await waitFor(() => {
      expect(result.current.modelState.current).toBe('gpt-4')
    })

    // Reset
    act(() => {
      result.current.reset()
    })
    expect(result.current.modelState.current).toBe('')
    expect(result.current.modelState.available).toHaveLength(0)
  })

  it('no carga modelos si no hay token', async () => {
    const mockApi = {
      models: mock(() => Promise.resolve({ agent_id: 'main', model: '', models: [] })),
    }

    const { result } = renderHook(() => useModels(mockApi as never, null))

    await act(async () => {
      await result.current.loadModels('main', null, false)
    })
    expect(mockApi.models).not.toHaveBeenCalled()
  })
})
