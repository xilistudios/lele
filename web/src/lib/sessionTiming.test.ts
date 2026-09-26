import { describe, expect, test } from 'bun:test'
import { PROACTIVE_REFRESH_THRESHOLD_MS, shouldProactivelyRefresh } from './sessionTiming'
import type { AuthSession } from './types'

/**
 * When the web UI should spend its refresh token.
 *
 * The server hands out 30-day credentials and only extends them on refresh,
 * but a refresh used to happen strictly on a 401 -- so a browser left idle for
 * longer than the credential's life came back to a rejected refresh token, a
 * deleted client and a forced logout. The fix is to renew a little before the
 * deadline, and these tests pin the decision that drives it: reuse the REAL
 * `expires` from the server, and never fire on a value that cannot be trusted.
 */

const NOW = Date.parse('2026-09-23T12:00:00Z')
const DAY = 24 * 3600 * 1000

const session = (over: Partial<AuthSession> = {}): AuthSession => ({
  client_id: 'client-1',
  device_name: 'My Desktop',
  token: 'access-1',
  refresh_token: 'refresh-1',
  expires: new Date(NOW + 30 * DAY).toISOString(),
  ...over,
})

/** `expires` as the raw value of a stored session, possibly absent. */
const withExpires = (expires: unknown): AuthSession =>
  ({ ...session(), expires }) as unknown as AuthSession

describe('shouldProactivelyRefresh', () => {
  test('renews a credential that expires inside the window', () => {
    expect(PROACTIVE_REFRESH_THRESHOLD_MS).toBe(7 * DAY)
    expect(
      shouldProactivelyRefresh(session({ expires: new Date(NOW + 6 * DAY).toISOString() }), NOW),
    ).toBe(true)
  })

  test('leaves a credential that expires well beyond the window alone', () => {
    expect(
      shouldProactivelyRefresh(session({ expires: new Date(NOW + 8 * DAY).toISOString() }), NOW),
    ).toBe(false)
  })

  test('renews an already expired credential', () => {
    expect(
      shouldProactivelyRefresh(session({ expires: new Date(NOW - DAY).toISOString() }), NOW),
    ).toBe(true)
  })

  test('the window is inclusive at the threshold', () => {
    expect(
      shouldProactivelyRefresh(session({ expires: new Date(NOW + 7 * DAY).toISOString() }), NOW),
    ).toBe(true)
    expect(
      shouldProactivelyRefresh(
        session({ expires: new Date(NOW + 7 * DAY + 1000).toISOString() }),
        NOW,
      ),
    ).toBe(false)
  })

  test('never fires on a missing or unparsable expires', () => {
    // A fabricated or corrupted deadline must read as "far away": guessing
    // "close" would rotate the credential on every tick forever.
    expect(shouldProactivelyRefresh(withExpires(undefined), NOW)).toBe(false)
    expect(shouldProactivelyRefresh(withExpires(''), NOW)).toBe(false)
    expect(shouldProactivelyRefresh(withExpires('not-a-date'), NOW)).toBe(false)
    expect(shouldProactivelyRefresh(withExpires(12345), NOW)).toBe(false)
  })

  test('never fires without a refresh token to spend', () => {
    // Desktop sessions are injected with a token but no refresh token; there is
    // nothing to renew with, so a refresh attempt would only burn a request.
    expect(shouldProactivelyRefresh(session({ refresh_token: '' }), NOW)).toBe(false)
  })

  test('never fires without a session or access token', () => {
    expect(shouldProactivelyRefresh(null, NOW)).toBe(false)
    expect(shouldProactivelyRefresh(session({ token: '' }), NOW)).toBe(false)
  })

  test('honours a custom threshold', () => {
    expect(
      shouldProactivelyRefresh(
        session({ expires: new Date(NOW + 2 * DAY).toISOString() }),
        NOW,
        DAY,
      ),
    ).toBe(false)
    expect(
      shouldProactivelyRefresh(
        session({ expires: new Date(NOW + 12 * 3600 * 1000).toISOString() }),
        NOW,
        DAY,
      ),
    ).toBe(true)
  })
})
