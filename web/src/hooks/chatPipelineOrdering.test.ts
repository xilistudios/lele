import { describe, expect, test } from 'bun:test'
import { QueryClient } from '@tanstack/react-query'
import type { ChatMessage, RawHistoryMessage } from '../lib/types'
import { handleHistoryUpdated } from './event-handlers/streaming'
import type { MessageEventContext } from './event-handlers/types'
import { computeAssistantInsertIndex } from './messageInsertion'
import { mergeMessages } from './messageMerge'
import { applyMessageComplete } from './streamingOps'
import { buildChatHistoryQueryKey } from './useChatHistory'

/**
 * REGRESSION SUITE — the WebUI "messages lose their order after a while"
 * bug, driven through the REAL WS + HTTP pipeline.
 *
 * Everything that decides order and retention is production code:
 *   - live placeholders are inserted with `computeAssistantInsertIndex` and
 *     finalized with `applyMessageComplete` (exactly what the WS handlers do),
 *   - the `history.updated` strip is `handleHistoryUpdated` (the real handler,
 *     with a real react-query cache),
 *   - the reconciliation that produces the rendered list is `mergeMessages`.
 * Only the transport (a scripted WS event stream and the refetch that lands
 * after `history.updated`) is emulated.
 *
 * WHY this shape: the conversation that breaks is a THINKING / TOOL-USER one.
 * The backend persists every LLM iteration as its OWN assistant message
 * (`iterationMsgID`, pkg/agent/llm_runner.go), so a tool turn yields an
 * assistant with `content === ''` (its payload in `reasoningContent`) plus the
 * final answer. `isConfirmedInCache` used to require a non-empty CONTENT
 * prefix, so those iterations could never be confirmed and were never stripped
 * from streaming state. One leftover accumulated per iteration; once the
 * 50-message history window (pkg/channels/rest_chat.go, `resultStartIdx :=
 * endIdx - limit`) slid past their base twin, `mergeMessages` could no longer
 * pair them and appended them at the END of the list — a growing block of old
 * "Thinking…" blocks below the newest answer. A page reload clears
 * `streamingMessages`, which is why refreshing restored the order.
 *
 * Baseline: with 4 messages per turn the window starts sliding at turn 13 —
 * i.e. "after a while", exactly as reported — and EVERY turn from 13 on
 * rendered one more stranded block (8 of 20 turns failed, 13+ strays were
 * rendered at once). Both the accumulation and the duplicate are pinned here.
 */
const SESSION = 's1'
/** pkg/channels/rest_chat.go: history is the last `limit` (default 50) messages. */
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

/** `role:content` (or the reasoning, for a text-less iteration) — order-sensitive. */
function shape(list: ChatMessage[]): string[] {
  return list.map((m) => `${m.role}:${m.content || `<reasoning:${m.reasoningContent ?? ''}>`}`)
}

/** Minimal MessageEventContext for `handleHistoryUpdated` (see historyUpdated.test.ts). */
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

/** A live assistant placeholder being created by the WS stream (ack → chunks). */
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
    /** The GET /chat/history the invalidate triggers a moment after the event. */
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
      // ── user sends: the optimistic bubble snapshots the cache at send time ──
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

      // ── iteration 1: thinking + a tool call, no text content ──
      const iter1 = `msg-${n}-1`
      streaming = startStreaming(streaming, iter1)
      streaming = streaming.map((m) =>
        m.id === iter1 ? { ...m, reasoningContent: `thinking about ${n}` } : m,
      )
      streaming = applyMessageComplete(streaming, iter1, SESSION, '', undefined)
      log.push({ id: `b_i${n}`, role: 'assistant', content: '', reasoning: `thinking about ${n}` })
      log.push({ id: `b_t${n}`, role: 'tool', content: `tool result ${n}` })

      // ── iteration 2: the final answer ──
      const iter2 = `msg-${n}-2`
      streaming = startStreaming(streaming, iter2)
      streaming = applyMessageComplete(streaming, iter2, SESSION, `answer ${n}`)
      log.push({ id: `b_a${n}`, role: 'assistant', content: `answer ${n}` })

      // ── WebSocket `history.updated` (emitted once per turn, after complete) ──
      handleHistoryUpdated(ctx, { session_key: SESSION })
      // ── the refetch it triggers lands. No further history.updated follows. ──
      refetch()

      const merged = mergeMessages(servedWindow(), streaming)
      if (JSON.stringify(shape(merged)) !== JSON.stringify(shape(servedWindow()))) {
        failures.push(
          `turn ${n}: merged=${merged.length} window=${servedWindow().length} tail=[${shape(merged).slice(-4).join(' , ')}]`,
        )
      }
      maxStreaming = Math.max(maxStreaming, streaming.length)
    }

    // Every turn rendered the served window exactly, in server order.
    expect(failures).toEqual([])
    // …and the streaming state never grew without bound: it holds at most the
    // CURRENT turn (optimistic user + both iterations). Before the fix it grew
    // by one immortal message per iteration (20+ leftovers by turn 20).
    expect(maxStreaming).toBeLessThanOrEqual(3)
    expect(streaming.length).toBeLessThanOrEqual(3)
  })
})
