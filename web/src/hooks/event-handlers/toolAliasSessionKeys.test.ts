/**
 * Regression cover: tool cards vanish live and only reappear after a reload.
 *
 * Root cause: the backend resolves the conversation alias BEFORE publishing
 * every outbound event — `dispatchOutboundMessage`
 * (pkg/channels/native.go) does
 * `sessionKey = agentLoop.ResolveSessionKey(msg.ChatID)` for tool.executing /
 * tool.result / subagent.result too, so after `/new` or `/agent` those events
 * carry `base:chat:N` while the UI session key is `base`.
 *
 * The streaming handlers already re-tagged aliased events with the current key
 * (effectiveSessionKey); the tool handlers did not, so the tool message was
 * stored under the aliased key and dropped by the session filter in
 * useChatHistory — the card disappeared until a reload brought it back from
 * REST history. Thinking cards survived because reasoning lives inside the
 * (re-tagged) assistant placeholder.
 */
import { describe, expect, mock, test } from 'bun:test'
import { QueryClient } from '@tanstack/react-query'
import type { ChatMessage } from '../../lib/types'
import { effectiveSessionKey } from './helpers'
import { handleSubagentResult, handleToolExecuting, handleToolResult } from './tools'
import type { MessageEventContext } from './types'

const BASE = 'native:A'
const ALIAS = 'native:A:chat:7'

function msg(
  id: string,
  role: ChatMessage['role'],
  content: string,
  extra: Partial<ChatMessage> = {},
): ChatMessage {
  return {
    id,
    role,
    content,
    streaming: false,
    createdAt: new Date().toISOString(),
    sessionKey: BASE,
    ...extra,
  }
}

function makeCtx(initialStreaming: ChatMessage[], currentSessionKey: string | null = BASE) {
  let streaming = initialStreaming
  const setStreamingMessages: MessageEventContext['setStreamingMessages'] = (updater) => {
    streaming =
      typeof updater === 'function'
        ? (updater as (p: ChatMessage[]) => ChatMessage[])(streaming)
        : updater
  }

  const ctx = {
    currentSessionKeyRef: { current: currentSessionKey },
    parentSessionKeyRef: { current: null },
    queryClient: new QueryClient(),
    debouncedSessionRefresh: mock(() => {}),
    setStreamingMessages,
    setToolStatus: mock(() => {}),
    setPendingAttachments: mock(() => {}),
    setApprovalRequest: () => {},
    showApprovalResult: () => {},
    enqueueChunk: mock(() => {}),
    clearQueue: mock(() => {}),
    clearAllQueues: () => {},
    ensureAssistantPlaceholder: mock(() => {}),
    addProcessingSession: mock(() => {}),
    removeProcessingSession: mock(() => {}),
    syncProcessingSession: mock(() => {}),
    processingSessionKeyRef: { current: BASE },
    upsertGroup: () => {},
    hydrateGroups: () => {},
    markActiveGroupsStopped: () => {},
    setGroupsEnabled: () => {},
    setTypingIndicator: () => {},
  } as unknown as MessageEventContext

  return { ctx, getStreaming: () => streaming }
}

// ── effectiveSessionKey ─────────────────────────────────────────────────────

describe('effectiveSessionKey', () => {
  const ref = (current: string | null) => ({ currentSessionKeyRef: { current } })

  test('aliased event key is re-tagged to the current UI key', () => {
    expect(effectiveSessionKey(ref(BASE), ALIAS)).toBe(BASE)
    expect(effectiveSessionKey(ref(ALIAS), BASE)).toBe(ALIAS)
  })

  test('missing event key falls back to the current key', () => {
    expect(effectiveSessionKey(ref(BASE), undefined)).toBe(BASE)
  })

  test('foreign key is preserved (never collapsed onto the current session)', () => {
    expect(effectiveSessionKey(ref(BASE), 'native:B:chat:1')).toBe('native:B:chat:1')
  })

  test('no current key keeps the event key as-is', () => {
    expect(effectiveSessionKey(ref(null), ALIAS)).toBe(ALIAS)
    expect(effectiveSessionKey(ref(null), undefined)).toBe('')
  })
})

// ── handleToolExecuting ─────────────────────────────────────────────────────

describe('handleToolExecuting with aliased session key', () => {
  test('tool message is tagged with the CURRENT key, not the alias', () => {
    const assistant = msg('a1', 'assistant', 'let me check', { streaming: true })
    const { ctx, getStreaming } = makeCtx([assistant])

    handleToolExecuting(ctx, {
      session_key: ALIAS,
      tool: 'exec',
      tool_call_id: 'call_1',
      arguments: { command: 'ls' },
    })

    const tool = getStreaming().find((m) => m.role === 'tool')
    expect(tool).toBeDefined()
    expect(tool?.sessionKey).toBe(BASE)
    // Same rule as the streaming handlers: it must survive the strict
    // session filter in useChatHistory.
    expect(getStreaming().filter((m) => m.sessionKey === BASE).length).toBe(2)
  })

  test('marks the preceding assistant of the CURRENT session as done', () => {
    // Regression: the comparison used the (aliased) tool key, so the assistant
    // bubble kept a stuck streaming spinner for the whole tool call.
    const assistant = msg('a1', 'assistant', 'let me check', { streaming: true })
    const { ctx, getStreaming } = makeCtx([assistant])

    handleToolExecuting(ctx, { session_key: ALIAS, tool: 'exec', tool_call_id: 'call_1' })

    expect(getStreaming().find((m) => m.id === 'a1')?.streaming).toBe(false)
  })

  test('does not touch assistants of other sessions', () => {
    const other = msg('other-a', 'assistant', 'hi', { streaming: true, sessionKey: 'native:B' })
    const mine = msg('a1', 'assistant', 'let me check', { streaming: true })
    const { ctx, getStreaming } = makeCtx([other, mine])

    handleToolExecuting(ctx, { session_key: ALIAS, tool: 'exec', tool_call_id: 'call_1' })

    expect(getStreaming().find((m) => m.id === 'other-a')?.streaming).toBe(true)
    expect(getStreaming().find((m) => m.id === 'a1')?.streaming).toBe(false)
  })

  test('update-by-tool_call_id still finds the card stored under the current key', () => {
    const { ctx, getStreaming } = makeCtx([])

    handleToolExecuting(ctx, { session_key: ALIAS, tool: 'exec', tool_call_id: 'call_1' })
    handleToolExecuting(ctx, {
      session_key: ALIAS,
      tool: 'exec',
      tool_call_id: 'call_1',
      arguments: { command: 'ls -la' },
    })

    const tools = getStreaming().filter((m) => m.role === 'tool')
    expect(tools.length).toBe(1)
    expect(tools[0].sessionKey).toBe(BASE)
  })

  test('foreign session is still dropped', () => {
    const { ctx, getStreaming } = makeCtx([])
    handleToolExecuting(ctx, { session_key: 'native:B:chat:1', tool: 'exec' })
    expect(getStreaming().filter((m) => m.role === 'tool').length).toBe(0)
  })
})

// ── handleToolResult / handleSubagentResult ─────────────────────────────────

describe('handleToolResult with aliased session key', () => {
  test('resolves the card created under the current key', () => {
    const { ctx, getStreaming } = makeCtx([])
    handleToolExecuting(ctx, { session_key: ALIAS, tool: 'exec', tool_call_id: 'call_1' })

    handleToolResult(ctx, {
      session_key: ALIAS,
      tool: 'exec',
      tool_call_id: 'call_1',
      result: 'file1\nfile2',
    })

    const tool = getStreaming().find((m) => m.role === 'tool')
    expect(tool?.toolStatus).toBe('completed')
    expect(tool?.toolResult).toBe('file1\nfile2')
    expect(tool?.sessionKey).toBe(BASE)
  })

  test('card stays visible through the session filter after the result', () => {
    const { ctx, getStreaming } = makeCtx([])
    handleToolExecuting(ctx, { session_key: ALIAS, tool: 'exec', tool_call_id: 'call_1' })
    handleToolResult(ctx, {
      session_key: ALIAS,
      tool: 'exec',
      tool_call_id: 'call_1',
      result: 'ok',
    })

    const visible = getStreaming().filter((m) => m.sessionKey === BASE)
    expect(visible.map((m) => m.role)).toContain('tool')
  })
})

describe('handleSubagentResult with aliased session key', () => {
  test('links the subagent session onto the current-key card', () => {
    const { ctx, getStreaming } = makeCtx([])
    handleToolExecuting(ctx, { session_key: ALIAS, tool: 'spawn', tool_call_id: 'call_spawn' })

    handleSubagentResult(ctx, {
      session_key: ALIAS,
      tool: 'spawn',
      tool_call_id: 'call_spawn',
      subagent_session_key: 'native:sub:1',
      result: 'done',
    })

    const tool = getStreaming().find((m) => m.role === 'tool')
    expect(tool?.subagentSessionKey).toBe('native:sub:1')
    expect(tool?.sessionKey).toBe(BASE)
  })
})
