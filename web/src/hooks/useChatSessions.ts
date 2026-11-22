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

const META_PAGE_SIZE = 200
const META_MAX_PAGES = 50

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

  const coordinatorRef = useRef<SessionRefreshCoordinator<string | null> | null>(null)
  if (coordinatorRef.current === null) {
    coordinatorRef.current = createSessionRefreshCoordinator<string | null>()
  }
  const coordinator = coordinatorRef.current

  const generationRef = useRef(0)

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

  const runRefreshPass = useCallback(async (): Promise<string | null> => {
    if (!token || !clientId) return null

    const gen = generationRef.current
    const shouldAbort = () => generationRef.current !== gen

    const result = await fetchAllSessions(api, undefined, undefined, true, shouldAbort)

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
    if (!token || !clientId) return null
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

      try {
        await api.createSession(sessionKey, mode)
      } catch (err) {
        console.error('[useChatSessions] Failed to create session on backend:', err)
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
