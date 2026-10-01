import '../test/setup'

import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render } from '@testing-library/react'
import { type ReactElement, StrictMode, useEffect } from 'react'
import { MemoryRouter } from 'react-router-dom'
import '../test/i18n'
import App from '../App'
import { saveSession } from '../lib/storage'
import type { AuthSession } from '../lib/types'
import { type AuthContextValue, AuthProvider, useAuthContext } from './AuthContext'
import { ThemeProvider } from './ThemeContext'

/**
 * Regression pin: a cold boot must never issue an unauthenticated request.
 *
 * `createApiClient` starts with `tokenState.token = null` and the only writer
 * is `AuthProvider`'s `useEffect`. React flushes CHILD passive effects before
 * PARENT passive effects, so every boot request issued from a descendant
 * effect (useAppLogic, useSlashCommands, useChatHistory, useSubagents,
 * useModels...) leaves before the token is installed, and the real gateway
 * answers `401 auth_missing` (pkg/channels/native.go) without ever consulting
 * the stored credential. The 401s then spend a single-use rotation on every
 * page load via `POST /api/v1/auth/refresh`.
 *
 * The fetch mock enforces auth like the real gateway:
 *   - `POST /api/v1/auth/refresh` is public (no header by design); every call
 *     is recorded as a rotation and mints `access-2`, invalidating `access-1`
 *     exactly like the server's single-token row does;
 *   - a request to a protected path with NO `Authorization` header is answered
 *     `401 {code: 'auth_missing'}`;
 *   - a request whose Bearer the mock does not hold is answered
 *     `401 {code: 'auth_invalid_token'}`;
 *   - anything else routes to the boot fixtures.
 *
 * Never answer 200 to a headerless request: that is precisely the defect that
 * makes `App.test.tsx` blind to this bug (its mock ignores headers entirely).
 *
 * Invariants (asserted on SHAPE, never on total request counts -- the boot
 * wave size is not stable, PRs #353/#355 changed it deliberately):
 *   I1. no request to a non-`/auth/` path is issued without an
 *       `Authorization: Bearer ...` header;
 *   I2. a boot whose stored `expires` is > 7 days away (outside
 *       PROACTIVE_REFRESH_THRESHOLD_MS) issues no `POST /auth/refresh` at all;
 *   I3. dead credential: every request still carries a header; at most one
 *       refresh for the whole boot; a request that 401s is replayed with a
 *       valid header.
 */

const API_URL = 'http://127.0.0.1:18793'
/** A *different* gateway: editing the API URL is what makes the memo rebuild
 * the client in the very commit that carries the freshly paired session. */
const CHANGED_API_URL = 'http://127.0.0.1:18794'
/** Bearer the gateway mints when the device pairs. */
const PAIRED_TOKEN = 'access-P'
const DAY_MS = 24 * 60 * 60 * 1000

const originalFetch = globalThis.fetch
const originalWebSocket = globalThis.WebSocket

type RecordedCall = { url: string; auth: string | null }

type FetchResponseBody = Record<string, unknown>

const jsonResponse = (body: FetchResponseBody, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

/** Reads `Authorization` from a RequestInit.headers of any shape. */
const readAuthorization = (init?: RequestInit): string | null => {
  const headers = init?.headers
  if (!headers) return null
  if (headers instanceof Headers) return headers.get('Authorization')
  if (Array.isArray(headers)) {
    const found = headers.find(([name]) => name.toLowerCase() === 'authorization')
    return found ? String(found[1]) : null
  }
  const record = headers as Record<string, unknown>
  const value = record.Authorization ?? record.authorization
  return value === undefined ? null : String(value)
}

/**
 * Replaces `globalThis.fetch` with a gateway that enforces auth. Records
 * `{url, auth}` for every call (that log is the reproduction of the 728
 * `auth_missing` in production) and every `POST /auth/refresh` as a rotation.
 *
 * `validTokens` are the bearers the gateway holds right now; a rotation
 * replaces them with `access-2` (the server keeps a single token per client
 * row, so the previous access token dies the moment a refresh succeeds).
 */
function installGateway({ validTokens }: { validTokens: string[] }) {
  const calls: RecordedCall[] = []
  const rotations: unknown[] = []
  const accepted = new Set(validTokens)

  globalThis.fetch = mock(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    const auth = readAuthorization(init)
    calls.push({ url, auth })

    // Public endpoint: called with no Authorization by design, body carries
    // the refresh token. Every call rotates the single-use credential.
    if (url.endsWith('/api/v1/auth/refresh')) {
      rotations.push(init?.body ? JSON.parse(String(init.body)) : null)
      accepted.clear()
      accepted.add('access-2')
      return jsonResponse({
        token: 'access-2',
        refresh_token: 'refresh-2',
        expires: new Date(Date.now() + 30 * DAY_MS).toISOString(),
      })
    }

    // Public endpoint: pairing mints the credential the new session carries
    // and the gateway starts holding it, so a client seeded with that
    // credential is observable (any other bearer is answered auth_invalid_token).
    if (url.endsWith('/api/v1/auth/pair')) {
      accepted.add(PAIRED_TOKEN)
      return jsonResponse({
        token: PAIRED_TOKEN,
        refresh_token: 'refresh-P',
        expires: new Date(Date.now() + 20 * DAY_MS).toISOString(),
        client_id: 'client-P',
        device_name: 'Test Device',
      })
    }

    // Everything outside /auth/ sits behind authMiddleware: missing header
    // -> auth_missing (the token is never consulted), present but unknown
    // bearer -> auth_invalid_token.
    if (!url.includes('/auth/')) {
      if (auth === null) {
        return jsonResponse({ error: 'missing authorization header', code: 'auth_missing' }, 401)
      }
      const token = auth.startsWith('Bearer ') ? auth.slice('Bearer '.length) : null
      if (token === null || !accepted.has(token)) {
        return jsonResponse({ error: 'invalid token', code: 'auth_invalid_token' }, 401)
      }
    }

    return routeBootRequest(url)
  }) as unknown as typeof fetch

  return { calls, rotations }
}

// --- Boot fixtures (same shapes App.test.tsx's createFetchMock serves) ------

const isSessionsEndpoint = (url: string) => {
  const path = url.split('?')[0]
  return path.endsWith('/api/v1/chat/sessions') || path.endsWith('/api/v1/chat/sessions/meta')
}

// A 404 (not a thrown Error) for unhandled URLs: it becomes an ApiError(404)
// that is thrown immediately with no retry, so no retry timer can outlive the
// test and fire against the real network once fetch is restored.
const notFoundResponse = (url: string) => jsonResponse({ error: `Unexpected fetch: ${url}` }, 404)

const mockConfigResponse = () => ({
  config: {
    agents: {
      defaults: {
        workspace: '~/.lele',
        restrict_to_workspace: false,
        provider: 'openai',
        model: 'gpt-4',
        max_tokens: 4096,
        max_tool_iterations: 30,
      },
      list: [{ id: 'main', name: 'Main Agent', workspace: '~/.lele', model: 'gpt-4' }],
    },
    session: { ephemeral: false, ephemeral_threshold: 3600 },
    gateway: { host: '127.0.0.1', port: 18793 },
    providers: { named: {} },
  },
  meta: {
    config_path: '/tmp/config.json',
    source: 'file',
    can_save: true,
    restart_required_sections: [],
    secrets_by_path: {},
  },
})

function routeBootRequest(url: string): Response {
  if (url.endsWith('/api/v1/agents')) {
    return jsonResponse({
      agents: [
        { id: 'main', name: 'Main Agent', workspace: '~/.lele', model: 'gpt-4', default: true },
      ],
    })
  }
  if (url.endsWith('/api/v1/agents/main')) {
    return jsonResponse({
      id: 'main',
      name: 'Main Agent',
      workspace: '~/.lele',
      model: 'gpt-4',
      default: true,
    })
  }
  if (url.endsWith('/api/v1/agents/main/status')) {
    return jsonResponse({ id: 'main', status: 'running', active_sessions: 1 })
  }
  if (url.endsWith('/api/v1/status')) {
    return jsonResponse({ status: 'ok', uptime: '1h', agents: [], channels: [], version: 'dev' })
  }
  if (url.endsWith('/api/v1/channels')) {
    return jsonResponse({ channels: [{ name: 'native', enabled: true, running: true }] })
  }
  if (url.endsWith('/api/v1/tools')) {
    return jsonResponse({ tools: [] })
  }
  if (url.endsWith('/api/v1/config')) {
    return jsonResponse(mockConfigResponse())
  }
  if (isSessionsEndpoint(url)) {
    return jsonResponse({
      sessions: [
        {
          key: 'native:client-1:1',
          created: '2026-01-01T00:00:00Z',
          updated: '2026-01-01T00:00:00Z',
        },
        {
          key: 'native:client-1:2',
          created: '2026-01-01T00:00:00Z',
          updated: '2026-01-01T00:01:00Z',
        },
      ],
    })
  }
  if (url.includes('/api/v1/chat/sessions/native:client-1:1/history')) {
    return jsonResponse({
      session_key: 'native:client-1:1',
      messages: [{ role: 'assistant', content: 'mensaje A' }],
    })
  }
  if (url.includes('/api/v1/chat/sessions/native:client-1:2/history')) {
    return jsonResponse({
      session_key: 'native:client-1:2',
      messages: [{ role: 'assistant', content: 'mensaje B' }],
    })
  }
  if (url.includes('/api/v1/models')) {
    return jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] })
  }
  if (url.includes('/api/v1/chat/sessions/')) {
    return jsonResponse({
      session_key: 'native:client-1:1',
      agent_id: 'main',
      model: 'gpt-4',
      models: ['gpt-4'],
    })
  }
  return notFoundResponse(url)
}

// --- Session fixture ---------------------------------------------------------

/** Far-deadline credential: 20 days out, i.e. outside the 7-day proactive
 * refresh threshold, so `renewIfDue()` must stay a no-op at boot. */
const farSession = (): AuthSession => ({
  token: 'access-1',
  refresh_token: 'refresh-1',
  expires: new Date(Date.now() + 20 * DAY_MS).toISOString(),
  client_id: 'client-1',
  device_name: 'My Desktop',
})

// --- Driving helpers ---------------------------------------------------------

/**
 * Drives the boot until the recorded fetch log stops growing: the parent
 * effect (token install) has run and any 401 -> refresh -> replay chain has
 * settled. Real timers only; the quiet window is longer than the client's
 * 1 s generic retry delay so a late retry cannot escape the log.
 */
async function settleBoot(calls: RecordedCall[]): Promise<void> {
  const QUIET_MS = 1200
  const TIMEOUT_MS = 15_000
  const deadline = Date.now() + TIMEOUT_MS
  let lastCount = -1
  let quietSince = Date.now()
  while (Date.now() < deadline) {
    if (calls.length !== lastCount) {
      lastCount = calls.length
      quietSince = Date.now()
    } else if (calls.length > 0 && Date.now() - quietSince >= QUIET_MS) {
      return
    }
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 25))
    })
  }
}

const unauthenticatedCalls = (calls: RecordedCall[]) =>
  calls.filter((call) => !call.url.includes('/auth/') && call.auth === null)

const refreshCalls = (calls: RecordedCall[]) =>
  calls.filter((call) => call.url.endsWith('/api/v1/auth/refresh'))

/** Prints the raw recorded log (url + Authorization) when the invariant is
 * violated, so the failure output itself carries the reproduction evidence. */
const dumpLog = (calls: RecordedCall[]) => {
  console.error(
    '[AuthBootstrap] recorded fetch log:',
    calls.map((call) => ({ url: call.url, auth: call.auth })),
  )
  console.error(
    `[AuthBootstrap] headerless non-/auth/ requests: ${unauthenticatedCalls(calls).length}; ` +
      `POST /auth/refresh calls: ${refreshCalls(calls).length}`,
  )
}

/** I1: every non-`/auth/` request carries an `Authorization` header. */
const expectNoHeaderlessRequests = (calls: RecordedCall[]) => {
  const headerless = unauthenticatedCalls(calls)
  if (headerless.length > 0) dumpLog(calls)
  expect(headerless).toHaveLength(0)
}

/** I2: a far-deadline boot spends no rotation. */
const expectNoBootRotation = (calls: RecordedCall[]) => {
  const rotations = refreshCalls(calls)
  if (rotations.length > 0) dumpLog(calls)
  expect(rotations).toHaveLength(0)
}

// --- App-level harness (idioms from App.test.tsx) ----------------------------

class MockWebSocket {
  static instances: MockWebSocket[] = []

  readyState = 0
  sent: string[] = []
  private listeners = new Map<string, Array<(event?: MessageEvent | Event) => void>>()

  constructor(public readonly url: string) {
    MockWebSocket.instances.push(this)
    queueMicrotask(() => {
      this.readyState = 1
      this.emit('open', new Event('open'))
    })
  }

  addEventListener(type: string, listener: (event?: MessageEvent | Event) => void) {
    const current = this.listeners.get(type) ?? []
    current.push(listener)
    this.listeners.set(type, current)
  }

  send(data: string) {
    this.sent.push(data)
  }

  close() {
    this.readyState = 3
    this.emit('close', new Event('close'))
  }

  emit(type: string, event?: MessageEvent | Event) {
    for (const listener of this.listeners.get(type) ?? []) {
      listener(event)
    }
  }

  emitJSON(payload: unknown) {
    this.emit('message', new MessageEvent('message', { data: JSON.stringify(payload) }))
  }

  static reset() {
    MockWebSocket.instances = []
  }
}

// Test-owned client: the shared singleton sets `retry: 2` with 1 s/2 s
// backoff, and with retries on react-query re-issues requests so the recorded
// sequence is no longer the app's. Never import the shared queryClient here.
let testClient: QueryClient

const renderApp = (ui: ReactElement) =>
  render(
    <ThemeProvider>
      <QueryClientProvider client={testClient}>
        <MemoryRouter initialEntries={['/']}>{ui}</MemoryRouter>
      </QueryClientProvider>
    </ThemeProvider>,
  )

beforeEach(() => {
  localStorage.clear()
  testClient = new QueryClient({
    defaultOptions: {
      queries: { retry: 0, staleTime: 0, gcTime: 0, refetchOnWindowFocus: false },
    },
  })
  MockWebSocket.reset()
  globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
})

afterEach(() => {
  cleanup()
  testClient.clear()
  globalThis.fetch = originalFetch
  globalThis.WebSocket = originalWebSocket
  localStorage.clear()
})

// --- Focused boot: AuthProvider + a probe child ------------------------------

function BootProbe() {
  const { api } = useAuthContext()
  useEffect(() => {
    // Fire-and-forget from a CHILD effect, exactly like useAppLogic /
    // useSlashCommands / useChatHistory / useSubagents do at boot.
    void api.agents().catch(() => undefined)
  }, [api])
  return null
}

const mountProbe = () =>
  render(
    <AuthProvider defaultApiUrl={API_URL}>
      <BootProbe />
    </AuthProvider>,
  )

describe('focused boot: AuthProvider + child-effect probe', () => {
  test('I1+I2: every boot request carries the stored credential and no rotation is spent', async () => {
    const { calls } = installGateway({ validTokens: ['access-1'] })
    saveSession(farSession())

    mountProbe()
    await settleBoot(calls)

    expect(calls.length).toBeGreaterThan(0)
    expectNoHeaderlessRequests(calls)
    expectNoBootRotation(calls)
    // No replay was needed: the FIRST attempt already presents the stored
    // credential.
    const firstAttempt = calls.find((call) => !call.url.includes('/auth/'))
    expect(firstAttempt?.auth).toBe('Bearer access-1')
  })

  test('I3: a dead credential still never leaves headerless, rotates at most once, and replays with a valid header', async () => {
    // The stored access-1 is dead: the gateway only holds access-2 (the token
    // the refresh mints).
    const { calls, rotations } = installGateway({ validTokens: ['access-2'] })
    saveSession(farSession())

    mountProbe()
    await settleBoot(calls)

    expect(calls.length).toBeGreaterThan(0)
    // Every request still carries a header...
    expectNoHeaderlessRequests(calls)
    // ...at most one refresh for the whole boot...
    expect(rotations.length).toBeLessThanOrEqual(1)
    // ...and the request that was rejected is replayed with a header the
    // gateway accepts (it holds only access-2).
    const attempts = calls.filter((call) => !call.url.includes('/auth/'))
    expect(attempts.length).toBeGreaterThan(0)
    expect(attempts[attempts.length - 1]?.auth).toBe('Bearer access-2')
  })
})

// --- F1: the seed across an apiUrl change ------------------------------------

/** Exposes the provider to the test, and issues one protected request per
 * `[api, session]` change, but only while a credential exists: a clean
 * profile shows the login page, not a boot wave. */
let pairAuth: AuthContextValue | null = null

function PairProbe() {
  const value = useAuthContext()
  pairAuth = value
  const { api, session } = value
  useEffect(() => {
    if (!session) return
    void api.agents().catch(() => undefined)
  }, [api, session])
  return null
}

const pairer = (): AuthContextValue => {
  if (!pairAuth) throw new Error('PairProbe did not capture AuthProvider')
  return pairAuth
}

const mountPairProbe = () =>
  render(
    <AuthProvider defaultApiUrl={API_URL}>
      <PairProbe />
    </AuthProvider>,
  )

describe('F1: the seed is read from the current render', () => {
  test('pairing against a changed apiUrl rebuilds a client that is already authenticated', async () => {
    const { calls } = installGateway({ validTokens: [] })
    pairAuth = null
    mountPairProbe()

    // Clean profile: no stored session, so nothing leaves before pairing.
    expect(calls).toHaveLength(0)

    // `handleAuth` changes `apiUrl` AND the session in one batch, so the memo
    // rebuilds the client in the very commit that carries the new session.
    await act(async () => {
      await pairer().handleAuth({
        apiUrl: CHANGED_API_URL,
        pin: '123456',
        deviceName: 'Test Device',
      })
    })
    await settleBoot(calls)

    const protectedCalls = calls.filter((call) => !call.url.includes('/auth/'))
    expect(protectedCalls.length).toBeGreaterThan(0)
    // (i) nothing left headerless: the rebuilt client must carry the session
    // of the render that created it.
    expectNoHeaderlessRequests(calls)
    // (ii) and it presents the credential the pair just minted -- not a
    // previous gateway's, and not nothing.
    expect(protectedCalls[0]?.auth).toBe(`Bearer ${PAIRED_TOKEN}`)
  })

  // N2: the clean-profile fixture above always trips assertion (i) first, so
  // (ii) is never the discriminator there. This E3 shape -- a live session for
  // the PREVIOUS gateway already exists when the user re-pairs against a
  // changed apiUrl -- seeds the rebuilt client with that old (still valid)
  // credential: the request DOES carry a header, (i) passes, and only the
  // `Bearer ${PAIRED_TOKEN}` assertion can catch the stale seed.
  test('pairing against a changed apiUrl does not present the previous gateway credential', async () => {
    const { calls } = installGateway({ validTokens: ['access-1'] })
    // A live session for the PREVIOUS gateway exists before pairing.
    saveSession(farSession())
    pairAuth = null
    mountPairProbe()
    await settleBoot(calls)

    const before = calls.length

    // `handleAuth` changes `apiUrl` AND the session in one batch, so the memo
    // rebuilds the client in the very commit that carries the new session.
    await act(async () => {
      await pairer().handleAuth({
        apiUrl: CHANGED_API_URL,
        pin: '123456',
        deviceName: 'Test Device',
      })
    })
    await settleBoot(calls)

    // Only the calls made AFTER the pair, minus the public /auth/ ones.
    const afterPair = calls.slice(before).filter((call) => !call.url.includes('/auth/'))
    expect(afterPair.length).toBeGreaterThan(0)
    // Passes even with the bug: the header is there, it is just the WRONG one.
    expectNoHeaderlessRequests(afterPair)
    // The discriminator: the rebuilt client must present the credential the
    // pair just minted, not the previous gateway's still-valid one.
    expect(afterPair[0]?.auth).toBe(`Bearer ${PAIRED_TOKEN}`)
  })
})

describe('the mock enforces auth like the gateway', () => {
  test('I3 control: a headerless request to a protected path really is 401 auth_missing', async () => {
    installGateway({ validTokens: ['access-1'] })

    const response = await globalThis.fetch(`${API_URL}/api/v1/agents`)

    expect(response.status).toBe(401)
    const body = (await response.json()) as { code?: string }
    expect(body.code).toBe('auth_missing')
  })
})

// --- App-level boot: the real <App/> -----------------------------------------

describe('app-level boot: the real <App/>', () => {
  test('I1+I2 across the real boot wave: no unauthenticated request, no rotation', async () => {
    const { calls } = installGateway({ validTokens: ['access-1'] })
    saveSession(farSession())
    localStorage.setItem('lele.currentSessionKey', 'native:client-1:1')

    renderApp(<App />)
    await settleBoot(calls)

    expect(calls.length).toBeGreaterThan(0)
    expectNoHeaderlessRequests(calls)
    expectNoBootRotation(calls)
  })

  test('I1+I2 under <StrictMode> (as main.tsx:20 mounts it): the double-invoked wave still carries the header and spends no rotation', async () => {
    const { calls } = installGateway({ validTokens: ['access-1'] })
    saveSession(farSession())
    localStorage.setItem('lele.currentSessionKey', 'native:client-1:1')

    renderApp(
      <StrictMode>
        <App />
      </StrictMode>,
    )
    await settleBoot(calls)

    expect(calls.length).toBeGreaterThan(0)
    expectNoHeaderlessRequests(calls)
    expectNoBootRotation(calls)
  })
})
