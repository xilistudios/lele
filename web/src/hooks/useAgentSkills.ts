import { type QueryClient, useQueryClient } from '@tanstack/react-query'
import { useCallback, useState } from 'react'
import type { SkillInstallScope } from '../lib/types'
import type { ApiClient } from '../services/http/client'

/**
 * Per-agent skill mutations (workspace administration).
 *
 * Why a hook and not inline handlers: the same four operations (toggle,
 * remove, install one, install a batch) each need pending-state tracking and a
 * catalog refresh, and the per-agent Skills tab is the only consumer — keeping
 * it here leaves the component declarative.
 *
 * Reads are NOT here: the list comes from `api.getAgentCatalog(agentId)` via
 * react-query (one endpoint, one source of truth). After any mutation the
 * catalog query key is invalidated so the grid re-fetches what the agent's
 * loader now reports — no optimistic state that could disagree with disk.
 *
 * These routes write to the AGENT's workspace (`<agent workspace>/skills` and
 * its `.lele/workspace.json`). The global `/api/v1/skills` endpoints used by
 * the Skills page are a different concern and untouched.
 */

/** Query key of the per-agent catalog. Kept in sync with AgentSkillsSection. */
export const agentCatalogQueryKey = (agentId: string) => ['agentCatalog', agentId] as const

/** Invalidate the catalog of one agent after a mutation. */
export function invalidateAgentCatalog(queryClient: QueryClient, agentId: string) {
  queryClient.invalidateQueries({ queryKey: agentCatalogQueryKey(agentId) })
}

type Options = {
  /** Agent whose catalog is invalidated after every mutation. */
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

  /** Install one skill. `scope` omitted = the server default (workspace). */
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
