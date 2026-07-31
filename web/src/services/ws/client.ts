import { wsDebug } from '../../lib/debug'
import type { ClientEvent } from './events'
import { type ClientCommand, parseEvent, serializeCommand } from './events'
import { type ReconnectStrategy, defaultReconnectStrategy } from './reconnect'

type ConnectionState = 'disconnected' | 'connecting' | 'connected'

type SocketEventMap = {
  connecting: () => void
  open: () => void
  close: () => void
  event: (event: ClientEvent) => void
  error: (error: Event) => void
  stateChange: (state: ConnectionState) => void
}

type SocketEventKey = keyof SocketEventMap

export type LeleSocketOptions = {
  reconnectStrategy?: Partial<ReconnectStrategy>
}

export class LeleSocket {
  private socket: WebSocket | null = null
  private shouldReconnect = true
  private reconnectDelay: number
  private reconnectStrategy: ReconnectStrategy
  private readonly openQueue: ClientCommand[] = []
  private reconnectAttempts = 0
  private pendingSessionKey: string | null = null
  private confirmedSessionKey: string | null = null
  private _state: ConnectionState = 'disconnected'
  private readonly listeners: { [K in SocketEventKey]?: Array<SocketEventMap[K]> } = {}

  constructor(
    private readonly baseUrl: string,
    private readonly token: string,
    opts: LeleSocketOptions = {},
  ) {
    this.reconnectStrategy = defaultReconnectStrategy(opts.reconnectStrategy)
    this.reconnectDelay = this.reconnectStrategy.initialDelay
  }

  get state(): ConnectionState {
    return this._state
  }

  on<K extends SocketEventKey>(event: K, handler: SocketEventMap[K]): this {
    const list = this.listeners[event] as Array<SocketEventMap[K]>
    if (list) {
      list.push(handler)
    } else {
      ;(this.listeners as Record<string, unknown[]>)[event] = [handler]
    }
    return this
  }

  off<K extends SocketEventKey>(event: K, handler: SocketEventMap[K]): this {
    const list = this.listeners[event] as Array<SocketEventMap[K]> | undefined
    if (!list) return this
    const index = list.indexOf(handler)
    if (index !== -1) list.splice(index, 1)
    return this
  }

  private emit<K extends SocketEventKey>(event: K, ...args: Parameters<SocketEventMap[K]>) {
    const list = this.listeners[event] as Array<(...a: unknown[]) => void> | undefined
    if (list) {
      for (const handler of list) {
        handler(...args)
      }
    }
  }

  private setState(next: ConnectionState) {
    this._state = next
    this.emit('stateChange', next)
  }

  connect() {
    this.shouldReconnect = true
    this.reconnectAttempts = 0
    this.setState('connecting')
    this.emit('connecting')
    this.open()
  }

  close() {
    this.shouldReconnect = false
    this.socket?.close()
    this.socket = null
    this.pendingSessionKey = null
    this.confirmedSessionKey = null
    this.setState('disconnected')
  }

  clearSubscription() {
    this.pendingSessionKey = null
    this.confirmedSessionKey = null
  }

  send<E extends ClientCommand['event']>(
    event: E,
    data: Extract<ClientCommand, { event: E }>['data'],
  ) {
    if (event === 'subscribe' && data && typeof data === 'object' && 'session_key' in data) {
      const sessionKey = (data as { session_key?: unknown }).session_key
      this.pendingSessionKey = typeof sessionKey === 'string' && sessionKey ? sessionKey : null
      wsDebug('[WS] Sending subscribe', {
        sessionKey: this.pendingSessionKey,
        data,
      })
    }
    if (event === 'unsubscribe') {
      const sessionKey = (data as { session_key?: unknown })?.session_key
      if (typeof sessionKey === 'string') {
        if (this.pendingSessionKey === sessionKey) {
          this.pendingSessionKey = null
        }
        if (this.confirmedSessionKey === sessionKey) {
          this.confirmedSessionKey = null
        }
      } else {
        this.pendingSessionKey = null
        this.confirmedSessionKey = null
      }
    }

    if (event === 'subscribe' || event === 'unsubscribe') {
      for (let index = this.openQueue.length - 1; index >= 0; index -= 1) {
        const queuedEvent = this.openQueue[index]?.event
        if (queuedEvent === 'subscribe' || queuedEvent === 'unsubscribe') {
          this.openQueue.splice(index, 1)
        }
      }
    }

    const command = { event, data } as ClientCommand

    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) {
      this.openQueue.push(command)
      return
    }

    this.socket.send(serializeCommand(command))
  }

  handleEvent(event: ClientEvent) {
    if (event.event === 'subscribe.ack') {
      const data = event.data as { session_key?: string; processing?: boolean }
      wsDebug('[WS] subscribe.ack received', {
        sessionKey: data.session_key,
        processing: data.processing,
        pendingSessionKey: this.pendingSessionKey,
        confirmedSessionKey: this.confirmedSessionKey,
      })
      if (data.session_key && data.session_key === this.pendingSessionKey) {
        this.confirmedSessionKey = this.pendingSessionKey
      }
    }
  }

  private open() {
    this.setState('connecting')
    this.emit('connecting')
    this.reconnectAttempts++
    const params = new URLSearchParams({ token: this.token })
    const sessionForKey = this.confirmedSessionKey ?? this.pendingSessionKey
    if (sessionForKey) {
      params.set('session_key', sessionForKey)
    }

    const url = `${this.baseUrl.replace(/^http/, 'ws').replace(/\/$/, '')}/api/v1/ws?${params.toString()}`
    const socket = new WebSocket(url)
    this.socket = socket

    socket.addEventListener('open', () => {
      this.reconnectDelay = this.reconnectStrategy.initialDelay
      this.reconnectAttempts = 0
      this.setState('connected')
      this.emit('open')
      while (this.openQueue.length > 0) {
        const message = this.openQueue.shift()
        if (message) {
          socket.send(serializeCommand(message))
        }
      }
    })

    socket.addEventListener('message', (event) => {
      try {
        const parsed = parseEvent(event.data as string)
        this.handleEvent(parsed)
        this.emit('event', parsed)
      } catch {
        this.emit('event', {
          event: 'error',
          data: { code: 'unknown_event', message: 'Invalid event payload' },
        })
      }
    })

    socket.addEventListener('error', (event) => {
      this.emit('error', event)
    })

    socket.addEventListener('close', () => {
      this.emit('close')
      if (!this.shouldReconnect) {
        this.setState('disconnected')
        return
      }

      if (this.reconnectAttempts >= this.reconnectStrategy.maxRetries) {
        this.setState('disconnected')
        this.emit('error', new Event('max_reconnect_attempts'))
        return
      }

      this.setState('connecting')
      window.setTimeout(() => this.open(), this.reconnectDelay)
      this.reconnectDelay = this.reconnectStrategy.nextDelay(this.reconnectDelay)
    })
  }
}
