import type { ClientCommand, ClientEvent } from './events'
import { initialReconnectDelay, nextReconnectDelay } from './reconnect'

type SocketHandlers = {
  onConnecting?: () => void
  onOpen?: () => void
  onClose?: () => void
  onEvent?: (event: ClientEvent) => void
  onError?: (error: Event) => void
}

export class LeleSocket {
  private socket: WebSocket | null = null
  private shouldReconnect = true
  private reconnectDelay = initialReconnectDelay
  private readonly openQueue: ClientCommand[] = []
  private pingIntervalId: number | null = null
  private subscribedSessionKey: string | null = null

  constructor(
    private readonly baseUrl: string,
    private readonly token: string,
    private readonly handlers: SocketHandlers,
  ) {}

  connect() {
    this.shouldReconnect = true
    this.handlers.onConnecting?.()
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
  }

  send(event: ClientCommand['event'], data: ClientCommand['data']) {
    if (event === 'subscribe' && data && typeof data === 'object' && 'session_key' in data) {
      const sessionKey = (data as { session_key?: unknown }).session_key
      this.subscribedSessionKey = typeof sessionKey === 'string' && sessionKey ? sessionKey : null
    }
    if (event === 'unsubscribe') {
      this.subscribedSessionKey = null
    }

    if (event === 'subscribe' || event === 'unsubscribe') {
      for (let index = this.openQueue.length - 1; index >= 0; index -= 1) {
        const queuedEvent = this.openQueue[index]?.event
        if (queuedEvent === 'subscribe' || queuedEvent === 'unsubscribe') {
          this.openQueue.splice(index, 1)
        }
      }
    }

    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) {
      this.openQueue.push({ event, data })
      return
    }

    this.socket.send(JSON.stringify({ event, data }))
  }

  private open() {
    const params = new URLSearchParams({ token: this.token })
    if (this.subscribedSessionKey) {
      params.set('session_key', this.subscribedSessionKey)
    }

    const url = `${this.baseUrl.replace(/^http/, 'ws').replace(/\/$/, '')}/api/v1/ws?${params.toString()}`
    const socket = new WebSocket(url)
    this.socket = socket

    socket.addEventListener('open', () => {
      this.reconnectDelay = initialReconnectDelay
      this.handlers.onOpen?.()
      while (this.openQueue.length > 0) {
        const message = this.openQueue.shift()
        if (message) {
          socket.send(JSON.stringify(message))
        }
      }
    })

    socket.addEventListener('message', (event) => {
      try {
        this.handlers.onEvent?.(JSON.parse(event.data as string) as ClientEvent)
      } catch {
        this.handlers.onEvent?.({
          event: 'error',
          data: { code: 'unknown_event', message: 'Invalid event payload' },
        })
      }
    })

    socket.addEventListener('error', (event) => {
      this.handlers.onError?.(event)
    })

    socket.addEventListener('open', () => {
      if (this.pingIntervalId !== null) {
        window.clearInterval(this.pingIntervalId)
      }

      this.pingIntervalId = window.setInterval(() => {
        if (this.socket?.readyState === WebSocket.OPEN) {
          this.send('ping', {})
        }
      }, 25000)
    })

    socket.addEventListener('close', () => {
      if (this.pingIntervalId !== null) {
        window.clearInterval(this.pingIntervalId)
        this.pingIntervalId = null
      }
      this.handlers.onClose?.()
      if (!this.shouldReconnect) {
        return
      }

      window.setTimeout(() => this.open(), this.reconnectDelay)
      this.reconnectDelay = nextReconnectDelay(this.reconnectDelay)
    })
  }
}
