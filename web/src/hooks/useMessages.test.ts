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

  test('muestra tool calls DESPUÉS del assistant cuando tiene content', () => {
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

    // [user, assistant, tool-call, tool-result] -> tool calls DESPUÉS del assistant
    expect(result.length).toBe(4)
    expect(result[0].role).toBe('user')
    expect(result[1].role).toBe('assistant')
    expect(result[1].content).toBe('Voy a leer el archivo')
    expect(result[2].role).toBe('tool') // tool call
    expect(result[2].toolName).toBe('read_file')
    expect(result[3].role).toBe('tool') // tool result
    expect(result[3].toolResult).toBe('Contenido del archivo')
  })

  test('muestra tool calls ANTES del assistant cuando assistant tiene content vacío', () => {
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

    // [user, tool-call, assistant, tool-result] -> tool call ANTES del assistant vacío
    // Note: el tool result viene del historial como mensaje separado, no se reordena
    expect(result.length).toBe(4)
    expect(result[0].role).toBe('user')
    expect(result[1].role).toBe('tool') // tool call (del assistant) - ANTES del assistant vacío
    expect(result[1].toolName).toBe('read_file')
    expect(result[2].role).toBe('assistant') // assistant vacío - DESPUÉS del tool call
    expect(result[2].content).toBe('')
    expect(result[3].role).toBe('tool') // tool result (mensaje separado del historial)
    expect(result[3].toolResult).toBe('Contenido del archivo')
  })

  test('maneja múltiples tool calls', () => {
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

    // [user, tool-call-1, tool-call-2, assistant, tool-result-1, tool-result-2]
    expect(result.length).toBe(6)
    expect(result[0].role).toBe('user')
    expect(result[1].role).toBe('tool') // tool-call-1
    expect(result[1].toolName).toBe('read_file')
    expect(result[2].role).toBe('tool') // tool-call-2
    expect(result[2].toolName).toBe('read_file')
    expect(result[3].role).toBe('assistant') // assistant vacío después de tool calls
    expect(result[4].role).toBe('tool') // tool-result-1
    expect(result[5].role).toBe('tool') // tool-result-2
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
})
