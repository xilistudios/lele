import { describe, expect, test } from 'bun:test'
import { PROACTIVE_REFRESH_THRESHOLD_MS, shouldProactivelyRefresh } from './sessionTiming'
import type { AuthSession } from './types'

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
    expect(shouldProactivelyRefresh(withExpires(undefined), NOW)).toBe(false)
    expect(shouldProactivelyRefresh(withExpires(''), NOW)).toBe(false)
    expect(shouldProactivelyRefresh(withExpires('not-a-date'), NOW)).toBe(false)
    expect(shouldProactivelyRefresh(withExpires(12345), NOW)).toBe(false)
  })

  test('never fires without a refresh token to spend', () => {
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
