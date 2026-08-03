import { afterEach, beforeEach, describe, expect, test } from 'bun:test'
import { LeleSocket } from './client'

type Listener = (ev: Record<string, unknown>) => void

/**
 * Minimal WebSocket mock that records every instance created.
 * The duplication bug (streaming chunks appearing twice) happened when two
 * live WebSocket instances coexisted: each delivered every server event, so
 * the UI appended each chunk twice.
 */
class MockWebSocket {
  static instances: MockWebSocket[] = []
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3

  readyState = MockWebSocket.CONNECTING
  url: string
  sent: string[] = []
  private listeners: Record<string, Listener[]> = {}

  constructor(url: string) {
    this.url = url
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

  /** Simulate a successful connection. */
  openNow() {
    this.readyState = MockWebSocket.OPEN
    this.dispatch('open')
  }

  /** Simulate a server-initiated close. */
  serverClose() {
    this.readyState = MockWebSocket.CLOSED
    this.dispatch('close')
  }

  /** Simulate an incoming server event. */
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
    // Guarantee cleanup even when an assertion fails mid-test, so pending
    // timers/handlers from one test never leak into the next.
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

    // Connection drops → reconnect timer scheduled.
    s1.serverClose()
    expect(client.state).toBe('connecting')

    // User returns to the tab before the timer fires.
    fireVisibilityChange('visible')
    expect(MockWebSocket.instances.length).toBe(2)

    // Wait well past the reconnect delay: the old timer must NOT spawn a
    // third socket (that was the duplication bug).
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

    // Drop and let the reconnect timer create a replacement.
    s1.serverClose()
    await Bun.sleep(200)
    expect(MockWebSocket.instances.length).toBe(2)
    const s2 = MockWebSocket.instances[1]
    s2.openNow()

    // The stale socket somehow delivers buffered/duplicated data: ignored.
    s1.message({ event: 'message.stream', data: { delta: 'a' } })
    expect(events).toEqual(['message.stream'])

    // A duplicated close event from the stale socket must not schedule
    // yet another reconnect.
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
    s1.serverClose() // schedules reconnect
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

    // Events flow exactly once per chunk.
    const events: string[] = []
    client.on('event', (e) => events.push(e.event))
    s2.message({ event: 'message.thinking', data: { delta: 'Let me check' } })
    expect(events).toEqual(['message.thinking'])
  })
})
