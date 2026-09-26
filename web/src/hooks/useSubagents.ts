import { useCallback, useEffect, useState } from 'react'
import { useAuthContext } from '../contexts/AuthContext'
import type { SubagentTaskInfo } from '../lib/types'
import { useOnPageVisible, usePageVisible } from './usePageVisible'

export type { SubagentTaskInfo as SubagentInfo }

/**
 * Lowest interval this hook will ever use for polling the subagent list.
 *
 * Contract: `pollIntervalMs <= 0` means "do not poll at all", while any
 * positive value below this floor is raised to it. Without the floor a caller
 * asking for (or accidentally passing) a tiny interval ends up with
 * `setInterval(fn, 0)`, which browsers clamp to ~4 ms — and this endpoint is
 * expensive on the server (SessionManager exclusive write lock + full SQLite
 * load), so a "0 means no polling" mistake floods the gateway with hundreds of
 * requests per second and stalls streaming for every session.
 */
export const MIN_POLL_MS = 1000

/**
 * "Never poll" sentinel for `pollIntervalMs`.
 *
 * `useSubagents` treats a non-positive interval as polling disabled and raises
 * any positive value below `MIN_POLL_MS` to it, because `setInterval(fn, 0)` is
 * clamped by browsers to ~4 ms and this endpoint is expensive on the server.
 * Use this constant — never a small number — wherever a one-shot read is
 * enough; the refresh mechanisms are the mount/session-change fetch and the
 * `notifySubagentsChanged()` fan-out.
 */
export const NO_POLL_MS = 0

/**
 * Tiny fan-out so WS spawn events can wake every `useSubagents` instance.
 *
 * Without this the list only refetched on mount / session change (and only
 * polled when a subagent was already running), so a freshly spawned task
 * stayed invisible until a full chat refresh.
 */
const subagentListeners = new Set<() => void>()

/** Called by spawn tool handlers when a subagent is created or updated. */
export function notifySubagentsChanged(): void {
  for (const listener of subagentListeners) listener()
}

export function useSubagents(
  sessionKey: string | null,
  pollIntervalMs = 5000,
  /**
   * When true, keep polling even if the current list has no running/pending
   * tasks — a spawn can appear mid-turn before the first list refresh.
   */
  active = false,
) {
  const { api } = useAuthContext()
  const [subagents, setSubagents] = useState<SubagentTaskInfo[]>([])
  const [loading, setLoading] = useState(false)
  const [hasRunning, setHasRunning] = useState(false)
  const pageVisible = usePageVisible()

  const fetchSubagents = useCallback(async () => {
    if (!sessionKey) {
      setSubagents([])
      return
    }
    setLoading(true)
    try {
      const data = await api.sessionSubagents(sessionKey)
      setSubagents(data?.subagents ?? [])
    } catch (err) {
      console.warn('[useSubagents] Failed to fetch:', err)
      setSubagents([])
    } finally {
      setLoading(false)
    }
  }, [sessionKey, api])

  // Fetch on mount and when session changes
  useEffect(() => {
    fetchSubagents()
  }, [fetchSubagents])

  // Wake on spawn / tool.result / subagent.result so a new task appears
  // without requiring a chat refresh.
  useEffect(() => {
    const listener = () => {
      void fetchSubagents()
    }
    subagentListeners.add(listener)
    return () => {
      subagentListeners.delete(listener)
    }
  }, [fetchSubagents])

  // Track running state so the polling effect can react to it. Pending tasks
  // are also live work: they transition to running once dependencies clear.
  useEffect(() => {
    setHasRunning(subagents.some((s) => s.status === 'running' || s.status === 'pending'))
  }, [subagents])

  // Poll while any subagent is live, OR while the parent turn is live (a
  // spawn can land before the list has been refreshed once).
  const shouldPoll = hasRunning || active
  useEffect(() => {
    // `pollIntervalMs <= 0` disables polling entirely (see MIN_POLL_MS).
    if (!shouldPoll || pollIntervalMs <= 0) return
    // Hidden tab: no interval at all. Visibility is an effect dependency, so
    // this effect re-runs when the tab is shown again — and the
    // `useOnPageVisible` refresh below fires the single immediate read.
    if (!pageVisible) return

    // Non-finite-safe floor, inlined so `pollIntervalMs` is the only interval
    // dependency of this effect. A positive value below the floor is raised to
    // it, and anything that is not a finite positive number (e.g. `NaN` from a
    // caller's bad arithmetic) also falls back to it: `Math.max(NaN, floor)` is
    // `NaN`, and `NaN <= 0` is false, so a bare `Math.max` would let the NaN
    // straight into `setInterval` — clamped by browsers to ~4 ms, which is the
    // request storm MIN_POLL_MS exists to prevent.
    const intervalMs =
      Number.isFinite(pollIntervalMs) && pollIntervalMs > 0
        ? Math.max(pollIntervalMs, MIN_POLL_MS)
        : MIN_POLL_MS

    const id = setInterval(() => {
      fetchSubagents()
    }, intervalMs)
    return () => clearInterval(id)
  }, [fetchSubagents, shouldPoll, pollIntervalMs, pageVisible])

  // Refresh once when the tab becomes visible again: the interval above was
  // cleared while hidden, so without this the list would stay stale for up to
  // one interval after the user returns — and WS events delivered while the tab
  // was backgrounded may never have been applied. Instances that do not poll
  // (`pollIntervalMs <= 0`, the NO_POLL_MS contract) keep their
  // mount/`notifySubagentsChanged` refresh path: the visibility refresh must not
  // turn them into pollers, so it carries the same guard as the effect above.
  useOnPageVisible(() => {
    if (!shouldPoll || pollIntervalMs <= 0) return
    void fetchSubagents()
  })

  return { subagents, loading, refresh: fetchSubagents }
}
