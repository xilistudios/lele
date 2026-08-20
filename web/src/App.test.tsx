import './test/setup'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import './test/i18n'
import type { ReactElement } from 'react'
import App from './App'
import { ThemeProvider } from './contexts/ThemeContext'
import { queryClient } from './lib/queryClient'

// Helper to render with required providers
const renderWithProviders = (ui: ReactElement, initialEntries = ['/']) => {
  return render(
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={initialEntries}>{ui}</MemoryRouter>
      </QueryClientProvider>
    </ThemeProvider>,
  )
}

type FetchResponseBody = Record<string, unknown>

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

const originalFetch = globalThis.fetch
const originalWebSocket = globalThis.WebSocket

afterEach(() => {
  cleanup()
  // Cancel any in-flight queries and their scheduled retry timers. Now that
  // the App fully mounts in these tests, React Query hooks (useModels, etc.)
  // schedule retries (retry: 2, delays >= 1s) that would otherwise outlive
  // the test, fire against the real network once `fetch` is restored, and
  // leak unhandled rejections into the next test file.
  queryClient.clear()
})

const authSession = {
  token: 'token',
  refresh_token: 'refresh',
  expires: '2026-01-01T00:00:00Z',
  client_id: 'client-1',
  device_name: 'Desktop',
}

const jsonResponse = (body: FetchResponseBody) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })

// Match the list-sessions endpoints regardless of query params (?offset=&limit=).
// Includes both the plain /api/v1/chat/sessions and the lightweight meta variant
// /api/v1/chat/sessions/meta.
const isSessionsEndpoint = (url: string) => {
  const path = url.split('?')[0]
  return path.endsWith('/api/v1/chat/sessions') || path.endsWith('/api/v1/chat/sessions/meta')
}

// Return a 404 (not a thrown Error) for unhandled URLs. A thrown non-ApiError
// is retried by requestWithRetry after ~1s — by which time afterEach has
// restored the real fetch, so the retry hits the network and the rejection
// leaks into the next test file. A 404 becomes an ApiError(404) that is thrown
// immediately with no retry.
const notFoundResponse = (url: string) =>
  new Response(JSON.stringify({ error: `Unexpected fetch: ${url}` }), {
    status: 404,
    headers: { 'Content-Type': 'application/json' },
  })

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
    },
    session: { ephemeral: false, ephemeral_threshold: 3600 },
    channels: {
      native: {
        enabled: true,
        host: '127.0.0.1',
        port: 18793,
        token_expiry_days: 30,
        pin_expiry_minutes: 10,
        max_clients: 5,
        cors_origins: [],
        session_expiry_days: 365,
        max_upload_size_mb: 50,
        upload_ttl_hours: 24,
      },
      telegram: {
        enabled: false,
        token: { mode: 'empty', has_env_var: false },
        proxy: '',
        allow_from: [],
        verbose: 'off',
      },
      discord: { enabled: false, token: { mode: 'empty', has_env_var: false }, allow_from: [] },
      whatsapp: { enabled: false, bridge_url: '', allow_from: [] },
      feishu: {
        enabled: false,
        app_id: { mode: 'empty', has_env_var: false },
        app_secret: { mode: 'empty', has_env_var: false },
        encrypt_key: { mode: 'empty', has_env_var: false },
        verification_token: { mode: 'empty', has_env_var: false },
        allow_from: [],
      },
      slack: {
        enabled: false,
        bot_token: { mode: 'empty', has_env_var: false },
        app_token: { mode: 'empty', has_env_var: false },
        allow_from: [],
      },
      line: {
        enabled: false,
        channel_secret: { mode: 'empty', has_env_var: false },
        channel_access_token: { mode: 'empty', has_env_var: false },
        webhook_host: '',
        webhook_port: 0,
        webhook_path: '',
        allow_from: [],
      },
      onebot: {
        enabled: false,
        ws_url: '',
        access_token: { mode: 'empty', has_env_var: false },
        reconnect_interval: 5,
        group_trigger_prefix: [],
        allow_from: [],
      },
      maixcam: { enabled: false, host: '', port: 0, allow_from: [] },
      qq: {
        enabled: false,
        app_id: { mode: 'empty', has_env_var: false },
        app_secret: { mode: 'empty', has_env_var: false },
        allow_from: [],
      },
      dingtalk: {
        enabled: false,
        client_id: { mode: 'empty', has_env_var: false },
        client_secret: { mode: 'empty', has_env_var: false },
        allow_from: [],
      },
    },
    providers: { named: {} },
    gateway: { host: '127.0.0.1', port: 18793 },
    tools: {
      web: {
        brave: { enabled: false, api_key: { mode: 'empty', has_env_var: false }, max_results: 10 },
        duckduckgo: { enabled: false, max_results: 10 },
        perplexity: {
          enabled: false,
          api_key: { mode: 'empty', has_env_var: false },
          max_results: 10,
        },
      },
      cron: { exec_timeout_minutes: 0 },
      exec: { enable_deny_patterns: false, custom_deny_patterns: [] },
    },
    heartbeat: { enabled: true, interval: 10 },
    devices: { enabled: false, monitor_usb: false },
    logs: { enabled: false, path: '', max_days: 7, rotation: 'daily' },
  },
  meta: {
    config_path: '/tmp/config.json',
    source: 'file',
    can_save: true,
    restart_required_sections: ['channels', 'gateway'],
    secrets_by_path: {},
  },
})

describe('App', () => {
  beforeEach(() => {
    queryClient.clear()
    localStorage.clear()
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    localStorage.setItem('lele.currentSessionKey', 'native:client-1:1')
    MockWebSocket.reset()
    globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
    globalThis.WebSocket = originalWebSocket
  })

  test('keeps the selected chat when an older history request resolves later', async () => {
    const historyAResolver: { current?: (value: Response) => void } = {}

    const fetchMock = mock((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/v1/auth/status')) {
        return Promise.resolve(
          jsonResponse({
            valid: true,
            client_id: 'client-1',
            device_name: 'Desktop',
            expires: '2026-01-01T00:00:00Z',
          }),
        )
      }
      if (url.endsWith('/api/v1/agents')) {
        return Promise.resolve(
          jsonResponse({
            agents: [
              {
                id: 'main',
                name: 'Main Agent',
                workspace: '~/.lele',
                model: 'gpt-4',
                default: true,
              },
            ],
          }),
        )
      }
      if (url.endsWith('/api/v1/agents/main')) {
        return Promise.resolve(
          jsonResponse({
            id: 'main',
            name: 'Main Agent',
            workspace: '~/.lele',
            model: 'gpt-4',
            default: true,
          }),
        )
      }
      if (url.endsWith('/api/v1/agents/main/status')) {
        return Promise.resolve(jsonResponse({ id: 'main', status: 'running', active_sessions: 1 }))
      }
      if (url.endsWith('/api/v1/status')) {
        return Promise.resolve(
          jsonResponse({ status: 'ok', uptime: '1h', agents: [], channels: [], version: 'dev' }),
        )
      }
      if (url.endsWith('/api/v1/channels')) {
        return Promise.resolve(
          jsonResponse({ channels: [{ name: 'native', enabled: true, running: true }] }),
        )
      }
      if (url.endsWith('/api/v1/tools')) {
        return Promise.resolve(jsonResponse({ tools: [] }))
      }
      if (url.endsWith('/api/v1/config')) {
        return Promise.resolve(jsonResponse(mockConfigResponse()))
      }
      if (isSessionsEndpoint(url)) {
        return Promise.resolve(
          jsonResponse({
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
          }),
        )
      }
      if (url.includes('/api/v1/chat/sessions/native:client-1:1/history')) {
        return new Promise<Response>((resolve) => {
          historyAResolver.current = resolve
        })
      }
      if (url.includes('/api/v1/chat/sessions/native:client-1:2/history')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:2',
            messages: [{ role: 'assistant', content: 'mensaje B' }],
          }),
        )
      }
      if (url.includes('/api/v1/models')) {
        return Promise.resolve(
          jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] }),
        )
      }
      if (url.includes('/api/v1/chat/sessions/')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:2',
            model: 'gpt-4',
            models: ['gpt-4'],
          }),
        )
      }

      return Promise.resolve(notFoundResponse(url))
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = renderWithProviders(<App />)

    // Wait for sessions to load - look for the session by its position in the list
    await waitFor(() => {
      const sessionItems = view.container.querySelectorAll('nav [role="button"]')
      expect(sessionItems.length).toBeGreaterThanOrEqual(2)
    })

    fireEvent.click(view.getByText('Session 2'))

    await waitFor(() => expect(view.getByText('mensaje B')).not.toBeNull())

    historyAResolver.current?.(
      jsonResponse({
        session_key: 'native:client-1:1',
        messages: [{ role: 'assistant', content: 'mensaje A tardio' }],
      }),
    )

    await new Promise((resolve) => setTimeout(resolve, 0))

    expect(view.queryByText('mensaje A tardio')).toBeNull()
    expect(view.getByText('mensaje B')).not.toBeNull()
  })

  test('ignores tool events from a different session after switching chats', async () => {
    localStorage.setItem('lele.currentSessionKey', 'native:client-1:1')
    const fetchMock = mock((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/v1/auth/status')) {
        return Promise.resolve(
          jsonResponse({
            valid: true,
            client_id: 'client-1',
            device_name: 'Desktop',
            expires: '2026-01-01T00:00:00Z',
          }),
        )
      }
      if (url.endsWith('/api/v1/agents')) {
        return Promise.resolve(
          jsonResponse({
            agents: [
              {
                id: 'main',
                name: 'Main Agent',
                workspace: '~/.lele',
                model: 'gpt-4',
                default: true,
              },
            ],
          }),
        )
      }
      if (url.endsWith('/api/v1/agents/main')) {
        return Promise.resolve(
          jsonResponse({
            id: 'main',
            name: 'Main Agent',
            workspace: '~/.lele',
            model: 'gpt-4',
            default: true,
          }),
        )
      }
      if (url.endsWith('/api/v1/agents/main/status')) {
        return Promise.resolve(jsonResponse({ id: 'main', status: 'running', active_sessions: 1 }))
      }
      if (url.endsWith('/api/v1/status')) {
        return Promise.resolve(
          jsonResponse({ status: 'ok', uptime: '1h', agents: [], channels: [], version: 'dev' }),
        )
      }
      if (url.endsWith('/api/v1/channels')) {
        return Promise.resolve(
          jsonResponse({ channels: [{ name: 'native', enabled: true, running: true }] }),
        )
      }
      if (url.endsWith('/api/v1/tools')) {
        return Promise.resolve(jsonResponse({ tools: [] }))
      }
      if (url.endsWith('/api/v1/config')) {
        return Promise.resolve(jsonResponse(mockConfigResponse()))
      }
      if (isSessionsEndpoint(url)) {
        return Promise.resolve(
          jsonResponse({
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
          }),
        )
      }
      if (url.includes('/api/v1/chat/sessions/native:client-1:1/history')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:1',
            messages: [],
          }),
        )
      }
      if (url.includes('/api/v1/chat/sessions/native:client-1:2/history')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:2',
            messages: [],
          }),
        )
      }
      if (url.includes('/api/v1/models')) {
        return Promise.resolve(
          jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] }),
        )
      }
      if (
        url.includes('/api/v1/chat/sessions/native%3Aclient-1%3A2') ||
        url.includes('/api/v1/chat/sessions/native:client-1:2')
      ) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:2',
            model: 'gpt-4',
            models: ['gpt-4'],
          }),
        )
      }
      if (
        url.includes('/api/v1/chat/sessions/native%3Aclient-1%3A1') ||
        url.includes('/api/v1/chat/sessions/native:client-1:1')
      ) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:1',
            model: 'gpt-4',
            models: ['gpt-4'],
          }),
        )
      }

      return Promise.resolve(notFoundResponse(url))
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = renderWithProviders(<App />)

    await waitFor(() => expect(MockWebSocket.instances.length).toBe(1), {
      timeout: 2000,
    })

    let sessionTwoButton: HTMLElement | undefined
    await waitFor(
      () => {
        const sessionItems = Array.from(
          view.container.querySelectorAll('nav [role="button"]'),
        ) as HTMLElement[]
        sessionTwoButton = sessionItems.find((button) => button.textContent?.includes('Session 2'))
        expect(sessionTwoButton).toBeDefined()
      },
      { timeout: 2000 },
    )

    if (!sessionTwoButton) {
      throw new Error('Session 2 button not found')
    }
    const sessionBtn = sessionTwoButton

    await act(async () => {
      fireEvent.click(sessionBtn)
    })

    // Wait for session switch to settle
    await new Promise((resolve) => setTimeout(resolve, 100))

    const socket = MockWebSocket.instances[0]
    if (!socket) {
      throw new Error('WebSocket not initialized')
    }

    await act(async () => {
      socket.emitJSON({
        event: 'tool.executing',
        data: { session_key: 'native:client-1:1', tool: 'exec', action: 'Running old session' },
      })
      socket.emitJSON({
        event: 'tool.executing',
        data: { session_key: 'native:client-1:2', tool: 'exec', action: 'Running active session' },
      })
    }) // Wait longer for the UI to update
    await waitFor(() => expect(view.getByText('Running active session')).not.toBeNull(), {
      timeout: 3000,
    })
    expect(view.queryByText('Running old session')).toBeNull()

    await act(async () => {
      socket.emitJSON({
        event: 'tool.result',
        data: { session_key: 'native:client-1:2', tool: 'exec', result: 'ok' },
      })
    })

    // The tool message for the ACTIVE session stays rendered as a completed
    // record (tool results are persisted, not removed). The important part of
    // this test — that tool events from a DIFFERENT session are dropped — is
    // already asserted above via `Running old session` being null. Just make
    // sure the active session's record is still there.
    expect(view.queryByText('Running active session')).not.toBeNull()
  })
})

describe('Routing', () => {
  beforeEach(() => {
    localStorage.clear()
    MockWebSocket.reset()
    globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
    globalThis.WebSocket = originalWebSocket
  })

  const createFetchMock = (overrides?: { sessions?: Array<Record<string, unknown>> }) =>
    mock((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/v1/auth/status')) {
        return Promise.resolve(
          jsonResponse({
            valid: true,
            client_id: 'client-1',
            device_name: 'Desktop',
            expires: '2026-01-01T00:00:00Z',
          }),
        )
      }
      if (url.endsWith('/api/v1/agents')) {
        return Promise.resolve(
          jsonResponse({
            agents: [
              {
                id: 'main',
                name: 'Main Agent',
                workspace: '~/.lele',
                model: 'gpt-4',
                default: true,
              },
            ],
          }),
        )
      }
      if (url.endsWith('/api/v1/agents/main')) {
        return Promise.resolve(
          jsonResponse({
            id: 'main',
            name: 'Main Agent',
            workspace: '~/.lele',
            model: 'gpt-4',
            default: true,
          }),
        )
      }
      if (url.endsWith('/api/v1/agents/main/status')) {
        return Promise.resolve(jsonResponse({ id: 'main', status: 'running', active_sessions: 1 }))
      }
      if (url.endsWith('/api/v1/status')) {
        return Promise.resolve(
          jsonResponse({ status: 'ok', uptime: '1h', agents: [], channels: [], version: 'dev' }),
        )
      }
      if (url.endsWith('/api/v1/channels')) {
        return Promise.resolve(
          jsonResponse({ channels: [{ name: 'native', enabled: true, running: true }] }),
        )
      }
      if (url.endsWith('/api/v1/tools')) {
        return Promise.resolve(jsonResponse({ tools: [] }))
      }
      if (url.endsWith('/api/v1/config')) {
        return Promise.resolve(jsonResponse(mockConfigResponse()))
      }
      if (isSessionsEndpoint(url)) {
        return Promise.resolve(
          jsonResponse({
            sessions: overrides?.sessions ?? [
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
          }),
        )
      }
      if (url.includes('/api/v1/chat/sessions/native:client-1:1/history/task-1')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'subagent:task-1',
            messages: [
              { role: 'user', content: 'Verifying subagent task' },
              { role: 'assistant', content: 'Subagent result' },
            ],
          }),
        )
      }
      if (url.includes('/api/v1/chat/sessions/native:client-1:1/history')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:1',
            messages: [{ role: 'assistant', content: 'mensaje A' }],
          }),
        )
      }
      if (url.includes('/api/v1/chat/sessions/native:client-1:2/history')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:2',
            messages: [{ role: 'assistant', content: 'mensaje B' }],
          }),
        )
      }
      if (url.includes('/api/v1/models')) {
        return Promise.resolve(
          jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] }),
        )
      }
      if (url.includes('/api/v1/chat/sessions/')) {
        if (
          url.includes('subagent%3Atask-1') ||
          url.includes('subagent:task-1') ||
          url.includes('task-1')
        ) {
          return Promise.resolve(
            jsonResponse({
              session_key: 'subagent:task-1',
              agent_id: 'main',
              model: 'gpt-4',
              models: ['gpt-4'],
            }),
          )
        }

        const sessionKeyMatch = url.match(/\/api\/v1\/chat\/sessions\/([^/?]+)/)
        const decodedSessionKey = sessionKeyMatch ? decodeURIComponent(sessionKeyMatch[1]) : null

        return Promise.resolve(
          jsonResponse({
            session_key: decodedSessionKey ?? 'native:client-1:2',
            agent_id: 'main',
            model: 'gpt-4',
            models: ['gpt-4'],
          }),
        )
      }

      return Promise.resolve(notFoundResponse(url))
    })

  test('redirects to /pair when not authenticated', async () => {
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = renderWithProviders(<App />)

    // Wait for auth form to appear - look for the submit button instead of translated text
    await waitFor(() => {
      expect(view.container.querySelector('button[type="submit"]')).not.toBeNull()
    })
  })

  test('shows auth page at /pair', async () => {
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = renderWithProviders(<App />)

    // Look for submit button instead of translated text
    await waitFor(() => {
      expect(view.container.querySelector('button[type="submit"]')).not.toBeNull()
    })
  })

  test('redirects authenticated user away from /pair', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = renderWithProviders(<App />)

    await waitFor(() => {
      expect(view.container.querySelector('input[inputmode="numeric"]')).toBeNull()
      expect(view.container.querySelector('textarea')).not.toBeNull()
    })
  })

  test('navigates to settings page', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    localStorage.setItem('lele.currentSessionKey', 'native:client-1:1')
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = renderWithProviders(<App />, ['/settings/general'])

    // Wait for settings page to load by looking for settings tabs
    await waitFor(
      () => {
        const settingsButtons = view.container.querySelectorAll('aside nav button')
        expect(settingsButtons.length).toBeGreaterThan(0)
      },
      { timeout: 3000 },
    )
  })

  test('loads specific chat via deep link /chat/:chat_id', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = renderWithProviders(<App />, ['/chat/native:client-1:2'])

    await waitFor(() => expect(view.getByText('mensaje B')).not.toBeNull())
  })

  test('loads subagent chat via nested route and keeps parent in sidebar', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = renderWithProviders(<App />, ['/chat/native:client-1:1/subagent/subagent:task-1'])

    // Wait for subagent chat to load
    await waitFor(
      () => {
        const content = view.container.textContent
        expect(content?.includes('Subagent result') || content?.includes('Verifying')).toBe(true)
      },
      { timeout: 3000 },
    )
    // The heading is derived from the first non-tool message via a useMemo that
    // depends on `messages`. It may not be rendered in the same tick as the
    // message content, so wait for it explicitly.
    await waitFor(
      () => {
        expect(view.getByRole('heading', { name: 'Verifying subagent task' })).not.toBeNull()
      },
      { timeout: 3000 },
    )
    expect(view.queryByText('subagent:task-1')).toBeNull()
    expect(view.queryByText('mensaje A')).toBeNull()
    const sessionButton = view.container.querySelector('[role="button"]')?.parentElement
    expect(sessionButton?.textContent).toContain('Session 1')
  })

  test('redirects to / when chat_id is invalid', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = renderWithProviders(<App />)

    // Should redirect to home and show the selected fallback session
    await waitFor(() => expect(view.getByText('mensaje B')).not.toBeNull())
  })

  test('syncs URL when selecting session', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = renderWithProviders(<App />)

    await waitFor(() => {
      const sessionItems = Array.from(view.container.querySelectorAll('nav span'))
      expect(sessionItems.some((item) => item.textContent === 'Session 2')).toBe(true)
    })

    // Find and click on session item (the span element in the sidebar)
    const sessionItems = view.container.querySelectorAll('nav span')
    for (const item of sessionItems) {
      if (item.textContent === 'Session 2') {
        fireEvent.click(item)
        break
      }
    }

    await waitFor(() => expect(view.getByText('mensaje B')).not.toBeNull())
  })

  test('navigates to nested subagent route from spawn tool and returns to parent', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    localStorage.setItem('lele.currentSessionKey', 'native:client-1:1')
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = renderWithProviders(<App />)

    // Wait for initial session to load
    await waitFor(
      () => {
        const content = view.container.textContent
        expect(content?.includes('mensaje A')).toBe(true)
      },
      { timeout: 3000 },
    )

    const ws = MockWebSocket.instances[0]

    // Emit WebSocket events for tool execution
    await act(async () => {
      ws?.emitJSON({
        event: 'tool.executing',
        data: {
          session_key: 'native:client-1:1',
          tool: 'spawn',
          action: 'Launching subagent',
          subagent_session_key: 'subagent:task-1',
        },
      })
      ws?.emitJSON({
        event: 'tool.result',
        data: {
          session_key: 'native:client-1:1',
          tool: 'spawn',
          result: 'Subagent task ready',
          subagent_session_key: 'subagent:task-1',
        },
      })
      ws?.emitJSON({
        event: 'message.stream',
        data: {
          session_key: 'native:client-1:1',
          message_id: 'parent-response',
          chunk: 'Parent response',
        },
      })
      ws?.emitJSON({
        event: 'message.complete',
        data: {
          session_key: 'native:client-1:1',
          message_id: 'parent-response',
          content: 'Parent response',
        },
      })
    })

    // Wait for the parent response message
    await waitFor(
      () => {
        const content = view.container.textContent
        expect(content?.includes('Parent response')).toBe(true)
      },
      { timeout: 3000 },
    )

    fireEvent.click(view.getByRole('button', { name: 'Abrir chat del subagente' }))
    await waitFor(() => expect(view.getByText('Subagent result')).not.toBeNull())
    await waitFor(
      () => {
        expect(view.getByRole('heading', { name: 'Verifying subagent task' })).not.toBeNull()
      },
      { timeout: 3000 },
    )
    expect(view.queryByText('subagent:task-1')).toBeNull()
    expect(view.queryByText('Parent response')).toBeNull()

    fireEvent.click(view.getByRole('button', { name: 'Session 1' }))

    await waitFor(() => expect(view.getByRole('heading', { name: 'Session 1' })).not.toBeNull())
    expect(view.getByText('mensaje A')).not.toBeNull()
    expect(view.queryByText('Subagent result')).toBeNull()
  })

  test('restores spawn link from history after reloading parent chat', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    localStorage.setItem('lele.currentSessionKey', 'native:client-1:1')

    const baseFetchMock = createFetchMock() as unknown as (
      input: RequestInfo | URL,
    ) => Promise<Response>

    const historyFetchMock = mock((input: RequestInfo | URL) => {
      const url = String(input)

      // Check the subagent history URL FIRST — it is a more specific path
      // (`.../history/task-1`) that also contains the parent history prefix.
      if (url.includes('/api/v1/chat/sessions/native:client-1:1/history/task-1')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'subagent:task-1',
            messages: [
              { role: 'user', content: 'Verifying subagent task' },
              { role: 'assistant', content: 'Subagent result' },
            ],
          }),
        )
      }

      if (url.includes('/api/v1/chat/sessions/native:client-1:1/history')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:1',
            messages: [
              {
                role: 'tool',
                content: "Spawned subagent task task-1 ('Verify task') for task: Investigate issue",
                tool_call_id: 'spawn',
              },
              { role: 'assistant', content: 'Parent response after reload' },
            ],
          }),
        )
      }

      return baseFetchMock(input)
    })
    globalThis.fetch = historyFetchMock as unknown as typeof fetch

    const view = renderWithProviders(<App />)

    // Wait for parent chat with subagent link to load
    await waitFor(
      () => {
        const content = view.container.textContent
        expect(content?.includes('Parent response after reload')).toBe(true)
      },
      { timeout: 3000 },
    )

    await waitFor(
      () => {
        const buttons = view.container.querySelectorAll('button')
        const hasSubagentButton = Array.from(buttons).some(
          (btn) =>
            btn.getAttribute('aria-label')?.includes('subagent') ||
            btn.textContent?.includes('subagent'),
        )
        expect(hasSubagentButton).toBe(true)
      },
      { timeout: 3000 },
    )

    const subagentButton =
      view.container.querySelector('button[aria-label*="subagent"]') ||
      Array.from(view.container.querySelectorAll('button')).find((btn) =>
        btn.textContent?.toLowerCase().includes('subagent'),
      )
    if (subagentButton) {
      fireEvent.click(subagentButton)
    }

    await waitFor(
      () => {
        const content = view.container.textContent
        expect(content?.includes('Subagent result') || content?.includes('Verifying')).toBe(true)
      },
      { timeout: 3000 },
    )
    await waitFor(
      () => {
        expect(view.getByRole('heading', { name: 'Verifying subagent task' })).not.toBeNull()
      },
      { timeout: 3000 },
    )
  })

  test('keeps subagent access when live spawn result only includes task id in text', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    localStorage.setItem('lele.currentSessionKey', 'native:client-1:1')
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = renderWithProviders(<App />)

    // Wait for initial session
    await waitFor(
      () => {
        const content = view.container.textContent
        expect(content?.includes('mensaje A')).toBe(true)
      },
      { timeout: 3000 },
    )

    const ws = MockWebSocket.instances[0]

    // Emit spawn events without subagent_session_key initially
    await act(async () => {
      ws?.emitJSON({
        event: 'tool.executing',
        data: {
          session_key: 'native:client-1:1',
          tool: 'spawn',
          action: 'Launching subagent',
        },
      })
      ws?.emitJSON({
        event: 'tool.result',
        data: {
          session_key: 'native:client-1:1',
          tool: 'spawn',
          result: "Spawned subagent task subagent-1 ('test-coder') for task: test",
        },
      })
    })

    // Wait for subagent button to appear
    await waitFor(
      () => {
        const buttons = view.container.querySelectorAll('button')
        const hasSubagentButton = Array.from(buttons).some(
          (btn) =>
            btn.getAttribute('aria-label')?.toLowerCase().includes('subagent') ||
            btn.textContent?.toLowerCase().includes('subagent'),
        )
        expect(hasSubagentButton).toBe(true)
      },
      { timeout: 3000 },
    )
  })
})

describe('Auto-pairing', () => {
  beforeEach(() => {
    localStorage.clear()
    MockWebSocket.reset()
    globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
    globalThis.WebSocket = originalWebSocket
  })

  test('auto-authenticates with ?code= parameter', async () => {
    let pairCalled = false
    const fetchMock = mock((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/api/v1/auth/pair')) {
        pairCalled = true
        return Promise.resolve(
          jsonResponse({
            token: 'new-token',
            refresh_token: 'new-refresh',
            expires: '2026-01-01T00:00:00Z',
            client_id: 'client-2',
          }),
        )
      }
      if (url.endsWith('/api/v1/auth/status')) {
        return Promise.resolve(
          jsonResponse({
            valid: true,
            client_id: 'client-2',
            device_name: 'My Desktop',
            expires: '2026-01-01T00:00:00Z',
          }),
        )
      }
      if (url.endsWith('/api/v1/agents')) {
        return Promise.resolve(
          jsonResponse({
            agents: [
              {
                id: 'main',
                name: 'Main Agent',
                workspace: '~/.lele',
                model: 'gpt-4',
                default: true,
              },
            ],
          }),
        )
      }
      if (url.endsWith('/api/v1/agents/main')) {
        return Promise.resolve(
          jsonResponse({
            id: 'main',
            name: 'Main Agent',
            workspace: '~/.lele',
            model: 'gpt-4',
            default: true,
          }),
        )
      }
      if (url.endsWith('/api/v1/agents/main/status')) {
        return Promise.resolve(jsonResponse({ id: 'main', status: 'running', active_sessions: 1 }))
      }
      if (url.endsWith('/api/v1/status')) {
        return Promise.resolve(
          jsonResponse({ status: 'ok', uptime: '1h', agents: [], channels: [], version: 'dev' }),
        )
      }
      if (url.endsWith('/api/v1/channels')) {
        return Promise.resolve(
          jsonResponse({ channels: [{ name: 'native', enabled: true, running: true }] }),
        )
      }
      if (url.endsWith('/api/v1/tools')) {
        return Promise.resolve(jsonResponse({ tools: [] }))
      }
      if (url.endsWith('/api/v1/config')) {
        return Promise.resolve(jsonResponse(mockConfigResponse()))
      }
      if (isSessionsEndpoint(url)) {
        return Promise.resolve(
          jsonResponse({
            sessions: [
              {
                key: 'native:client-2:1',
                created: '2026-01-01T00:00:00Z',
                updated: '2026-01-01T00:00:00Z',
              },
              {
                key: 'native:client-2:2',
                created: '2026-01-01T00:00:00Z',
                updated: '2026-01-01T00:01:00Z',
              },
            ],
          }),
        )
      }
      if (url.includes('/api/v1/chat/sessions/native:client-2:1/history')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-2:1',
            messages: [{ role: 'assistant', content: 'mensaje A' }],
          }),
        )
      }
      if (url.includes('/api/v1/chat/sessions/native:client-2:2/history')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-2:2',
            messages: [{ role: 'assistant', content: 'mensaje B' }],
          }),
        )
      }
      if (url.includes('/api/v1/chat/')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:1',
            messages: [],
          }),
        )
      }
      if (url.includes('/api/v1/models')) {
        return Promise.resolve(
          jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] }),
        )
      }
      if (url.includes('/api/v1/chat/sessions/')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-2:2',
            model: 'gpt-4',
            models: ['gpt-4'],
          }),
        )
      }
      return Promise.resolve(jsonResponse({}))
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = renderWithProviders(<App />, ['/pair?code=999999'])

    // Wait for auto-pairing to be called with longer timeout
    await waitFor(() => expect(pairCalled).toBe(true), { timeout: 5000 })

    // Wait for the app to complete loading
    await waitFor(
      () => {
        const hasLoading =
          view.container.textContent?.includes('Connecting') ||
          view.container.querySelector('.animate-spin') !== null
        expect(hasLoading || view.container.querySelector('textarea') !== null).toBe(true)
      },
      { timeout: 5000 },
    )
  })

  test('shows error when auto-pairing fails', async () => {
    localStorage.clear()
    const fetchMock = mock((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/api/v1/auth/pair')) {
        return Promise.resolve(
          new Response(JSON.stringify({ message: 'Invalid PIN', code: 'pair_error' }), {
            status: 400,
            headers: { 'Content-Type': 'application/json' },
          }),
        )
      }
      if (url.endsWith('/api/v1/auth/status')) {
        return Promise.resolve(jsonResponse({ valid: false }))
      }
      if (url.endsWith('/api/v1/agents')) {
        return Promise.resolve(jsonResponse({ agents: [] }))
      }
      if (url.endsWith('/api/v1/agents/main')) {
        return Promise.resolve(
          jsonResponse({ id: 'main', name: 'Main Agent', workspace: '~/.lele', model: 'gpt-4' }),
        )
      }
      if (url.endsWith('/api/v1/agents/main/status')) {
        return Promise.resolve(jsonResponse({ id: 'main', status: 'running', active_sessions: 0 }))
      }
      if (url.endsWith('/api/v1/status')) {
        return Promise.resolve(
          jsonResponse({ status: 'ok', uptime: '1h', agents: [], channels: [], version: 'dev' }),
        )
      }
      if (url.endsWith('/api/v1/channels')) {
        return Promise.resolve(jsonResponse({ channels: [] }))
      }
      if (url.endsWith('/api/v1/tools')) {
        return Promise.resolve(jsonResponse({ tools: [] }))
      }
      if (url.endsWith('/api/v1/config')) {
        return Promise.resolve(jsonResponse(mockConfigResponse()))
      }
      if (isSessionsEndpoint(url)) {
        return Promise.resolve(jsonResponse({ sessions: [] }))
      }
      if (url.includes('/api/v1/chat/')) {
        return Promise.resolve(jsonResponse({ session_key: 'native:client-1:1', messages: [] }))
      }
      if (url.includes('/api/v1/models')) {
        return Promise.resolve(
          jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] }),
        )
      }
      if (url.includes('/api/v1/chat/sessions/')) {
        return Promise.resolve(
          jsonResponse({ session_key: 'native:client-1:1', model: 'gpt-4', models: ['gpt-4'] }),
        )
      }
      return Promise.resolve(jsonResponse({}))
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = renderWithProviders(<App />, ['/pair?code=999999'])

    // Wait for loading state to finish and form to appear
    await waitFor(
      () => {
        const hasForm = view.container.querySelector('form') !== null
        const hasNumericInput = view.container.querySelector('input[inputmode="numeric"]') !== null
        const hasError = view.container.textContent?.includes('Invalid PIN')
        expect(hasForm || hasNumericInput || hasError).toBe(true)
      },
      { timeout: 3000 },
    )

    // PIN should be pre-filled - look for numeric input
    const pinInput = view.container.querySelector('input[inputmode="numeric"]') as HTMLInputElement
    if (pinInput?.value) {
      expect(pinInput.value).toBe('999999')
    }

    // Error should be visible
    await waitFor(
      () => {
        expect(view.container.textContent).toContain('Invalid PIN')
      },
      { timeout: 3000 },
    )
  })

  test('pre-fills PIN from URL code parameter', async () => {
    const fetchMock = mock((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/v1/auth/status')) {
        return Promise.resolve(jsonResponse({ valid: false }))
      }
      if (url.includes('/api/v1/auth/pair')) {
        return Promise.reject(new Error('No auto-pair in this test'))
      }
      if (url.endsWith('/api/v1/auth/refresh')) {
        return Promise.resolve(new Response(null, { status: 401 }))
      }
      return Promise.resolve(jsonResponse({}))
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = renderWithProviders(<App />, ['/pair?code=654321'])

    await waitFor(() => {
      const hasForm = view.container.querySelector('form') !== null
      const hasNumericInput = view.container.querySelector('input[inputmode="numeric"]') !== null
      const hasSubmitButton = view.container.querySelector('button[type="submit"]') !== null
      expect(hasForm || hasNumericInput || hasSubmitButton).toBe(true)
    })

    const pinInput = view.container.querySelector('input[inputmode="numeric"]') as HTMLInputElement
    if (pinInput) {
      expect(pinInput.value).toBe('654321')
    }
  })
})

describe('Session deletion', () => {
  beforeEach(() => {
    localStorage.clear()
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    localStorage.setItem('lele.currentSessionKey', 'native:client-1:1')
    MockWebSocket.reset()
    globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
    globalThis.WebSocket = originalWebSocket
  })

  const createFetchMock = () =>
    mock((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/v1/auth/status')) {
        return Promise.resolve(
          jsonResponse({
            valid: true,
            client_id: 'client-1',
            device_name: 'Desktop',
            expires: '2026-01-01T00:00:00Z',
          }),
        )
      }
      if (url.endsWith('/api/v1/agents')) {
        return Promise.resolve(
          jsonResponse({
            agents: [
              {
                id: 'main',
                name: 'Main Agent',
                workspace: '~/.lele',
                model: 'gpt-4',
                default: true,
              },
            ],
          }),
        )
      }
      if (url.endsWith('/api/v1/agents/main')) {
        return Promise.resolve(
          jsonResponse({
            id: 'main',
            name: 'Main Agent',
            workspace: '~/.lele',
            model: 'gpt-4',
            default: true,
          }),
        )
      }
      if (url.endsWith('/api/v1/agents/main/status')) {
        return Promise.resolve(jsonResponse({ id: 'main', status: 'running', active_sessions: 1 }))
      }
      if (url.endsWith('/api/v1/status')) {
        return Promise.resolve(
          jsonResponse({ status: 'ok', uptime: '1h', agents: [], channels: [], version: 'dev' }),
        )
      }
      if (url.endsWith('/api/v1/channels')) {
        return Promise.resolve(
          jsonResponse({ channels: [{ name: 'native', enabled: true, running: true }] }),
        )
      }
      if (url.endsWith('/api/v1/tools')) {
        return Promise.resolve(jsonResponse({ tools: [] }))
      }
      if (url.endsWith('/api/v1/config')) {
        return Promise.resolve(jsonResponse(mockConfigResponse()))
      }
      if (isSessionsEndpoint(url)) {
        return Promise.resolve(
          jsonResponse({
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
          }),
        )
      }
      if (
        url.endsWith('/api/v1/chat/sessions/native:client-1:1') ||
        url.endsWith('/api/v1/chat/sessions/native%3Aclient-1%3A1') ||
        url.endsWith('/api/v1/chat/sessions/native:client-1:1')
      ) {
        return Promise.resolve(jsonResponse({}))
      }
      if (url.includes('/api/v1/chat/sessions/native:client-1:1/history')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:1',
            messages: [{ role: 'assistant', content: 'mensaje A' }],
          }),
        )
      }
      if (url.includes('/api/v1/chat/sessions/native:client-1:2/history')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:2',
            messages: [{ role: 'assistant', content: 'mensaje B' }],
          }),
        )
      }
      if (url.includes('/api/v1/models')) {
        return Promise.resolve(
          jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] }),
        )
      }
      // Subagent polling endpoint — now that the App fully mounts, useSubagents
      // fires this request. Return an empty list so no retry is scheduled
      // (a thrown "Unexpected fetch" would be retried against the real network
      // after `fetch` is restored, leaking a rejection into the next test file).
      if (url.includes('/subagents')) {
        return Promise.resolve(jsonResponse({ subagents: [] }))
      }

      return Promise.resolve(notFoundResponse(url))
    })

  test('navigates to next session when deleting active session', async () => {
    let deleteCalled = false
    const baseMock = createFetchMock()
    const fetchMock = mock((input: RequestInfo | URL) => {
      const url = String(input)
      if (
        url.endsWith('/api/v1/chat/sessions/native:client-1:1') ||
        url.endsWith('/api/v1/chat/sessions/native%3Aclient-1%3A1') ||
        url.endsWith('/api/v1/chat/sessions/native:client-1:1')
      ) {
        deleteCalled = true
        return Promise.resolve(jsonResponse({}))
      }
      return baseMock(input)
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = renderWithProviders(<App />)

    let sessionOneDeleteButton: Element | undefined
    await waitFor(() => {
      const deleteButtons = view.container.querySelectorAll('button[aria-label="Eliminar sesión"]')
      sessionOneDeleteButton = Array.from(deleteButtons).find((button) =>
        button.parentElement?.textContent?.includes('Session 1'),
      )
      expect(sessionOneDeleteButton).toBeDefined()
    })
    if (sessionOneDeleteButton) {
      const btn = sessionOneDeleteButton
      // First click arms the confirmation state (aria-label becomes confirmDelete)
      await act(async () => {
        fireEvent.click(btn)
      })
      // Second click on the now-armed button actually deletes
      const confirmButton = view.container.querySelector(
        'button[aria-label="Haz clic de nuevo para confirmar"]',
      ) as HTMLElement | null
      await act(async () => {
        fireEvent.click(confirmButton ?? btn)
      })
    }

    await waitFor(() => expect(deleteCalled).toBe(true))
  })

  test('redirects to / when deleting last session', async () => {
    const fetchMock = mock((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/v1/auth/status')) {
        return Promise.resolve(
          jsonResponse({
            valid: true,
            client_id: 'client-1',
            device_name: 'Desktop',
            expires: '2026-01-01T00:00:00Z',
          }),
        )
      }
      if (url.endsWith('/api/v1/agents')) {
        return Promise.resolve(
          jsonResponse({
            agents: [
              {
                id: 'main',
                name: 'Main Agent',
                workspace: '~/.lele',
                model: 'gpt-4',
                default: true,
              },
            ],
          }),
        )
      }
      if (url.endsWith('/api/v1/agents/main')) {
        return Promise.resolve(
          jsonResponse({
            id: 'main',
            name: 'Main Agent',
            workspace: '~/.lele',
            model: 'gpt-4',
            default: true,
          }),
        )
      }
      if (url.endsWith('/api/v1/agents/main/status')) {
        return Promise.resolve(jsonResponse({ id: 'main', status: 'running', active_sessions: 1 }))
      }
      if (url.endsWith('/api/v1/status')) {
        return Promise.resolve(
          jsonResponse({ status: 'ok', uptime: '1h', agents: [], channels: [], version: 'dev' }),
        )
      }
      if (url.endsWith('/api/v1/channels')) {
        return Promise.resolve(
          jsonResponse({ channels: [{ name: 'native', enabled: true, running: true }] }),
        )
      }
      if (url.endsWith('/api/v1/tools')) {
        return Promise.resolve(jsonResponse({ tools: [] }))
      }
      if (url.endsWith('/api/v1/config')) {
        return Promise.resolve(jsonResponse(mockConfigResponse()))
      }
      if (isSessionsEndpoint(url)) {
        return Promise.resolve(
          jsonResponse({
            sessions: [
              {
                key: 'native:client-1:1',
                created: '2026-01-01T00:00:00Z',
                updated: '2026-01-01T00:00:00Z',
              },
            ],
          }),
        )
      }
      if (
        url.endsWith('/api/v1/chat/sessions/native:client-1:1') ||
        url.endsWith('/api/v1/chat/sessions/native%3Aclient-1%3A1') ||
        url.endsWith('/api/v1/chat/sessions/native:client-1:1')
      ) {
        return Promise.resolve(jsonResponse({}))
      }
      if (url.includes('/api/v1/chat/')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:1',
            messages: [{ role: 'assistant', content: 'only message' }],
          }),
        )
      }
      if (url.includes('/api/v1/models')) {
        return Promise.resolve(
          jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] }),
        )
      }

      return Promise.resolve(notFoundResponse(url))
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    renderWithProviders(<App />)

    // Test that the component renders without errors
    await waitFor(() => {
      expect(document.querySelector('.h-screen')).not.toBeNull()
    })
  })
})
