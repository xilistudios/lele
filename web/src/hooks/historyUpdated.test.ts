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
    const { ctx, getStreaming } = makeCtx([optimisticUser, assistant], [])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    const roles = getStreaming().map((m) => `${m.role}:${m.id}`)
    console.log('STREAMING (base empty):', roles.join('  '))
    expect(getStreaming().some((m) => m.role === 'user')).toBe(true)
    expect(getStreaming().some((m) => m.role === 'assistant')).toBe(true)
  })

  test('removes optimistic user once base contains the confirmed copy', () => {
    const optimisticUser = msg('temp-user-1', 'user', 'hello', {
      optimistic: true,
      optimisticBaseCount: 0,
    })
    const assistant = msg('ack-uuid', 'assistant', 'Hi there!', { streaming: true })
    const realUser = msg('real-u', 'user', 'hello')
    const { ctx, getStreaming } = makeCtx([optimisticUser, assistant], [realUser])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    const roles = getStreaming().map((m) => `${m.role}:${m.id}`)
    console.log('STREAMING (base has user):', roles.join('  '))
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
    const confirmed = msg('hash-id-1', 'assistant', 'Confirmed answer content')
    const { ctx, getStreaming } = makeCtx([assistant], [confirmed])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.id === 'ack-uuid-1')).toBe(false)
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

    const TAIL = { bindStableIds: true }
    const built = toChatMessages(
      [{ id: 'hash-a-42', role: 'assistant', content: 'Answer that was streamed' }],
      's1',
      TAIL,
    )
    expect(built[0].id).toBe('hash-a-42')
    expect(built[0].stableId).toBe('ack-uuid-42')

    const rebuilt = toChatMessages(
      [{ id: 'hash-a-42', role: 'assistant', content: 'Answer that was streamed' }],
      's1',
      TAIL,
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
      { bindStableIds: true },
    )

    expect(built[0].content).toBe('')
    expect(built[0].stableId).toBe('ack-uuid-thinking')
  })

  test('keeps the live iteration while the cache has no twin yet (no over-strip)', () => {
    const live = msg('ack-uuid-thinking', 'assistant', '', {
      reasoningContent: 'thinking about the tool call',
    })
    const other = msg('hash-other', 'assistant', '', {
      reasoningContent: 'thinking about something else',
    })
    const { ctx, getStreaming } = makeCtx([live], [other])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.id === 'ack-uuid-thinking')).toBe(true)
  })

  test('drops a COMPLETED iteration with neither text nor reasoning (renders nothing)', () => {
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
    const withText = msg('h1', 'user', 'look at this', {
      attachments: [{ path: PERSISTED, name: 'photo.jpg', kind: 'image' }],
    })
    const { ctx, getStreaming } = makeCtx([attachmentsOnlyOptimistic()], [withText])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.optimistic)).toBe(true)
  })
})

describe('handleHistoryUpdated — attachments-only assistant display bubbles', () => {
  const PERSISTED = '/data/lele/attachments/20261123/aa11_photo.png'
  const OTHER = '/data/lele/attachments/20261123/bb22_photo.png'

  const image = (path: string) => ({
    name: path.split('/').pop() ?? 'photo.png',
    path,
    mime_type: 'image/png',
    kind: 'image',
  })

  const liveBubble = () =>
    msg('ack-file-1', 'assistant', '', { attachments: [image(PERSISTED)] })

  const persistedBase = (path: string) =>
    toChatMessages(
      [
        {
          id: 'h1',
          role: 'tool',
          content: '',
          tool_call_id: 'file:ab12cd34ef56ab78',
          attachments: [image(path)],
        },
      ],
      's1',
    )

  test('keeps the live bubble while the cache has no matching row (no flicker)', () => {
    const { ctx, getStreaming } = makeCtx([liveBubble()], persistedBase(OTHER))

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.id === 'ack-file-1')).toBe(true)
  })

  test('keeps the live bubble when the cache is empty', () => {
    const { ctx, getStreaming } = makeCtx([liveBubble()], [])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.id === 'ack-file-1')).toBe(true)
  })

  test('drops the live bubble once the cache holds the matching row', () => {
    const { ctx, getStreaming } = makeCtx([liveBubble()], persistedBase(PERSISTED))

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.id === 'ack-file-1')).toBe(false)
  })

  test('registers the live id as stable under the attachment identity before dropping it', async () => {
    const { lookupStableId, clearStableIdRegistry } = await import('./stableIdRegistry')
    clearStableIdRegistry()

    const { ctx } = makeCtx([liveBubble()], persistedBase(PERSISTED))

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(lookupStableId('assistant', PERSISTED)).toBe('ack-file-1')
  })

  test('a caption-carrying live bubble is confirmed by its text, not by the path', () => {
    const live = msg('ack-file-2', 'assistant', 'ventas del trimestre', {
      attachments: [image(PERSISTED)],
    })
    const base = toChatMessages(
      [
        {
          id: 'h1',
          role: 'tool',
          content: 'ventas del trimestre',
          tool_call_id: 'file:0011223344556677',
          attachments: [image(PERSISTED)],
        },
      ],
      's1',
    )

    const { ctx, getStreaming } = makeCtx([live], base)

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.id === 'ack-file-2')).toBe(false)
  })

  test('a completed assistant row with neither text, reasoning nor attachments is still dropped', () => {
    const done = msg('ack-empty', 'assistant', '', { streaming: false })
    const { ctx, getStreaming } = makeCtx([done], [])

    handleHistoryUpdated(ctx, { session_key: 's1' })

    expect(getStreaming().some((m) => m.id === 'ack-empty')).toBe(false)
  })
})
