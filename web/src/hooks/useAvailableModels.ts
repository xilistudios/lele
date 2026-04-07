import { useEffect, useState } from 'react'
import type { ApiClient } from '../lib/api'
import type { ModelGroup } from '../lib/types'

export type AvailableModelsState = {
  available: string[]
  groups: ModelGroup[]
  isLoading: boolean
  error: string | null
}

export function useAvailableModels(api: ApiClient | null) {
  const [state, setState] = useState<AvailableModelsState>({
    available: [],
    groups: [],
    isLoading: true,
    error: null,
  })

  useEffect(() => {
    if (!api) return

    const loadModels = async () => {
      setState((prev) => ({ ...prev, isLoading: true, error: null }))
      try {
        const result = await api.models('', null)
        setState({
          available: result.models,
          groups: result.model_groups ?? [],
          isLoading: false,
          error: null,
        })
      } catch (err) {
        setState((prev) => ({
          ...prev,
          isLoading: false,
          error: err instanceof Error ? err.message : 'Failed to load models',
        }))
      }
    }

    loadModels()
  }, [api])

  return state
}
