import { saveSession } from './storage'
import type { AuthSession } from './types'

export { ApiError, parseApiError } from '../services/http/errors'
export { createApiClient } from '../services/http/client'
export type { ApiClient } from '../services/http/client'

declare global {
  interface Window {
    __LELE_DESKTOP__?: {
      mode?: string
      apiUrl?: string
      token?: string
      refresh?: string
      session?: {
        token: string
        refresh_token: string
        expires: string
        client_id: string
        device_name?: string
      }
    }
  }
}

const isDesktopTokenPresent = (): boolean => {
  if (typeof window === 'undefined') {
    return false
  }
  const desktop = window.__LELE_DESKTOP__
  return Boolean(desktop?.token ?? desktop?.session?.token)
}

export function getDesktopToken(): string | null {
  if (typeof window === 'undefined') {
    return null
  }
  const desktop = window.__LELE_DESKTOP__
  return desktop?.token ?? desktop?.session?.token ?? null
}

export function bootstrapDesktopSession(): AuthSession | null {
  const token = getDesktopToken()
  if (!token) {
    return null
  }

  const desktop = window.__LELE_DESKTOP__
  const session: AuthSession = {
    token,
    refresh_token: desktop?.refresh ?? desktop?.session?.refresh_token ?? '',
    expires: desktop?.session?.expires ?? new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString(),
    client_id: desktop?.session?.client_id ?? 'desktop-local',
    device_name: desktop?.session?.device_name,
  }

  saveSession(session)
  return session
}

export { isDesktopTokenPresent }
