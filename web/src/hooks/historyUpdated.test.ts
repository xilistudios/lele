import { describe, expect, test } from 'bun:test'
import { QueryClient } from '@tanstack/react-query'
import type { ChatMessage } from '../lib/types'
import { handleHistoryUpdated } from './event-handlers/streaming'
import type { MessageEventContext } from './event-handlers/types'
import { buildChatHistoryQueryKey } from './useChatHistory'

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

function makeCtx(initialStreaming: ChatMessage[], baseMessages: ChatMessage[]) {
  const queryClient = new QueryClient()
  queryClient.setQueryData(buildChatHistoryQueryKey('s1'), {
    sessionKey: 's1',
    messages: baseMessages,
    rawMessages: [],
    processing: false,
  })

  let streaming = initialStreaming
  const setStreamingMessages: MessageEventContext['setStreamingMessages'] = (updater) => {
    streaming =
      typeof updater === 'function'
        ? (updater as (p: ChatMessage[]) => ChatMessage[])(streaming)
        : updater
  }

  const ctx = {
    currentSessionKeyRef: { current: 's1' },
    parentSessionKeyRef: { current: null },
    queryClient,
    debouncedSessionRefresh: () => {},
    setStreamingMessages,
    setToolStatus: () => {},
    setPendingAttachments: () => {},
    setApprovalRequest: () => {},
    showApprovalResult: () => {},
    enqueueChunk: () => {},
    clearQueue: () => {},
    clearAllQueues: () => {},
    ensureAssistantPlaceholder: () => {},
    addProcessingSession: () => {},
    removeProcessingSession: () => {},
    syncProcessingSession: () => {},
    processingSessionKeyRef: { current: null },
    upsertGroup: () => {},
    hydrateGroups: () => {},
    markActiveGroupsStopped: () => {},
    setGroupsEnabled: () => {},
    setTypingIndicator: () => {},
  } as unknown as MessageEventContext

  return { ctx, getStreaming: () => streaming }
}

describe('handleHistoryUpdated optimistic-user guard', () => {
  test('keeps optimistic user when base refetch has NOT landed yet', () => {
    const optimisticUser = msg('temp-user-1', 'user', 'hello', {
      optimistic: true,
      optimisticBaseCount: 0,
    })
    const assistant = msg('ack-uuid', 'assistant', 'Hi there!', { streaming: true })
    // base is empty → refetch still in flight
    const { ctx, getStreaming } = makeCtx([optimisticUser, assistant], [])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    const roles = getStreaming().map((m) => `${m.role}:${m.id}`)
    console.log('STREAMING (base empty):', roles.join('  '))
    // Optimistic user must survive so the assistant doesn't render alone
    expect(getStreaming().some((m) => m.role === 'user')).toBe(true)
    expect(getStreaming().some((m) => m.role === 'assistant')).toBe(true)
  })

  test('removes optimistic user once base contains the confirmed copy', () => {
    const optimisticUser = msg('temp-user-1', 'user', 'hello', {
      optimistic: true,
      optimisticBaseCount: 0,
    })
    const assistant = msg('ack-uuid', 'assistant', 'Hi there!', { streaming: true })
    // base already has the real user (refetch landed)
    const realUser = msg('real-u', 'user', 'hello')
    const { ctx, getStreaming } = makeCtx([optimisticUser, assistant], [realUser])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    const roles = getStreaming().map((m) => `${m.role}:${m.id}`)
    console.log('STREAMING (base has user):', roles.join('  '))
    // Optimistic user is now redundant → removed (mergeMessages would dedup anyway)
    expect(getStreaming().some((m) => m.optimistic)).toBe(false)
  })
})
