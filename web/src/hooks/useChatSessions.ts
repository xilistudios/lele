import { useCallback, useEffect, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import {
  clearCurrentSessionKey,
  loadCurrentSessionKey,
  saveCurrentSessionKey,
} from '../lib/storage'
import type { ChatSession } from '../lib/types'
import { generateUUID } from '../lib/uuid'

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

async function fetchAllSessions(
  api: ApiClient,
  mode?: string,
  kind?: string,
  includeSystem?: boolean,
): Promise<ChatSession[]> {
  const all: ChatSession[] = []
  let offset = 0
  for (;;) {
    const page = await api.sessionsMeta(mode, kind, includeSystem, {
      offset,
      limit: META_PAGE_SIZE,
    })
    all.push(...(page?.sessions ?? []))
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

  const refreshSessions = useCallback(async () => {
    if (!token || !clientId) return null

    // include_system=true merges every persisted session (including system
    // sessions like heartbeat/cron/subagents) from the session manager. The
    // native client only tracks a handful of session keys, so without this
    // the sidebar would silently drop the vast majority of chats (e.g. show
    // ~30 of 300). The /chats page already uses include_system=true.
    const result = await fetchAllSessions(api, undefined, undefined, true)
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
      await api.createSession(sessionKey, mode).catch((err) => {
        console.error('[useChatSessions] Failed to create session on backend:', err)
        return null
      })

      return sessionKey
    },
    [clientId, persistCurrentSessionKey, api],
  )

  const deleteSession = useCallback(
    async (sessionKey: string): Promise<string | null> => {
      if (!token) return null

      await api.deleteSession(sessionKey)
      setSessions((current) => current.filter((s) => s.key !== sessionKey))

      if (sessionKey === currentSessionKeyRef.current) {
        const remainingSessions = sessionsRef.current.filter((s) => s.key !== sessionKey)
        const nextSessionKey = remainingSessions.length > 0 ? remainingSessions[0].key : null
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
    setSessions([])
    persistCurrentSessionKey(null)
  }, [persistCurrentSessionKey])

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
