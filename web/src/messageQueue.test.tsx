import './test/setup'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, renderHook, waitFor } from '@testing-library/react'
import type { ReactElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import App from './App'
import { ThemeProvider } from './contexts/ThemeContext'
import { QUEUE_CAP, useMessageQueue } from './hooks/useMessageQueue'
import { queryClient } from './lib/queryClient'
import './test/i18n'

const SESSION = 'native:client-1:1'
const SESSION_B = 'native:client-1:2'

class MockWebSocket {
  static instances: MockWebSocket[] = []

  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3

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

type FetchResponseBody = Record<string, unknown>

const jsonResponse = (body: FetchResponseBody) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })

const notFoundResponse = (url: string) =>
  new Response(JSON.stringify({ error: `Unexpected fetch: ${url}` }), {
    status: 404,
    headers: { 'Content-Type': 'application/json' },
  })

const isSessionsEndpoint = (url: string) => {
  const path = url.split('?')[0]
  return path.endsWith('/api/v1/chat/sessions') || path.endsWith('/api/v1/chat/sessions/meta')
}

const createFetchMock = mock((input: RequestInfo | URL) => {
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
    return Promise.resolve(
      jsonResponse({ channels: [{ name: 'native', enabled: true, running: true }] }),
    )
  }
  if (url.endsWith('/api/v1/tools')) {
    return Promise.resolve(jsonResponse({ tools: [] }))
  }
  if (url.endsWith('/api/v1/config')) {
    return Promise.resolve(
      jsonResponse({
        config: {
          agents: { defaults: { workspace: '~/.lele', provider: 'openai', model: 'gpt-4' } },
        },
        meta: { config_path: '/tmp/config.json', source: 'file', can_save: true },
      }),
    )
  }
  if (isSessionsEndpoint(url)) {
    return Promise.resolve(
      jsonResponse({
        sessions: [
          { key: SESSION, created: '2026-01-01T00:00:00Z', updated: '2026-01-01T00:00:00Z' },
          { key: SESSION_B, created: '2026-01-01T00:00:00Z', updated: '2026-01-01T00:01:00Z' },
        ],
      }),
    )
  }
  if (url.includes('/subagents')) {
    return Promise.resolve(jsonResponse({ subagents: [] }))
  }
  if (url.includes('/commands')) {
    return Promise.resolve(jsonResponse({ commands: [] }))
  }
  if (url.includes('/api/v1/models')) {
    return Promise.resolve(jsonResponse({ agent_id: 'main', model: 'gpt-4', models: ['gpt-4'] }))
  }
  if (url.includes('/api/v1/chat/sessions/')) {
    return Promise.resolve(
      jsonResponse({
        session_key: SESSION,
        agent_id: 'main',
        model: 'gpt-4',
        messages: [],
        processing: false,
        level: 'default',
        folder: '',
      }),
    )
  }

  return Promise.resolve(notFoundResponse(url))
})

const authSession = {
  token: 'token',
  refresh_token: 'refresh',
  expires: '2026-01-01T00:00:00Z',
  client_id: 'client-1',
  device_name: 'Desktop',
}

const renderWithProviders = (ui: ReactElement, initialEntries = [`/chat/${SESSION}`]) =>
  render(
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={initialEntries}>{ui}</MemoryRouter>
      </QueryClientProvider>
    </ThemeProvider>,
  )

function sentUserMessages(ws: MockWebSocket): string[] {
  return ws.sent
    .map((raw) => JSON.parse(raw) as { event?: string; data?: { content?: string } })
    .filter((payload) => payload.event === 'message')
    .map((payload) => payload.data?.content ?? '')
}

function getComposer(view: ReturnType<typeof renderWithProviders>): HTMLTextAreaElement {
  const textarea = view.container.querySelector('textarea')
  if (!textarea) throw new Error('composer textarea not found')
  return textarea as HTMLTextAreaElement
}

function submitMessage(view: ReturnType<typeof renderWithProviders>, text: string) {
  const textarea = getComposer(view)
  fireEvent.change(textarea, { target: { value: text } })
  fireEvent.keyDown(textarea, { key: 'Enter', shiftKey: false })
}

function sentCancelScopes(ws: MockWebSocket): Array<string | undefined> {
  return ws.sent
    .map((raw) => JSON.parse(raw) as { event?: string; data?: { scope?: string } })
    .filter((payload) => payload.event === 'cancel')
    .map((payload) => payload.data?.scope)
}

function queuedRows(view: ReturnType<typeof renderWithProviders>): string[] {
  return Array.from(view.container.querySelectorAll('[data-testid="queued-message"]')).map(
    (row) => row.textContent ?? '',
  )
}

describe('useMessageQueue', () => {
  test('mantiene orden FIFO por sesion', () => {
    const { result } = renderHook(() => useMessageQueue())

    act(() => {
      expect(result.current.enqueueMessage(SESSION, 'uno')).toBe(true)
      expect(result.current.enqueueMessage(SESSION, 'dos')).toBe(true)
    })

    expect(result.current.queueCount(SESSION)).toBe(2)
    expect(result.current.queuedMessages.map((m) => m.content)).toEqual(['uno', 'dos'])

    expect(result.current.peekNext(SESSION)?.content).toBe('uno')
    expect(result.current.queueCount(SESSION)).toBe(2)

    act(() => {
      expect(result.current.dequeueNext(SESSION)?.content).toBe('uno')
    })
    act(() => {
      expect(result.current.dequeueNext(SESSION)?.content).toBe('dos')
    })
    expect(result.current.dequeueNext(SESSION)).toBeUndefined()
    expect(result.current.queuedMessages).toEqual([])
  })

  test('aisla las colas por sesion', () => {
    const { result } = renderHook(() => useMessageQueue())

    act(() => {
      result.current.enqueueMessage(SESSION, 'a1')
      result.current.enqueueMessage(SESSION_B, 'b1')
      result.current.enqueueMessage(SESSION, 'a2')
    })

    expect(result.current.queueCount(SESSION)).toBe(2)
    expect(result.current.queueCount(SESSION_B)).toBe(1)
    act(() => {
      expect(result.current.dequeueNext(SESSION)?.content).toBe('a1')
    })
    expect(result.current.peekNext(SESSION_B)?.content).toBe('b1')
    expect(result.current.queueCount(SESSION)).toBe(1)

    act(() => {
      result.current.clearQueue(SESSION)
    })
    expect(result.current.queueCount(SESSION)).toBe(0)
    expect(result.current.queueCount(SESSION_B)).toBe(1)
  })

  test('takeQueuedMessage extrae una entrada especifica sin tocar las demas', () => {
    const { result } = renderHook(() => useMessageQueue())

    act(() => {
      result.current.enqueueMessage(SESSION, 'uno')
      result.current.enqueueMessage(SESSION, 'dos')
      result.current.enqueueMessage(SESSION, 'tres')
    })

    const middle = result.current.queuedMessages[1]
    expect(middle.content).toBe('dos')

    let taken: { content: string; attachments: string[] } | undefined
    act(() => {
      taken = result.current.takeQueuedMessage(middle.id)
    })
    expect(taken?.content).toBe('dos')
    expect(result.current.queuedMessages.map((m) => m.content)).toEqual(['uno', 'tres'])
    expect(result.current.peekNext(SESSION)?.content).toBe('uno')

    act(() => {
      expect(result.current.takeQueuedMessage('missing')).toBeUndefined()
    })
    expect(result.current.queueCount(SESSION)).toBe(2)
  })

  test('rechaza al alcanzar el tope y libera al eliminar', () => {
    const { result } = renderHook(() => useMessageQueue())

    for (let i = 0; i < QUEUE_CAP; i += 1) {
      act(() => {
        expect(result.current.enqueueMessage(SESSION, `m${i}`)).toBe(true)
      })
    }
    expect(result.current.enqueueMessage(SESSION_B, 'other')).toBe(true)
    expect(result.current.enqueueMessage(SESSION, 'overflow')).toBe(false)
    expect(result.current.queueCount(SESSION)).toBe(QUEUE_CAP)
    expect(result.current.queuedMessages.some((m) => m.content === 'overflow')).toBe(false)

    const first = result.current.queuedMessages.find((m) => m.sessionKey === SESSION)
    expect(first).toBeDefined()
    act(() => {
      result.current.removeQueuedMessage((first as { id: string }).id)
    })
    expect(result.current.queueCount(SESSION)).toBe(QUEUE_CAP - 1)
    expect(result.current.enqueueMessage(SESSION, 'overflow')).toBe(true)
  })

  test('ignora entradas vacias o sin sesion', () => {
    const { result } = renderHook(() => useMessageQueue())

    expect(result.current.enqueueMessage('', 'hola')).toBe(false)
    expect(result.current.enqueueMessage(SESSION, '   ')).toBe(false)
    expect(result.current.enqueueMessage(SESSION, '', ['/tmp/a.png'])).toBe(true)
    expect(result.current.queueCount(SESSION)).toBe(1)
    expect(result.current.peekNext(null)).toBeUndefined()
    expect(result.current.queueCount(null)).toBe(0)
    expect(result.current.dequeueNext(null)).toBeUndefined()
  })

  test('no muta el arreglo ya expuesto en el estado', () => {
    const { result } = renderHook(() => useMessageQueue())

    act(() => {
      result.current.enqueueMessage(SESSION, 'a')
    })
    act(() => {
      result.current.enqueueMessage(SESSION, 'b')
    })
    const snapshot = result.current.queuedMessages
    expect(snapshot.map((m) => m.content)).toEqual(['a', 'b'])

    act(() => {
      result.current.dequeueNext(SESSION)
    })

    expect(snapshot.map((m) => m.content)).toEqual(['a', 'b'])
    expect(result.current.queuedMessages.map((m) => m.content)).toEqual(['b'])
  })
})

describe('cola de mensajes del composer', () => {
  beforeEach(() => {
    queryClient.clear()
    localStorage.clear()
    localStorage.setItem('lele.session', JSON.stringify(authSession))
    localStorage.setItem('lele.currentSessionKey', SESSION)
    MockWebSocket.reset()
    globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
    globalThis.fetch = createFetchMock as unknown as typeof fetch
  })

  afterEach(() => {
    cleanup()
    queryClient.clear()
    globalThis.fetch = originalFetch
    globalThis.WebSocket = originalWebSocket
  })

  async function openChat() {
    const view = renderWithProviders(<App />)
    await waitFor(() => expect(getComposer(view)).toBeTruthy())
    const ws = MockWebSocket.instances[0]
    expect(ws).toBeDefined()
    return { view, ws: ws as MockWebSocket }
  }

  test('encola mientras el agente trabaja y lo reenvia al cerrar el turno', async () => {
    const { view, ws } = await openChat()

    submitMessage(view, 'first message')
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message']))
    await act(async () => {
      ws.emitJSON({ event: 'message.ack', data: { session_key: SESSION, message_id: 'm1' } })
    })

    submitMessage(view, 'second message')
    await waitFor(() => expect(queuedRows(view).length).toBe(1))
    expect(queuedRows(view)[0]).toContain('second message')
    expect(sentUserMessages(ws)).toEqual(['first message'])

    const strip = view.container.querySelector('[data-testid="queued-messages"]')
    expect(strip?.textContent).toContain('En cola · 1')

    await act(async () => {
      ws.emitJSON({
        event: 'message.complete',
        data: { session_key: SESSION, message_id: 'm1', content: 'reply one' },
      })
    })

    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message', 'second message']))
    await waitFor(() => expect(queuedRows(view).length).toBe(0))
  })

  test('drena de a un mensaje por turno y conserva el orden', async () => {
    const { view, ws } = await openChat()

    submitMessage(view, 'first message')
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message']))
    await act(async () => {
      ws.emitJSON({ event: 'message.ack', data: { session_key: SESSION, message_id: 'm1' } })
    })

    submitMessage(view, 'second message')
    submitMessage(view, 'third message')
    await waitFor(() => expect(queuedRows(view).length).toBe(2))

    await act(async () => {
      ws.emitJSON({
        event: 'message.complete',
        data: { session_key: SESSION, message_id: 'm1', content: 'reply one' },
      })
    })

    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message', 'second message']))
    await waitFor(() => {
      const rows = queuedRows(view)
      expect(rows.length).toBe(1)
      expect(rows[0]).toContain('third message')
    })

    await act(async () => {
      ws.emitJSON({ event: 'message.ack', data: { session_key: SESSION, message_id: 'm2' } })
    })
    await waitFor(() =>
      expect(view.container.querySelector('button[aria-label="Cancelar"]')).not.toBeNull(),
    )
    await act(async () => {
      ws.emitJSON({
        event: 'message.complete',
        data: { session_key: SESSION, message_id: 'm2', content: 'reply two' },
      })
    })

    await waitFor(() =>
      expect(sentUserMessages(ws)).toEqual(['first message', 'second message', 'third message']),
    )
    await waitFor(() => expect(queuedRows(view).length).toBe(0))
  })

  test('no encola cuando el agente esta libre', async () => {
    const { view, ws } = await openChat()

    submitMessage(view, 'idle message')

    await new Promise((r) => setTimeout(r, 200))
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['idle message']))
    expect(queuedRows(view).length).toBe(0)
    expect(view.container.querySelector('[data-testid="queued-messages"]')).toBeNull()
  })

  test('permite eliminar una entrada o vaciar la cola', async () => {
    const { view, ws } = await openChat()

    submitMessage(view, 'first message')
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message']))
    await act(async () => {
      ws.emitJSON({ event: 'message.ack', data: { session_key: SESSION, message_id: 'm1' } })
    })

    submitMessage(view, 'keep me')
    submitMessage(view, 'drop me')
    await waitFor(() => expect(queuedRows(view).length).toBe(2))

    const dropRow = Array.from(
      view.container.querySelectorAll('[data-testid="queued-message"]'),
    ).find((row) => (row.textContent ?? '').includes('drop me'))
    expect(dropRow).toBeDefined()
    const removeButton = (dropRow as Element).querySelector(
      'button[aria-label="Quitar de la cola"]',
    )
    expect(removeButton).toBeDefined()
    await act(async () => {
      fireEvent.click(removeButton as HTMLElement)
    })

    await waitFor(() => {
      const rows = queuedRows(view)
      expect(rows.length).toBe(1)
      expect(rows[0]).toContain('keep me')
    })

    await act(async () => {
      ws.emitJSON({
        event: 'message.complete',
        data: { session_key: SESSION, message_id: 'm1', content: 'reply one' },
      })
    })
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message', 'keep me']))

    await act(async () => {
      ws.emitJSON({ event: 'message.ack', data: { session_key: SESSION, message_id: 'm2' } })
    })
    submitMessage(view, 'queued a')
    submitMessage(view, 'queued b')
    await waitFor(() => expect(queuedRows(view).length).toBe(2))

    const clearButton = view.container.querySelector('button[aria-label="Vaciar cola"]')
    expect(clearButton).toBeDefined()
    await act(async () => {
      fireEvent.click(clearButton as HTMLElement)
    })

    await waitFor(() => expect(queuedRows(view).length).toBe(0))
    await act(async () => {
      ws.emitJSON({
        event: 'message.complete',
        data: { session_key: SESSION, message_id: 'm2', content: 'reply two' },
      })
    })
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(sentUserMessages(ws)).toEqual(['first message', 'keep me'])
  })

  test('no envia nada al cerrar el turno si la cola quedo vacia al quitar', async () => {
    const { view, ws } = await openChat()

    submitMessage(view, 'first message')
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message']))
    await act(async () => {
      ws.emitJSON({ event: 'message.ack', data: { session_key: SESSION, message_id: 'm1' } })
    })

    submitMessage(view, 'drop before turn end')
    await waitFor(() => expect(queuedRows(view).length).toBe(1))

    const row = view.container.querySelector('[data-testid="queued-message"]')
    expect(row?.textContent).toContain('drop before turn end')
    const removeButton = (row as Element).querySelector('button[aria-label="Quitar de la cola"]')
    expect(removeButton).toBeDefined()
    await act(async () => {
      fireEvent.click(removeButton as HTMLElement)
    })

    await waitFor(() => expect(queuedRows(view).length).toBe(0))
    expect(view.container.querySelector('[data-testid="queued-messages"]')).toBeNull()

    await act(async () => {
      ws.emitJSON({
        event: 'message.complete',
        data: { session_key: SESSION, message_id: 'm1', content: 'reply one' },
      })
    })
    await new Promise((resolve) => setTimeout(resolve, 200))

    expect(sentUserMessages(ws)).toEqual(['first message'])
    const messageEvents = ws.sent
      .map((raw) => JSON.parse(raw) as { event?: string })
      .filter((payload) => payload.event === 'message')
    expect(messageEvents.length).toBe(1)
  })

  test('no contamina otras sesiones al cambiar de chat', async () => {
    const { view, ws } = await openChat()

    submitMessage(view, 'first message')
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message']))
    await act(async () => {
      ws.emitJSON({ event: 'message.ack', data: { session_key: SESSION, message_id: 'm1' } })
    })

    submitMessage(view, 'second message')
    await waitFor(() => expect(queuedRows(view).length).toBe(1))

    await act(async () => {
      fireEvent.click(view.getByText('Session 2'))
    })
    await waitFor(() => {
      expect(view.container.querySelector('[data-testid="queued-messages"]')).toBeNull()
    })
    expect(sentUserMessages(ws)).toEqual(['first message'])

    await act(async () => {
      fireEvent.click(view.getByText('Session 1'))
    })
    await waitFor(() => expect(queuedRows(view).length).toBe(1))
    expect(queuedRows(view)[0]).toContain('second message')

    await act(async () => {
      ws.emitJSON({
        event: 'message.complete',
        data: { session_key: SESSION, message_id: 'm1', content: 'reply one' },
      })
    })
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message', 'second message']))
    const flushed = ws.sent
      .map(
        (raw) =>
          JSON.parse(raw) as {
            event?: string
            data?: { session_key?: string; content?: string }
          },
      )
      .find((payload) => payload.event === 'message' && payload.data?.content === 'second message')
    expect(flushed?.data?.session_key).toBe(SESSION)
  })

  test('conserva el borrador cuando la cola esta llena', async () => {
    const { view, ws } = await openChat()

    submitMessage(view, 'first message')
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message']))
    await act(async () => {
      ws.emitJSON({ event: 'message.ack', data: { session_key: SESSION, message_id: 'm1' } })
    })

    for (let i = 0; i < QUEUE_CAP; i += 1) {
      submitMessage(view, `queued ${i}`)
    }
    await waitFor(() => expect(queuedRows(view).length).toBe(QUEUE_CAP))

    submitMessage(view, 'overflow message')
    await waitFor(() => {
      expect(view.getByRole('alert')).not.toBeNull()
      expect(view.getByRole('alert').textContent).toContain('cola está llena')
    })
    expect(getComposer(view).value).toBe('overflow message')
    expect(queuedRows(view).length).toBe(QUEUE_CAP)
    expect(sentUserMessages(ws)).toEqual(['first message'])
  })

  test('encola comandos slash sin procesarlos en el composer', async () => {
    const { view, ws } = await openChat()

    submitMessage(view, 'first message')
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message']))
    await act(async () => {
      ws.emitJSON({ event: 'message.ack', data: { session_key: SESSION, message_id: 'm1' } })
    })

    submitMessage(view, '/compact')
    await waitFor(() => {
      const rows = queuedRows(view)
      expect(rows.length).toBe(1)
      expect(rows[0]).toContain('/compact')
    })
    expect(sentUserMessages(ws)).toEqual(['first message'])

    await act(async () => {
      ws.emitJSON({
        event: 'message.complete',
        data: { session_key: SESSION, message_id: 'm1', content: 'reply one' },
      })
    })
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message', '/compact']))
  })

  test('el boton de parar pide una parada de sesion completa', async () => {
    const { view, ws } = await openChat()

    submitMessage(view, 'first message')
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message']))
    await act(async () => {
      ws.emitJSON({ event: 'message.ack', data: { session_key: SESSION, message_id: 'm1' } })
    })

    await act(async () => {
      fireEvent.click(view.getByRole('button', { name: 'Cancelar' }))
    })

    expect(sentCancelScopes(ws)).toEqual(['session'])
  })

  test('envia ya una entrada de la cola cuando el agente esta libre', async () => {
    const { view, ws } = await openChat()

    submitMessage(view, 'first message')
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message']))
    await act(async () => {
      ws.emitJSON({ event: 'message.ack', data: { session_key: SESSION, message_id: 'm1' } })
    })

    submitMessage(view, 'keep me')
    submitMessage(view, 'send me now')
    await waitFor(() => expect(queuedRows(view).length).toBe(2))

    await act(async () => {
      ws.emitJSON({
        event: 'message.complete',
        data: { session_key: SESSION, message_id: 'm1', content: 'reply one' },
      })
    })
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message', 'keep me']))
    await waitFor(() => expect(queuedRows(view).length).toBe(1))
    expect(queuedRows(view)[0]).toContain('send me now')

    const sendNow = view.container.querySelector('[data-testid="queued-send-now"]')
    expect(sendNow).toBeTruthy()
    await act(async () => {
      fireEvent.click(sendNow as HTMLElement)
    })

    await waitFor(() =>
      expect(sentUserMessages(ws)).toEqual(['first message', 'keep me', 'send me now']),
    )
    expect(view.container.querySelector('[data-testid="queued-messages"]')).toBeNull()
  })

  test('enviar ahora mientras esta ocupado cancela el turno y reenvia', async () => {
    const { view, ws } = await openChat()

    submitMessage(view, 'first message')
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message']))
    await act(async () => {
      ws.emitJSON({ event: 'message.ack', data: { session_key: SESSION, message_id: 'm1' } })
    })

    submitMessage(view, 'leave me')
    submitMessage(view, 'jump the queue')
    await waitFor(() => expect(queuedRows(view).length).toBe(2))

    const rows = Array.from(view.container.querySelectorAll('[data-testid="queued-message"]'))
    const jumpRow = rows.find((row) => (row.textContent ?? '').includes('jump the queue'))
    expect(jumpRow).toBeDefined()
    const sendNow = (jumpRow as Element).querySelector('[data-testid="queued-send-now"]')
    expect(sendNow).toBeTruthy()
    await act(async () => {
      fireEvent.click(sendNow as HTMLElement)
    })

    await waitFor(() => {
      const remaining = queuedRows(view)
      expect(remaining.length).toBe(1)
      expect(remaining[0]).toContain('leave me')
    })

    expect(sentCancelScopes(ws)).toEqual(['turn'])

    await act(async () => {
      ws.emitJSON({ event: 'cancel.ack', data: { session_key: SESSION, status: 'ok' } })
    })
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message', 'jump the queue']))
    await waitFor(() => expect(queuedRows(view).length).toBe(1))
    expect(queuedRows(view)[0]).toContain('leave me')
  })

  test('no entrega un force-send aparcado a otra sesion al cambiar de chat', async () => {
    const { view, ws } = await openChat()

    submitMessage(view, 'first message')
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message']))
    await act(async () => {
      ws.emitJSON({ event: 'message.ack', data: { session_key: SESSION, message_id: 'm1' } })
    })

    submitMessage(view, 'leaky message')
    await waitFor(() => expect(queuedRows(view).length).toBe(1))

    const sendNow = view.container.querySelector('[data-testid="queued-send-now"]')
    expect(sendNow).toBeTruthy()
    await act(async () => {
      fireEvent.click(sendNow as HTMLElement)
    })
    await waitFor(() => expect(queuedRows(view).length).toBe(0))

    await act(async () => {
      fireEvent.click(view.getByText('Session 2'))
    })
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(sentUserMessages(ws)).toEqual(['first message'])

    await act(async () => {
      ws.emitJSON({ event: 'cancel.ack', data: { session_key: SESSION, status: 'ok' } })
    })
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(sentUserMessages(ws)).toEqual(['first message'])

    await act(async () => {
      fireEvent.click(view.getByText('Session 1'))
    })
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message', 'leaky message']))
    const flushed = ws.sent
      .map(
        (raw) =>
          JSON.parse(raw) as {
            event?: string
            data?: { session_key?: string; content?: string }
          },
      )
      .find((payload) => payload.event === 'message' && payload.data?.content === 'leaky message')
    expect(flushed?.data?.session_key).toBe(SESSION)
  })

  test('no descarta un segundo enviar-ahora mientras hay un turno cancelandose', async () => {
    const { view, ws } = await openChat()

    submitMessage(view, 'first message')
    await waitFor(() => expect(sentUserMessages(ws)).toEqual(['first message']))
    await act(async () => {
      ws.emitJSON({ event: 'message.ack', data: { session_key: SESSION, message_id: 'm1' } })
    })

    submitMessage(view, 'alpha')
    submitMessage(view, 'beta')
    await waitFor(() => expect(queuedRows(view).length).toBe(2))

    let sendNowButtons = Array.from(
      view.container.querySelectorAll('[data-testid="queued-send-now"]'),
    )
    expect(sendNowButtons.length).toBe(2)
    await act(async () => {
      fireEvent.click(sendNowButtons[0] as HTMLElement)
    })
    await waitFor(() => {
      const remaining = Array.from(
        view.container.querySelectorAll('[data-testid="queued-message"]'),
      )
      expect(remaining.length).toBe(1)
    })
    sendNowButtons = Array.from(view.container.querySelectorAll('[data-testid="queued-send-now"]'))
    expect(sendNowButtons.length).toBe(1)
    await act(async () => {
      fireEvent.click(sendNowButtons[0] as HTMLElement)
    })

    await waitFor(() => {
      const rows = queuedRows(view)
      expect(rows.length).toBe(1)
      expect(rows[0]).toContain('beta')
    })

    await act(async () => {
      ws.emitJSON({ event: 'cancel.ack', data: { session_key: SESSION, status: 'ok' } })
    })
    await waitFor(() => expect(sentUserMessages(ws)).toContain('alpha'))
    expect(sentUserMessages(ws)).not.toContain('beta')
    await waitFor(() => {
      const rows = queuedRows(view)
      expect(rows.length).toBe(1)
      expect(rows[0]).toContain('beta')
    })
  })
})
