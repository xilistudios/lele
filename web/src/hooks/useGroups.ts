import { useMemo } from 'react'
import type { GroupInfo } from '../lib/types'

export function useGroups(groups: Map<string, GroupInfo>, currentSessionKey: string | null) {
  const sessionGroups = useMemo(() => {
    if (!currentSessionKey) return []
    return Array.from(groups.values())
  }, [groups, currentSessionKey])

  const hasActiveGroups = useMemo(
    () => sessionGroups.some((g) => g.status === 'started'),
    [sessionGroups],
  )

  return { groups: sessionGroups, hasActiveGroups }
}
