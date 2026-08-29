import { describe, expect, test } from 'bun:test'
import { QueryClient } from '@tanstack/react-query'
import type { ChatMessage } from '../lib/types'
import { handleHistoryUpdated } from './event-handlers/streaming'
import type { MessageEventContext } from './event-handlers/types'
import { buildChatHistoryQueryKey } from './useChatHistory'
import { toChatMessages } from '../lib/chatMessageBuilder'

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

describe('handleHistoryUpdated stableId registry (durable flicker fix)', () => {
  test('records stableId for confirmed assistant before stripping it', async () => {
    const { registerStableId, lookupStableId, clearStableIdRegistry } = await import(
      './stableIdRegistry'
    )
    clearStableIdRegistry()

    const assistant = msg('ack-uuid-1', 'assistant', 'Confirmed answer content', {
      streaming: false,
    })
    // base already contains the confirmed copy (refetch landed)
    const confirmed = msg('hash-id-1', 'assistant', 'Confirmed answer content')
    const { ctx, getStreaming } = makeCtx([assistant], [confirmed])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    // Streaming copy stripped (confirmed in cache)...
    expect(getStreaming().some((m) => m.id === 'ack-uuid-1')).toBe(false)
    // ...but its ephemeral id was recorded for future history builds.
    expect(lookupStableId('assistant', 'Confirmed answer content')).toBe('ack-uuid-1')
    void registerStableId
  })

  test('records stableId for confirmed optimistic user before stripping it', async () => {
    const { lookupStableId, clearStableIdRegistry } = await import('./stableIdRegistry')
    clearStableIdRegistry()

    const optimisticUser = msg('temp-user-9', 'user', 'my question', {
      optimistic: true,
      optimisticBaseCount: 0,
    })
    const confirmed = msg('hash-u-9', 'user', 'my question')
    const { ctx } = makeCtx([optimisticUser], [confirmed])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(lookupStableId('user', 'my question')).toBe('temp-user-9')
  })

  test('toChatMessages re-attaches registered stableId on every history build (survives refetches)', async () => {
    const { registerStableId, clearStableIdRegistry } = await import('./stableIdRegistry')
    clearStableIdRegistry()

    registerStableId('assistant', 'Answer that was streamed', 'ack-uuid-42')

    // Simulate a fresh refetch building messages from raw history — the
    // registry must re-apply the ephemeral id as stableId so React keys
    // don't change even though the whole cache was rebuilt.
    const built = toChatMessages(
      [{ id: 'hash-a-42', role: 'assistant', content: 'Answer that was streamed' }],
      's1',
    )
    expect(built[0].id).toBe('hash-a-42')
    expect(built[0].stableId).toBe('ack-uuid-42')

    // A second "refetch" (new build) yields the same stableId → same key.
    const rebuilt = toChatMessages(
      [{ id: 'hash-a-42', role: 'assistant', content: 'Answer that was streamed' }],
      's1',
    )
    expect(rebuilt[0].stableId).toBe('ack-uuid-42')
  })

  test('toChatMessages does not attach stableId when nothing was registered', () => {
    const built = toChatMessages(
      [{ id: 'hash-a-77', role: 'assistant', content: 'Never streamed content' }],
      's1',
    )
    expect(built[0].stableId).toBeUndefined()
  })
})
