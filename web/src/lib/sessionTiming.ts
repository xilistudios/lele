import type { AuthSession } from './types'

export const PROACTIVE_REFRESH_THRESHOLD_MS = 7 * 24 * 3600 * 1000

export const PROACTIVE_REFRESH_INTERVAL_MS = 24 * 3600 * 1000

export function shouldProactivelyRefresh(
  session: AuthSession | null,
  now: number,
  thresholdMs: number = PROACTIVE_REFRESH_THRESHOLD_MS,
): boolean {
  if (!session?.token || !session.refresh_token) return false

  const expiresAt = Date.parse(session.expires)
  if (!Number.isFinite(expiresAt)) return false

  return expiresAt - now <= thresholdMs
}
