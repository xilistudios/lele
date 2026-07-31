import { useCallback, useReducer } from 'react'
import type { GroupInfo } from '../lib/types'

// ── Actions ──────────────────────────────────────────────────────────────────

type GroupAction =
  | { type: 'upsert'; groupId: string; updater: (existing: GroupInfo | undefined) => GroupInfo }
  | { type: 'hydrate'; infos: GroupInfo[] }
  | { type: 'markStopped' }
  | { type: 'clear' }

// ── Reducer ──────────────────────────────────────────────────────────────────

function groupReducer(state: Map<string, GroupInfo>, action: GroupAction): Map<string, GroupInfo> {
  switch (action.type) {
    case 'upsert': {
      const next = new Map(state)
      next.set(action.groupId, action.updater(state.get(action.groupId)))
      return next
    }
    case 'hydrate': {
      const next = new Map(state)
      for (const info of action.infos) {
        next.set(info.groupID, info)
      }
      return next
    }
    case 'markStopped': {
      let found = false
      for (const g of state.values()) {
        if (g.status === 'started') {
          found = true
          break
        }
      }
      if (!found) return state // No change — return same reference to skip re-render

      const next = new Map(state)
      for (const [id, g] of state) {
        if (g.status === 'started') {
          next.set(id, { ...g, status: 'stopped' })
        }
      }
      return next
    }
    case 'clear':
      return new Map()
    default:
      return state
  }
}

// ── Hook ─────────────────────────────────────────────────────────────────────

export function useGroupState() {
  const [groups, dispatch] = useReducer(groupReducer, undefined, () => new Map<string, GroupInfo>())

  const upsertGroup = useCallback(
    (groupId: string, updater: (existing: GroupInfo | undefined) => GroupInfo) => {
      dispatch({ type: 'upsert', groupId, updater })
    },
    [],
  )

  const hydrateGroups = useCallback((infos: GroupInfo[]) => {
    dispatch({ type: 'hydrate', infos })
  }, [])

  const markActiveGroupsStopped = useCallback(() => {
    dispatch({ type: 'markStopped' })
  }, [])

  const clearGroups = useCallback(() => {
    dispatch({ type: 'clear' })
  }, [])

  return {
    groups,
    upsertGroup,
    hydrateGroups,
    markActiveGroupsStopped,
    clearGroups,
  }
}
