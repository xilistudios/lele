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

  const touchSession = useCallback((sessionKey: string, name?: string) => {
    setSessions((current) =>
      current.map((s) =>
        s.key === sessionKey
          ? {
              ...s,
              updated: new Date().toISOString(),
              message_count: s.message_count + 1,
              ...(name ? { name } : {}),
            }
          : s,
      ),
    )
  }, [])

  const refreshSessions = useCallback(async () => {
    if (!token || !clientId) return null

    const result = await api.sessions()
    const defaultSessionKey = buildDefaultSessionKey(clientId)
    const fallbackSessions =
      result.sessions.length > 0
        ? result.sessions
        : [
            {
              key: defaultSessionKey,
              created: new Date().toISOString(),
              updated: new Date().toISOString(),
              message_count: 0,
            },
          ]

    let nextSessions = fallbackSessions.sort(
      (b, a) => new Date(a.updated).getTime() - new Date(b.updated).getTime(),
    )

    // Keep locally-created session in the list even if not yet on the backend
    if (
      currentSessionKeyRef.current &&
      !nextSessions.some((s) => s.key === currentSessionKeyRef.current)
    ) {
      nextSessions = [
        {
          key: currentSessionKeyRef.current,
          created: new Date().toISOString(),
          updated: new Date().toISOString(),
          message_count: 0,
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
          : currentSessionKeyRef.current
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

  const createSession = useCallback(async (): Promise<string | null> => {
    if (!clientId) return null

    const sessionKey = generateUUID()
    const newSession: ChatSession = {
      key: sessionKey,
      created: new Date().toISOString(),
      updated: new Date().toISOString(),
      message_count: 0,
    }

    setSessions((current) =>
      [newSession, ...current.filter((s) => s.key !== sessionKey)].sort(
        (b, a) => new Date(a.updated).getTime() - new Date(b.updated).getTime(),
      ),
    )
    persistCurrentSessionKey(sessionKey)

    // Await the API call to ensure backend confirms session creation before navigation
    await api.createSession(sessionKey).catch((err) => {
      console.error('[useChatSessions] Failed to create session on backend:', err)
      return null
    })

    return sessionKey
  }, [clientId, persistCurrentSessionKey, api])

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
