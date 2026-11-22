import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import App from '../App'
import { ThemeProvider } from '../contexts/ThemeContext'
import { queryClient } from '../lib/queryClient'

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

  emit(type: string, event?: Event) {
    for (const listener of this.listeners.get(type) ?? []) {
      listener(event)
    }
  }

  static reset() {
    MockWebSocket.instances = []
  }
}

const jsonResponse = (body: Record<string, unknown>) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })

const CONFIG_RESPONSE = {
  config: {
    agents: {
      list: [{ id: 'coder', name: 'Coder', workspace: '~/.lele/workspace-coder' }],
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
    restart_required_sections: [],
    secrets_by_path: {},
  },
}

const MODELS_RESPONSE = {
  agent_id: 'main',
  model: 'gpt-4',
  models: ['gpt-4', 'gpt-4o'],
  model_groups: [
    {
      provider: 'openai',
      models: [
        { value: 'gpt-4', label: 'gpt-4' },
        { value: 'gpt-4o', label: 'gpt-4o' },
      ],
    },
  ],
}

const AGENTS_RESPONSE = {
  agents: [{ id: 'main', name: 'Main Agent', workspace: '~/.lele', model: 'gpt-4', default: true }],
}

const createFetchMock = (options?: { slowConfig?: boolean; triggerModels401?: boolean }) => {
  const { slowConfig = false, triggerModels401 = false } = options ?? {}
  let modelsCallCount = 0
  let configCallCount = 0

  return mock((input: RequestInfo | URL) => {
    const url = String(input)

    if (url.endsWith('/api/v1/auth/status')) {
      return Promise.resolve(
        jsonResponse({
          valid: true,
          client_id: 'client-1',
          device_name: 'Desktop',
          expires: '2026-12-01T00:00:00Z',
        }),
      )
    }
    if (url.endsWith('/api/v1/auth/refresh')) {
      return Promise.resolve(
        jsonResponse({
          token: `refreshed-token-${Date.now()}`,
          refresh_token: 'new-refresh-token',
          expires: '2026-12-02T00:00:00Z',
        }),
      )
    }

    if (url.endsWith('/api/v1/config')) {
      configCallCount++
      const response = jsonResponse(CONFIG_RESPONSE)
      if (slowConfig && configCallCount > 1) {
        return new Promise((resolve) => setTimeout(() => resolve(response), 200))
      }
      return Promise.resolve(response)
    }

    if (url.includes('/api/v1/models')) {
      modelsCallCount++
      if (triggerModels401 && modelsCallCount > 1) {
        return Promise.resolve(
          new Response(JSON.stringify({ error: 'unauthorized' }), {
            status: 401,
            headers: { 'Content-Type': 'application/json' },
          }),
        )
      }
      return Promise.resolve(jsonResponse(MODELS_RESPONSE))
    }

    if (url.endsWith('/api/v1/agents')) {
      return Promise.resolve(jsonResponse(AGENTS_RESPONSE))
    }
    if (
      url.match(/\/api\/v1\/agents\/[^/]+$/) &&
      !url.includes('/status') &&
      !url.includes('/skills')
    ) {
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
    if (url.includes('/api/v1/agents/') && url.endsWith('/status')) {
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
    if (url.includes('/api/v1/secrets')) {
      return Promise.resolve(jsonResponse({ secrets: [] }))
    }
    if (url.includes('/api/v1/cron')) {
      return Promise.resolve(
        jsonResponse({ jobs: [], status: { enabled: true, jobs: 0, next_run: null } }),
      )
    }
    if (url.includes('/api/v1/background-exec')) {
      return Promise.resolve(jsonResponse({ processes: [] }))
    }
    if (url.includes('/api/v1/skills')) {
      return Promise.resolve(jsonResponse({ skills: [] }))
    }
    if (url.includes('/subagents')) {
      return Promise.resolve(jsonResponse({ subagents: [] }))
    }
    if (url.includes('/api/v1/providers')) {
      return Promise.resolve(jsonResponse({ providers: [] }))
    }
    if (url.includes('/history')) {
      return Promise.resolve(jsonResponse({ session_key: 'native:client-1:1', messages: [] }))
    }
    if (url.includes('/api/v1/chat/sessions')) {
      return Promise.resolve(
        jsonResponse({
          sessions: [],
          session_key: 'native:client-1:1',
          model: 'gpt-4',
          models: ['gpt-4'],
        }),
      )
    }

    return Promise.resolve(
      new Response(JSON.stringify({ error: `Unexpected: ${url}` }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
  })
}

function renderApp(route: string) {
  return render(
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={[route]}>
          <App />
        </MemoryRouter>
      </QueryClientProvider>
    </ThemeProvider>,
  )
}

async function waitForInputs(timeout = 5000): Promise<NodeListOf<HTMLInputElement>> {
  await waitFor(
    () => {
      const inputs = document.querySelectorAll('input')
      expect(inputs.length).toBeGreaterThan(0)
    },
    { timeout },
  )
  return document.querySelectorAll('input')
}

describe('Settings focus regression — refetch must not unmount focused input', () => {
  const originalFetch = globalThis.fetch
  const originalWebSocket = globalThis.WebSocket

  beforeEach(() => {
    queryClient.clear()
    localStorage.clear()
    localStorage.setItem(
      'lele.session',
      JSON.stringify({
        token: 'token',
        refresh_token: 'refresh',
        expires: '2026-12-01T00:00:00Z',
        client_id: 'client-1',
        device_name: 'Desktop',
      }),
    )
    localStorage.setItem('lele.currentSessionKey', 'native:client-1:1')

    MockWebSocket.reset()
    globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket

    Object.defineProperty(window, 'innerWidth', { value: 1600, writable: true, configurable: true })
    Object.defineProperty(window, 'innerHeight', {
      value: 1000,
      writable: true,
      configurable: true,
    })
    try {
      window.dispatchEvent(new window.Event('resize'))
    } catch {}
  })

  afterEach(() => {
    cleanup()
    queryClient.clear()
    globalThis.fetch = originalFetch
    globalThis.WebSocket = originalWebSocket
  })

  test('settings form stays mounted — no loading skeleton after initial load', async () => {
    const fetchMock = createFetchMock()
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = renderApp('/settings/general')

    const inputs = await waitForInputs()
    const inputCount = inputs.length
    expect(inputCount).toBeGreaterThan(0)

    const mainContent = document.querySelector('main')
    expect(mainContent).toBeTruthy()
    expect(document.querySelectorAll('input').length).toBe(inputCount)

    view.unmount()
  })

  test('typing survives a token refresh that triggers persistSession', async () => {
    const fetchMock = createFetchMock({ slowConfig: true, triggerModels401: true })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = renderApp('/settings/general')

    await waitForInputs()

    const allInputs = Array.from(document.querySelectorAll('input')).filter(
      (el) => el.getAttribute('type') !== 'file' && el.getAttribute('type') !== 'hidden',
    )
    expect(allInputs.length).toBeGreaterThan(0)

    const input = allInputs[0] as HTMLInputElement
    const probeId = 'regression-probe'
    input.setAttribute('data-probe', probeId)

    input.focus()
    fireEvent.focus(input)
    expect(document.activeElement).toBe(input)

    const text = 'abcdefgh'
    for (let i = 0; i < text.length; i++) {
      const ch = text[i]
      const partial = text.slice(0, i + 1)

      fireEvent.keyDown(input, { key: ch, code: `Key${ch.toUpperCase()}` })
      input.value = partial
      fireEvent.input(input, { target: { value: partial } })
      fireEvent.change(input, { target: { value: partial } })
      fireEvent.keyUp(input, { key: ch, code: `Key${ch.toUpperCase()}` })

      await act(async () => {
        await new Promise((r) => setTimeout(r, 10))
      })

      const activeProbe = document.activeElement?.getAttribute('data-probe')
      expect(activeProbe).toBe(probeId)
    }

    expect(document.activeElement?.getAttribute('data-probe')).toBe(probeId)
    expect(document.activeElement).toBe(input)

    view.unmount()
  })

  test('form survives multiple render cycles without unmounting inputs', async () => {
    const fetchMock = createFetchMock()
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = renderApp('/settings/general')

    await waitForInputs()
    const initialInputCount = document.querySelectorAll('input').length
    expect(initialInputCount).toBeGreaterThan(0)

    const firstInput = document.querySelectorAll('input')[0] as HTMLInputElement
    firstInput.setAttribute('data-probe', 'survival-check')
    firstInput.focus()
    fireEvent.focus(firstInput)

    for (let cycle = 0; cycle < 5; cycle++) {
      await act(async () => {
        await new Promise((r) => setTimeout(r, 50))
      })

      expect(document.querySelectorAll('input').length).toBe(initialInputCount)

      const tagged = document.querySelector('[data-probe="survival-check"]')
      expect(tagged).not.toBeNull()
      expect(tagged).toBe(firstInput)
    }

    view.unmount()
  })

  test('settings form on /settings/agents also survives typing', async () => {
    const fetchMock = createFetchMock()
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = renderApp('/settings/agents')

    await waitForInputs()

    const allInputs = Array.from(document.querySelectorAll('input')).filter(
      (el) => el.getAttribute('type') !== 'file' && el.getAttribute('type') !== 'hidden',
    )
    if (allInputs.length === 0) {
      view.unmount()
      return
    }

    const input = allInputs[0] as HTMLInputElement
    input.setAttribute('data-probe', 'agents-probe')
    input.focus()
    fireEvent.focus(input)

    const text = 'test'
    for (let i = 0; i < text.length; i++) {
      const ch = text[i]
      const partial = text.slice(0, i + 1)

      fireEvent.keyDown(input, { key: ch })
      input.value = partial
      fireEvent.input(input, { target: { value: partial } })
      fireEvent.change(input, { target: { value: partial } })
      fireEvent.keyUp(input, { key: ch })

      await act(async () => {
        await new Promise((r) => setTimeout(r, 0))
      })

      expect(document.activeElement?.getAttribute('data-probe')).toBe('agents-probe')
    }

    view.unmount()
  })
})
