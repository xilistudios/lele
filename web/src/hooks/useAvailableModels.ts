import { useEffect, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import type { ModelGroup } from '../lib/types'

export type AvailableModelsState = {
  available: string[]
  groups: ModelGroup[]
  isLoading: boolean
  isReloading: boolean
  error: string | null
}

export function useAvailableModels(api: ApiClient | null) {
  const [state, setState] = useState<AvailableModelsState>({
    available: [],
    groups: [],
    isLoading: true,
    isReloading: false,
    error: null,
  })
  // Guards against out-of-order responses when `api` changes mid-flight.
  const generationRef = useRef(0)

  useEffect(() => {
    if (!api) return

    const generation = ++generationRef.current

    const loadModels = async () => {
      // Distinguish first load (no data yet) from background refetch.
      setState((prev) => {
        const isFirstLoad = prev.available.length === 0 && prev.groups.length === 0
        return {
          ...prev,
          isLoading: isFirstLoad,
          isReloading: !isFirstLoad,
          error: null,
        }
      })

      try {
        const result = await api.models('', null)
        if (generation !== generationRef.current) return
        setState({
          available: result.models,
          groups: result.model_groups ?? [],
          isLoading: false,
          isReloading: false,
          error: null,
        })
      } catch (err) {
        if (generation !== generationRef.current) return
        setState((prev) => ({
          ...prev,
          isLoading: false,
          isReloading: false,
          error: err instanceof Error ? err.message : 'Failed to load models',
        }))
      }
    }

    loadModels()
  }, [api])

  return state
}