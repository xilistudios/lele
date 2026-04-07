import { useCallback, useState } from 'react'
import type { ApiClient } from '../lib/api'
import type { ModelGroup } from '../lib/types'

type ModelState = {
  current: string
  available: string[]
  groups: ModelGroup[]
}

export function useModels(api: ApiClient, token: string | null) {
  const [modelState, setModelState] = useState<ModelState>({
    current: '',
    available: [],
    groups: [],
  })

  const loadModels = useCallback(
    async (agentId: string, sessionKey: string | null, hasConversation: boolean) => {
      if (!token) return

      const result =
        sessionKey && hasConversation
          ? await api.sessionModel(sessionKey)
          : await api.models(agentId, sessionKey)

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
  }, [])

  return {
    modelState,
    setModelState,
    loadModels,
    selectModel,
    reset,
  }
}
