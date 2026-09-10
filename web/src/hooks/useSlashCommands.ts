import { useCallback, useEffect, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import type { SlashCommandInfo } from '../lib/types'

export type SlashCommandsState = {
  commands: SlashCommandInfo[]
  loading: boolean
  error: string | null
  refresh: () => Promise<void>
}

/**
 * Loads the backend slash commands for the agent that will answer the next
 * message, refetching whenever `api` or `agentId` changes.
 *
 * Mirrors useAvailableModels (plain useEffect, no react-query). The built-in
 * half of the catalog is global, but custom (harness) commands are per-agent
 * and can change at runtime through the agent's Commands page, so the fetch is
 * keyed on agentId instead of being a one-shot per api instance. `refresh` is
 * exposed for callers that want to re-read it. A mounted guard keeps a late
 * response from setting state on an unmounted composer.
 *
 * On error the list stays empty on purpose: the palette silently degrades to a
 * plain composer instead of blocking the user, so `error` is informational only.
 */
export function useSlashCommands(api: ApiClient | null, agentId?: string): SlashCommandsState {
  const [commands, setCommands] = useState<SlashCommandInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const mountedRef = useRef(true)

  const fetchCommands = useCallback(async () => {
    if (!api) return
    setLoading(true)
    setError(null)
    try {
      const res = await api.chatCommands(agentId)
      if (!mountedRef.current) return
      setCommands(res.commands ?? [])
    } catch (err) {
      if (!mountedRef.current) return
      setError(err instanceof Error ? err.message : 'Failed to load commands')
      setCommands([])
    } finally {
      if (mountedRef.current) setLoading(false)
    }
  }, [api, agentId])

  useEffect(() => {
    if (!api) return
    mountedRef.current = true
    fetchCommands()
    return () => {
      mountedRef.current = false
    }
  }, [api, fetchCommands])

  return { commands, loading, error, refresh: fetchCommands }
}
