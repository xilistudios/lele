import '../test/setup'

import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { act, cleanup, render, waitFor } from '@testing-library/react'
import { loadSession, saveSession } from '../lib/storage'
import type { AuthSession } from '../lib/types'
import { type AuthContextValue, AuthProvider, useAuthContext } from './AuthContext'

/**
 * Who is allowed to erase the shared session.
 *
 * `localStorage['lele.session']` is one slot shared by every tab and by every
 * client paired with this browser. The HTTP client reports a fatal refresh by
 * calling the auth-failure handler with `mayClearStorage`; these tests pin that
 * the provider honours it -- a dead credential of ours signs the whole browser
 * out, a dead credential of someone else only disconnects this tab.
 */

const API_URL = 'http://127.0.0.1:18793'
const originalFetch = globalThis.fetch

let captured: AuthContextValue | null = null

function Probe() {
  captured = useAuthContext()
  return null
}

const session = (over: Partial<AuthSession> = {}): AuthSession => ({
  client_id: 'client-MINE',
  device_name: 'My Desktop',
  token: 'access-1',
  refresh_token: 'refresh-1',
  expires: new Date(Date.now() + 3_600_000).toISOString(),
  ...over,
})

/** Protected routes answer 401 and /auth/refresh rejects the presented token. */
function mockExpiredCredential() {
  globalThis.fetch = mock(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.endsWith('/auth/refresh')) {
      return new Response(
        JSON.stringify({ error: 'invalid refresh token', code: 'refresh_error' }),
        { status: 400, headers: { 'Content-Type': 'application/json' } },
      )
    }
    return new Response(JSON.stringify({ error: 'unauthorized', code: 'auth_error' }), {
      status: 401,
      headers: { 'Content-Type': 'application/json' },
    })
  }) as unknown as typeof fetch
}

const mountProvider = () =>
  render(
    <AuthProvider defaultApiUrl={API_URL}>
      <Probe />
    </AuthProvider>,
  )

const auth = (): AuthContextValue => {
  if (!captured) throw new Error('AuthProvider did not mount')
  return captured
}

/** Drives a request all the way into the fatal-refresh path. */
async function expireSession() {
  const client = auth().api
  await act(async () => {
    await client.getAgentCatalog('coder').catch(() => undefined)
  })
}

beforeEach(() => {
  localStorage.clear()
  captured = null
})

afterEach(() => {
  cleanup()
  globalThis.fetch = originalFetch
})

describe('auth failure and the shared session slot', () => {
  test('does not erase the stored session of another client', async () => {
    saveSession(session())
    mockExpiredCredential()
    mountProvider()
    expect(auth().session?.client_id).toBe('client-MINE')

    // Another client signs into this browser (another tab, same origin) while
    // our tab is open: the slot now holds a session we never owned.
    saveSession(
      session({
        client_id: 'client-OTHER',
        device_name: 'some other device',
        token: 'access-of-other-client',
        refresh_token: 'refresh-of-other-client',
      }),
    )

    await expireSession()

    // This tab is signed out from React state...
    expect(auth().session).toBeNull()
    // ...but the other device's session is still there for its own tab.
    expect(loadSession()?.client_id).toBe('client-OTHER')
    expect(loadSession()?.refresh_token).toBe('refresh-of-other-client')
  })

  test('clears the stored session when the dead credential is ours', async () => {
    saveSession(session())
    mockExpiredCredential()
    mountProvider()

    await expireSession()

    expect(auth().session).toBeNull()
    expect(loadSession()).toBeNull()
  })
})
/**
 * Renewing before the deadline, not after the rejection.
 *
 * A browser that has been idle for longer than the credential's life used to
 * find out the hard way: the first request 401'd, the refresh token had already
 * expired server-side, the client was deleted and the user was signed out ('My
 * Desktop', paired on the 5th, expired on the 4th, without a single refresh).
 * These tests pin the proactive renewal the provider now runs on mount, driven
 * by the REAL `expires` the server persisted with the session.
 */

const DAY_MS = 24 * 3600 * 1000
const THIRTY_DAYS_MS = 30 * DAY_MS

describe('proactive session renewal', () => {
  /** Answers /auth/refresh with a fresh 30-day credential; records every call. */
  function mockRefreshServer() {
    const calls: { url: string; body: unknown }[] = []
    const serverExpires = new Date(Date.now() + THIRTY_DAYS_MS).toISOString()
    globalThis.fetch = mock(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/auth/refresh')) {
        calls.push({ url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
        return new Response(
          JSON.stringify({ token: 'access-2', refresh_token: 'refresh-2', expires: serverExpires }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        )
      }
      return new Response(JSON.stringify({ error: 'not found', code: 'not_found' }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      })
    }) as unknown as typeof fetch
    return { calls, serverExpires }
  }

  test('renews on mount when the credential is about to expire', async () => {
    saveSession(session({ expires: new Date(Date.now() + DAY_MS).toISOString() }))
    const { calls, serverExpires } = mockRefreshServer()

    mountProvider()

    await waitFor(() => expect(calls).toHaveLength(1))
    // The rotation must present the refresh token this tab holds.
    expect(calls[0]?.body).toEqual({ refresh_token: 'refresh-1' })

    await act(async () => {})
    expect(loadSession()?.token).toBe('access-2')
    expect(loadSession()?.refresh_token).toBe('refresh-2')
    // The server's own deadline is what gets persisted, not a fabricated one.
    expect(loadSession()?.expires).toBe(serverExpires)
    expect(auth().session?.expires).toBe(serverExpires)
    // Renewal keeps the device identity it was paired with.
    expect(loadSession()?.client_id).toBe('client-MINE')
    expect(loadSession()?.device_name).toBe('My Desktop')
  })

  test('does not touch a credential that is far from expiring', async () => {
    saveSession(session({ expires: new Date(Date.now() + 20 * DAY_MS).toISOString() }))
    const { calls } = mockRefreshServer()

    mountProvider()
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50))
    })

    expect(calls).toHaveLength(0)
    expect(auth().session?.token).toBe('access-1')
  })

  test('does not attempt renewal without a refresh token', async () => {
    // Desktop sessions are injected with a token only: there is nothing to
    // renew with, so a check must not turn into a doomed request.
    saveSession(
      session({ refresh_token: '', expires: new Date(Date.now() + 60_000).toISOString() }),
    )
    const { calls } = mockRefreshServer()

    mountProvider()
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50))
    })

    expect(calls).toHaveLength(0)
  })
})
