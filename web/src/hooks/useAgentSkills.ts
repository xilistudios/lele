import { type QueryClient, useQueryClient } from '@tanstack/react-query'
import { useCallback, useState } from 'react'
import type { SkillInstallScope } from '../lib/types'
import type { ApiClient } from '../services/http/client'

export const agentCatalogQueryKey = (agentId: string) => ['agentCatalog', agentId] as const

export function invalidateAgentCatalog(queryClient: QueryClient, agentId: string) {
  queryClient.invalidateQueries({ queryKey: agentCatalogQueryKey(agentId) })
}

type Options = {
  agentId: string
}

export function useAgentSkills(api: ApiClient, { agentId }: Options) {
  const queryClient = useQueryClient()
  const [isToggling, setIsToggling] = useState<string | null>(null)
  const [isRemoving, setIsRemoving] = useState<string | null>(null)
  const [isInstalling, setIsInstalling] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(
    () => invalidateAgentCatalog(queryClient, agentId),
    [queryClient, agentId],
  )

  const toggle = useCallback(
    async (name: string, enabled: boolean) => {
      setIsToggling(name)
      setError(null)
      try {
        await api.agentToggleSkill(agentId, name, enabled)
      } catch (err) {
        setError((err as Error).message)
      } finally {
        setIsToggling(null)
        refresh()
      }
    },
    [api, agentId, refresh],
  )

  const remove = useCallback(
    async (name: string) => {
      setIsRemoving(name)
      setError(null)
      try {
        await api.agentRemoveSkill(agentId, name)
      } catch (err) {
        setError((err as Error).message)
      } finally {
        setIsRemoving(null)
        refresh()
      }
    },
    [api, agentId, refresh],
  )

  const install = useCallback(
    async (url: string, scope?: SkillInstallScope) => {
      setIsInstalling(true)
      setError(null)
      try {
        await api.agentInstallSkill(agentId, url, scope)
        return true
      } catch (err) {
        setError((err as Error).message)
        return false
      } finally {
        setIsInstalling(false)
        refresh()
      }
    },
    [api, agentId, refresh],
  )

  const installBatch = useCallback(
    async (repo: string, skills: string[], scope?: SkillInstallScope) => {
      setIsInstalling(true)
      setError(null)
      try {
        await api.agentInstallSkillsBatch(agentId, repo, skills, scope)
        return true
      } catch (err) {
        setError((err as Error).message)
        return false
      } finally {
        setIsInstalling(false)
        refresh()
      }
    },
    [api, agentId, refresh],
  )

  return {
    isToggling,
    isRemoving,
    isInstalling,
    error,
    toggle,
    remove,
    install,
    installBatch,
  }
}
