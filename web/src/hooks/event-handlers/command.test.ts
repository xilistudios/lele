import { describe, expect, test } from 'bun:test'
import type { ChatMessage, WSCommandAppliedPayload } from '../../lib/types'
import { handleCommandApplied } from './command'
import { dispatchMessageEvent } from './index'
import type { MessageEventContext } from './types'

// ── Minimal context stub ────────────────────────────────────────────────────
// Only the two members the command handler touches are real; the rest of the
// context is an empty object cast, exactly like the handler-facing tests in
// useMessages.test.ts but scoped down (no groups/approvals/queues here).

function createContext(currentSessionKey: string | null) {
  let streaming: ChatMessage[] = []
  const ctx = {
    currentSessionKeyRef: { current: currentSessionKey },
    setStreamingMessages: (updater: unknown) => {
      streaming =
        typeof updater === 'function'
          ? (updater as (prev: typeof streaming) => typeof streaming)(streaming)
          : (updater as typeof streaming)
    },
  } as unknown as MessageEventContext

  return { ctx, get: () => streaming }
}

const reviewPayload: WSCommandAppliedPayload = {
  session_key: 'web:abc',
  command: 'review',
  description: 'Review the current diff',
  args: 'src/main.go',
  agent: 'coder',
  model: 'openai/gpt-5',
  source: 'workspace',
}

describe('buildCommandAppliedArgs (via handleCommandApplied)', () => {
  test('creates a completed tool message named "command"', () => {
    const { ctx, get } = createContext('web:abc')

    handleCommandApplied(ctx, { ...reviewPayload })

    const [msg] = get()
    expect(msg).toBeDefined()
    expect(msg.role).toBe('tool')
    expect(msg.toolName).toBe('command')
    expect(msg.toolStatus).toBe('completed')
    expect(msg.streaming).toBe(false)
    expect(msg.sessionKey).toBe('web:abc')
    expect(msg.id.startsWith('tool-command-')).toBe(true)
  })

  test('serializes the full payload as "command {json}" with the leading slash', () => {
    const { ctx, get } = createContext('web:abc')

    handleCommandApplied(ctx, { ...reviewPayload })

    const [msg] = get()
    expect(msg.toolArgs?.startsWith('command ')).toBe(true)
    expect(JSON.parse(msg.toolArgs?.slice('command '.length) ?? '{}')).toEqual({
      command: '/review',
      args: 'src/main.go',
      agent: 'coder',
      model: 'openai/gpt-5',
      source: 'workspace',
      description: 'Review the current diff',
    })
  })

  test('does not double the slash when the backend already sends one', () => {
    const { ctx, get } = createContext('web:abc')

    handleCommandApplied(ctx, { ...reviewPayload, command: '/review' } as never)

    const [msg] = get()
    expect(JSON.parse((msg.toolArgs ?? '').slice(8)).command).toBe('/review')
  })

  test('missing optional fields degrade to empty strings', () => {
    const { ctx, get } = createContext('web:abc')

    handleCommandApplied(ctx, { session_key: 'web:abc', command: 'hola' } as never)

    const [msg] = get()
    expect(JSON.parse((msg.toolArgs ?? '').slice(8))).toEqual({
      command: '/hola',
      args: '',
      agent: '',
      model: '',
      source: '',
      description: '',
    })
  })
})

describe('handleCommandApplied ordering & filtering', () => {
  test('drops events from another conversation', () => {
    const { ctx, get } = createContext('web:other')

    handleCommandApplied(ctx, { ...reviewPayload })

    expect(get()).toHaveLength(0)
  })

  test('accepts the aliased conversation key (base:chat:N)', () => {
    const { ctx, get } = createContext('web:abc')

    handleCommandApplied(ctx, { ...reviewPayload, session_key: 'web:abc:chat:2' })

    expect(get()).toHaveLength(1)
  })

  test('inserts after the last assistant and its trailing tools', () => {
    const { ctx, get } = createContext('web:abc')
    ctx.setStreamingMessages([
      {
        id: 'u1',
        role: 'user',
        content: 'hi',
        streaming: false,
        createdAt: '',
        sessionKey: 'web:abc',
      },
      {
        id: 'a1',
        role: 'assistant',
        content: 'ok',
        streaming: true,
        createdAt: '',
        sessionKey: 'web:abc',
      },
      {
        id: 't1',
        role: 'tool',
        content: '',
        streaming: false,
        createdAt: '',
        sessionKey: 'web:abc',
        toolName: 'exec',
        toolStatus: 'completed',
      },
    ] as never)

    handleCommandApplied(ctx, { ...reviewPayload })

    const messages = get()
    expect(messages.map((m) => m.id).slice(0, 3)).toEqual(['u1', 'a1', 't1'])
    expect(messages).toHaveLength(4)
    expect(messages[3].toolName).toBe('command')
    // The assistant that produced this turn is no longer streaming.
    expect(messages[1].streaming).toBe(false)
  })

  test('marks the streaming assistant of the same session as finished', () => {
    const { ctx, get } = createContext('web:abc')
    ctx.setStreamingMessages([
      {
        id: 'a1',
        role: 'assistant',
        content: 'ok',
        streaming: true,
        createdAt: '',
        sessionKey: 'web:abc',
      },
      {
        id: 'a2',
        role: 'assistant',
        content: 'other',
        streaming: true,
        createdAt: '',
        sessionKey: 'web:zzz',
      },
    ] as never)

    handleCommandApplied(ctx, { ...reviewPayload })

    const [mine, other] = get()
    expect(mine.streaming).toBe(false)
    expect(other.streaming).toBe(true)
  })

  test('routes through dispatchMessageEvent', () => {
    const { ctx, get } = createContext('web:abc')

    dispatchMessageEvent(ctx, {
      event: 'command.applied',
      data: { ...reviewPayload },
    })

    expect(get()).toHaveLength(1)
    expect(get()[0].toolName).toBe('command')
  })
})
