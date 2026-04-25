import { describe, expect, test } from 'bun:test'
import {
  buildToolCallMap,
  createAssistantMessage,
  createHistoryMessageId,
  createOptimisticUserId,
  createToolMessage,
  createToolMessageId,
  createUserMessage,
  formatToolCallArgs,
  parseAttachmentsFromContent,
  parseSubagentSessionKey,
} from './chatMessageBuilder'
import type { HistoryToolCall } from './types'

describe('createHistoryMessageId', () => {
  test('genera ID determinístico para mensajes de historial', () => {
    const id = createHistoryMessageId('session:1', 0, 'user')
    expect(id).toBe('session:1:0:user')
  })

  test('genera IDs diferentes para distintos índices', () => {
    const a = createHistoryMessageId('session:1', 0, 'user')
    const b = createHistoryMessageId('session:1', 1, 'user')
    expect(a).not.toBe(b)
  })

  test('genera IDs diferentes para distintos roles', () => {
    const a = createHistoryMessageId('session:1', 0, 'user')
    const b = createHistoryMessageId('session:1', 0, 'assistant')
    expect(a).not.toBe(b)
  })
})

describe('createOptimisticUserId', () => {
  test('genera un ID único basado en timestamp', () => {
    const id = createOptimisticUserId()
    expect(id.startsWith('temp-user-')).toBe(true)
    const timestamp = Number(id.replace('temp-user-', ''))
    expect(timestamp).toBeGreaterThan(0)
    expect(Number.isNaN(timestamp)).toBe(false)
  })

  test('empieza con el prefijo temp-user-', () => {
    const id = createOptimisticUserId()
    expect(id.startsWith('temp-user-')).toBe(true)
  })
})

describe('createToolMessageId', () => {
  test('genera ID con el nombre de la herramienta', () => {
    const id = createToolMessageId('read_file')
    expect(id.startsWith('tool-read_file-')).toBe(true)
  })

  test('acepta un sufijo opcional', () => {
    const id = createToolMessageId('exec', '123')
    expect(id).toBe('tool-exec-123')
  })
})

describe('parseAttachmentsFromContent', () => {
  test('retorna contenido original si no hay header de attachments', () => {
    const result = parseAttachmentsFromContent('Hola mundo')
    expect(result.content).toBe('Hola mundo')
    expect(result.attachments).toHaveLength(0)
  })

  test('extrae attachments de contenido con header', () => {
    const content = `Mira esta imagen
## Attachments
- /tmp/foto.png
- /tmp/documento.pdf`

    const result = parseAttachmentsFromContent(content)
    expect(result.content).toBe('Mira esta imagen')
    expect(result.attachments).toHaveLength(2)
    expect(result.attachments[0].path).toBe('/tmp/foto.png')
    expect(result.attachments[0].name).toBe('foto.png')
    expect(result.attachments[1].path).toBe('/tmp/documento.pdf')
    expect(result.attachments[1].name).toBe('documento.pdf')
  })

  test('retorna attachments vacío si header existe pero sin rutas válidas', () => {
    const content = `Texto
## Attachments
-   
- `

    const result = parseAttachmentsFromContent(content)
    expect(result.content).toBe('Texto')
    expect(result.attachments).toHaveLength(0)
  })

  test('ignora guiones que no sean de lista', () => {
    const content = `Texto
## Attachments
- /tmp/file.jpg
Esto no es un attachment`

    const result = parseAttachmentsFromContent(content)
    expect(result.content).toBe('Texto')
    expect(result.attachments).toHaveLength(1)
    expect(result.attachments[0].path).toBe('/tmp/file.jpg')
  })

  test('maneja contenido sin saltos de línea', () => {
    const result = parseAttachmentsFromContent('')
    expect(result.content).toBe('')
    expect(result.attachments).toHaveLength(0)
  })

  test('trimea el contenido limpio', () => {
    const content = `Hola  

## Attachments
- /tmp/a.txt`

    const result = parseAttachmentsFromContent(content)
    expect(result.content).toBe('Hola')
    expect(result.attachments).toHaveLength(1)
  })
})

describe('parseSubagentSessionKey', () => {
  test('extrae subagent key con formato subagent:XXX', () => {
    const result = parseSubagentSessionKey('Spawned subagent task subagent:task-1 (test)')
    expect(result).toBe('subagent:task-1')
  })

  test('extrae subagent key con formato task XXX', () => {
    const result = parseSubagentSessionKey(
      "Spawned subagent task task-1 ('test-coder') for task: Do something",
    )
    expect(result).toBe('subagent:task-1')
  })

  test('extrae subagent key con formato task id: XXX', () => {
    const result = parseSubagentSessionKey('task id: abc123-xyz')
    expect(result).toBe('subagent:abc123-xyz')
  })

  test('retorna undefined para strings vacíos', () => {
    expect(parseSubagentSessionKey('')).toBeUndefined()
    expect(parseSubagentSessionKey(undefined)).toBeUndefined()
    expect(parseSubagentSessionKey('   ')).toBeUndefined()
  })

  test('retorna undefined cuando no hay coincidencia', () => {
    expect(parseSubagentSessionKey('Resultado normal sin subagentes')).toBeUndefined()
  })
})

describe('formatToolCallArgs', () => {
  test('formatea con nombre y argumentos', () => {
    const tc: HistoryToolCall = {
      id: 'call-1',
      name: 'read_file',
      arguments: { path: '/test.txt' },
    }
    expect(formatToolCallArgs(tc)).toBe('read_file {"path":"/test.txt"}')
  })

  test('usa solo el nombre si no hay argumentos', () => {
    const tc: HistoryToolCall = { id: 'call-1', name: 'read_file' }
    expect(formatToolCallArgs(tc)).toBe('read_file')
  })

  test('usa solo argumentos si no hay nombre', () => {
    const tc: HistoryToolCall = { id: 'call-1', arguments: { path: '/test.txt' } }
    expect(formatToolCallArgs(tc)).toBe('{"path":"/test.txt"}')
  })
})

describe('buildToolCallMap', () => {
  test('construye mapa de tool_call_id → HistoryToolCall', () => {
    const history = [
      {
        role: 'assistant' as const,
        content: '',
        tool_calls: [
          { id: 'call-1', name: 'read_file', arguments: { path: '/a.txt' } },
          { id: 'call-2', name: 'exec', arguments: { command: 'ls' } },
        ],
      },
    ]

    const map = buildToolCallMap(history)
    expect(map.size).toBe(2)
    expect(map.get('call-1')?.name).toBe('read_file')
    expect(map.get('call-2')?.name).toBe('exec')
  })

  test('retorna mapa vacío si no hay tool_calls', () => {
    const map = buildToolCallMap([
      { role: 'user', content: 'Hola' } as unknown as { tool_calls?: HistoryToolCall[] },
    ])
    expect(map.size).toBe(0)
  })

  test('retorna mapa vacío si el array está vacío', () => {
    const map = buildToolCallMap([])
    expect(map.size).toBe(0)
  })

  test('ignora tool_calls sin id', () => {
    const history: Array<{ tool_calls?: HistoryToolCall[] }> = [
      {
        tool_calls: [{ id: '', name: 'read_file', arguments: { path: '/a.txt' } }],
      },
    ]

    const map = buildToolCallMap(history)
    expect(map.size).toBe(0)
  })
})

describe('createUserMessage', () => {
  test('crea un mensaje de usuario con valores por defecto', () => {
    const msg = createUserMessage({
      id: '1',
      sessionKey: 'session:1',
      content: 'Hola',
    })

    expect(msg.role).toBe('user')
    expect(msg.content).toBe('Hola')
    expect(msg.sessionKey).toBe('session:1')
    expect(msg.streaming).toBe(false)
    expect(msg.optimistic).toBeUndefined()
    expect(msg.createdAt).toBeTruthy()
  })

  test('crea un mensaje optimista', () => {
    const msg = createUserMessage({
      id: 'temp-1',
      sessionKey: 'session:1',
      content: 'Hola',
      optimistic: true,
      optimisticBaseCount: 5,
    })

    expect(msg.optimistic).toBe(true)
    expect(msg.optimisticBaseCount).toBe(5)
  })

  test('incluye attachments', () => {
    const msg = createUserMessage({
      id: '1',
      sessionKey: 'session:1',
      content: 'Hola',
      attachments: [{ path: '/foto.png', name: 'foto.png' }],
    })

    expect(msg.attachments).toHaveLength(1)
    expect(msg.attachments?.[0].path).toBe('/foto.png')
  })
})

describe('createAssistantMessage', () => {
  test('crea un mensaje assistant no streaming por defecto', () => {
    const msg = createAssistantMessage({
      id: '1',
      sessionKey: 'session:1',
      content: 'Respuesta',
    })

    expect(msg.role).toBe('assistant')
    expect(msg.content).toBe('Respuesta')
    expect(msg.streaming).toBe(false)
    expect(msg.reasoningContent).toBeUndefined()
  })

  test('crea un mensaje assistant en streaming', () => {
    const msg = createAssistantMessage({
      id: '1',
      sessionKey: 'session:1',
      content: 'Resp',
      streaming: true,
    })

    expect(msg.streaming).toBe(true)
  })

  test('incluye reasoningContent', () => {
    const msg = createAssistantMessage({
      id: '1',
      sessionKey: 'session:1',
      content: 'Resp',
      reasoningContent: 'Pensando...',
    })

    expect(msg.reasoningContent).toBe('Pensando...')
  })

  test('incluye attachments', () => {
    const msg = createAssistantMessage({
      id: '1',
      sessionKey: 'session:1',
      content: 'Aquí tienes la imagen',
      attachments: [{ path: '/img.jpg', name: 'img.jpg' }],
    })

    expect(msg.attachments).toHaveLength(1)
  })
})

describe('createToolMessage', () => {
  test('crea un mensaje tool con toolName y toolResult', () => {
    const msg = createToolMessage({
      id: 'tool-1',
      sessionKey: 'session:1',
      toolName: 'read_file',
      toolArgs: 'read_file {"path":"/test.txt"}',
      toolResult: 'contenido',
      toolStatus: 'completed',
    })

    expect(msg.role).toBe('tool')
    expect(msg.content).toBe('')
    expect(msg.toolName).toBe('read_file')
    expect(msg.toolArgs).toBe('read_file {"path":"/test.txt"}')
    expect(msg.toolResult).toBe('contenido')
    expect(msg.toolStatus).toBe('completed')
  })

  test('crea un mensaje tool en ejecución', () => {
    const msg = createToolMessage({
      id: 'tool-1',
      sessionKey: 'session:1',
      toolName: 'exec',
      toolArgs: 'exec ls -la',
      toolStatus: 'executing',
    })

    expect(msg.toolStatus).toBe('executing')
    expect(msg.toolResult).toBeUndefined()
  })

  test('crea un mensaje tool con error', () => {
    const msg = createToolMessage({
      id: 'tool-1',
      sessionKey: 'session:1',
      toolName: 'exec',
      toolArgs: 'exec rm -rf /',
      toolResult: 'Error: permission denied',
      toolStatus: 'error',
    })

    expect(msg.toolStatus).toBe('error')
  })

  test('incluye subagentSessionKey para spawn', () => {
    const msg = createToolMessage({
      id: 'tool-1',
      sessionKey: 'session:1',
      toolName: 'spawn',
      toolStatus: 'completed',
      subagentSessionKey: 'subagent:task-1',
    })

    expect(msg.subagentSessionKey).toBe('subagent:task-1')
  })
})
