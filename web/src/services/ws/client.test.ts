import { afterEach, beforeEach, describe, expect, test } from 'bun:test'
import { LeleSocket } from './client'

type Listener = (ev: Record<string, unknown>) => void

class MockWebSocket {
  static instances: MockWebSocket[] = []
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3

  readyState = MockWebSocket.CONNECTING
  url: string
  protocols: string[]
  sent: string[] = []
  private listeners: Record<string, Listener[]> = {}

  constructor(url: string, protocols?: string | string[]) {
    this.url = url
    this.protocols =
      protocols === undefined ? [] : Array.isArray(protocols) ? [...protocols] : [protocols]
    MockWebSocket.instances.push(this)
  }

  addEventListener(type: string, cb: Listener) {
    const list = this.listeners[type]
    if (list) {
      list.push(cb)
    } else {
      this.listeners[type] = [cb]
    }
  }

  removeEventListener(type: string, cb: Listener) {
    const list = this.listeners[type]
    if (!list) return
    const i = list.indexOf(cb)
    if (i !== -1) list.splice(i, 1)
  }

  dispatch(type: string, ev: Record<string, unknown> = {}) {
    for (const cb of [...(this.listeners[type] ?? [])]) cb(ev)
  }

  send(data: string) {
    this.sent.push(data)
  }

  close() {
    if (this.readyState === MockWebSocket.CLOSED) return
    this.readyState = MockWebSocket.CLOSED
    this.dispatch('close')
  }

  openNow() {
    this.readyState = MockWebSocket.OPEN
    this.dispatch('open')
  }

  serverClose() {
    this.readyState = MockWebSocket.CLOSED
    this.dispatch('close')
  }

  message(payload: unknown) {
    this.dispatch('message', { data: JSON.stringify(payload) })
  }
}

let activeClient: LeleSocket | null = null

function fireVisibilityChange(state: DocumentVisibilityState = 'visible') {
  Object.defineProperty(document, 'visibilityState', {
    value: state,
    configurable: true,
  })
  document.dispatchEvent(new window.Event('visibilitychange'))
}

const opts = { reconnectStrategy: { initialDelay: 40, maxDelay: 80, factor: 2 } }

describe('LeleSocket single-connection guarantee', () => {
  beforeEach(() => {
    MockWebSocket.instances = []
    ;(globalThis as Record<string, unknown>).WebSocket = MockWebSocket
    activeClient = null
  })

  afterEach(() => {
    activeClient?.close()
    activeClient = null
    MockWebSocket.instances = []
  })

  test('visibility-triggered reconnect cancels the pending reconnect timer', async () => {
    const client = new LeleSocket('http://localhost', 'tok', opts)
    activeClient = client
    client.connect()
    expect(MockWebSocket.instances.length).toBe(1)

    const s1 = MockWebSocket.instances[0]
    s1.openNow()
    expect(client.state).toBe('connected')

    s1.serverClose()
    expect(client.state).toBe('connecting')

    fireVisibilityChange('visible')
    expect(MockWebSocket.instances.length).toBe(2)

    await Bun.sleep(200)
    expect(MockWebSocket.instances.length).toBe(2)
  })

  test('stale socket events are not delivered after being replaced', async () => {
    const client = new LeleSocket('http://localhost', 'tok', opts)
    activeClient = client
    const events: string[] = []
    client.on('event', (e) => events.push(e.event))
    client.connect()

    const s1 = MockWebSocket.instances[0]
    s1.openNow()
    s1.message({ event: 'message.stream', data: { delta: 'a' } })
    expect(events).toEqual(['message.stream'])

    s1.serverClose()
    await Bun.sleep(200)
    expect(MockWebSocket.instances.length).toBe(2)
    const s2 = MockWebSocket.instances[1]
    s2.openNow()

    s1.message({ event: 'message.stream', data: { delta: 'a' } })
    expect(events).toEqual(['message.stream'])

    s1.dispatch('close')
    await Bun.sleep(200)
    expect(MockWebSocket.instances.length).toBe(2)
  })

  test('close() cancels a pending reconnect', async () => {
    const client = new LeleSocket('http://localhost', 'tok', opts)
    activeClient = client
    client.connect()

    const s1 = MockWebSocket.instances[0]
    s1.openNow()
    s1.serverClose()
    client.close()

    await Bun.sleep(200)
    expect(MockWebSocket.instances.length).toBe(1)
  })

  test('reconnect after close replaces the dead socket, keeping exactly one live', async () => {
    const client = new LeleSocket('http://localhost', 'tok', opts)
    activeClient = client
    client.connect()

    const s1 = MockWebSocket.instances[0]
    s1.openNow()
    s1.serverClose()

    await Bun.sleep(200)
    expect(MockWebSocket.instances.length).toBe(2)

    const s2 = MockWebSocket.instances[1]
    s2.openNow()
    expect(client.state).toBe('connected')

    const events: string[] = []
    client.on('event', (e) => events.push(e.event))
    s2.message({ event: 'message.thinking', data: { delta: 'Let me check' } })
    expect(events).toEqual(['message.thinking'])
  })
})

describe('LeleSocket credential transport', () => {
  beforeEach(() => {
    MockWebSocket.instances = []
    ;(globalThis as Record<string, unknown>).WebSocket = MockWebSocket
    activeClient = null
  })

  afterEach(() => {
    activeClient?.close()
    activeClient = null
    MockWebSocket.instances = []
  })

  test('the token never appears in the handshake URL', () => {
    const client = new LeleSocket('http://localhost', 'secret-token', opts)
    activeClient = client
    client.connect()

    const socket = MockWebSocket.instances[0]
    expect(socket.url).toBe('ws://localhost/api/v1/ws')
    expect(socket.url).not.toContain('secret-token')
  })

  test('the token travels as a bearer subprotocol so the server can echo it', () => {
    const client = new LeleSocket('http://localhost', 'secret-token', opts)
    activeClient = client
    client.connect()

    expect(MockWebSocket.instances[0].protocols).toEqual(['bearer', 'secret-token'])
  })

  test('only the session key rides in the query string, never the token', async () => {
    const client = new LeleSocket('http://localhost', 'secret-token', opts)
    activeClient = client
    client.connect()

    const first = MockWebSocket.instances[0]
    first.openNow()
    client.send('subscribe', { session_key: 'native:abc' })
    first.serverClose()

    await Bun.sleep(200)
    expect(MockWebSocket.instances.length).toBe(2)

    const second = MockWebSocket.instances[1]
    expect(second.url).toBe('ws://localhost/api/v1/ws?session_key=native%3Aabc')
    expect(second.url).not.toContain('secret-token')
    expect(second.protocols).toEqual(['bearer', 'secret-token'])
  })
})
