import type { AuthSession } from './types'

/**
 * How long before a credential's deadline the UI renews it.
 *
 * The server issues 30-day sessions and only pushes the deadline further on a
 * refresh, so a week of slack is several rotations' worth of margin while
 * still leaving the token untouched in the ordinary case of an app opened
 * daily.
 */
export const PROACTIVE_REFRESH_THRESHOLD_MS = 7 * 24 * 3600 * 1000

/**
 * How often the mounted app re-checks the stored deadline.
 *
 * Renewal is only interesting when the deadline is near, and the threshold is
 * measured in days, so an hourly wake-up would be wasted work. Once a day is
 * enough to catch any session long before it lapses.
 */
export const PROACTIVE_REFRESH_INTERVAL_MS = 24 * 3600 * 1000

/**
 * Should this session be renewed now, before the server rejects it?
 *
 * True only when there is a credential to renew (`token`) AND something to
 * renew it with (`refresh_token`), and the session's `expires` is both
 * parseable and within `thresholdMs` of `now` -- an already expired session
 * counts, since renewing it is exactly what saves it from a forced logout.
 *
 * A session whose `expires` is missing or unparsable answers false. Reading a
 * corrupted deadline as "close" would rotate the credential on every check for
 * as long as the value stays broken, so an untrusted value must never start a
 * renewal; the natural 401 path is the fallback.
 */
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
