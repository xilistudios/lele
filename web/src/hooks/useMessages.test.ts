import { describe, expect, test } from 'bun:test'
import { toChatMessages } from '../lib/chatMessageBuilder'
import type { HistoryToolCall } from '../lib/types'

describe('toChatMessages', () => {
  const sessionKey = 'test-session'

  test('convierte mensajes simples', () => {
    const history = [
      { id: '0', role: 'user' as const, content: 'Hola' },
      { id: '1', role: 'assistant' as const, content: 'Hola usuario' },
    ]

    const result = toChatMessages(history, sessionKey)

    expect(result.length).toBe(2)
    expect(result[0].role).toBe('user')
    expect(result[0].content).toBe('Hola')
    expect(result[1].role).toBe('assistant')
    expect(result[1].content).toBe('Hola usuario')
  })

  test('mapea assistant con tool_calls y tool result sin duplicar tool messages', () => {
    const history = [
      { id: '0', role: 'user' as const, content: 'Lee archivo' },
      {
        id: '1',
        role: 'assistant' as const,
        content: 'Voy a leer el archivo',
        tool_calls: [
          { id: 'call-1', name: 'read_file', arguments: { path: '/test.txt' } },
        ] as HistoryToolCall[],
      },
      { id: '2', role: 'tool' as const, content: 'Contenido del archivo', tool_call_id: 'call-1' },
    ]

    const result = toChatMessages(history, sessionKey)

    // 3 mensajes: user + assistant + tool result (no se genera duplicado del tool_call)
    expect(result.length).toBe(3)
    expect(result[0].role).toBe('user')
    expect(result[1].role).toBe('assistant')
    expect(result[1].content).toBe('Voy a leer el archivo')
    // Tool result message (with tool info from tool_call)
    expect(result[2].role).toBe('tool')
    expect(result[2].toolName).toBe('read_file')
    expect(result[2].toolArgs).toBe('read_file {"path":"/test.txt"}')
    expect(result[2].toolResult).toBe('Contenido del archivo')
    expect(result[2].toolStatus).toBe('completed')
  })

  test('no genera tool message duplicado cuando existe tool result en historial', () => {
    const history = [
      { id: '0', role: 'user' as const, content: 'Lee archivo' },
      {
        id: '1',
        role: 'assistant' as const,
        content: '', // vacío
        tool_calls: [
          { id: 'call-1', name: 'read_file', arguments: { path: '/test.txt' } },
        ] as HistoryToolCall[],
      },
      { id: '2', role: 'tool' as const, content: 'Contenido del archivo', tool_call_id: 'call-1' },
    ]

    const result = toChatMessages(history, sessionKey)

    // Solo 2 mensajes: user + tool result (no se genera duplicado del tool_call)
    expect(result.length).toBe(2)
    expect(result[0].role).toBe('user')
    expect(result[1].role).toBe('tool')
    expect(result[1].toolName).toBe('read_file')
    expect(result[1].toolArgs).toBe('read_file {"path":"/test.txt"}')
    expect(result[1].toolResult).toBe('Contenido del archivo')
    expect(result[1].toolStatus).toBe('completed')
  })

  test('maneja múltiples tool_calls sin duplicar cuando existen tool results', () => {
    const history = [
      { id: '0', role: 'user' as const, content: 'Lee dos archivos' },
      {
        id: '1',
        role: 'assistant' as const,
        content: '', // vacío
        tool_calls: [
          { id: 'call-1', name: 'read_file', arguments: { path: '/a.txt' } },
          { id: 'call-2', name: 'read_file', arguments: { path: '/b.txt' } },
        ] as HistoryToolCall[],
      },
      { id: '2', role: 'tool' as const, content: 'Contenido A', tool_call_id: 'call-1' },
      { id: '3', role: 'tool' as const, content: 'Contenido B', tool_call_id: 'call-2' },
    ]

    const result = toChatMessages(history, sessionKey)

    // Solo 3 mensajes: user + 2 tool results (no duplicados)
    expect(result.length).toBe(3)
    expect(result[0].role).toBe('user')
    expect(result[1].role).toBe('tool')
    expect(result[1].toolName).toBe('read_file')
    expect(result[1].toolArgs).toBe('read_file {"path":"/a.txt"}')
    expect(result[1].toolResult).toBe('Contenido A')
    expect(result[2].role).toBe('tool')
    expect(result[2].toolName).toBe('read_file')
    expect(result[2].toolArgs).toBe('read_file {"path":"/b.txt"}')
    expect(result[2].toolResult).toBe('Contenido B')
  })

  test('assistant con content vacío pero sin tool_calls se muestra normal', () => {
    const history = [
      { id: '0', role: 'user' as const, content: 'Hola' },
      { id: '1', role: 'assistant' as const, content: '' }, // vacío sin tool_calls
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
        id: '0',
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
        id: '0',
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

  test('no genera tool messages sintéticos para tool_calls sin resultado (vienen por WS)', () => {
    const history = [
      { id: '0', role: 'user' as const, content: 'Ejecuta algo' },
      {
        id: '1',
        role: 'assistant' as const,
        content: 'Voy a ejecutar',
        tool_calls: [
          { id: 'call-1', name: 'exec', arguments: { command: 'ls' } },
        ] as HistoryToolCall[],
      },
    ]

    const result = toChatMessages(history, sessionKey)

    expect(result.length).toBe(2)
    expect(result[0].role).toBe('user')
    expect(result[1].role).toBe('assistant')
    expect(result[1].content).toBe('Voy a ejecutar')
  })
})
