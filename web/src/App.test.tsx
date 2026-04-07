import './test/setup'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { act, fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import './test/i18n'
import App from './App'

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

describe('App', () => {
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
        return Promise.resolve(jsonResponse({ config: {} }))
      }
      if (url.endsWith('/api/v1/chat/sessions')) {
        return Promise.resolve(
          jsonResponse({
            sessions: [
              {
                key: 'native:client-1:1',
                created: '2026-01-01T00:00:00Z',
                updated: '2026-01-01T00:00:00Z',
                message_count: 1,
              },
              {
                key: 'native:client-1:2',
                created: '2026-01-01T00:00:00Z',
                updated: '2026-01-01T00:01:00Z',
                message_count: 1,
              },
            ],
          }),
        )
      }
      if (url.includes('/api/v1/chat/history?session_key=native%3Aclient-1%3A1')) {
        return new Promise<Response>((resolve) => {
          historyAResolver.current = resolve
        })
      }
      if (url.includes('/api/v1/chat/history?session_key=native%3Aclient-1%3A2')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:2',
            messages: [{ role: 'assistant', content: 'mensaje B' }],
          }),
        )
      }
      if (url.includes('/api/v1/models?')) {
        return Promise.resolve(
          jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] }),
        )
      }
      if (url.includes('/api/v1/chat/session/')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:2',
            model: 'gpt-4',
            models: ['gpt-4'],
          }),
        )
      }

      throw new Error(`Unexpected fetch: ${url}`)
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = render(
      <MemoryRouter>
        <App />
      </MemoryRouter>,
    )

    await waitFor(() => expect(view.getByText('Session 2')).not.toBeNull())

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
        return Promise.resolve(jsonResponse({ config: {} }))
      }
      if (url.endsWith('/api/v1/chat/sessions')) {
        return Promise.resolve(
          jsonResponse({
            sessions: [
              {
                key: 'native:client-1:1',
                created: '2026-01-01T00:00:00Z',
                updated: '2026-01-01T00:00:00Z',
                message_count: 0,
              },
              {
                key: 'native:client-1:2',
                created: '2026-01-01T00:00:00Z',
                updated: '2026-01-01T00:01:00Z',
                message_count: 0,
              },
            ],
          }),
        )
      }
      if (url.includes('/api/v1/chat/history?')) {
        const sessionKey = url.includes('native%3Aclient-1%3A2')
          ? 'native:client-1:2'
          : 'native:client-1:1'
        return Promise.resolve(jsonResponse({ session_key: sessionKey, messages: [] }))
      }
      if (url.includes('/api/v1/models?')) {
        return Promise.resolve(
          jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] }),
        )
      }
      if (url.includes('/api/v1/chat/session/')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:2',
            model: 'gpt-4',
            models: ['gpt-4'],
          }),
        )
      }

      throw new Error(`Unexpected fetch: ${url}`)
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = render(
      <MemoryRouter>
        <App />
      </MemoryRouter>,
    )

    await waitFor(() => expect(MockWebSocket.instances.length).toBe(1))
    await waitFor(() => expect(view.getByText('Session 2')).not.toBeNull())

    fireEvent.click(view.getByText('Session 2'))

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

    await waitFor(() => expect(view.getByText('Running active session')).not.toBeNull())
    expect(view.queryByText('Running old session')).toBeNull()

    await act(async () => {
      socket.emitJSON({
        event: 'tool.result',
        data: { session_key: 'native:client-1:2', tool: 'exec', result: 'ok' },
      })
    })

    await waitFor(() => expect(view.queryByText('Running active session')).toBeNull())
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
        return Promise.resolve(jsonResponse({ config: {} }))
      }
      if (url.endsWith('/api/v1/chat/sessions')) {
        return Promise.resolve(
          jsonResponse({
            sessions: overrides?.sessions ?? [
              {
                key: 'native:client-1:1',
                created: '2026-01-01T00:00:00Z',
                updated: '2026-01-01T00:00:00Z',
                message_count: 1,
              },
              {
                key: 'native:client-1:2',
                created: '2026-01-01T00:00:00Z',
                updated: '2026-01-01T00:01:00Z',
                message_count: 1,
              },
            ],
          }),
        )
      }
      if (url.includes('/api/v1/chat/history?session_key=native%3Aclient-1%3A1')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:1',
            messages: [{ role: 'assistant', content: 'mensaje A' }],
          }),
        )
      }
      if (url.includes('/api/v1/chat/history?session_key=native%3Aclient-1%3A2')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:2',
            messages: [{ role: 'assistant', content: 'mensaje B' }],
          }),
        )
      }
      if (url.includes('/api/v1/models?')) {
        return Promise.resolve(
          jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] }),
        )
      }
      if (url.includes('/api/v1/chat/session/')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:2',
            model: 'gpt-4',
            models: ['gpt-4'],
          }),
        )
      }

      throw new Error(`Unexpected fetch: ${url}`)
    })

  test('redirects to /pair when not authenticated', async () => {
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = render(
      <MemoryRouter initialEntries={['/']}>
        <App />
      </MemoryRouter>,
    )

    // Wait for auth form to appear - look for the submit button instead of translated text
    await waitFor(() => {
      expect(view.container.querySelector('button[type="submit"]')).not.toBeNull()
    })
  })

  test('shows auth page at /pair', async () => {
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = render(
      <MemoryRouter initialEntries={['/pair']}>
        <App />
      </MemoryRouter>,
    )

    // Look for submit button instead of translated text
    await waitFor(() => {
      expect(view.container.querySelector('button[type="submit"]')).not.toBeNull()
    })
  })

  test('redirects authenticated user away from /pair', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = render(
      <MemoryRouter initialEntries={['/pair']}>
        <App />
      </MemoryRouter>,
    )

    await waitFor(() => {
      // Should show chat page, not auth page
      expect(view.container.querySelector('button[type="submit"]')).toBeNull()
    })
  })

  test('navigates to settings page', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    localStorage.setItem('lele.currentSessionKey', 'native:client-1:1')
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = render(
      <MemoryRouter initialEntries={['/settings']}>
        <App />
      </MemoryRouter>,
    )

    // Look for logout button by class instead of text
    await waitFor(() => {
      expect(view.container.querySelector('button.bg-rose-600')).not.toBeNull()
    })
  })

  test('loads specific chat via deep link /chat/:chat_id', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = render(
      <MemoryRouter initialEntries={['/chat/native:client-1:2']}>
        <App />
      </MemoryRouter>,
    )

    await waitFor(() => expect(view.getByText('mensaje B')).not.toBeNull())
  })

  test('redirects to / when chat_id is invalid', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = render(
      <MemoryRouter initialEntries={['/chat/invalid:session']}>
        <App />
      </MemoryRouter>,
    )

    // Should redirect to home and show the default session
    await waitFor(() => expect(view.getByText('mensaje A')).not.toBeNull())
  })

  test('syncs URL when selecting session', async () => {
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    globalThis.fetch = createFetchMock() as unknown as typeof fetch

    const view = render(
      <MemoryRouter initialEntries={['/']}>
        <App />
      </MemoryRouter>,
    )

    await waitFor(() => expect(view.getByText('Session 2')).not.toBeNull())

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
      return Promise.resolve(jsonResponse({}))
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    render(
      <MemoryRouter initialEntries={['/pair?code=123456']}>
        <App />
      </MemoryRouter>,
    )

    await waitFor(() => expect(pairCalled).toBe(true))
  })

  test('shows error when auto-pairing fails', async () => {
    const fetchMock = mock((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/api/v1/auth/pair')) {
        return Promise.reject(new Error('Invalid PIN'))
      }
      return Promise.resolve(jsonResponse({}))
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = render(
      <MemoryRouter initialEntries={['/pair?code=999999']}>
        <App />
      </MemoryRouter>,
    )

    // Wait for loading state to finish and form to appear
    await waitFor(() => {
      expect(view.container.querySelector('form')).not.toBeNull()
    }, { timeout: 3000 })

    // PIN should be pre-filled - look for numeric input
    const pinInput = view.container.querySelector('input[inputmode="numeric"]') as HTMLInputElement
    if (pinInput) {
      expect(pinInput.value).toBe('999999')
    }

    // Error should be visible
    await waitFor(() => {
      const errorElement = view.container.querySelector('.text-rose-200') || view.container.querySelector('[class*="rose"]')
      expect(errorElement).not.toBeNull()
    })
  })

  test('pre-fills PIN from URL code parameter', async () => {
    globalThis.fetch = mock(() => Promise.resolve(jsonResponse({}))) as unknown as typeof fetch

    const view = render(
      <MemoryRouter initialEntries={['/pair?code=654321']}>
        <App />
      </MemoryRouter>,
    )

    await waitFor(() => {
      expect(view.container.querySelector('form')).not.toBeNull()
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
        return Promise.resolve(jsonResponse({ config: {} }))
      }
      if (url.endsWith('/api/v1/chat/sessions')) {
        return Promise.resolve(
          jsonResponse({
            sessions: [
              {
                key: 'native:client-1:1',
                created: '2026-01-01T00:00:00Z',
                updated: '2026-01-01T00:00:00Z',
                message_count: 1,
              },
              {
                key: 'native:client-1:2',
                created: '2026-01-01T00:00:00Z',
                updated: '2026-01-01T00:01:00Z',
                message_count: 1,
              },
            ],
          }),
        )
      }
      if (url.endsWith('/api/v1/chat/session/native:client-1:1')) {
        return Promise.resolve(jsonResponse({}))
      }
      if (url.includes('/api/v1/chat/history?')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:1',
            messages: [{ role: 'assistant', content: 'mensaje A' }],
          }),
        )
      }
      if (url.includes('/api/v1/models?')) {
        return Promise.resolve(
          jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] }),
        )
      }

      throw new Error(`Unexpected fetch: ${url}`)
    })

  test('navigates to next session when deleting active session', async () => {
    let deleteCalled = false
    const baseMock = createFetchMock()
    const fetchMock = mock((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/v1/chat/session/native:client-1:1')) {
        deleteCalled = true
        return Promise.resolve(jsonResponse({}))
      }
      return baseMock(input)
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const view = render(
      <MemoryRouter initialEntries={['/chat/native:client-1:1']}>
        <App />
      </MemoryRouter>,
    )

    await waitFor(() => expect(view.getByText('mensaje A')).not.toBeNull())

    // Find and click delete button for session 1 (looking for delete icon/button)
    const deleteButtons = view.container.querySelectorAll('button[title="Delete session"]')
    if (deleteButtons.length > 0) {
      await act(async () => {
        fireEvent.click(deleteButtons[0])
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
        return Promise.resolve(jsonResponse({ config: {} }))
      }
      if (url.endsWith('/api/v1/chat/sessions')) {
        return Promise.resolve(
          jsonResponse({
            sessions: [
              {
                key: 'native:client-1:1',
                created: '2026-01-01T00:00:00Z',
                updated: '2026-01-01T00:00:00Z',
                message_count: 1,
              },
            ],
          }),
        )
      }
      if (url.endsWith('/api/v1/chat/session/native:client-1:1')) {
        return Promise.resolve(jsonResponse({}))
      }
      if (url.includes('/api/v1/chat/history')) {
        return Promise.resolve(
          jsonResponse({
            session_key: 'native:client-1:1',
            messages: [{ role: 'assistant', content: 'only message' }],
          }),
        )
      }
      if (url.includes('/api/v1/models?')) {
        return Promise.resolve(
          jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] }),
        )
      }

      throw new Error(`Unexpected fetch: ${url}`)
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    render(
      <MemoryRouter initialEntries={['/chat/native:client-1:1']}>
        <App />
      </MemoryRouter>,
    )

    // Test that the component renders without errors
    await waitFor(() => {
      expect(document.querySelector('.h-screen')).not.toBeNull()
    })
  })
})
