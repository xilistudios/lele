import { describe, expect, test } from 'bun:test'
import { toChatMessages } from '../lib/chatMessageBuilder'
import type { ChatMessage, GroupInfo, HistoryToolCall, ToolStatus } from '../lib/types'
import { type MessageEventContext, dispatchMessageEvent } from './messageEventHandlers'

// ── Test helpers for messageEventHandlers ────────────────────────────────────

function createMockContext(
  overrides?: Partial<MessageEventContext> & { currentSessionKey?: string },
): { ctx: MessageEventContext; state: MockState } {
  const state: MockState = {
    streamingMessages: [],
    toolStatus: null,
    pendingAttachments: [],
    processingSessions: new Set<string>(),
    processingSessionKey: null as string | null,
    groups: new Map<string, GroupInfo>(),
  }

  const currentSessionKeyRef = {
    current: overrides?.currentSessionKey ?? 'test-session',
  }

  const ctx: MessageEventContext = {
    currentSessionKeyRef,
    parentSessionKeyRef: { current: null },
    queryClient: {
      invalidateQueries: () => {},
      getQueryData: () => undefined,
      setQueryData: () => {},
    } as unknown as MessageEventContext['queryClient'],
    debouncedSessionRefresh: () => {},
    setStreamingMessages: (updater) => {
      if (typeof updater === 'function') {
        state.streamingMessages = updater(state.streamingMessages)
      } else {
        state.streamingMessages = updater
      }
    },
    setToolStatus: (updater) => {
      state.toolStatus = typeof updater === 'function' ? updater(state.toolStatus) : updater
    },
    setPendingAttachments: (updater) => {
      if (typeof updater === 'function') {
        state.pendingAttachments = updater(state.pendingAttachments)
      } else {
        state.pendingAttachments = updater
      }
    },
    setApprovalRequest: () => {},
    showApprovalResult: () => {},
    enqueueChunk: () => {},
    clearQueue: () => {},
    clearAllQueues: () => {},
    ensureAssistantPlaceholder: () => {},
    addProcessingSession: (key: string) => state.processingSessions.add(key),
    removeProcessingSession: (key: string) => state.processingSessions.delete(key),
    syncProcessingSession: () => {},
    processingSessionKeyRef: {
      get current() {
        return state.processingSessionKey
      },
      set current(v: string | null) {
        state.processingSessionKey = v
      },
    },
    upsertGroup: (groupId: string, updater: (existing: GroupInfo | undefined) => GroupInfo) => {
      state.groups = new Map(state.groups)
      state.groups.set(groupId, updater(state.groups.get(groupId)))
    },
    hydrateGroups: (infos: GroupInfo[]) => {
      const next = new Map(state.groups)
      for (const info of infos) {
        next.set(info.groupID, info)
      }
      state.groups = next
    },
    markActiveGroupsStopped: () => {
      let found = false
      for (const g of state.groups.values()) {
        if (g.status === 'started') {
          found = true
          break
        }
      }
      if (!found) return

      const next = new Map(state.groups)
      for (const [id, g] of state.groups) {
        if (g.status === 'started') {
          next.set(id, { ...g, status: 'stopped' })
        }
      }
      state.groups = next
    },
    setGroupsEnabled: () => {},
    ...overrides,
  }

  return { ctx, state }
}

type MockState = {
  streamingMessages: ChatMessage[]
  toolStatus: ToolStatus | null
  pendingAttachments: string[]
  processingSessions: Set<string>
  processingSessionKey: string | null
  groups: Map<string, GroupInfo>
}

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

// ── cancel.ack + group lifecycle tests ───────────────────────────────────────

describe('cancel.ack stops active groups', () => {
  const sessionKey = 'test-session'

  test('group in started transitions to stopped on cancel.ack', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    // Start a group via group.status event
    dispatchMessageEvent(ctx, {
      event: 'group.status',
      data: {
        group_id: 'g1',
        status: 'started',
        participants: 'agent-a,agent-b',
        session_key: sessionKey,
      },
    })

    expect(state.groups.get('g1')?.status).toBe('started')

    // Fire cancel.ack
    dispatchMessageEvent(ctx, {
      event: 'cancel.ack',
      data: { session_key: sessionKey },
    })

    expect(state.groups.get('g1')?.status).toBe('stopped')
  })

  test('group fields (turns, synthesis, strategy, participants, layers, totalTokens) are preserved after cancel', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    // Start a group
    dispatchMessageEvent(ctx, {
      event: 'group.status',
      data: {
        group_id: 'g1',
        status: 'started',
        participants: 'agent-a,agent-b',
        session_key: sessionKey,
      },
    })

    // Simulate a turn
    dispatchMessageEvent(ctx, {
      event: 'group.turn',
      data: {
        group_id: 'g1',
        speaker: 'agent-a',
        label: 'Agent A',
        role: 'proposer',
        layer: 0,
        turn_index: 0,
        content: 'My proposal',
        session_key: sessionKey,
      },
    })

    const beforeCancel = state.groups.get('g1') as GroupInfo
    expect(beforeCancel.status).toBe('started')
    expect(beforeCancel.turns).toHaveLength(1)
    expect(beforeCancel.turns[0].content).toBe('My proposal')

    // Fire cancel.ack
    dispatchMessageEvent(ctx, {
      event: 'cancel.ack',
      data: { session_key: sessionKey },
    })

    const afterCancel = state.groups.get('g1') as GroupInfo
    expect(afterCancel.status).toBe('stopped')
    expect(afterCancel.turns).toHaveLength(1)
    expect(afterCancel.turns[0].content).toBe('My proposal')
    expect(afterCancel.strategy).toBe('')
    expect(afterCancel.participants).toBe('agent-a,agent-b')
    expect(afterCancel.layers).toBe(1)
    expect(afterCancel.totalTokens).toBe(0)
  })

  test('group already in done is NOT modified by cancel.ack', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    // Start a group
    dispatchMessageEvent(ctx, {
      event: 'group.status',
      data: {
        group_id: 'g1',
        status: 'started',
        participants: 'agent-a',
        session_key: sessionKey,
      },
    })

    // Complete the group
    dispatchMessageEvent(ctx, {
      event: 'group.complete',
      data: {
        group_id: 'g1',
        content: 'Final synthesis',
        strategy: 'moa',
        layers: 2,
        total_tokens: 1500,
        session_key: sessionKey,
      },
    })

    expect(state.groups.get('g1')?.status).toBe('done')

    // Fire cancel.ack — should not touch the done group
    dispatchMessageEvent(ctx, {
      event: 'cancel.ack',
      data: { session_key: sessionKey },
    })

    const afterCancel = state.groups.get('g1') as GroupInfo
    expect(afterCancel.status).toBe('done')
    expect(afterCancel.synthesis).toBe('Final synthesis')
    expect(afterCancel.strategy).toBe('moa')
    expect(afterCancel.totalTokens).toBe(1500)
  })

  test('only started groups are stopped; done and error groups are untouched', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    // Create multiple groups in different states
    dispatchMessageEvent(ctx, {
      event: 'group.status',
      data: {
        group_id: 'g-started',
        status: 'started',
        participants: 'a,b',
        session_key: sessionKey,
      },
    })

    dispatchMessageEvent(ctx, {
      event: 'group.status',
      data: {
        group_id: 'g-done',
        status: 'started',
        participants: 'c',
        session_key: sessionKey,
      },
    })

    // Complete one of them
    dispatchMessageEvent(ctx, {
      event: 'group.complete',
      data: {
        group_id: 'g-done',
        content: 'done content',
        session_key: sessionKey,
      },
    })

    dispatchMessageEvent(ctx, {
      event: 'group.status',
      data: {
        group_id: 'g-error',
        status: 'started',
        participants: 'd',
        session_key: sessionKey,
      },
    })

    // Manually set g-error to error via upsert (simulating a server error event)
    ctx.upsertGroup('g-error', (existing) => ({
      groupID: 'g-error',
      status: 'error' as const,
      strategy: existing?.strategy ?? '',
      participants: existing?.participants ?? '',
      layers: existing?.layers ?? 0,
      totalTokens: existing?.totalTokens ?? 0,
      createdAt: existing?.createdAt ?? new Date().toISOString(),
      turns: existing?.turns ?? [],
      synthesis: existing?.synthesis,
    }))

    expect(state.groups.get('g-started')?.status).toBe('started')
    expect(state.groups.get('g-done')?.status).toBe('done')
    expect(state.groups.get('g-error')?.status).toBe('error')

    // Cancel
    dispatchMessageEvent(ctx, {
      event: 'cancel.ack',
      data: { session_key: sessionKey },
    })

    expect(state.groups.get('g-started')?.status).toBe('stopped')
    expect(state.groups.get('g-done')?.status).toBe('done')
    expect(state.groups.get('g-error')?.status).toBe('error')
  })

  test('cancel.ack clears processingSessionKeyRef', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    state.processingSessionKey = sessionKey

    dispatchMessageEvent(ctx, {
      event: 'cancel.ack',
      data: { session_key: sessionKey },
    })

    expect(state.processingSessionKey).toBeNull()
  })
})

// ── handleGroupTool tests ────────────────────────────────────────────────────

describe('handleGroupTool', () => {
  const sessionKey = 'test-session'

  test('creates placeholder turn and upserts tool call on executing status', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    dispatchMessageEvent(ctx, {
      event: 'group.tool',
      data: {
        group_id: 'g1',
        speaker: 'agent-a',
        label: 'Agent A',
        layer: 0,
        turn_index: 0,
        tool_call_id: 'tc-1',
        tool: 'read_file',
        status: 'executing',
        arguments: '{"path":"/test.txt"}',
        session_key: sessionKey,
      },
    })

    const group = state.groups.get('g1') as GroupInfo
    expect(group).toBeDefined()
    expect(group.status).toBe('started')
    expect(group.turns).toHaveLength(1)
    expect(group.turns[0].content).toBe('')
    expect(group.turns[0].speaker).toBe('agent-a')
    expect(group.turns[0].label).toBe('Agent A')
    expect(group.turns[0].toolCalls).toHaveLength(1)
    expect(group.turns[0].toolCalls?.[0]).toEqual({
      tool_call_id: 'tc-1',
      tool: 'read_file',
      status: 'executing',
      arguments: '{"path":"/test.txt"}',
      result: undefined,
    })
  })

  test('upserts tool call status to completed and preserves arguments', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    // First: executing
    dispatchMessageEvent(ctx, {
      event: 'group.tool',
      data: {
        group_id: 'g1',
        speaker: 'agent-a',
        layer: 0,
        turn_index: 0,
        tool_call_id: 'tc-1',
        tool: 'read_file',
        status: 'executing',
        arguments: '{"path":"/test.txt"}',
        session_key: sessionKey,
      },
    })

    // Then: completed (no arguments field)
    dispatchMessageEvent(ctx, {
      event: 'group.tool',
      data: {
        group_id: 'g1',
        speaker: 'agent-a',
        layer: 0,
        turn_index: 0,
        tool_call_id: 'tc-1',
        tool: 'read_file',
        status: 'completed',
        result: 'file contents here',
        session_key: sessionKey,
      },
    })

    const group = state.groups.get('g1') as GroupInfo
    expect(group.turns[0].toolCalls).toHaveLength(1)
    expect(group.turns[0].toolCalls?.[0].status).toBe('completed')
    // Arguments preserved from executing event
    expect(group.turns[0].toolCalls?.[0].arguments).toBe('{"path":"/test.txt"}')
    expect(group.turns[0].toolCalls?.[0].result).toBe('file contents here')
  })

  test('upserts tool call status to error', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    dispatchMessageEvent(ctx, {
      event: 'group.tool',
      data: {
        group_id: 'g1',
        speaker: 'agent-a',
        layer: 0,
        turn_index: 0,
        tool_call_id: 'tc-1',
        tool: 'exec',
        status: 'executing',
        arguments: '{"command":"rm -rf /"}',
        session_key: sessionKey,
      },
    })

    dispatchMessageEvent(ctx, {
      event: 'group.tool',
      data: {
        group_id: 'g1',
        speaker: 'agent-a',
        layer: 0,
        turn_index: 0,
        tool_call_id: 'tc-1',
        tool: 'exec',
        status: 'error',
        result: 'permission denied',
        session_key: sessionKey,
      },
    })

    const group = state.groups.get('g1') as GroupInfo
    expect(group.turns[0].toolCalls?.[0].status).toBe('error')
    expect(group.turns[0].toolCalls?.[0].result).toBe('permission denied')
  })

  test('uses label fallback to speaker when label is missing', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    dispatchMessageEvent(ctx, {
      event: 'group.tool',
      data: {
        group_id: 'g1',
        speaker: 'agent-b',
        layer: 0,
        turn_index: 0,
        tool_call_id: 'tc-1',
        tool: 'web_search',
        status: 'executing',
        session_key: sessionKey,
      },
    })

    const group = state.groups.get('g1') as GroupInfo
    expect(group.turns[0].label).toBe('agent-b')
  })

  test('multiple tool calls on same turn', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    dispatchMessageEvent(ctx, {
      event: 'group.tool',
      data: {
        group_id: 'g1',
        speaker: 'agent-a',
        layer: 0,
        turn_index: 0,
        tool_call_id: 'tc-1',
        tool: 'read_file',
        status: 'executing',
        session_key: sessionKey,
      },
    })

    dispatchMessageEvent(ctx, {
      event: 'group.tool',
      data: {
        group_id: 'g1',
        speaker: 'agent-a',
        layer: 0,
        turn_index: 0,
        tool_call_id: 'tc-2',
        tool: 'web_search',
        status: 'executing',
        session_key: sessionKey,
      },
    })

    const group = state.groups.get('g1') as GroupInfo
    expect(group.turns[0].toolCalls).toHaveLength(2)
    expect(group.turns[0].toolCalls?.[0].tool_call_id).toBe('tc-1')
    expect(group.turns[0].toolCalls?.[1].tool_call_id).toBe('tc-2')
  })
})

// ── handleGroupTurn preserving toolCalls ─────────────────────────────────────

describe('handleGroupTurn preserves existing toolCalls', () => {
  const sessionKey = 'test-session'

  test('incoming turn merge preserves existing toolCalls', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    // Create a group with a tool call
    dispatchMessageEvent(ctx, {
      event: 'group.tool',
      data: {
        group_id: 'g1',
        speaker: 'agent-a',
        layer: 0,
        turn_index: 0,
        tool_call_id: 'tc-1',
        tool: 'read_file',
        status: 'completed',
        result: 'content',
        session_key: sessionKey,
      },
    })

    expect(state.groups.get('g1')?.turns[0].toolCalls).toHaveLength(1)

    // Now send a group.turn for the same turn_index — should preserve toolCalls
    dispatchMessageEvent(ctx, {
      event: 'group.turn',
      data: {
        group_id: 'g1',
        speaker: 'agent-a',
        label: 'Agent A',
        role: 'proposer',
        layer: 0,
        turn_index: 0,
        content: 'Here is my proposal after reading the file',
        session_key: sessionKey,
      },
    })

    const group = state.groups.get('g1') as GroupInfo
    expect(group.turns).toHaveLength(1)
    expect(group.turns[0].content).toBe('Here is my proposal after reading the file')
    // Tool calls preserved!
    expect(group.turns[0].toolCalls).toHaveLength(1)
    expect(group.turns[0].toolCalls?.[0].tool_call_id).toBe('tc-1')
  })

  test('new turn without existing toolCalls has undefined toolCalls', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    dispatchMessageEvent(ctx, {
      event: 'group.turn',
      data: {
        group_id: 'g1',
        speaker: 'agent-a',
        label: 'Agent A',
        role: 'proposer',
        layer: 0,
        turn_index: 0,
        content: 'My proposal',
        session_key: sessionKey,
      },
    })

    const group = state.groups.get('g1') as GroupInfo
    expect(group.turns[0].toolCalls).toBeUndefined()
  })
})

// ── welcome-driven hydrateGroups ─────────────────────────────────────────────

describe('welcome-driven hydrateGroups', () => {
  const sessionKey = 'test-session'

  test('welcome event with groups array hydrates groups', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    dispatchMessageEvent(ctx, {
      event: 'welcome',
      data: {
        session_key: sessionKey,
        groups: [
          {
            group_id: 'g1',
            status: 'done',
            strategy: 'moa',
            participants: 'agent-a,agent-b',
            layers: 2,
            total_tokens: 1500,
            synthesis: 'Final answer',
            turns: [
              {
                turn_index: 0,
                speaker: 'agent-a',
                label: 'Agent A',
                role: 'proposer',
                layer: 0,
                content: 'Proposal A',
              },
              {
                turn_index: 1,
                speaker: 'agent-b',
                label: 'Agent B',
                role: 'proposer',
                layer: 0,
                content: 'Proposal B',
                tool_calls: [
                  {
                    tool_call_id: 'tc-1',
                    tool: 'web_search',
                    status: 'completed',
                    result: 'search results',
                  },
                ],
              },
            ],
          },
        ],
      },
    })

    const group = state.groups.get('g1') as GroupInfo
    expect(group).toBeDefined()
    expect(group.status).toBe('done')
    expect(group.strategy).toBe('moa')
    expect(group.participants).toBe('agent-a,agent-b')
    expect(group.layers).toBe(2)
    expect(group.totalTokens).toBe(1500)
    expect(group.synthesis).toBe('Final answer')
    expect(group.turns).toHaveLength(2)
    expect(group.turns[0].content).toBe('Proposal A')
    expect(group.turns[0].groupID).toBe('g1')
    expect(group.turns[1].toolCalls).toHaveLength(1)
    expect(group.turns[1].toolCalls?.[0].tool_call_id).toBe('tc-1')
  })

  test('reconnected event with groups also hydrates', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    dispatchMessageEvent(ctx, {
      event: 'reconnected',
      data: {
        session_key: sessionKey,
        groups: [
          {
            group_id: 'g-reconnect',
            status: 'started',
            strategy: 'round_robin',
            participants: 'agent-x',
            layers: 1,
            total_tokens: 100,
            synthesis: '',
            turns: [],
          },
        ],
      },
    })

    const group = state.groups.get('g-reconnect') as GroupInfo
    expect(group).toBeDefined()
    expect(group.status).toBe('started')
    expect(group.strategy).toBe('round_robin')
  })

  test('welcome without groups does not affect existing groups', () => {
    const { ctx, state } = createMockContext({ currentSessionKey: sessionKey })

    // First add a group manually
    dispatchMessageEvent(ctx, {
      event: 'group.status',
      data: {
        group_id: 'existing',
        status: 'started',
        participants: 'a',
        session_key: sessionKey,
      },
    })

    // Welcome without groups
    dispatchMessageEvent(ctx, {
      event: 'welcome',
      data: { session_key: sessionKey },
    })

    // Existing group untouched
    expect(state.groups.get('existing')?.status).toBe('started')
  })
})
