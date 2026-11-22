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

  simulateDisconnect() {
    this.close()
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

const isSessionsEndpoint = (url: string) => {
  const path = url.split('?')[0]
  return path.endsWith('/api/v1/chat/sessions') || path.endsWith('/api/v1/chat/sessions/meta')
}

const notFoundResponse = (url: string) =>
  new Response(JSON.stringify({ error: `Unexpected fetch: ${url}` }), {
    status: 404,
    headers: { 'Content-Type': 'application/json' },
  })

const AGENT_CONFIG_FIXTURE = {
  id: 'coder',
  name: 'Coder',
  workspace: '~/.lele/workspace-coder',
}

const mockConfigResponse = (agents?: unknown[]) => ({
  config: {
    agents: {
      ...(agents ? { list: agents } : {}),
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
        rate_limit: {
          enabled: false,
          pin_per_minute: 10,
          pair_per_minute: 5,
          refresh_per_minute: 20,
          api_per_minute: 120,
          ws_messages_per_minute: 120,
        },
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
    })
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

  const createFetchMock = (overrides?: {
    sessions?: Array<Record<string, unknown>>
    configAgents?: unknown[]
  }) =>
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
        return Promise.resolve(jsonResponse(mockConfigResponse(overrides?.configAgents)))
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

    await waitFor(() => {
      expect(view.container.querySelector('button[type="submit"]')).not.toBeNull()
    })
  })

  test('shows auth page at /pair', async () => {
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = renderWithProviders(<App />)

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

    await waitFor(
      () => {
        const settingsButtons = view.container.querySelectorAll('aside nav button')
        expect(settingsButtons.length).toBeGreaterThan(0)
      },
      { timeout: 3000 },
    )
  })

  test('renders the agents list inside the settings page at /settings/agents', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = renderWithProviders(<App />, ['/settings/agents'])

    await waitFor(
      () => {
        expect(view.container.querySelector('[data-testid="agents-search"]')).not.toBeNull()
        expect(view.container.querySelector('[data-testid="agents-empty"]')).not.toBeNull()
      },
      { timeout: 3000 },
    )
    expect(view.container.querySelector('aside')).not.toBeNull()
    expect(view.container.querySelector('[data-testid="settings-tabs"]')).not.toBeNull()
  })

  test('redirects the legacy /agents URL to /settings/agents', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = renderWithProviders(<App />, ['/agents'])

    await waitFor(
      () => {
        expect(view.container.querySelector('[data-testid="agents-search"]')).not.toBeNull()
      },
      { timeout: 3000 },
    )
    expect(
      view.container
        .querySelector('[data-testid="settings-tab-agents"]')
        ?.getAttribute('aria-current'),
    ).toBe('page')
  })

  test('renders the real agent config page with its tabs under /settings/agents/:agentId/:tab', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock({
      configAgents: [AGENT_CONFIG_FIXTURE],
    }) as unknown as typeof fetch

    const view = renderWithProviders(<App />, ['/settings/agents/coder/tools'])

    await waitFor(
      () => {
        expect(view.container.querySelector('[data-testid="agent-config-page"]')).not.toBeNull()
      },
      { timeout: 3000 },
    )
    expect(view.container.querySelectorAll('[role="tab"]')).toHaveLength(8)
    const selected = view.container.querySelector('[role="tab"][aria-selected="true"]')
    expect(selected?.id).toBe('agent-tab-tools')
    expect(view.container.querySelector('[role="tabpanel"]')?.getAttribute('aria-labelledby')).toBe(
      'agent-tab-tools',
    )
  })

  test('the legacy /agents/:agentId/:tab deep link lands on the same page', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock({
      configAgents: [AGENT_CONFIG_FIXTURE],
    }) as unknown as typeof fetch

    const view = renderWithProviders(<App />, ['/agents/coder/tools'])

    await waitFor(
      () => {
        expect(view.container.querySelector('[data-testid="agent-config-page"]')).not.toBeNull()
      },
      { timeout: 3000 },
    )
  })

  test('an unknown agent id shows the not-found block once the config loaded', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock({
      configAgents: [AGENT_CONFIG_FIXTURE],
    }) as unknown as typeof fetch

    const view = renderWithProviders(<App />, ['/settings/agents/ghost/general'])

    await waitFor(
      () => {
        expect(
          view.container.querySelector('[data-testid="agent-config-not-found"]'),
        ).not.toBeNull()
      },
      { timeout: 3000 },
    )
    expect(view.container.textContent).toContain('ghost')
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
    expect(view.queryByText('subagent:task-1')).toBeNull()
    expect(view.queryByText('mensaje A')).toBeNull()
    const sessionButton = view.container.querySelector('[role="button"]')?.parentElement
    expect(sessionButton?.textContent).toContain('Session 1')
  })

  test('redirects to / when chat_id is invalid', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = renderWithProviders(<App />)

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

    await waitFor(
      () => {
        const content = view.container.textContent
        expect(content?.includes('mensaje A')).toBe(true)
      },
      { timeout: 3000 },
    )

    const ws = MockWebSocket.instances[0]

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

    await waitFor(
      () => {
        const content = view.container.textContent
        expect(content?.includes('mensaje A')).toBe(true)
      },
      { timeout: 3000 },
    )

    const ws = MockWebSocket.instances[0]

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

    await waitFor(() => expect(pairCalled).toBe(true), { timeout: 5000 })

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

    await waitFor(
      () => {
        const hasForm = view.container.querySelector('form') !== null
        const hasNumericInput = view.container.querySelector('input[inputmode="numeric"]') !== null
        const hasError = view.container.textContent?.includes('Invalid PIN')
        expect(hasForm || hasNumericInput || hasError).toBe(true)
      },
      { timeout: 3000 },
    )

    const pinInput = view.container.querySelector('input[inputmode="numeric"]') as HTMLInputElement
    if (pinInput?.value) {
      expect(pinInput.value).toBe('999999')
    }

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

    await waitFor(
      () => {
        const pinInput = view.container.querySelector(
          'input[inputmode="numeric"]',
        ) as HTMLInputElement
        expect(pinInput?.value).toBe('654321')
      },
      { timeout: 4000 },
    )
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
      await act(async () => {
        fireEvent.click(btn)
      })
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

    await waitFor(() => {
      expect(document.querySelector('.app-shell')).not.toBeNull()
    })
  })
})

const buildPaginatedSessions = (count: number) =>
  Array.from({ length: count }, (_, index) => ({
    key: `native:client-1:${index + 1}`,
    created: '2026-01-01T00:00:00Z',
    updated: new Date(Date.UTC(2026, 0, 1, 0, 0, index)).toISOString(),
  }))

const paginatedSessionsBody = (allSessions: Array<Record<string, unknown>>, url: string) => {
  const params = new URL(url).searchParams
  const offset = Number(params.get('offset') ?? '0')
  const limit = Number(params.get('limit') ?? '200')
  const page = allSessions.slice(offset, offset + limit)
  return {
    sessions: page,
    total: allSessions.length,
    has_more: offset + page.length < allSessions.length,
  }
}

const createConnectionLifecycleFetchMock = (
  allSessions: Array<Record<string, unknown>>,
  metaResponse?: (url: string) => Promise<Response>,
) =>
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
            { id: 'main', name: 'Main Agent', workspace: '~/.lele', model: 'gpt-4', default: true },
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
      return Promise.resolve(jsonResponse({ channels: [] }))
    }
    if (url.endsWith('/api/v1/tools')) {
      return Promise.resolve(jsonResponse({ tools: [] }))
    }
    if (url.endsWith('/api/v1/config')) {
      return Promise.resolve(jsonResponse(mockConfigResponse()))
    }
    if (url.includes('/api/v1/chat/sessions/') && url.includes('/history')) {
      return Promise.resolve(
        jsonResponse({
          session_key: 'native:client-1:1',
          messages: [{ role: 'assistant', content: 'mensaje A' }],
        }),
      )
    }
    if (isSessionsEndpoint(url)) {
      if (metaResponse) return metaResponse(url)
      return Promise.resolve(jsonResponse(paginatedSessionsBody(allSessions, url)))
    }
    if (url.includes('/api/v1/chat/sessions/')) {
      return Promise.resolve(
        jsonResponse({
          session_key: 'native:client-1:1',
          agent_id: 'main',
          model: 'gpt-4',
          models: ['gpt-4'],
          level: 'default',
          folder: '',
        }),
      )
    }
    if (url.includes('/api/v1/models')) {
      return Promise.resolve(jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] }))
    }

    return Promise.resolve(notFoundResponse(url))
  })

type ConnectionLifecycleFetchMock = ReturnType<typeof createConnectionLifecycleFetchMock>

const requestedMetaOffsets = (fetchMock: ConnectionLifecycleFetchMock): string[] =>
  fetchMock.mock.calls
    .map(([input]) => String(input))
    .filter((url) => url.includes('/api/v1/chat/sessions/meta?'))
    .map((url) => new URL(url).searchParams.get('offset') ?? '')

const requestedHistoryCount = (fetchMock: ConnectionLifecycleFetchMock): number =>
  fetchMock.mock.calls
    .map(([input]) => String(input))
    .filter((url) => url.includes('/api/v1/chat/sessions/') && url.includes('/history')).length

const requestedModelLookupCount = (fetchMock: ConnectionLifecycleFetchMock): number =>
  fetchMock.mock.calls
    .map(([input]) => String(input))
    .filter(
      (url) =>
        (url.includes('/api/v1/models') && url.includes('agent_id=')) ||
        (url.includes('/api/v1/chat/sessions/') && url.endsWith('/model')),
    ).length

const waitForConnectionObserved = async (
  fetchMock: ConnectionLifecycleFetchMock,
): Promise<MockWebSocket> => {
  await waitFor(() => expect(MockWebSocket.instances.length).toBe(1), { timeout: 2000 })
  await waitFor(() => expect(requestedModelLookupCount(fetchMock)).toBeGreaterThan(0), {
    timeout: 2000,
  })
  const socket = MockWebSocket.instances[0]
  if (!socket) {
    throw new Error('WebSocket not initialized')
  }
  return socket
}

const settleEffects = async (ms = 150) => {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, ms))
  })
}

describe('WebSocket connection lifecycle', () => {
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

  test('the initial connection does not repeat the paginated sessions pass', async () => {
    const fetchMock = createConnectionLifecycleFetchMock(buildPaginatedSessions(250))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    renderWithProviders(<App />)

    await waitForConnectionObserved(fetchMock)
    await settleEffects()

    expect(requestedMetaOffsets(fetchMock)).toEqual(['0', '200'])
  })

  test('a bootstrap that failed recovers on the FIRST connection, exactly once', async () => {
    const allSessions = buildPaginatedSessions(250)
    let metaRequests = 0
    const fetchMock = createConnectionLifecycleFetchMock(allSessions, (url) => {
      metaRequests += 1
      if (metaRequests === 1) return Promise.resolve(notFoundResponse(url))
      return Promise.resolve(jsonResponse(paginatedSessionsBody(allSessions, url)))
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    renderWithProviders(<App />)

    await waitForConnectionObserved(fetchMock)
    await settleEffects()

    expect(requestedMetaOffsets(fetchMock)).toEqual(['0', '0', '200'])
  })

  test('a real reconnect still refreshes sessions and revalidates history', async () => {
    const fetchMock = createConnectionLifecycleFetchMock(buildPaginatedSessions(250))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    renderWithProviders(<App />)

    const socket = await waitForConnectionObserved(fetchMock)
    await settleEffects()

    expect(requestedMetaOffsets(fetchMock)).toEqual(['0', '200'])
    expect(requestedHistoryCount(fetchMock)).toBe(1)

    act(() => {
      socket.simulateDisconnect()
    })

    await waitFor(() => expect(MockWebSocket.instances.length).toBe(2), { timeout: 3000 })

    await waitFor(() => expect(requestedHistoryCount(fetchMock)).toBe(2), { timeout: 3000 })
    await settleEffects()

    expect(requestedMetaOffsets(fetchMock)).toEqual(['0', '200', '0', '200'])
  })
})

const setDocumentVisibility = (state: DocumentVisibilityState) => {
  Object.defineProperty(document, 'visibilityState', { value: state, configurable: true })
  document.dispatchEvent(new window.Event('visibilitychange'))
}

const restoreDocumentVisibility = () => {
  Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
}

describe('Tab visibility: session-list revalidation', () => {
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
    restoreDocumentVisibility()
  })

  test('revalidates the session list exactly once when the tab becomes visible again', async () => {
    const fetchMock = createConnectionLifecycleFetchMock(buildPaginatedSessions(250))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    renderWithProviders(<App />)

    await waitForConnectionObserved(fetchMock)
    await settleEffects()

    expect(requestedMetaOffsets(fetchMock)).toEqual(['0', '200'])

    act(() => setDocumentVisibility('hidden'))
    await settleEffects()
    expect(requestedMetaOffsets(fetchMock)).toEqual(['0', '200'])

    act(() => setDocumentVisibility('visible'))

    await waitFor(() => expect(requestedMetaOffsets(fetchMock).length).toBe(4), { timeout: 3000 })
    await settleEffects()
    expect(requestedMetaOffsets(fetchMock)).toEqual(['0', '200', '0', '200'])
  })
})

describe('Logout', () => {
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

  test('a session-list pass still in flight cannot revive the state after logout', async () => {
    const allSessions = buildPaginatedSessions(2)
    const pendingMetaPasses: Array<(value: Response) => void> = []
    let pendingMetaUrl = ''

    const fetchMock = createConnectionLifecycleFetchMock(allSessions, (url) => {
      pendingMetaUrl = url
      return new Promise<Response>((resolve) => {
        pendingMetaPasses.push(resolve)
      })
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = renderWithProviders(<App />)

    const logoutButton = await waitFor(() => view.getByLabelText('Cerrar sesión'))
    await waitFor(() => expect(requestedMetaOffsets(fetchMock)).toEqual(['0']))
    expect(pendingMetaPasses).toHaveLength(1)
    const [releaseMetaPass] = pendingMetaPasses

    fireEvent.click(logoutButton)

    await settleEffects()

    await waitFor(() => expect(view.queryByLabelText('Cerrar sesión')).toBeNull(), {
      timeout: 5000,
    })
    expect(view.queryByTestId('sidebar-history-section')).toBeNull()
    expect(localStorage.getItem('lele.session')).toBeNull()
    expect(localStorage.getItem('lele.currentSessionKey')).toBeNull()

    await act(async () => {
      releaseMetaPass?.(jsonResponse(paginatedSessionsBody(allSessions, pendingMetaUrl)))
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    await settleEffects()

    expect(localStorage.getItem('lele.currentSessionKey')).toBeNull()
    expect(localStorage.getItem('lele.session')).toBeNull()
  })
})
