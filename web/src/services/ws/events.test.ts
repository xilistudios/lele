import { describe, expect, test } from 'bun:test'
import type { ClientEvent } from '../../lib/types'
import { parseEvent, serializeCommand } from './events'

describe('serializeCommand', () => {
  test('serializa comando subscribe', () => {
    const json = serializeCommand({
      event: 'subscribe',
      data: { session_key: 'session:1', agent_id: 'main' },
    })
    const parsed = JSON.parse(json)
    expect(parsed.event).toBe('subscribe')
    expect(parsed.data.session_key).toBe('session:1')
  })

  test('serializa comando ping', () => {
    const json = serializeCommand({ event: 'ping', data: {} })
    const parsed = JSON.parse(json)
    expect(parsed.event).toBe('ping')
  })

  test('serializa comando approve', () => {
    const json = serializeCommand({
      event: 'approve',
      data: { request_id: 'req-1', approved: true },
    })
    const parsed = JSON.parse(json)
    expect(parsed.event).toBe('approve')
    expect(parsed.data.approved).toBe(true)
  })
})

describe('parseEvent', () => {
  test('parsea evento welcome', () => {
    const raw = JSON.stringify({
      event: 'welcome',
      data: {
        client_id: 'client-1',
        device_name: 'Desktop',
        session_key: 'native:client-1:1',
        status: 'ok',
        agents: [{ id: 'main', name: 'Main', workspace: '~/.lele', model: 'gpt-4' }],
        server_time: '2026-01-01T00:00:00Z',
      },
    })

    const event = parseEvent(raw) as ClientEvent
    expect(event.event).toBe('welcome')
    if (event.event === 'welcome') {
      expect(event.data.client_id).toBe('client-1')
      expect(event.data.agents).toHaveLength(1)
    }
  })

  test('parsea evento message.complete', () => {
    const raw = JSON.stringify({
      event: 'message.complete',
      data: {
        message_id: 'msg-1',
        session_key: 'session:1',
        content: 'Respuesta completa',
      },
    })

    const event = parseEvent(raw)
    expect(event.event).toBe('message.complete')
  })

  test('parsea evento tool.executing', () => {
    const raw = JSON.stringify({
      event: 'tool.executing',
      data: {
        session_key: 'session:1',
        tool: 'read_file',
        action: 'Reading /test.txt',
      },
    })

    const event = parseEvent(raw) as ClientEvent
    expect(event.event).toBe('tool.executing')
  })

  test('parsea evento error', () => {
    const raw = JSON.stringify({
      event: 'error',
      data: { code: 'auth_error', message: 'Invalid token' },
    })

    const event = parseEvent(raw) as ClientEvent
    expect(event.event).toBe('error')
    if (event.event === 'error') {
      expect(event.data.code).toBe('auth_error')
    }
  })

  test('parsea evento pong', () => {
    const raw = JSON.stringify({ event: 'pong', data: { time: '2026-01-01T00:00:00Z' } })
    const event = parseEvent(raw)
    expect(event.event).toBe('pong')
  })
})
