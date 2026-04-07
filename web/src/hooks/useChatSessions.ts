import { useCallback, useEffect, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import {
  clearCurrentSessionKey,
  loadCurrentSessionKey,
  saveCurrentSessionKey,
} from '../lib/storage'
import type { ChatSession } from '../lib/types'

const buildDefaultSessionKey = (clientId: string) => `native:${clientId}`

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

    const nextSessions = [
      ...sessionsRef.current.filter(
        (sessionItem) =>
          sessionItem.key === currentSessionKeyRef.current &&
          !fallbackSessions.some((nextSession) => nextSession.key === sessionItem.key),
      ),
      ...fallbackSessions,
    ].sort((b, a) => new Date(a.updated).getTime() - new Date(b.updated).getTime())

    setSessions(nextSessions)

    const availableKeys = new Set(nextSessions.map((item) => item.key))
    const fallbackKey = availableKeys.has(defaultSessionKey)
      ? defaultSessionKey
      : (nextSessions[0]?.key ?? null)
    const storedSessionKey = loadCurrentSessionKey()
    const nextSessionKey =
      currentSessionKeyRef.current && availableKeys.has(currentSessionKeyRef.current)
        ? currentSessionKeyRef.current
        : storedSessionKey && availableKeys.has(storedSessionKey)
          ? storedSessionKey
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

  const createSession = useCallback(() => {
    if (!clientId) return null

    const sessionKey = `${buildDefaultSessionKey(clientId)}:${Date.now()}`
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
    return sessionKey
  }, [clientId, persistCurrentSessionKey])

  const deleteSession = useCallback(
    async (sessionKey: string) => {
      if (!token) return

      await api.deleteSession(sessionKey)
      setSessions((current) => current.filter((s) => s.key !== sessionKey))

      if (sessionKey === currentSessionKeyRef.current) {
        const remainingSessions = sessionsRef.current.filter((s) => s.key !== sessionKey)
        const nextSessionKey = remainingSessions.length > 0 ? remainingSessions[0].key : null
        persistCurrentSessionKey(nextSessionKey)
      }
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
    selectSession,
    createSession,
    deleteSession,
    clearSession,
    reset,
  }
}
