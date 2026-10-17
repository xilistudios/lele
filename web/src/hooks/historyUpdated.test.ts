import { describe, expect, test } from 'bun:test'
import { QueryClient } from '@tanstack/react-query'
import { toChatMessages } from '../lib/chatMessageBuilder'
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

/**
 * A thinking step, or the tool-call iteration of any model that emits no text,
 * is persisted as its OWN assistant message with `content === ''` and its
 * payload in `reasoning_content` (pkg/agent/llm_runner.go `iterationMsgID`).
 * The confirmation match used a content-prefix test, and an empty prefix can
 * never match, so such a copy was NEVER stripped from streaming state: one
 * immortal leftover per iteration. Once the 50-message sliding history window
 * slid past its twin, `mergeMessages` could no longer pair it and appended it
 * at the END of the list — the "messages lose their order until I refresh"
 * report (see chatPipelineOrdering.test.ts for the end-to-end pin).
 */
describe('handleHistoryUpdated — reasoning-only assistant iterations', () => {
  test('strips the live iteration once the cache holds its persisted twin', () => {
    const live = msg('ack-uuid-thinking', 'assistant', '', {
      reasoningContent: 'thinking about the tool call',
    })
    const persisted = msg('hash-thinking', 'assistant', '', {
      reasoningContent: 'thinking about the tool call',
    })
    const { ctx, getStreaming } = makeCtx([live], [persisted])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.id === 'ack-uuid-thinking')).toBe(false)
  })

  test('registers the iteration under its reasoning text so the key survives', async () => {
    const { lookupStableId, clearStableIdRegistry } = await import('./stableIdRegistry')
    clearStableIdRegistry()

    const live = msg('ack-uuid-thinking', 'assistant', '', {
      reasoningContent: 'thinking about the tool call',
    })
    const persisted = msg('hash-thinking', 'assistant', '', {
      reasoningContent: 'thinking about the tool call',
    })
    const { ctx } = makeCtx([live], [persisted])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(lookupStableId('assistant', 'thinking about the tool call')).toBe('ack-uuid-thinking')
  })

  test('toChatMessages re-attaches it from reasoning_content (content is empty)', async () => {
    const { registerStableId, clearStableIdRegistry } = await import('./stableIdRegistry')
    clearStableIdRegistry()
    registerStableId('assistant', 'thinking about the tool call', 'ack-uuid-thinking')

    const built = toChatMessages(
      [
        {
          id: 'hash-thinking',
          role: 'assistant',
          content: '',
          reasoning_content: 'thinking about the tool call',
        },
      ],
      's1',
    )

    expect(built[0].content).toBe('')
    expect(built[0].stableId).toBe('ack-uuid-thinking')
  })

  test('keeps the live iteration while the cache has no twin yet (no over-strip)', () => {
    const live = msg('ack-uuid-thinking', 'assistant', '', {
      reasoningContent: 'thinking about the tool call',
    })
    // A DIFFERENT turn's thinking step: same role, different reasoning text.
    const other = msg('hash-other', 'assistant', '', {
      reasoningContent: 'thinking about something else',
    })
    const { ctx, getStreaming } = makeCtx([live], [other])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.id === 'ack-uuid-thinking')).toBe(true)
  })

  test('drops a COMPLETED iteration with neither text nor reasoning (renders nothing)', () => {
    // The common shape of a tool-call iteration on a model without thinking:
    // `content === ''`, no reasoning payload. It can never be "confirmed" (its
    // identity is empty) and mergeMessages drops it from the output, so keeping
    // it in state leaked one entry per tool iteration for the whole session.
    const done = msg('ack-uuid-empty-done', 'assistant', '', { streaming: false })
    const { ctx, getStreaming } = makeCtx([done], [])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.id === 'ack-uuid-empty-done')).toBe(false)
  })

  test('still keeps the IN-FLIGHT empty placeholder (the visible streaming bubble)', () => {
    const inFlight = msg('ack-uuid-inflight', 'assistant', '', { streaming: true })
    const { ctx, getStreaming } = makeCtx([inFlight], [])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.id === 'ack-uuid-inflight')).toBe(true)
  })
})

/**
 * Attachments-only sends (text `''` + attachments) are the OTHER shape whose
 * confirmation identity cannot be its content: `confirmationIdentity` returns
 * '' and an empty prefix matches no cached copy, so the bubble was immortal here
 * too — this is the `message.complete` / `history.updated` half of the fix in
 * `messageMerge.collectConfirmCandidates` (see
 * hooks/attachmentsOnlySend.test.ts for the merge half). The image therefore
 * rendered twice: once from the staging path, once from the persisted one.
 */
describe('handleHistoryUpdated — attachments-only optimistic users', () => {
  const STAGING = '/lele/tmp/uploads/ab12cd34_photo.jpg'
  const PERSISTED = '/data/lele/attachments/20261017/ff99aa11_photo.jpg'

  const attachmentsOnlyOptimistic = () =>
    msg('temp-user-attach', 'user', '', {
      optimistic: true,
      optimisticBaseCount: 0,
      attachments: [{ path: STAGING, name: 'photo.jpg', kind: 'image' }],
    })

  test('strips the bubble once the cache holds the parsed persisted copy', () => {
    const base = toChatMessages(
      [{ id: 'h1', role: 'user', content: `## Attachments\n- ${PERSISTED}` }],
      's1',
    )
    // The parsed copy has NO content (`parseAttachmentsFromContent` moved the
    // marker into `attachments`) — exactly what the history cache stores.
    expect(base[0].content).toBe('')
    expect(base[0].attachments?.length).toBe(1)

    const { ctx, getStreaming } = makeCtx([attachmentsOnlyOptimistic()], base)

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.optimistic)).toBe(false)
  })

  test('strips it for the raw history shape too (marker still in content)', () => {
    const raw = msg('h1', 'user', `## Attachments\n- ${PERSISTED}`)
    const { ctx, getStreaming } = makeCtx([attachmentsOnlyOptimistic()], [raw])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.optimistic)).toBe(false)
  })

  test('keeps it while the cache has no twin yet (no over-strip / no flicker)', () => {
    const other = msg('h1', 'user', 'a completely different question')
    const { ctx, getStreaming } = makeCtx([attachmentsOnlyOptimistic()], [other])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.optimistic)).toBe(true)
  })

  test('keeps it when the cached user carries BOTH text and attachments', () => {
    // Same attachment, but the cached turn has text → its identity is that text,
    // not the marker, so it cannot vouch for an attachments-only send.
    const withText = msg('h1', 'user', 'look at this', {
      attachments: [{ path: PERSISTED, name: 'photo.jpg', kind: 'image' }],
    })
    const { ctx, getStreaming } = makeCtx([attachmentsOnlyOptimistic()], [withText])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.optimistic)).toBe(true)
  })
})
