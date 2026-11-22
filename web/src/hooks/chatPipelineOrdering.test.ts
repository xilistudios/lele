import { describe, expect, test } from 'bun:test'
import { QueryClient } from '@tanstack/react-query'
import type { ChatMessage, RawHistoryMessage } from '../lib/types'
import { handleHistoryUpdated } from './event-handlers/streaming'
import type { MessageEventContext } from './event-handlers/types'
import { computeAssistantInsertIndex } from './messageInsertion'
import { mergeMessages } from './messageMerge'
import { applyMessageComplete } from './streamingOps'
import { buildChatHistoryQueryKey } from './useChatHistory'

const SESSION = 's1'
const WINDOW = 50
const TURNS = 20

type Persisted = { id: string; role: ChatMessage['role']; content: string; reasoning?: string }

function toBase(p: Persisted): ChatMessage {
  return {
    id: p.id,
    role: p.role,
    content: p.content,
    reasoningContent: p.reasoning,
    streaming: false,
    createdAt: '2026-01-01T00:00:00.000Z',
    sessionKey: SESSION,
  }
}

function shape(list: ChatMessage[]): string[] {
  return list.map((m) => `${m.role}:${m.content || `<reasoning:${m.reasoningContent ?? ''}>`}`)
}

function makeContext(
  setStreamingMessages: MessageEventContext['setStreamingMessages'],
  queryClient: QueryClient,
): MessageEventContext {
  return {
    currentSessionKeyRef: { current: SESSION },
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
}

function startStreaming(current: ChatMessage[], id: string): ChatMessage[] {
  const arr = [...current]
  arr.splice(computeAssistantInsertIndex(current), 0, {
    id,
    role: 'assistant',
    content: '',
    streaming: true,
    createdAt: '2026-01-01T00:00:00.000Z',
    sessionKey: SESSION,
  })
  return arr
}

describe('chat pipeline ordering (thinking / tool-heavy session)', () => {
  test('merged order always matches the canonical server order, and streaming state stays bounded', () => {
    const log: Persisted[] = []
    let streaming: ChatMessage[] = []
    const setStreamingMessages: MessageEventContext['setStreamingMessages'] = (updater) => {
      streaming =
        typeof updater === 'function'
          ? (updater as (p: ChatMessage[]) => ChatMessage[])(streaming)
          : updater
    }

    const queryClient = new QueryClient()
    const ctx = makeContext(setStreamingMessages, queryClient)

    const servedWindow = (): ChatMessage[] =>
      log.slice(Math.max(0, log.length - WINDOW)).map(toBase)
    const refetch = () => {
      queryClient.setQueryData(buildChatHistoryQueryKey(SESSION), {
        sessionKey: SESSION,
        messages: servedWindow(),
        rawMessages: [] as unknown as RawHistoryMessage[],
        processing: false,
      })
    }

    refetch()
    const failures: string[] = []
    let maxStreaming = 0

    for (let n = 1; n <= TURNS; n++) {
      const base = servedWindow()
      const lastUserId = [...base].reverse().find((m) => m.role === 'user')?.id
      streaming = [
        ...streaming,
        {
          id: `temp-user-${n}`,
          role: 'user',
          content: `question ${n}`,
          streaming: false,
          createdAt: '2026-01-01T00:00:00.000Z',
          sessionKey: SESSION,
          optimistic: true,
          optimisticBaseCount: base.filter((m) => m.role === 'user').length,
          optimisticAnchorId: lastUserId,
        },
      ]
      log.push({ id: `b_q${n}`, role: 'user', content: `question ${n}` })

      const iter1 = `msg-${n}-1`
      streaming = startStreaming(streaming, iter1)
      streaming = streaming.map((m) =>
        m.id === iter1 ? { ...m, reasoningContent: `thinking about ${n}` } : m,
      )
      streaming = applyMessageComplete(streaming, iter1, SESSION, '', undefined)
      log.push({ id: `b_i${n}`, role: 'assistant', content: '', reasoning: `thinking about ${n}` })
      log.push({ id: `b_t${n}`, role: 'tool', content: `tool result ${n}` })

      const iter2 = `msg-${n}-2`
      streaming = startStreaming(streaming, iter2)
      streaming = applyMessageComplete(streaming, iter2, SESSION, `answer ${n}`)
      log.push({ id: `b_a${n}`, role: 'assistant', content: `answer ${n}` })

      handleHistoryUpdated(ctx, { session_key: SESSION })
      refetch()

      const merged = mergeMessages(servedWindow(), streaming)
      if (JSON.stringify(shape(merged)) !== JSON.stringify(shape(servedWindow()))) {
        failures.push(
          `turn ${n}: merged=${merged.length} window=${servedWindow().length} tail=[${shape(merged).slice(-4).join(' , ')}]`,
        )
      }
      maxStreaming = Math.max(maxStreaming, streaming.length)
    }

    expect(failures).toEqual([])
    expect(maxStreaming).toBeLessThanOrEqual(3)
    expect(streaming.length).toBeLessThanOrEqual(3)
  })
})
