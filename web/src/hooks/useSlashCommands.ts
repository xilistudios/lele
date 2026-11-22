import { useCallback, useEffect, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import type { SlashCommandInfo } from '../lib/types'

export type SlashCommandsState = {
  commands: SlashCommandInfo[]
  loading: boolean
  error: string | null
  refresh: () => Promise<void>
}

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
