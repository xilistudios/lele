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
  pingIntervalMs?: number
}

export class LeleSocket {
  private socket: WebSocket | null = null
  private shouldReconnect = true
  private reconnectDelay: number
  private reconnectStrategy: ReconnectStrategy
  private readonly openQueue: ClientCommand[] = []
  private reconnectAttempts = 0
  private pingIntervalId: number | null = null
  private subscribedSessionKey: string | null = null
  private subscribedAgentId: string | null = null
  private _state: ConnectionState = 'disconnected'
  private readonly listeners: { [K in SocketEventKey]?: Array<SocketEventMap[K]> } = {}
  private readonly pingIntervalMs: number

  constructor(
    private readonly baseUrl: string,
    private readonly token: string,
    opts: LeleSocketOptions = {},
  ) {
    this.reconnectStrategy = defaultReconnectStrategy(opts.reconnectStrategy)
    this.reconnectDelay = this.reconnectStrategy.initialDelay
    this.pingIntervalMs = opts.pingIntervalMs ?? 25000
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
    if (this.pingIntervalId !== null) {
      window.clearInterval(this.pingIntervalId)
      this.pingIntervalId = null
    }
    this.socket?.close()
    this.socket = null
    this.setState('disconnected')
  }

  send<E extends ClientCommand['event']>(
    event: E,
    data: Extract<ClientCommand, { event: E }>['data'],
  ) {
    if (event === 'subscribe' && data && typeof data === 'object' && 'session_key' in data) {
      const sessionKey = (data as { session_key?: unknown }).session_key
      this.subscribedSessionKey = typeof sessionKey === 'string' && sessionKey ? sessionKey : null
      const agentId = (data as { agent_id?: unknown }).agent_id
      this.subscribedAgentId = typeof agentId === 'string' && agentId ? agentId : null
    }
    if (event === 'unsubscribe') {
      this.subscribedSessionKey = null
      this.subscribedAgentId = null
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

  private open() {
    this.setState('connecting')
    this.emit('connecting')
    this.reconnectAttempts++
    const params = new URLSearchParams({ token: this.token })
    if (this.subscribedSessionKey) {
      params.set('session_key', this.subscribedSessionKey)
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
      if (this.subscribedSessionKey) {
        const subscribeData: { session_key: string; agent_id?: string } = {
          session_key: this.subscribedSessionKey,
        }
        if (this.subscribedAgentId) {
          subscribeData.agent_id = this.subscribedAgentId
        }
        socket.send(serializeCommand({ event: 'subscribe', data: subscribeData }))
      }
    })

    socket.addEventListener('message', (event) => {
      try {
        this.emit('event', parseEvent(event.data as string))
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

    socket.addEventListener('open', () => {
      if (this.pingIntervalId !== null) {
        window.clearInterval(this.pingIntervalId)
      }

      this.pingIntervalId = window.setInterval(() => {
        if (this.socket?.readyState === WebSocket.OPEN) {
          this.send('ping', {})
        }
      }, this.pingIntervalMs)
    })

    socket.addEventListener('close', () => {
      if (this.pingIntervalId !== null) {
        window.clearInterval(this.pingIntervalId)
        this.pingIntervalId = null
      }
      this.setState('disconnected')
      this.emit('close')
      if (!this.shouldReconnect) {
        return
      }

      if (this.reconnectAttempts >= this.reconnectStrategy.maxRetries) {
        this.emit('error', new Event('max_reconnect_attempts'))
        return
      }

      window.setTimeout(() => this.open(), this.reconnectDelay)
      this.reconnectDelay = this.reconnectStrategy.nextDelay(this.reconnectDelay)
    })
  }
}
