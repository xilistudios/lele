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
 * Loads the backend slash commands once, when `api` becomes available.
 *
 * Mirrors useAvailableModels (plain useEffect, no react-query): the registry is
 * package data on the server and only changes across restarts, so one fetch per
 * api instance is enough. `refresh` is exposed for callers that want to re-read
 * it. A mounted guard keeps a late response from setting state on an unmounted
 * composer.
 *
 * On error the list stays empty on purpose: the palette silently degrades to a
 * plain composer instead of blocking the user, so `error` is informational only.
 */
export function useSlashCommands(api: ApiClient | null): SlashCommandsState {
  const [commands, setCommands] = useState<SlashCommandInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const mountedRef = useRef(true)

  const fetchCommands = useCallback(async () => {
    if (!api) return
    setLoading(true)
    setError(null)
    try {
      const res = await api.chatCommands()
      if (!mountedRef.current) return
      setCommands(res.commands ?? [])
    } catch (err) {
      if (!mountedRef.current) return
      setError(err instanceof Error ? err.message : 'Failed to load commands')
      setCommands([])
    } finally {
      if (mountedRef.current) setLoading(false)
    }
  }, [api])

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
