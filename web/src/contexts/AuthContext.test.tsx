import '../test/setup'

import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { act, cleanup, render, waitFor } from '@testing-library/react'
import { loadSession, saveSession } from '../lib/storage'
import type { AuthSession } from '../lib/types'
import { type AuthContextValue, AuthProvider, useAuthContext } from './AuthContext'

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

    saveSession(
      session({
        client_id: 'client-OTHER',
        device_name: 'some other device',
        token: 'access-of-other-client',
        refresh_token: 'refresh-of-other-client',
      }),
    )

    await expireSession()

    expect(auth().session).toBeNull()
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

const DAY_MS = 24 * 3600 * 1000
const THIRTY_DAYS_MS = 30 * DAY_MS

describe('proactive session renewal', () => {
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
    expect(calls[0]?.body).toEqual({ refresh_token: 'refresh-1' })

    await act(async () => {})
    expect(loadSession()?.token).toBe('access-2')
    expect(loadSession()?.refresh_token).toBe('refresh-2')
    expect(loadSession()?.expires).toBe(serverExpires)
    expect(auth().session?.expires).toBe(serverExpires)
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
