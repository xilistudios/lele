import { useCallback, useEffect, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import {
  clearCurrentSessionKey,
  loadCurrentSessionKey,
  saveCurrentSessionKey,
} from '../lib/storage'
import type { ChatSession } from '../lib/types'
import { generateUUID } from '../lib/uuid'
import {
  type SessionRefreshCoordinator,
  createSessionRefreshCoordinator,
} from './sessionRefreshCoordinator'

const buildDefaultSessionKey = (clientId: string) => clientId
const isSubagentSessionKey = (sessionKey: string | null | undefined) =>
  Boolean(sessionKey?.startsWith('subagent:'))

// fetchAllSessions retrieves every session from the lightweight metadata
// endpoint. The endpoint returns up to 200 sessions per page and does NOT load
// the full history for each session (unlike the plain /sessions endpoint),
// which is the primary bottleneck when a user has many chats. Without
// pagination we would silently drop older chats once there are more than a
// page.
const META_PAGE_SIZE = 200
// Safety valves for the pagination loop. A proxy or backend that ignored
// `offset` and always answered with page 0 (`has_more: true`) would spin
// forever, and because the coordinator allows only ONE pass in flight that
// would freeze the session list for the whole life of the tab.
// 50 pages x 200 sessions = 10k chats, far above real usage.
const META_MAX_PAGES = 50

/**
 * Walks the paginated metadata endpoint until it is exhausted.
 *
 * `shouldAbort` (optional) is the cancellation guard owned by the caller
 * (the generation of the refresh pass). It is checked BEFORE every request —
 * including the first one — and again once a page has been received, so an
 * invalidated pass stops asking for pages as soon as possible. On abort the
 * pages accumulated so far are returned (instead of throwing): the caller
 * discards the whole pass by generation, and returning keeps the abort path
 * free of error-handling noise. The pagination contract itself is unchanged:
 * the offset advances by `page.sessions.length` and the loop ends on
 * `!has_more` or on an empty page.
 *
 * Two safety valves bound the loop in case the contract is violated by a
 * buggy backend/proxy, so the walk can never spin forever: the loop also stops
 * when a page repeats the previous one's first key (the offset is being
 * ignored, so every page is the same) and after `META_MAX_PAGES` pages. Both
 * exits warn on the console and return what was collected so far.
 */
async function fetchAllSessions(
  api: ApiClient,
  mode?: string,
  kind?: string,
  includeSystem?: boolean,
  shouldAbort?: () => boolean,
): Promise<ChatSession[]> {
  const all: ChatSession[] = []
  let offset = 0
  let pages = 0
  let previousFirstKey: string | undefined
  for (;;) {
    if (shouldAbort?.()) break
    if (pages >= META_MAX_PAGES) {
      console.warn(
        `[useChatSessions] sessions/meta exceeded ${META_MAX_PAGES} pages; aborting pagination`,
      )
      break
    }
    const page = await api.sessionsMeta(mode, kind, includeSystem, {
      offset,
      limit: META_PAGE_SIZE,
    })
    const firstKey = page?.sessions?.[0]?.key
    if (firstKey !== undefined && firstKey === previousFirstKey) {
      console.warn('[useChatSessions] sessions/meta returned a repeated page; aborting pagination')
      break
    }
    previousFirstKey = firstKey
    pages += 1
    all.push(...(page?.sessions ?? []))
    if (shouldAbort?.()) break
    if (!page?.has_more || page.sessions.length === 0) break
    offset += page.sessions.length
  }
  return all
}

export function useChatSessions(api: ApiClient, token: string | null, clientId: string | null) {
  const [sessions, setSessions] = useState<ChatSession[]>([])
  const [currentSessionKey, setCurrentSessionKey] = useState<string | null>(() =>
    loadCurrentSessionKey(),
  )
  const sessionsRef = useRef(sessions)
  const currentSessionKeyRef = useRef(currentSessionKey)

  // Single-flight coordinator (stable for the whole life of the hook). It
  // collapses a burst of refresh triggers into ONE paginated pass and runs at
  // most ONE trailing pass with the latest task, instead of walking the
  // paginated metadata endpoint once per trigger.
  const coordinatorRef = useRef<SessionRefreshCoordinator<string | null> | null>(null)
  if (coordinatorRef.current === null) {
    coordinatorRef.current = createSessionRefreshCoordinator<string | null>()
  }
  const coordinator = coordinatorRef.current

  // Bumped by `reset()` so an in-flight pass can detect that it was
  // invalidated and avoid re-writing (or reviving) the session list.
  const generationRef = useRef(0)

  // False after the hook is gone. A pass that resolves after unmount must not
  // write state nor `localStorage`: on a forced logout (unrecoverable 401) the
  // tree unmounts WITHOUT `reset()`, so the generation guard alone would let a
  // slow /sessions/meta response persist the previous user's session key.
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  useEffect(() => {
    sessionsRef.current = sessions
  }, [sessions])

  useEffect(() => {
    currentSessionKeyRef.current = currentSessionKey
  }, [currentSessionKey])

  const persistCurrentSessionKey = useCallback((sessionKey: string | null) => {
    currentSessionKeyRef.current = sessionKey
    setCurrentSessionKey(sessionKey)
    if (sessionKey) {
      saveCurrentSessionKey(sessionKey)
      return
    }
    clearCurrentSessionKey()
  }, [])

  const touchSession = useCallback((sessionKey: string, name?: string, mode?: string) => {
    setSessions((current) =>
      current.map((s) =>
        s.key === sessionKey
          ? {
              ...s,
              updated: new Date().toISOString(),
              ...(name ? { name } : {}),
              ...(mode ? { mode: mode as ChatSession['mode'] } : {}),
            }
          : s,
      ),
    )
  }, [])

  // The refresh pass itself. Its identity is stable as long as `api`, `token`,
  // `clientId` and `persistCurrentSessionKey` do not change, which is what
  // lets the coordinator recognise a re-trigger of the SAME task and skip a
  // redundant trailing pass.
  const runRefreshPass = useCallback(async (): Promise<string | null> => {
    // `refreshSessions` refuses to start a pass without a token and a client
    // id; re-checking here keeps the pass self-contained (so its task identity
    // is independent of the call site) and narrows the types. It is also what
    // makes `token` a real dependency of the pass: a new credential must
    // produce a NEW task, so a coalesced trailing pass re-runs with it instead
    // of being skipped as a duplicate.
    if (!token || !clientId) return null

    // Generation captured when the pass starts: if `reset()` bumps it while
    // the pass is fetching, the pass must neither apply its result nor keep
    // requesting pages.
    const gen = generationRef.current
    const shouldAbort = () => generationRef.current !== gen

    // include_system=true merges every persisted session (including system
    // sessions like heartbeat/cron/subagents) from the session manager. The
    // native client only tracks a handful of session keys, so without this
    // the sidebar would silently drop the vast majority of chats (e.g. show
    // ~30 of 300). The /chats page already uses include_system=true.
    const result = await fetchAllSessions(api, undefined, undefined, true, shouldAbort)

    // Nothing below this line awaits, so one guard is enough to make sure an
    // invalidated pass never touches the state.
    // Same reason as the generation guard, but for unmount (which does not bump
    // any generation on the forced-logout path).
    if (!mountedRef.current) return null
    if (shouldAbort()) return null

    const defaultSessionKey = buildDefaultSessionKey(clientId)
    const fallbackSessions =
      result.length > 0
        ? result
        : [
            {
              key: defaultSessionKey,
              created: new Date().toISOString(),
              updated: new Date().toISOString(),
            },
          ]

    let nextSessions = fallbackSessions.sort(
      (b, a) => new Date(a.updated).getTime() - new Date(b.updated).getTime(),
    )

    // Keep locally-created session in the list even if not yet on the backend.
    // Subagent sessions are intentionally excluded: they are nested views
    // (parent/subagent) rather than top-level sessions, so they must not
    // appear in the sidebar list (or they would shadow the name derived from
    // their messages in ChatPageContext).
    if (
      currentSessionKeyRef.current &&
      !isSubagentSessionKey(currentSessionKeyRef.current) &&
      !nextSessions.some((s) => s.key === currentSessionKeyRef.current)
    ) {
      nextSessions = [
        {
          key: currentSessionKeyRef.current,
          created: new Date().toISOString(),
          updated: new Date().toISOString(),
        },
        ...nextSessions,
      ]
    }

    setSessions(nextSessions)

    const availableKeys = new Set(nextSessions.map((item) => item.key))
    const fallbackKey = availableKeys.has(defaultSessionKey)
      ? defaultSessionKey
      : (nextSessions[0]?.key ?? null)
    const storedSessionKey = loadCurrentSessionKey()
    const nextSessionKey = isSubagentSessionKey(currentSessionKeyRef.current)
      ? currentSessionKeyRef.current
      : storedSessionKey && availableKeys.has(storedSessionKey)
        ? storedSessionKey
        : currentSessionKeyRef.current && availableKeys.has(currentSessionKeyRef.current)
          ? currentSessionKeyRef.current
          : fallbackKey

    persistCurrentSessionKey(nextSessionKey)
    return nextSessionKey
  }, [api, token, clientId, persistCurrentSessionKey])

  const refreshSessions = useCallback(async (): Promise<string | null> => {
    // NOTE: this guard runs BEFORE the coordinator, so a pass already in flight
    // (started while credentials were valid) is neither aborted nor observed
    // here and will still run to completion. What keeps such a pass from
    // writing after the credentials are gone is NOT this guard: it is the
    // generation guard for `reset()` (which invalidates the pass first) and
    // `mountedRef` for the forced-logout path, where the tree unmounts without
    // `reset()`. Accepted deliberately: moving the check inside the
    // coordinator would leak its internals into the hook.
    if (!token || !clientId) return null
    // Single-flight: concurrent triggers join the pass in progress (one
    // paginated walk) and at most one trailing pass runs afterwards.
    return coordinator.run(runRefreshPass)
  }, [token, clientId, runRefreshPass, coordinator])

  const selectSession = useCallback(
    (sessionKey: string) => {
      persistCurrentSessionKey(sessionKey)
    },
    [persistCurrentSessionKey],
  )

  const createSession = useCallback(
    async (mode?: string): Promise<string | null> => {
      if (!clientId) return null

      const sessionKey = generateUUID()
      const previousSessionKey = currentSessionKeyRef.current
      const newSession: ChatSession = {
        key: sessionKey,
        created: new Date().toISOString(),
        updated: new Date().toISOString(),
        ...(mode ? { mode: mode as ChatSession['mode'] } : {}),
      }

      setSessions((current) =>
        [newSession, ...current.filter((s) => s.key !== sessionKey)].sort(
          (b, a) => new Date(a.updated).getTime() - new Date(b.updated).getTime(),
        ),
      )
      persistCurrentSessionKey(sessionKey)

      // Await the API call to ensure backend confirms session creation before navigation
      try {
        await api.createSession(sessionKey, mode)
      } catch (err) {
        console.error('[useChatSessions] Failed to create session on backend:', err)
        // Roll back the optimistic local session so the sidebar does not show
        // a phantom chat that the backend never registered.
        setSessions((current) => current.filter((s) => s.key !== sessionKey))
        if (currentSessionKeyRef.current === sessionKey) {
          persistCurrentSessionKey(previousSessionKey)
        }
        return null
      }

      return sessionKey
    },
    [clientId, persistCurrentSessionKey, api],
  )

  const deleteSession = useCallback(
    async (sessionKey: string): Promise<string | null> => {
      if (!token) return null

      await api.deleteSession(sessionKey)

      let nextSessionKey: string | null = null
      setSessions((current) => {
        const remainingSessions = current.filter((s) => s.key !== sessionKey)
        if (sessionKey === currentSessionKeyRef.current) {
          nextSessionKey = remainingSessions.length > 0 ? remainingSessions[0].key : null
        }
        return remainingSessions
      })

      if (sessionKey === currentSessionKeyRef.current) {
        persistCurrentSessionKey(nextSessionKey)
        return nextSessionKey
      }
      return currentSessionKeyRef.current
    },
    [api, token, persistCurrentSessionKey],
  )

  const clearSession = useCallback(
    async (sessionKey: string) => {
      if (!token) return

      await api.clearSession(sessionKey)
      await refreshSessions()
    },
    [api, token, refreshSessions],
  )

  const reset = useCallback(() => {
    // Invalidate any pass in flight BEFORE clearing the state, so a pass that
    // is still fetching cannot apply (and thus revive) the old list; the
    // coordinator also drops any coalesced trailing refresh.
    generationRef.current += 1
    coordinator.cancel()
    setSessions([])
    persistCurrentSessionKey(null)
  }, [persistCurrentSessionKey, coordinator])

  return {
    sessions,
    currentSessionKey,
    currentSessionKeyRef,
    sessionsRef,
    persistCurrentSessionKey,
    refreshSessions,
    touchSession,
    selectSession,
    createSession,
    deleteSession,
    clearSession,
    reset,
  }
}
