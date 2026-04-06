import type { AuthSession } from './types'

const KEY = 'lele.session'
const API_URL_KEY = 'lele.apiUrl'
const SESSION_KEY = 'lele.currentSessionKey'

export const loadSession = (): AuthSession | null => {
  if (typeof localStorage === 'undefined') {
    return null
  }

  const raw = localStorage.getItem(KEY)
  if (!raw) {
    return null
  }

  try {
    return JSON.parse(raw) as AuthSession
  } catch {
    return null
  }
}

export const saveSession = (session: AuthSession) => {
  localStorage.setItem(KEY, JSON.stringify(session))
}

export const clearSession = () => {
  localStorage.removeItem(KEY)
}

export const loadApiUrl = (fallback: string): string => {
  if (typeof localStorage === 'undefined') {
    return fallback
  }

  return localStorage.getItem(API_URL_KEY) ?? fallback
}

export const saveApiUrl = (apiUrl: string) => {
  localStorage.setItem(API_URL_KEY, apiUrl)
}

export const loadCurrentSessionKey = (): string | null => {
  if (typeof localStorage === 'undefined') {
    return null
  }

  return localStorage.getItem(SESSION_KEY)
}

export const saveCurrentSessionKey = (sessionKey: string) => {
  localStorage.setItem(SESSION_KEY, sessionKey)
}

export const clearCurrentSessionKey = () => {
  localStorage.removeItem(SESSION_KEY)
}
