import { useCallback, useEffect, useRef, useState } from 'react'
import type { ClientEvent } from '../lib/types'
import { LeleSocket } from '../services/ws/client'

type SocketStatus = 'disconnected' | 'connecting' | 'connected'

type EventHandlers = {
  onEvent?: (event: ClientEvent) => void
  onStatusChange?: (status: SocketStatus) => void
}

export function useSocket(
  apiUrl: string | null,
  token: string | null,
  handlers: EventHandlers = {},
) {
  const [status, setStatus] = useState<SocketStatus>('disconnected')
  const socketRef = useRef<LeleSocket | null>(null)
  const handlersRef = useRef(handlers)

  useEffect(() => {
    handlersRef.current = handlers
  }, [handlers])

  useEffect(() => {
    if (!apiUrl || !token) {
      socketRef.current?.close()
      socketRef.current = null
      setStatus('disconnected')
      return
    }

    const socket = new LeleSocket(apiUrl, token, {
      onConnecting: () => {
        setStatus('connecting')
        handlersRef.current.onStatusChange?.('connecting')
      },
      onOpen: () => {
        setStatus('connected')
        handlersRef.current.onStatusChange?.('connected')
      },
      onClose: () => {
        setStatus('disconnected')
        handlersRef.current.onStatusChange?.('disconnected')
      },
      onEvent: (event) => {
        handlersRef.current.onEvent?.(event)
      },
    })

    socketRef.current = socket
    socket.connect()

    return () => {
      socket.close()
      socketRef.current = null
      setStatus('disconnected')
    }
  }, [apiUrl, token])

  const send = useCallback((event: string, data: Record<string, unknown>) => {
    socketRef.current?.send(event as 'subscribe', data)
  }, [])

  const close = useCallback(() => {
    socketRef.current?.close()
    socketRef.current = null
    setStatus('disconnected')
  }, [])

  return {
    status,
    send,
    close,
    socket: socketRef.current,
  }
}
