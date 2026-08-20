import { useCallback, useState } from 'react'
import type { ApiClient } from '../lib/api'
import { queryClient } from '../lib/queryClient'
import type { ModelGroup } from '../lib/types'

type ModelState = {
  current: string
  available: string[]
  groups: ModelGroup[]
}

// Cached for 1 minute so switching sessions/agents back and forth (or repeated
// bootstrap calls) does not re-fetch the model list over the network every
// time. The list rarely changes within that window.
const MODELS_STALE_TIME = 60_000

export function useModels(api: ApiClient, token: string | null) {
  const [modelState, setModelState] = useState<ModelState>({
    current: '',
    available: [],
    groups: [],
  })

  const loadModels = useCallback(
    async (agentId: string, sessionKey: string | null, hasConversation: boolean) => {
      if (!token) return

      const useSessionModel = Boolean(sessionKey && hasConversation)
      const sessionKeyOrNull = sessionKey ?? null
      // Cache key captures the agent, the session, and whether we are reading
      // the per-session model (sessionModel) or the agent-level model list.
      const queryKey = [
        'models',
        agentId,
        sessionKeyOrNull,
        useSessionModel ? 'session' : 'list',
      ] as const

      const result = await queryClient.fetchQuery<{
        model?: string
        models: string[]
        model_groups?: ModelGroup[]
      }>({
        queryKey: [...queryKey],
        queryFn: () => {
          if (useSessionModel && sessionKey) {
            return api.sessionModel(sessionKey)
          }
          return api.models(agentId, sessionKeyOrNull)
        },
        staleTime: MODELS_STALE_TIME,
      })

      setModelState({
        current: result.model ?? '',
        available: result.models,
        groups: result.model_groups ?? [],
      })
      return result
    },
    [api, token],
  )

  const selectModel = useCallback(
    async (model: string, sessionKey: string) => {
      if (!token || !sessionKey) return

      const result = await api.updateSessionModel(sessionKey, model)
      // Invalidate the cache so the next loadModels call re-fetches the updated
      // model list rather than serving a stale cached list.
      queryClient.invalidateQueries({ queryKey: ['models'] })
      setModelState({
        current: result.model,
        available: result.models,
        groups: result.model_groups ?? [],
      })
      return result
    },
    [api, token],
  )

  const reset = useCallback(() => {
    setModelState({ current: '', available: [], groups: [] })
    queryClient.removeQueries({ queryKey: ['models'] })
  }, [])

  return {
    modelState,
    setModelState,
    loadModels,
    selectModel,
    reset,
  }
}
