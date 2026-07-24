import { useMemo } from 'react'
import type { GroupInfo } from '../lib/types'

/**
 * Filters and exposes group state for the current session.
 * The actual group state is owned by useMessages (populated via WebSocket
 * event handlers). This hook derives a session-scoped view.
 */
export function useGroups(groups: Map<string, GroupInfo>, currentSessionKey: string | null) {
  const sessionGroups = useMemo(() => {
    if (!currentSessionKey) return []
    // Groups matching the current session are keyed by group events that
    // carry session_key. Since all group events for the active session are
    // already filtered by isSessionMismatch in the handlers, every entry in
    // the map belongs to the current session. We expose all of them.
    return Array.from(groups.values())
  }, [groups, currentSessionKey])

  const hasActiveGroups = useMemo(
    () => sessionGroups.some((g) => g.status === 'started'),
    [sessionGroups],
  )

  return { groups: sessionGroups, hasActiveGroups }
}
