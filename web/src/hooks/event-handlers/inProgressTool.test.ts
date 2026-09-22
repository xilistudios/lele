import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../../lib/types'
import { handleSubscribeAck } from './approvals'
import { handleWelcome } from './streaming'
import { handleToolExecuting, restoreInProgressTool } from './tools'
import type { MessageEventContext } from './types'

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
    sessionKey: 's1',
    ...extra,
  }
}

/**
 * Minimal fake MessageEventContext. setStreamingMessages applies updater
 * functions against a local array so tests can inspect the resulting list;
 * setToolStatus and the subagent notification are recorded.
 */
function makeCtx(initialStreaming: ChatMessage[] = []) {
  let streaming = initialStreaming
  const toolStatuses: Array<Record<string, unknown> | null> = []

  const setStreamingMessages: MessageEventContext['setStreamingMessages'] = (updater) => {
    streaming =
      typeof updater === 'function'
        ? (updater as (p: ChatMessage[]) => ChatMessage[])(streaming)
        : updater
  }

  const noop = () => {}

  const ctx = {
    currentSessionKeyRef: { current: 's1' },
    parentSessionKeyRef: { current: null },
    queryClient: {},
    debouncedSessionRefresh: noop,
    setStreamingMessages,
    setToolStatus: (status: unknown) => {
      toolStatuses.push(status as Record<string, unknown> | null)
    },
    setPendingAttachments: noop,
    setApprovalRequest: noop,
    showApprovalResult: noop,
    enqueueChunk: noop,
    clearQueue: noop,
    clearAllQueues: noop,
    ensureAssistantPlaceholder: noop,
    addProcessingSession: noop,
    removeProcessingSession: noop,
    syncProcessingSession: noop,
    processingSessionKeyRef: { current: null },
    upsertGroup: noop,
    hydrateGroups: noop,
    markActiveGroupsStopped: noop,
    setGroupsEnabled: noop,
    setTypingIndicator: noop,
  } as unknown as MessageEventContext

  return {
    ctx,
    getStreaming: () => streaming,
    toolStatuses,
    getToolCards: () => streaming.filter((m) => m.role === 'tool'),
  }
}

describe('restoreInProgressTool', () => {
  test('restores the running tool card for the session', () => {
    const { ctx, getToolCards, toolStatuses } = makeCtx([msg('u1', 'user', 'run it')])

    restoreInProgressTool(ctx, 's1', {
      tool: 'exec',
      action: 'exec: sleep 600',
      arguments: { command: 'sleep 600' },
      tool_call_id: 'call_1',
    })

    const cards = getToolCards()
    expect(cards).toHaveLength(1)
    expect(cards[0].toolName).toBe('exec')
    expect(cards[0].toolArgs).toBe('exec {"command":"sleep 600"}')
    expect(cards[0].toolStatus).toBe('executing')
    expect(cards[0].toolCallId).toBe('call_1')
    expect(cards[0].sessionKey).toBe('s1')
    // The status bar indicator is restored too (same as the live event).
    expect(toolStatuses).toHaveLength(1)
    expect(toolStatuses[0]?.tool).toBe('exec')
  })

  test('falls back to the pre-formatted action when there are no arguments', () => {
    const { ctx, getToolCards } = makeCtx()

    restoreInProgressTool(ctx, 's1', { tool: 'sleep', action: 'sleep: 600ms' })

    expect(getToolCards()[0].toolArgs).toBe('sleep: 600ms')
  })

  test('does nothing without a payload or a session key', () => {
    const { ctx, getToolCards, toolStatuses } = makeCtx()

    restoreInProgressTool(ctx, 's1', undefined)
    restoreInProgressTool(ctx, 's1', null)
    restoreInProgressTool(ctx, '', { tool: 'exec' })
    restoreInProgressTool(ctx, 's1', {})

    expect(getToolCards()).toHaveLength(0)
    expect(toolStatuses).toHaveLength(0)
  })

  test('is idempotent: re-applying the same call updates, never duplicates', () => {
    const { ctx, getToolCards } = makeCtx()
    const payload = { tool: 'exec', action: 'exec: sleep 600', tool_call_id: 'call_1' }

    restoreInProgressTool(ctx, 's1', payload)
    restoreInProgressTool(ctx, 's1', payload)

    expect(getToolCards()).toHaveLength(1)
  })

  test('carries the spawn subagent session key into the card', () => {
    const { ctx, getToolCards } = makeCtx()

    restoreInProgressTool(ctx, 's1', {
      tool: 'spawn',
      action: 'spawn: research',
      tool_call_id: 'call_2',
      subagent_session_key: 'native:c1:subagent-1',
    })

    expect(getToolCards()[0].subagentSessionKey).toBe('native:c1:subagent-1')
  })
})

describe('handleSubscribeAck in-progress tool', () => {
  test('processing:true restores the running tool card', () => {
    const { ctx, getToolCards } = makeCtx([msg('u1', 'user', 'go')])

    handleSubscribeAck(ctx, {
      session_key: 's1',
      processing: true,
      in_progress_tool: {
        tool: 'wait_for_subagent',
        action: 'wait_for_subagent: subagent-1',
        tool_call_id: 'call_3',
      },
    })

    const cards = getToolCards()
    expect(cards).toHaveLength(1)
    expect(cards[0].toolName).toBe('wait_for_subagent')
    expect(cards[0].toolStatus).toBe('executing')
  })

  test('processing:false does NOT restore a stale tool card', () => {
    const { ctx, getToolCards } = makeCtx([msg('u1', 'user', 'go')])

    handleSubscribeAck(ctx, {
      session_key: 's1',
      processing: false,
      in_progress_tool: { tool: 'exec', action: 'exec: sleep 600', tool_call_id: 'call_4' },
    })

    expect(getToolCards()).toHaveLength(0)
  })

  test('processing:true without an in-progress tool stays untouched', () => {
    const existing = msg('t1', 'tool', '', {
      toolName: 'exec',
      toolStatus: 'executing',
      toolCallId: 'call_5',
    })
    const { ctx, getToolCards } = makeCtx([existing])

    handleSubscribeAck(ctx, { session_key: 's1', processing: true })

    expect(getToolCards()).toHaveLength(1)
    expect(getToolCards()[0].id).toBe('t1')
  })
})

describe('handleWelcome in-progress tool', () => {
  test('restores the running tool card on (re)connect', () => {
    const { ctx, getToolCards } = makeCtx()

    handleWelcome(ctx, {
      session_key: 's1',
      processing: true,
      in_progress_tool: {
        tool: 'exec',
        action: 'exec: sleep 600',
        arguments: { command: 'sleep 600' },
        tool_call_id: 'call_6',
      },
    })

    const cards = getToolCards()
    expect(cards).toHaveLength(1)
    expect(cards[0].toolName).toBe('exec')
    expect(cards[0].sessionKey).toBe('s1')
  })

  test('absent payload leaves the chat untouched', () => {
    const { ctx, getToolCards } = makeCtx()

    handleWelcome(ctx, { session_key: 's1', processing: false })

    expect(getToolCards()).toHaveLength(0)
  })

  test('processing:false does not restore a payload that would never complete', () => {
    const { ctx, getToolCards } = makeCtx()

    handleWelcome(ctx, {
      session_key: 's1',
      processing: false,
      in_progress_tool: { tool: 'exec', action: 'exec: sleep 600', tool_call_id: 'call_8' },
    })

    expect(getToolCards()).toHaveLength(0)
  })
})

describe('live and restore paths agree', () => {
  test('a live tool.executing card is updated (not duplicated) by the restore payload', () => {
    const { ctx, getToolCards } = makeCtx([msg('u1', 'user', 'go')])

    handleToolExecuting(ctx, {
      session_key: 's1',
      tool: 'exec',
      action: 'exec: sleep 600',
      tool_call_id: 'call_7',
    })
    restoreInProgressTool(ctx, 's1', {
      tool: 'exec',
      action: 'exec: sleep 600',
      tool_call_id: 'call_7',
    })

    const cards = getToolCards()
    expect(cards).toHaveLength(1)
    expect(cards[0].toolCallId).toBe('call_7')
    expect(cards[0].toolStatus).toBe('executing')
  })
})
