import { describe, expect, test } from 'bun:test'
import type { HistoryToolCall } from '../lib/types'
import { toChatMessages } from './useMessages'

describe('toChatMessages', () => {
  const sessionKey = 'test-session'

  test('convierte mensajes simples', () => {
    const history = [
      { role: 'user' as const, content: 'Hola' },
      { role: 'assistant' as const, content: 'Hola usuario' },
    ]

    const result = toChatMessages(history, sessionKey)

    expect(result.length).toBe(2)
    expect(result[0].role).toBe('user')
    expect(result[0].content).toBe('Hola')
    expect(result[1].role).toBe('assistant')
    expect(result[1].content).toBe('Hola usuario')
  })

  test('mapea assistant con tool_calls y tool result sin mensajes sintéticos extra', () => {
    const history = [
      { role: 'user' as const, content: 'Lee archivo' },
      {
        role: 'assistant' as const,
        content: 'Voy a leer el archivo',
        tool_calls: [
          { id: 'call-1', name: 'read_file', arguments: { path: '/test.txt' } },
        ] as HistoryToolCall[],
      },
      { role: 'tool' as const, content: 'Contenido del archivo', tool_call_id: 'call-1' },
    ]

    const result = toChatMessages(history, sessionKey)

    expect(result.length).toBe(3)
    expect(result[0].role).toBe('user')
    expect(result[1].role).toBe('assistant')
    expect(result[1].content).toBe('Voy a leer el archivo')
    expect(result[2].role).toBe('tool')
    expect(result[2].toolName).toBe('read_file')
    expect(result[2].toolResult).toBe('Contenido del archivo')
  })

  test('omite assistant vacío con tool_calls y conserva tool result', () => {
    const history = [
      { role: 'user' as const, content: 'Lee archivo' },
      {
        role: 'assistant' as const,
        content: '', // vacío
        tool_calls: [
          { id: 'call-1', name: 'read_file', arguments: { path: '/test.txt' } },
        ] as HistoryToolCall[],
      },
      { role: 'tool' as const, content: 'Contenido del archivo', tool_call_id: 'call-1' },
    ]

    const result = toChatMessages(history, sessionKey)

    expect(result.length).toBe(2)
    expect(result[0].role).toBe('user')
    expect(result[1].role).toBe('tool')
    expect(result[1].toolName).toBe('read_file')
    expect(result[1].toolResult).toBe('Contenido del archivo')
  })

  test('maneja múltiples tool results asociados', () => {
    const history = [
      { role: 'user' as const, content: 'Lee dos archivos' },
      {
        role: 'assistant' as const,
        content: '', // vacío
        tool_calls: [
          { id: 'call-1', name: 'read_file', arguments: { path: '/a.txt' } },
          { id: 'call-2', name: 'read_file', arguments: { path: '/b.txt' } },
        ] as HistoryToolCall[],
      },
      { role: 'tool' as const, content: 'Contenido A', tool_call_id: 'call-1' },
      { role: 'tool' as const, content: 'Contenido B', tool_call_id: 'call-2' },
    ]

    const result = toChatMessages(history, sessionKey)

    expect(result.length).toBe(3)
    expect(result[0].role).toBe('user')
    expect(result[1].role).toBe('tool')
    expect(result[1].toolName).toBe('read_file')
    expect(result[2].role).toBe('tool')
    expect(result[2].toolName).toBe('read_file')
  })

  test('assistant con content vacío pero sin tool_calls se muestra normal', () => {
    const history = [
      { role: 'user' as const, content: 'Hola' },
      { role: 'assistant' as const, content: '' }, // vacío sin tool_calls
    ]

    const result = toChatMessages(history, sessionKey)

    expect(result.length).toBe(2)
    expect(result[0].role).toBe('user')
    expect(result[1].role).toBe('assistant')
    expect(result[1].content).toBe('')
  })

  test('reconstruye subagentSessionKey desde resultado histórico de spawn', () => {
    const history = [
      {
        role: 'tool' as const,
        content: "Spawned subagent task task-1 ('Verify task') for task: Investigate issue",
        tool_call_id: 'spawn',
      },
    ]

    const result = toChatMessages(history, sessionKey)

    expect(result).toHaveLength(1)
    expect(result[0].role).toBe('tool')
    expect(result[0].toolName).toBe('spawn')
    expect(result[0].subagentSessionKey).toBe('subagent:task-1')
  })

  test('usa tool_call_id cuando no encuentra tool call asociada', () => {
    const history = [
      {
        role: 'tool' as const,
        content: 'Resultado huérfano',
        tool_call_id: 'spawn',
      },
    ]

    const result = toChatMessages(history, sessionKey)

    expect(result).toHaveLength(1)
    expect(result[0].toolName).toBe('spawn')
    expect(result[0].toolResult).toBe('Resultado huérfano')
  })
})
